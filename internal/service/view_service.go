package service

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// ViewStore 视图持久化接口（internal/store.ViewStore 满足）。
type ViewStore interface {
	Load() ([]model.View, error)
	Save([]model.View) error
}

// ViewPipelineStore 枚举任务（视图任务解析需要全量任务名）。
type ViewPipelineStore interface {
	List(ctx context.Context) ([]*model.Pipeline, error)
}

// ViewPermChecker 判断用户对任务的操作权限（我的视图与权限过滤使用）。
type ViewPermChecker interface {
	HasJobPermission(user, action, jobName string) bool
}

// ViewService 视图 CRUD 与任务解析。视图本质是给任务归类：
// 列表视图按「显式任务 + 任务名正则」归类；我的视图按当前用户权限自动归类。
type ViewService struct {
	mu        sync.Mutex
	store     ViewStore
	pipelines ViewPipelineStore
	rbac      ViewPermChecker
}

// NewViewService 构造视图服务；pipelines 用于解析视图内任务，rbac 用于权限过滤（可为 nil）。
func NewViewService(store ViewStore, pipelines ViewPipelineStore, rbac ViewPermChecker) *ViewService {
	return &ViewService{store: store, pipelines: pipelines, rbac: rbac}
}

// List 返回全部视图（按创建顺序；视图数量小无需排序）。
func (s *ViewService) List() []model.View {
	s.mu.Lock()
	defer s.mu.Unlock()
	views, err := s.store.Load()
	if err != nil {
		return []model.View{}
	}
	if views == nil {
		return []model.View{}
	}
	return views
}

// Get 返回单个视图。
func (s *ViewService) Get(name string) (model.View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	views, err := s.store.Load()
	if err != nil {
		return model.View{}, err
	}
	for _, v := range views {
		if v.Name == name {
			return v, nil
		}
	}
	return model.View{}, pkg.ErrNotFound("视图「%s」不存在", name)
}

// Create 新建视图（名称唯一；类型/正则/列均做校验）。
func (s *ViewService) Create(_ context.Context, in *model.View) (*model.View, error) {
	if err := validateView(in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	views, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	for _, v := range views {
		if v.Name == in.Name {
			return nil, pkg.ErrConflict("视图「%s」已存在", in.Name)
		}
	}
	now := time.Now()
	in.CreatedAt = now
	in.UpdatedAt = now
	views = append(views, *in)
	if err := s.store.Save(views); err != nil {
		return nil, err
	}
	return in, nil
}

// Update 编辑视图。名称不可修改（对齐 Jenkins 编辑视图），请求体中的 name 被忽略。
func (s *ViewService) Update(_ context.Context, name string, in *model.View) (*model.View, error) {
	// 先写入 URL 中的名称再校验（编辑页请求体不含 name）。
	in.Name = name
	if err := validateView(in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	views, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	for i := range views {
		if views[i].Name == name {
			in.CreatedAt = views[i].CreatedAt
			in.UpdatedAt = time.Now()
			views[i] = *in
			if err := s.store.Save(views); err != nil {
				return nil, err
			}
			return in, nil
		}
	}
	return nil, pkg.ErrNotFound("视图「%s」不存在", name)
}

// Delete 删除视图（不影响任务本身）。
func (s *ViewService) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	views, err := s.store.Load()
	if err != nil {
		return err
	}
	out := views[:0:0]
	found := false
	for _, v := range views {
		if v.Name == name {
			found = true
			continue
		}
		out = append(out, v)
	}
	if !found {
		return pkg.ErrNotFound("视图「%s」不存在", name)
	}
	return s.store.Save(out)
}

// JobNames 解析视图中实际展示的任务名（已去重、仅现存任务）：
//   - 我的视图：当前用户有 job.read 权限的全部任务（对齐 Jenkins「该视图自动显示
//     当前用户有权限访问的任务」）；
//   - 列表视图：显式任务 + 任务名正则匹配（忽略大小写），再按 job.read 权限过滤，
//     未授权任务一律隐藏，对齐 Jenkins 视图安全过滤。
func (s *ViewService) JobNames(ctx context.Context, view *model.View, user string) ([]string, error) {
	pipelines, err := s.pipelines.List(ctx)
	if err != nil {
		return nil, err
	}
	canRead := func(name string) bool {
		if s.rbac == nil {
			return true
		}
		return s.rbac.HasJobPermission(user, model.PermJobRead, name)
	}

	out := make([]string, 0, len(pipelines))
	if view.Type == model.ViewTypeMy {
		for _, p := range pipelines {
			if canRead(p.Name) {
				out = append(out, p.Name)
			}
		}
		return out, nil
	}

	// 列表视图：显式任务集合 + 正则匹配，二者取并集。
	var re *regexp.Regexp
	if view.JobRegex != "" {
		re, err = regexp.Compile("(?i)" + view.JobRegex)
		if err != nil {
			return nil, pkg.ErrBadRequest("任务正则表达式无效: %v", err)
		}
	}
	selected := make(map[string]bool, len(view.Jobs))
	for _, name := range view.Jobs {
		selected[name] = true
	}
	for _, p := range pipelines {
		if !selected[p.Name] && (re == nil || !re.MatchString(p.Name)) {
			continue
		}
		if !canRead(p.Name) {
			continue
		}
		out = append(out, p.Name)
	}
	return out, nil
}

// validateView 校验并规范化视图（名称/类型/正则/列/任务名去重）。
func validateView(in *model.View) error {
	if in == nil {
		return pkg.ErrBadRequest("视图不能为空")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return pkg.ErrBadRequest("视图名称不能为空")
	}
	// 视图名会出现在 URL 中，禁止路径分隔符等字符。
	if strings.ContainsAny(in.Name, `/\:*?"<>|`) {
		return pkg.ErrBadRequest(`视图名称不能包含 /\:*?"<>| 等字符`)
	}
	if in.Type == "" {
		in.Type = model.ViewTypeList
	}
	if in.Type != model.ViewTypeList && in.Type != model.ViewTypeMy {
		return pkg.ErrBadRequest("不支持的视图类型: %s", in.Type)
	}
	if in.JobRegex != "" {
		if _, err := regexp.Compile(in.JobRegex); err != nil {
			return pkg.ErrBadRequest("任务正则表达式无效: %v", err)
		}
	}
	// 任务名去重 + 去空白。
	jobs := make([]string, 0, len(in.Jobs))
	seen := make(map[string]bool, len(in.Jobs))
	for _, j := range in.Jobs {
		j = strings.TrimSpace(j)
		if j == "" || seen[j] {
			continue
		}
		seen[j] = true
		jobs = append(jobs, j)
	}
	in.Jobs = jobs
	// 列：为空取全部默认；非法列直接报错，避免静默丢弃。
	if len(in.Columns) == 0 {
		in.Columns = model.ViewColumns()
	} else {
		known := make(map[string]bool, 8)
		for _, c := range model.ViewColumns() {
			known[c] = true
		}
		for _, c := range in.Columns {
			if !known[c] {
				return pkg.ErrBadRequest("不支持的视图列: %s", c)
			}
		}
	}
	return nil
}
