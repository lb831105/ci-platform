package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ci-platform/internal/pkg"
)

// Job 是一次待执行的构建任务。
type Job struct {
	PipelineID   string
	BuildID      string
	WorkspaceDir string // data/workspace 下的目录名；为空时退回 PipelineID
}

// BuildRunner 由 BuildService 实现，供 Scheduler 回调执行构建。
type BuildRunner interface {
	Run(ctx context.Context, job Job)
}

// Enqueuer 由 Scheduler 实现，供 BuildService 投递任务（避免两者强耦合）。
type Enqueuer interface {
	Enqueue(job Job) error
}

// WorkspaceManager 统一管理工作区路径命名，保证调度器与构建服务使用同一约定。
// 独立于 Scheduler，避免 BuildService 与 Scheduler 相互持有导致构造循环。
type WorkspaceManager struct {
	root string
}

// NewWorkspaceManager 创建workspace(path)解析器。
func NewWorkspaceManager(root string) *WorkspaceManager {
	return &WorkspaceManager{root: root}
}

// Scheduler 负责构建队列、worker 池与工作区定期清理。
// 队列满时立即返回业务错误，不阻塞调用方。
type Scheduler struct {
	queue     chan Job
	runner    BuildRunner
	workspace *WorkspaceManager
	ttl       time.Duration
	interval  time.Duration
	workers   int
	quiet     atomic.Bool // 准备关机：静默后拒绝新构建

	// 同一任务串行执行（Jenkins 语义），不同任务并行。
	// busy/waiting 避免“一个繁忙任务占用全部 worker”。
	mu      sync.Mutex
	wg      sync.WaitGroup
	busy    map[string]bool
	waiting map[string][]Job
	active  map[string]bool // 正在使用的工作区（清理时跳过）

	// PipelineID → 工作区目录名，用于清理时保护活动工作区。
	workspaceNames map[string]string

	// 返回当前仍存在的任务工作区目录集合；只清理不在集合中的孤儿目录。
	// nil 时退化为按 TTL 清理全部目录（用于测试或未注入的旧装配）。
	workspaceKeeper func() (map[string]bool, error)
}

