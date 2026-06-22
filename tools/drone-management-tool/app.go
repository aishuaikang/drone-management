package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/crypto/ssh"
)

const (
	defaultInstallDir = "/spbatc/drone-management"
	defaultSSHPort    = 22
)

type App struct {
	ctx context.Context

	sshMu sync.Mutex
	conn  *sshConnection
}

type sshConnection struct {
	config SSHConnectRequest
	client *ssh.Client
}

type ProgressEvent struct {
	Step        int    `json:"step"`
	StepName    string `json:"stepName"`
	Message     string `json:"message"`
	Status      string `json:"status"`
	Progress    int    `json:"progress"`
	ErrorDetail string `json:"errorDetail,omitempty"`
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) emit(event string, payload any) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, event, payload)
}

func (a *App) emitProgress(event string, step int, stepName, message, status string, progress int, err error) {
	payload := ProgressEvent{
		Step:     step,
		StepName: stepName,
		Message:  message,
		Status:   status,
		Progress: clampProgress(progress),
	}
	if err != nil {
		payload.ErrorDetail = err.Error()
	}
	a.emit(event, payload)
}

func (a *App) getSSHClient() (*ssh.Client, error) {
	a.sshMu.Lock()
	conn := a.conn
	a.sshMu.Unlock()
	if conn == nil || conn.client == nil {
		return nil, errors.New("请先连接 SSH")
	}
	return conn.client, nil
}

func (a *App) getInstallDir(fallback string) string {
	installDir := cleanRemotePath(fallback)
	if installDir == "" {
		installDir = defaultInstallDir
	}
	return installDir
}

func (a *App) SelectReleasePackage() (string, error) {
	if a.ctx == nil {
		return "", errors.New("应用尚未就绪")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择 Drone Management Linux 发布包",
		Filters: []runtime.FileFilter{
			{DisplayName: "Linux 发布包 (*.tar.gz)", Pattern: "*.gz"},
			{DisplayName: "所有文件", Pattern: "*"},
		},
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

func clampProgress(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func cleanRemotePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\\", "/")
	for strings.Contains(value, "//") {
		value = strings.ReplaceAll(value, "//", "/")
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	if value == "/" {
		return value
	}
	return strings.TrimRight(value, "/")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func remoteJoin(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.ReplaceAll(part, "\\", "/"), "/")
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 {
		return "/"
	}
	return "/" + strings.Join(cleaned, "/")
}

func existingLocalDir(path string) string {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return ""
	}
	return path
}

func localDir(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Dir(path)
}
