package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// envelope 是平台统一响应体：HTTP 状态通常恒为 200，业务态看 code。
type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type apiError struct {
	Status int
	Code   int
	Msg    string
}

func (e *apiError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("接口返回 code=%d: %s", e.Code, e.Msg)
	}
	return fmt.Sprintf("接口返回 HTTP %d: %s", e.Status, e.Msg)
}

// client 是 CLI 的最小 HTTP 客户端，不依赖平台内部包，便于单独编译分发。
type client struct {
	base  string
	token string
	http  *http.Client
}

func newClient(server, token string, timeout time.Duration) (*client, error) {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		return nil, fmt.Errorf("未配置服务器地址，请先执行 ci login --server http://host:8080")
	}
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("服务器地址必须是 http(s):// 开头的完整地址: %s", server)
	}
	return &client{base: server, token: strings.TrimSpace(token), http: &http.Client{Timeout: timeout}}, nil
}

func (c *client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader = http.NoBody
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ci-cli/0.1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("请求 %s %s 失败: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		snippet := strings.TrimSpace(string(data))
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return &apiError{Status: resp.StatusCode, Msg: "响应不是平台 JSON 格式: " + snippet}
	}
	if env.Code != 0 {
		return &apiError{Status: resp.StatusCode, Code: env.Code, Msg: env.Msg}
	}
	if resp.StatusCode >= 400 {
		return &apiError{Status: resp.StatusCode, Msg: env.Msg}
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("解析响应失败: %w", err)
		}
	}
	return nil
}

// pipeline 只保留 CLI 需要的字段，避免把整个任务配置打到终端。
type pipeline struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Environment string `json:"environment,omitempty"`
	GitBranch   string `json:"git_branch"`
	Disabled    bool   `json:"disabled,omitempty"`
	GitURL      string `json:"git_url,omitempty"`
	Params      []struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Required bool   `json:"required"`
	} `json:"params,omitempty"`
}

type build struct {
	ID         string `json:"id"`
	PipelineID string `json:"pipeline_id"`
	Status     string `json:"status"`
	Branch     string `json:"branch,omitempty"`
	ExitCode   int    `json:"exit_code"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

type logResult struct {
	Log          string `json:"log"`
	TotalLines   int    `json:"total_lines"`
	MatchedLines int    `json:"matched_lines"`
	Truncated    bool   `json:"truncated"`
}

type tokenView struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Prefix    string   `json:"prefix"`
	Scopes    []string `json:"scopes,omitempty"`
	CreatedAt string   `json:"created_at"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

type tokenCreateResult struct {
	Token  tokenView `json:"token"`
	Secret string    `json:"secret"`
}

func (c *client) listPipelines(ctx context.Context) ([]pipeline, error) {
	var items []pipeline
	err := c.do(ctx, http.MethodGet, "/api/pipelines", nil, &items)
	return items, err
}

// resolvePipeline 支持任务 ID 或任务名；同名任务理论上不会存在，但仍做唯一性检查。
func (c *client) resolvePipeline(ctx context.Context, ref string) (pipeline, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return pipeline{}, fmt.Errorf("任务不能为空")
	}
	items, err := c.listPipelines(ctx)
	if err != nil {
		return pipeline{}, err
	}
	for _, item := range items {
		if item.ID == ref {
			return item, nil
		}
	}
	var matched []pipeline
	for _, item := range items {
		if item.Name == ref || strings.EqualFold(item.Name, ref) {
			matched = append(matched, item)
		}
	}
	if len(matched) == 1 {
		return matched[0], nil
	}
	if len(matched) > 1 {
		return pipeline{}, fmt.Errorf("任务名 %q 匹配到多个任务，请改用任务 ID", ref)
	}
	return pipeline{}, fmt.Errorf("找不到任务 %q（可用 ci list 查看）", ref)
}

func (c *client) trigger(ctx context.Context, pid string, params map[string]string, idempotencyKey string) (build, error) {
	var out build
	body := map[string]any{"params": params}
	if idempotencyKey != "" {
		body["idempotency_key"] = idempotencyKey
	}
	err := c.do(ctx, http.MethodPost, "/api/pipelines/"+url.PathEscape(pid)+"/build", body, &out)
	return out, err
}

func (c *client) getBuild(ctx context.Context, pid, bid string) (build, error) {
	var out build
	err := c.do(ctx, http.MethodGet,
		"/api/pipelines/"+url.PathEscape(pid)+"/builds/"+url.PathEscape(bid), nil, &out)
	return out, err
}

func (c *client) listBuilds(ctx context.Context, pid string) ([]build, error) {
	var out []build
	err := c.do(ctx, http.MethodGet, "/api/pipelines/"+url.PathEscape(pid)+"/builds", nil, &out)
	return out, err
}

func (c *client) logs(ctx context.Context, pid, bid string, tail int, grep string) (logResult, error) {
	q := url.Values{}
	if tail > 0 {
		q.Set("tail", fmt.Sprintf("%d", tail))
	}
	if grep != "" {
		q.Set("grep", grep)
	}
	path := "/api/pipelines/" + url.PathEscape(pid) + "/builds/" + url.PathEscape(bid) + "/log"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out logResult
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *client) cancel(ctx context.Context, pid, bid string) error {
	return c.do(ctx, http.MethodPost,
		"/api/pipelines/"+url.PathEscape(pid)+"/builds/"+url.PathEscape(bid)+"/cancel", nil, nil)
}

func (c *client) login(ctx context.Context, username, password string) (string, string, error) {
	var out struct {
		Token     string `json:"token"`
		Username  string `json:"username"`
		ExpiresAt string `json:"expires_at"`
	}
	err := c.do(ctx, http.MethodPost, "/api/auth/login",
		map[string]string{"username": username, "password": password}, &out)
	return out.Token, out.ExpiresAt, err
}

func (c *client) me(ctx context.Context) (string, error) {
	var out struct {
		Username string `json:"username"`
	}
	err := c.do(ctx, http.MethodGet, "/api/auth/me", nil, &out)
	return out.Username, err
}

func (c *client) listTokens(ctx context.Context) ([]tokenView, error) {
	var out []tokenView
	err := c.do(ctx, http.MethodGet, "/api/profile/tokens", nil, &out)
	return out, err
}

func (c *client) createToken(ctx context.Context, name string, days int, scopes []string) (tokenCreateResult, error) {
	var out tokenCreateResult
	err := c.do(ctx, http.MethodPost, "/api/profile/tokens", map[string]any{
		"name": name, "expires_in_days": days, "scopes": scopes,
	}, &out)
	return out, err
}

func (c *client) revokeToken(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/profile/tokens/"+url.PathEscape(id), nil, nil)
}

// waitBuild 轮询到构建结束。pending_approval 也视为「尚未结束」，
// 避免把等待审批误报成 success/failed。
func (c *client) waitBuild(ctx context.Context, pid, bid string, interval time.Duration) (build, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		b, err := c.getBuild(ctx, pid, bid)
		if err != nil {
			return b, err
		}
		switch b.Status {
		case "success", "failed", "aborted":
			return b, nil
		}
		select {
		case <-ctx.Done():
			return b, ctx.Err()
		case <-ticker.C:
		}
	}
}
