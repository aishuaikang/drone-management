package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/pkg/sftp"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type AboutInfo struct {
	UserCompany string `json:"userCompany"`
	UserName    string `json:"userName"`
}

type AboutInfoRequest struct {
	InstallDir string    `json:"installDir"`
	Local      bool      `json:"local"`
	Info       AboutInfo `json:"info"`
}

func normalizeAboutInfo(info AboutInfo) (AboutInfo, error) {
	info.UserCompany = strings.TrimSpace(info.UserCompany)
	info.UserName = strings.TrimSpace(info.UserName)
	if utf8.RuneCountInString(info.UserCompany) > 128 {
		return AboutInfo{}, errors.New("使用厂家最多 128 个字符")
	}
	if utf8.RuneCountInString(info.UserName) > 64 {
		return AboutInfo{}, errors.New("使用人员最多 64 个字符")
	}
	return info, nil
}

func decodeAboutInfo(reader io.Reader) (AboutInfo, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 64*1024+1))
	if err != nil {
		return AboutInfo{}, fmt.Errorf("读取软件信息失败: %w", err)
	}
	if len(data) > 64*1024 {
		return AboutInfo{}, errors.New("软件信息文件过大")
	}
	var info AboutInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return AboutInfo{}, fmt.Errorf("解析软件信息失败: %w", err)
	}
	return normalizeAboutInfo(info)
}

func localAboutInfoPath(installDir string) (string, error) {
	if strings.TrimSpace(installDir) == "" {
		return "", errors.New("请选择本机程序目录")
	}
	root, err := filepath.Abs(installDir)
	if err != nil {
		return "", fmt.Errorf("解析本机程序目录失败: %w", err)
	}
	stat, err := os.Stat(root)
	if err != nil || !stat.IsDir() {
		return "", errors.New("本机程序目录不存在")
	}
	return filepath.Join(root, "data", "about.json"), nil
}

func (a *App) SelectLocalInstallDir() (string, error) {
	if a.ctx == nil {
		return "", errors.New("应用尚未就绪")
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择 Drone Management 本机程序目录"})
}

func (a *App) ReadAboutInfo(installDir string, local bool) (AboutInfo, error) {
	if local {
		path, err := localAboutInfoPath(installDir)
		if err != nil {
			return AboutInfo{}, err
		}
		file, err := os.Open(path)
		if os.IsNotExist(err) {
			return AboutInfo{}, nil
		}
		if err != nil {
			return AboutInfo{}, fmt.Errorf("打开软件信息失败: %w", err)
		}
		defer file.Close()
		return decodeAboutInfo(file)
	}
	client, err := a.getSSHClient()
	if err != nil {
		return AboutInfo{}, err
	}
	remote, err := sftp.NewClient(client)
	if err != nil {
		return AboutInfo{}, fmt.Errorf("创建 SFTP 客户端失败: %w", err)
	}
	defer remote.Close()
	file, err := remote.Open(remoteJoin(a.getInstallDir(installDir), "data", "about.json"))
	if os.IsNotExist(err) {
		return AboutInfo{}, nil
	}
	if err != nil {
		return AboutInfo{}, fmt.Errorf("打开远程软件信息失败: %w", err)
	}
	defer file.Close()
	return decodeAboutInfo(file)
}

func (a *App) SaveAboutInfo(req AboutInfoRequest) (AboutInfo, error) {
	info, err := normalizeAboutInfo(req.Info)
	if err != nil {
		return AboutInfo{}, err
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return AboutInfo{}, err
	}
	data = append(data, '\n')
	if req.Local {
		path, err := localAboutInfoPath(req.InstallDir)
		if err != nil {
			return AboutInfo{}, err
		}
		if err := atomicWriteFile(path, data, 0o600); err != nil {
			return AboutInfo{}, err
		}
		return info, nil
	}
	path := remoteJoin(a.getInstallDir(req.InstallDir), "data", "about.json")
	if _, err := a.runCommand(buildRemoteAboutInfoWriteCommand(path, data)); err != nil {
		return AboutInfo{}, fmt.Errorf("保存远程软件信息失败: %w", err)
	}
	return info, nil
}

func buildRemoteAboutInfoWriteCommand(path string, data []byte) string {
	// Keep the temporary file on the same filesystem so mv replaces the target atomically.
	// Exit 77 only for operations that may require the deployment account's sudo access.
	script := fmt.Sprintf(`set -eu
umask 022
target=%s
dir=%s
if [ -d "$target" ]; then
  echo '软件信息路径是目录，无法保存' >&2
  exit 1
fi
mkdir -p -- "$dir" || exit 77
tmp=$(mktemp "$dir/.about-XXXXXXXX") || exit 77
trap 'rm -f -- "$tmp"' 0
trap 'exit 1' HUP INT TERM
printf '%%s' %s > "$tmp"
chmod 0644 "$tmp"
mv -fT -- "$tmp" "$target" || exit 77
`, shellQuote(path), shellQuote(remoteDir(path)), shellQuote(string(data)))
	return fmt.Sprintf(`if sh -c %s; then
  exit 0
else
  status=$?
fi
if [ "$status" -ne 77 ] || [ "$(id -u)" -eq 0 ]; then
  exit "$status"
fi
exec sudo -n sh -c %s
`, shellQuote(script), shellQuote(script))
}
