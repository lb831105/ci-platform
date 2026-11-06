package store

import (
	"os"
	"path/filepath"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// SettingsStore 读写 data/settings.json。
// 敏感字段（SMTP 密码、SSH 密码、通知 URL、标记为 secret 的全局变量）
// 使用 data/credentials.key 加密，避免明文落盘。
type SettingsStore struct {
	path string
	box  *pkg.SecretBox
}

// NewSettingsStore 创建系统配置存储，dataHome 为 data 目录。
func NewSettingsStore(dataHome string) *SettingsStore {
	return &SettingsStore{
		path: filepath.Join(dataHome, "settings.json"),
		box:  pkg.NewSecretBox(filepath.Join(dataHome, "credentials.key")),
	}
}

// Load 读取配置并解密敏感字段；文件不存在时返回 (nil, nil)，由 service 决定默认值。
func (s *SettingsStore) Load() (*model.Settings, error) {
	if !pkg.Exists(s.path) {
		return nil, nil
	}
	var out model.Settings
	if err := pkg.ReadJSON(s.path, &out); err != nil {
		return nil, err
	}
	if err := s.decrypt(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Save 原子写入配置。先在副本上加密，避免修改调用方内存中的明文配置。
func (s *SettingsStore) Save(in *model.Settings) error {
	out := *in
	out.Email = in.Email
	out.WebhookNotify = in.WebhookNotify
	out.SshServers = append([]model.SshServer(nil), in.SshServers...)
	out.GlobalEnv = append([]model.EnvVar(nil), in.GlobalEnv...)
	if err := s.encrypt(&out); err != nil {
		return err
	}
	return pkg.WriteJSONAtomic(s.path, &out)
}

func (s *SettingsStore) encrypt(in *model.Settings) error {
	seal := func(value string) (string, error) { return s.box.Seal(value) }
	var err error
	if in.Email.Password, err = seal(in.Email.Password); err != nil {
		return err
	}
	if in.WebhookNotify.URL, err = seal(in.WebhookNotify.URL); err != nil {
		return err
	}
	for i := range in.SshServers {
		if in.SshServers[i].Password, err = seal(in.SshServers[i].Password); err != nil {
			return err
		}
	}
	for i := range in.GlobalEnv {
		if !in.GlobalEnv[i].Secret && !model.IsSecretEnvName(in.GlobalEnv[i].Key) {
			continue
		}
		if in.GlobalEnv[i].Value, err = seal(in.GlobalEnv[i].Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *SettingsStore) decrypt(in *model.Settings) error {
	open := func(value string) (string, error) { return s.box.Unseal(value) }
	var err error
	if in.Email.Password, err = open(in.Email.Password); err != nil {
		return err
	}
	if in.WebhookNotify.URL, err = open(in.WebhookNotify.URL); err != nil {
		return err
	}
	for i := range in.SshServers {
		if in.SshServers[i].Password, err = open(in.SshServers[i].Password); err != nil {
			return err
		}
	}
	for i := range in.GlobalEnv {
		if !in.GlobalEnv[i].Secret && !model.IsSecretEnvName(in.GlobalEnv[i].Key) {
			continue
		}
		if in.GlobalEnv[i].Value, err = open(in.GlobalEnv[i].Value); err != nil {
			return err
		}
	}
	return nil
}

// Path 返回配置文件路径，供「系统信息」展示。
func (s *SettingsStore) Path() string {
	return s.path
}

// Remove 删除配置文件（用于测试/重置）。
func (s *SettingsStore) Remove() error {
	err := os.Remove(s.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
