package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type AppConfig struct {
	SSH            *SavedSSHConfig `json:"ssh,omitempty"`
	InstallDir     string          `json:"installDir,omitempty"`
	ReleasePackage string          `json:"releasePackage,omitempty"`
}

type SavedSSHConfig struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	User             string `json:"user"`
	RememberPassword bool   `json:"rememberPassword,omitempty"`
	Password         string `json:"password,omitempty"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("获取用户配置目录失败: %w", err)
	}
	return filepath.Join(dir, "drone-management-tool", "config.json"), nil
}

func (a *App) LoadConfig() (AppConfig, error) {
	path, err := configPath()
	if err != nil {
		return AppConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AppConfig{InstallDir: defaultInstallDir}, nil
		}
		return AppConfig{}, fmt.Errorf("读取配置失败: %w", err)
	}
	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return AppConfig{}, fmt.Errorf("解析配置失败: %w", err)
	}
	if cfg.InstallDir == "" {
		cfg.InstallDir = defaultInstallDir
	}
	cfg.loadRememberedPassword()
	return cfg, nil
}

func (a *App) SaveConfig(cfg AppConfig) error {
	if cfg.SSH != nil && cfg.SSH.Port == 0 {
		cfg.SSH.Port = defaultSSHPort
	}
	if cfg.InstallDir == "" {
		cfg.InstallDir = defaultInstallDir
	}
	if err := cfg.syncRememberedPassword(); err != nil {
		return err
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	if cfg.SSH != nil {
		cfg.SSH.Password = ""
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	return atomicWriteFile(path, data, 0o600)
}

func (a *App) updateConfig(mutator func(*AppConfig)) {
	cfg, err := a.LoadConfig()
	if err != nil {
		return
	}
	mutator(&cfg)
	_ = a.SaveConfig(cfg)
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时配置失败: %w", err)
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入临时配置失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("同步临时配置失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时配置失败: %w", err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("设置配置权限失败: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	ok = true
	return nil
}
