package executor

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitlabCommitStatus 是一次 GitLab commit status 回写请求。
type GitlabCommitStatus struct {
	// BaseURL 是 GitLab 站点地址（如 https://gitlab.example.com），不含 /api/v4。
	BaseURL string
	// Token 是 Personal Access Token（需要 api 权限）。
	Token string
	// Project 是项目 ID 或 group%2Fapp 形式的路径。
	Project string
	// SHA 是本次构建对应的提交。
	SHA string
	// State 为 pending / running / success / failed / canceled。
	State string
	// Name 是状态名（同一次提交上同名的状态会覆盖）。
	Name string
	// TargetURL 是"详情"链接，指向平台里的构建页。
	TargetURL string
	// Description 是简短说明（GitLab 在 MR 里展示）。
	Description string
}

// PostGitlabCommitStatus 调用 GitLab 的 commit status API。
//
// 为什么不用 GitHub 的 check runs：平台目前只承诺 GitLab（用户实际用的是 GitLab）；
// 将来接 GitHub 时只需在同样的挂点上换成 checks/statuses 接口。
func PostGitlabCommitStatus(ctx context.Context, in GitlabCommitStatus) error {
	base := strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if base == "" {
		return fmt.Errorf("gitlab base url is not configured")
	}
	project := strings.TrimSpace(in.Project)
	sha := strings.TrimSpace(in.SHA)
	if project == "" || sha == "" {
		return fmt.Errorf("gitlab project/sha is required")
	}
	// 项目路径要整体转义（group/app → group%2Fapp），否则会被当成多级路径。
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/statuses/%s", base, url.PathEscape(project), url.PathEscape(sha))

	form := url.Values{}
	form.Set("state", in.State)
	if in.Name != "" {
		form.Set("name", in.Name)
	}
	if in.TargetURL != "" {
		form.Set("target_url", in.TargetURL)
	}
	if in.Description != "" {
		// GitLab 限制 255 字符，超长会 400。
		desc := in.Description
		if len([]rune(desc)) > 255 {
			desc = string([]rune(desc)[:255])
		}
		form.Set("description", desc)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build gitlab status request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("PRIVATE-TOKEN", in.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post gitlab status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 只回状态码：响应体可能包含项目信息，不适合写进构建日志。
		return fmt.Errorf("gitlab status api returned HTTP %d（请检查 token 的 api 权限与项目路径）", resp.StatusCode)
	}
	return nil
}
