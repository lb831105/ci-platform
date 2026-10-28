package model

import "time"

// 凭据类型枚举，语义对齐 Jenkins Credentials。
const (
	CredentialUsernamePassword = "username_password" // 用户名 + 密码
	CredentialSecretText       = "secret_text"       // 单个密钥（token）
	CredentialSSHKey           = "ssh_key"           // SSH 私钥
)

// Credential 是持久化的凭据，落盘为 data/credentials/{id}.json。
type Credential struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Username    string `json:"username,omitempty"`
	Description string `json:"description"`
	Secret      string `json:"secret,omitempty"` // 密码 / token / 私钥
	// AllowedHosts 限制该凭据只能用于哪些 Git 主机（精确域名或 *.example.com）。
	// 为空表示兼容旧凭据（不限制），但新凭据建议显式配置，防止任务配置者
	// 把凭据发到任意 URL。
	AllowedHosts []string  `json:"allowed_hosts,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Mask 返回脱敏副本：用于列表与详情接口，绝不回传密钥明文。
func (c Credential) Mask() Credential {
	c.Secret = ""
	if c.AllowedHosts != nil {
		c.AllowedHosts = append([]string(nil), c.AllowedHosts...)
	}
	return c
}
