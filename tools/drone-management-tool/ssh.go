package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type SSHConnectRequest struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	User             string `json:"user"`
	Password         string `json:"password"`
	RememberPassword bool   `json:"rememberPassword,omitempty"`
}

type SSHStatus struct {
	Connected bool   `json:"connected"`
	Host      string `json:"host,omitempty"`
	Port      int    `json:"port,omitempty"`
	User      string `json:"user,omitempty"`
	Message   string `json:"message"`
}

type RemoteProbe struct {
	InstallDir     string   `json:"installDir"`
	ServiceActive  bool     `json:"serviceActive"`
	ServiceEnabled bool     `json:"serviceEnabled"`
	ServiceStatus  string   `json:"serviceStatus"`
	HasSystemd     bool     `json:"hasSystemd"`
	HasTar         bool     `json:"hasTar"`
	BinaryExists   bool     `json:"binaryExists"`
	HealthOK       bool     `json:"healthOk"`
	HealthStatus   string   `json:"healthStatus"`
	Warnings       []string `json:"warnings,omitempty"`
}

func normalizeSSHParams(req SSHConnectRequest) (SSHConnectRequest, error) {
	req.Host = strings.TrimSpace(req.Host)
	req.User = strings.TrimSpace(req.User)
	if req.Host == "" {
		return SSHConnectRequest{}, errors.New("主机地址不能为空")
	}
	if req.User == "" {
		return SSHConnectRequest{}, errors.New("用户名不能为空")
	}
	if host, portText, err := net.SplitHostPort(req.Host); err == nil {
		req.Host = host
		if req.Port == 0 || req.Port == defaultSSHPort {
			port, convErr := strconv.Atoi(portText)
			if convErr != nil {
				return SSHConnectRequest{}, fmt.Errorf("端口格式无效: %w", convErr)
			}
			req.Port = port
		}
	}
	if req.Port == 0 {
		req.Port = defaultSSHPort
	}
	if req.Port < 1 || req.Port > 65535 {
		return SSHConnectRequest{}, fmt.Errorf("端口范围无效: %d", req.Port)
	}
	return req, nil
}

