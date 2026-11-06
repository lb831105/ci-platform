package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// cliConfig 是本地保存的连接信息。Token 既可以是登录会话，也可以是 API Token；
// 会话过期后重新 login 即可，脚本场景建议用 API Token。
type cliConfig struct {
	Server string `json:"server"`
	Token  string `json:"token,omitempty"`
}

// configPath 允许用 CI_CONFIG 覆盖，便于 CI 中挂载临时配置。
func configPath() (string, error) {
	if p := os.Getenv("CI_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ci-platform", "config.json"), nil
}

func loadConfig() (cliConfig, error) {
	var cfg cliConfig
	p, err := configPath()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("读取配置 %s 失败: %w", p, err)
	}
	return cfg, nil
}

func saveConfig(cfg cliConfig) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// 配置里有 token，权限必须是 0600；先写临时文件再改名，避免半截文件。
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