// NewScheduler 创建调度器。
func NewScheduler(runner BuildRunner, queueSize int, workspace *WorkspaceManager, ttl, interval time.Duration) *Scheduler {
	if queueSize <= 0 {
		queueSize = 100
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Scheduler{
		queue:          make(chan Job, queueSize),
		runner:         runner,
		workspace:      workspace,
		ttl:            ttl,
		interval:       interval,
		busy:           make(map[string]bool),
		waiting:        make(map[string][]Job),
		active:         make(map[string]bool),
		workspaceNames: make(map[string]string),
	}
}

// Enqueue 投递构建任务，队列满或处于静默模式时返回业务错误。
// waiting 中的同任务构建也计入容量，避免繁忙任务通过 waiting 绕过队列上限。
func (s *Scheduler) Enqueue(job Job) error {
	if s.quiet.Load() {
		return pkg.ErrConflict("实例处于静默模式（准备关机），暂不接受新的构建任务")
	}
	s.mu.Lock()
	waiting := 0
	for _, list := range s.waiting {
		waiting += len(list)
	}
	if len(s.queue)+waiting >= cap(s.queue) {
		s.mu.Unlock()
		return pkg.ErrConflict("build queue is full (%d pending), please retry later", cap(s.queue))
	}
	s.mu.Unlock()
	select {
	case s.queue <- job:
		return nil
	default:
		return pkg.ErrConflict("build queue is full (%d pending), please retry later", cap(s.queue))
	}
}

// QueueLength 返回当前排队中的任务数（含同一任务的 waiting 队列）。
func (s *Scheduler) QueueLength() int {
	n := len(s.queue)
	s.mu.Lock()
	for _, list := range s.waiting {
		n += len(list)
	}
	s.mu.Unlock()
	return n
}

// QueueCapacity 返回队列容量。
func (s *Scheduler) QueueCapacity() int { return cap(s.queue) }

// Workers 返回 worker 数量。
func (s *Scheduler) Workers() int { return s.workers }

// IsQuiet 返回是否处于静默模式。
func (s *Scheduler) IsQuiet() bool { return s.quiet.Load() }

// SetQuiet 切换静默模式。
func (s *Scheduler) SetQuiet(quiet bool) { s.quiet.Store(quiet) }

// Start 启动 worker 池与工作区清理协程。
func (s *Scheduler) Start(ctx context.Context, workers int) {
	if workers <= 0 {
		workers = 1
	}
	s.workers = workers
	s.wg.Add(workers + 1)
	for i := 0; i < workers; i++ {
		go func(id int) {
			defer s.wg.Done()
			s.worker(ctx, id)
		}(i)
	}
	go func() {
		defer s.wg.Done()
		s.cleanupLoop(ctx)
	}()
	_ = pkg.EnsureDir(s.workspace.root)
}

// Drain 等待正在执行的构建结束（含同一任务的串行队列），供优雅停机使用。
// 不等待尚未被 worker 取出的队列任务；调用方应在 Drain 后取消 worker 上下文，
// 让这些任务保持 pending，由下次启动的 Recover 统一标记为中断。
func (s *Scheduler) Drain(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		running := len(s.busy) > 0
		s.mu.Unlock()
		if !running {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Wait 等待 worker 与清理协程退出；ctx 到期时返回，避免停机无限阻塞。
func (s *Scheduler) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// worker 串行消费队列；同一 pipeline 的多个构建由 process 串行执行。
func (s *Scheduler) worker(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-s.queue:
			if !ok {
				return
			}
			s.process(ctx, job)
		}
	}
}

// process 保证同一任务的构建串行执行，同时不让一个繁忙任务占满所有 worker：
// 若该任务已在执行，把作业放入其等待队列后立即返回，由正在执行的 worker 继续消费。
func (s *Scheduler) process(ctx context.Context, job Job) {
	s.mu.Lock()
	if s.busy[job.PipelineID] {
		s.waiting[job.PipelineID] = append(s.waiting[job.PipelineID], job)
		s.mu.Unlock()
		return
	}
	s.busy[job.PipelineID] = true
	s.workspaceNames[job.PipelineID] = workspaceDirOf(job)
	s.mu.Unlock()

	for {
		s.setActive(job.PipelineID, true)
		s.touchWorkspace(job)
		s.runner.Run(ctx, job)
		s.setActive(job.PipelineID, false)
		s.touchWorkspace(job)

		// 关闭中：不再继续消费该任务的排队构建，留在 pending，重启时统一标记中断。
		if ctx.Err() != nil {
			s.mu.Lock()
			delete(s.waiting, job.PipelineID)
			delete(s.busy, job.PipelineID)
			delete(s.workspaceNames, job.PipelineID)
			s.mu.Unlock()
			return
		}

		s.mu.Lock()
		list := s.waiting[job.PipelineID]
		if len(list) == 0 {
			delete(s.waiting, job.PipelineID)
			delete(s.busy, job.PipelineID)
			delete(s.workspaceNames, job.PipelineID)
			s.mu.Unlock()
			return
		}
		job = list[0]
		s.workspaceNames[job.PipelineID] = workspaceDirOf(job)
		s.waiting[job.PipelineID] = list[1:]
		s.mu.Unlock()
	}
}

// setActive 标记任务工作区正在使用。
func (s *Scheduler) setActive(pipelineID string, active bool) {
	s.mu.Lock()
	if active {
		s.active[pipelineID] = true
	} else {
		delete(s.active, pipelineID)
	}
	s.mu.Unlock()
}

// touchWorkspace 构建结束后刷新工作区目录的修改时间。
func (s *Scheduler) touchWorkspace(job Job) {
	path := s.workspace.Path(job)
	if !pkg.Exists(path) {
		return
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

// SetWorkspaceKeeper 注入当前仍存在任务的工作区目录集合。
// 仍存在的任务不参与 TTL 清理，这样 node_modules / target / dist 等缓存不会被定时任务清掉。
func (s *Scheduler) SetWorkspaceKeeper(fn func() (map[string]bool, error)) {
	s.workspaceKeeper = fn
}

// cleanupLoop 周期性清理超过 TTL 的工作区目录。
func (s *Scheduler) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupOnce()
		}
	}
}

// cleanupOnce 删除 mtime 早于 TTL 的工作区。
func (s *Scheduler) cleanupOnce() {
	entries, err := os.ReadDir(s.workspace.root)
	if err != nil {
		return
	}
	deadline := time.Now().Add(-s.ttl)

	// 跳过正在执行或排队中的任务，避免误删活动工作区。
	s.mu.Lock()
	protected := make(map[string]bool, len(s.active)+len(s.busy))
	for id := range s.active {
		if dir := s.workspaceNames[id]; dir != "" {
			protected[dir] = true
		}
	}
	for id := range s.busy {
		if dir := s.workspaceNames[id]; dir != "" {
			protected[dir] = true
		}
	}
	s.mu.Unlock()

	// 仍存在的任务保留工作区；读取失败时放弃本轮清理，避免误删。
	var keep map[string]bool
	if s.workspaceKeeper != nil {
		keep, err = s.workspaceKeeper()
		if err != nil {
			return
		}
	}

	for _, e := range entries {
		// 跳过隐藏目录：Git 更新期间会把构建产物临时放到 .ci-untracked-*。
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || protected[e.Name()] {
			continue
		}
		// 任务仍然存在：保留工作区以支持增量构建。
		// 只清理已删除任务遗留的孤儿目录，避免磁盘无限增长。
		if keep[e.Name()] {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(deadline) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.workspace.root, e.Name()))
	}
}