func (a *App) ConnectSSH(req SSHConnectRequest) (SSHStatus, error) {
	req, err := normalizeSSHParams(req)
	if err != nil {
		return SSHStatus{}, err
	}
	sshCfg := &ssh.ClientConfig{
		User: req.User,
		Auth: []ssh.AuthMethod{
			ssh.Password(req.Password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(req.Host, strconv.Itoa(req.Port))
	client, err := dialSSH(addr, sshCfg)
	if err != nil {
		status := SSHStatus{
			Connected: false,
			Host:      req.Host,
			Port:      req.Port,
			User:      req.User,
			Message:   err.Error(),
		}
		a.emit("ssh-status", status)
		return status, fmt.Errorf("SSH 连接失败: %w", err)
	}

	a.sshMu.Lock()
	if a.conn != nil && a.conn.client != nil {
		_ = a.conn.client.Close()
	}
	a.conn = &sshConnection{config: req, client: client}
	a.sshMu.Unlock()

	message := "SSH 已连接"
	appCfg, cfgErr := a.LoadConfig()
	if cfgErr == nil {
		appCfg.SSH = &SavedSSHConfig{
			Host:             req.Host,
			Port:             req.Port,
			User:             req.User,
			RememberPassword: req.RememberPassword,
			Password:         req.Password,
		}
		if err := a.SaveConfig(appCfg); err != nil {
			message = "SSH 已连接，但" + err.Error()
		}
	} else {
		message = "SSH 已连接，但读取配置失败: " + cfgErr.Error()
	}

	status := SSHStatus{
		Connected: true,
		Host:      req.Host,
		Port:      req.Port,
		User:      req.User,
		Message:   message,
	}
	a.emit("ssh-status", status)
	return status, nil
}

func (a *App) ReconnectSSH() (SSHStatus, error) {
	req, err := a.reconnectRequest()
	if err != nil {
		status := SSHStatus{
			Connected: false,
			Host:      req.Host,
			Port:      req.Port,
			User:      req.User,
			Message:   err.Error(),
		}
		a.emit("ssh-status", status)
		return status, err
	}
	return a.ConnectSSH(req)
}

func (a *App) reconnectRequest() (SSHConnectRequest, error) {
	a.sshMu.Lock()
	conn := a.conn
	a.sshMu.Unlock()
	if conn != nil {
		req := conn.config
		if strings.TrimSpace(req.Password) != "" {
			return req, nil
		}
	}

	cfg, err := a.LoadConfig()
	if err != nil {
		return SSHConnectRequest{}, fmt.Errorf("读取重连配置失败: %w", err)
	}
	if cfg.SSH == nil {
		return SSHConnectRequest{}, errors.New("没有可用于重连的 SSH 配置")
	}
	req := SSHConnectRequest{
		Host:             cfg.SSH.Host,
		Port:             cfg.SSH.Port,
		User:             cfg.SSH.User,
		Password:         cfg.SSH.Password,
		RememberPassword: cfg.SSH.RememberPassword,
	}
	if strings.TrimSpace(req.Password) == "" {
		return req, errors.New("没有可用于重连的密码，请重新输入密码")
	}
	return req, nil
}

func dialSSH(addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	var lastErr error
	for attempt, delay := range []time.Duration{0, 300 * time.Millisecond, 900 * time.Millisecond} {
		if delay > 0 {
			time.Sleep(delay)
		}
		conn, err := net.DialTimeout("tcp", addr, cfg.Timeout)
		if err != nil {
			lastErr = err
		} else {
			if err := conn.SetDeadline(time.Now().Add(cfg.Timeout)); err != nil {
				_ = conn.Close()
				return nil, err
			}
			cc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
			if err == nil {
				_ = conn.SetDeadline(time.Time{})
				return ssh.NewClient(cc, chans, reqs), nil
			}
			_ = conn.Close()
			lastErr = err
		}
		if attempt == 2 || !isRetryableSSHError(lastErr) {
			break
		}
	}
	return nil, lastErr
}

func isRetryableSSHError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"no route to host",
		"network is unreachable",
		"connection timed out",
		"connection reset by peer",
		"i/o timeout",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func (a *App) DisconnectSSH() error {
	a.sshMu.Lock()
	conn := a.conn
	a.conn = nil
	a.sshMu.Unlock()
	if conn != nil && conn.client != nil {
		_ = conn.client.Close()
	}
	a.emit("ssh-status", SSHStatus{Connected: false, Message: "SSH 已断开"})
	return nil
}

func (a *App) GetSSHStatus() SSHStatus {
	a.sshMu.Lock()
	conn := a.conn
	a.sshMu.Unlock()
	if conn == nil || conn.client == nil {
		return SSHStatus{Connected: false, Message: "未连接"}
	}
	session, err := conn.client.NewSession()
	if err != nil {
		return SSHStatus{
			Connected: false,
			Host:      conn.config.Host,
			Port:      conn.config.Port,
			User:      conn.config.User,
			Message:   err.Error(),
		}
	}
	_ = session.Close()
	return SSHStatus{
		Connected: true,
		Host:      conn.config.Host,
		Port:      conn.config.Port,
		User:      conn.config.User,
		Message:   "SSH 已连接",
	}
}

func (a *App) ProbeRemote(installDir string) (RemoteProbe, error) {
	installDir = a.getInstallDir(installDir)
	result := RemoteProbe{
		InstallDir: installDir,
		Warnings:   []string{},
	}
	check := func(cmd string) bool {
		out, err := a.runCommand(cmd)
		return err == nil && strings.TrimSpace(out) == "yes"
	}
	result.HasSystemd = check("command -v systemctl >/dev/null 2>&1 && echo yes || echo no")
	result.HasTar = check("command -v tar >/dev/null 2>&1 && echo yes || echo no")
	result.BinaryExists = check("test -x " + shellQuote(remoteJoin(installDir, "drone-management")) + " && echo yes || echo no")
	if !result.HasSystemd {
		result.Warnings = append(result.Warnings, "未检测到 systemctl")
	}
	if !result.HasTar {
		result.Warnings = append(result.Warnings, "未检测到 tar")
	}

	status, err := a.runCommand("systemctl is-active drone-management.service 2>/dev/null || true")
	if err == nil {
		result.ServiceStatus = strings.TrimSpace(status)
		result.ServiceActive = result.ServiceStatus == "active"
	}
	enabled, err := a.runCommand("systemctl is-enabled drone-management.service 2>/dev/null || true")
	if err == nil {
		result.ServiceEnabled = strings.TrimSpace(enabled) == "enabled"
	}
	health, err := a.runCommand(`if command -v curl >/dev/null 2>&1; then
  curl -fsS http://127.0.0.1:18080/healthz >/dev/null 2>&1 && echo yes || echo no
elif command -v wget >/dev/null 2>&1; then
  wget -q -O /dev/null http://127.0.0.1:18080/healthz >/dev/null 2>&1 && echo yes || echo no
else
  echo unknown
fi`)
	if err == nil {
		result.HealthStatus = strings.TrimSpace(health)
		result.HealthOK = result.HealthStatus == "yes"
		if result.HealthStatus == "unknown" {
			result.Warnings = append(result.Warnings, "未检测到 curl 或 wget，无法探测健康检查")
		}
	}
	return result, nil
}

func (a *App) runCommand(command string) (string, error) {
	client, err := a.getSSHClient()
	if err != nil {
		return "", err
	}
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("创建 SSH 会话失败: %w", err)
	}
	defer session.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Run(command); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.String(), errors.New(detail)
	}
	return stdout.String(), nil
}
