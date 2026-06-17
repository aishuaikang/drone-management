package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/pkg/sftp"
)

type RemoteEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

type progressReader struct {
	reader  io.Reader
	total   int64
	read    int64
	onBytes func(read, total int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.read += int64(n)
		if r.onBytes != nil {
			r.onBytes(r.read, r.total)
		}
	}
	return n, err
}

func (a *App) uploadFile(localPath, remotePath string, onProgress func(read, total int64)) error {
	client, err := a.getSSHClient()
	if err != nil {
		return err
	}
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("创建 SFTP 客户端失败: %w", err)
	}
	defer sftpClient.Close()

	local, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("打开本地文件失败: %w", err)
	}
	defer local.Close()
	stat, err := local.Stat()
	if err != nil {
		return fmt.Errorf("读取本地文件信息失败: %w", err)
	}
	dir := remoteDir(remotePath)
	if dir != "" {
		if err := sftpClient.MkdirAll(dir); err != nil {
			return fmt.Errorf("创建远程目录失败: %w", err)
		}
	}
	tmpPath := remotePath + ".tmp"
	remote, err := sftpClient.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("创建远程文件失败: %w", err)
	}
	reader := &progressReader{
		reader:  local,
		total:   stat.Size(),
		onBytes: onProgress,
	}
	_, copyErr := io.Copy(remote, reader)
	closeErr := remote.Close()
	if copyErr != nil {
		_ = sftpClient.Remove(tmpPath)
		return fmt.Errorf("上传文件失败: %w", copyErr)
	}
	if closeErr != nil {
		_ = sftpClient.Remove(tmpPath)
		return fmt.Errorf("关闭远程文件失败: %w", closeErr)
	}
	if err := sftpClient.Rename(tmpPath, remotePath); err != nil {
		_ = sftpClient.Remove(tmpPath)
		return fmt.Errorf("提交远程文件失败: %w", err)
	}
	return nil
}

func remoteDir(remotePath string) string {
	idx := strings.LastIndex(remotePath, "/")
	if idx <= 0 {
		return ""
	}
	return remotePath[:idx]
}

func (a *App) BrowseRemoteDir(path string) ([]RemoteEntry, error) {
	client, err := a.getSSHClient()
	if err != nil {
		return nil, err
	}
	path = cleanRemotePath(path)
	if path == "" {
		path = "/"
	}
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("创建 SFTP 客户端失败: %w", err)
	}
	defer sftpClient.Close()

	entries, err := sftpClient.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("读取远程目录失败: %w", err)
	}
	result := make([]RemoteEntry, 0, len(entries))
	basePath := strings.TrimRight(path, "/")
	if basePath == "" {
		basePath = "/"
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		entryPath := basePath + "/" + name
		if basePath == "/" {
			entryPath = "/" + name
		}
		result = append(result, RemoteEntry{
			Name:  name,
			Path:  entryPath,
			IsDir: entry.IsDir(),
			Size:  entry.Size(),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}
