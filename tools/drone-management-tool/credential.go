package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

const keyringService = "drone-management-tool"

var (
	keyringGet    = keyring.Get
	keyringSet    = keyring.Set
	keyringDelete = keyring.Delete
)

func (cfg *AppConfig) loadRememberedPassword() {
	if cfg == nil || cfg.SSH == nil || !cfg.SSH.RememberPassword {
		return
	}
	password, err := keyringGet(keyringService, sshCredentialAccount(*cfg.SSH))
	if err != nil {
		return
	}
	cfg.SSH.Password = password
}

func (cfg *AppConfig) syncRememberedPassword() error {
	if cfg == nil || cfg.SSH == nil {
		return nil
	}
	cfg.SSH.Host = strings.TrimSpace(cfg.SSH.Host)
	cfg.SSH.User = strings.TrimSpace(cfg.SSH.User)
	if cfg.SSH.Port == 0 {
		cfg.SSH.Port = defaultSSHPort
	}
	account := sshCredentialAccount(*cfg.SSH)
	if !cfg.SSH.RememberPassword {
		if err := keyringDelete(keyringService, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("删除已保存密码失败: %w", err)
		}
		return nil
	}
	if strings.TrimSpace(cfg.SSH.Password) == "" {
		return nil
	}
	if err := keyringSet(keyringService, account, cfg.SSH.Password); err != nil {
		return fmt.Errorf("保存密码失败: %w", err)
	}
	return nil
}

func sshCredentialAccount(cfg SavedSSHConfig) string {
	port := cfg.Port
	if port == 0 {
		port = defaultSSHPort
	}
	return fmt.Sprintf("%s@%s:%d", strings.TrimSpace(cfg.User), strings.TrimSpace(cfg.Host), port)
}
