package tools

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxDownloadBytes 限制单个工具包大小（JDK 约 190MB，留足余量）：
// 防止误配镜像返回一个巨大文件把磁盘写满。
const maxDownloadBytes = 1024 * 1024 * 1024 // 1GB

// homeMarker 记录解压后的实际主目录（相对安装目录），
// 有了它就不必每次重新猜「顶层是否只有一个目录」。
const homeMarker = ".ci-tool-home"

// 安装任务状态。下载是后台任务，页面只看状态：
// 关掉浏览器、切走页面都不影响，重新打开还能看到进度。
const (
	TaskQueued      = "queued"
	TaskDownloading = "downloading"
	TaskExtracting  = "extracting"
	TaskDone        = "done"
	TaskFailed      = "failed"
)

// Task 是一次工具安装的后台任务快照（可直接序列化给前端）。
type Task struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	State   string `json:"state"`
	// Downloaded/Total 用于显示进度条（Total 未知时为 0）。
	Downloaded int64     `json:"downloaded"`
	Total      int64     `json:"total"`
	Home       string    `json:"home,omitempty"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// taskState 是任务的可变内部状态。
type taskState struct {
	mu   sync.Mutex
	task Task
	done chan struct{}
	// ctx 让「构建等待安装」可以随构建取消而退出；安装本身用后台 ctx，不受请求影响。
	installCtx context.Context
}

// Installer 把自动安装的工具下载并解压到 <root>/<type>/<version>/。
type Installer struct {
	root   string
	mirror string
	client *http.Client
	logf   func(format string, args ...any)

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	tasks map[string]*taskState
	// bgCtx 是安装任务的生命周期上下文：与 HTTP 请求无关（页面关掉也继续装）。
	bgCtx  context.Context
	cancel context.CancelFunc
}

// NewInstaller 构造安装器。root 通常是 <DataHome>/tools。
func NewInstaller(root, mirror string, logf func(format string, args ...any)) *Installer {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	bgCtx, cancel := context.WithCancel(context.Background())
	return &Installer{
		root:   root,
		mirror: mirror,
		logf:   logf,
		// 下载大文件不能设总超时（JDK 上百 MB），但必须限制"连不上"的等待：
		// 否则内网环境会卡在无法访问的官方地址上，构建一直排队。
		client: &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				ResponseHeaderTimeout: 60 * time.Second,
				TLSHandshakeTimeout:   20 * time.Second,
			},
		},
		locks:  map[string]*sync.Mutex{},
		tasks:  map[string]*taskState{},
		bgCtx:  bgCtx,
		cancel: cancel,
	}
}

// Close 停止所有后台安装任务（进程退出时调用）。
func (i *Installer) Close() {
	if i.cancel != nil {
		i.cancel()
	}
}

// Mirror 返回配置的下载镜像前缀（空表示直连官方地址）。
func (i *Installer) Mirror() string {
	return i.mirror
}

// Dir 返回某工具某版本的安装目录（不含解压出的顶层目录）。
// 版本号里的 + 在 Windows 目录名里合法但容易踩坑，统一替换成 -。
func (i *Installer) Dir(toolType, version string) string {
	return filepath.Join(i.root, sanitize(toolType), sanitize(normalizeVersion(version)))
}

// Home 返回已安装工具的主目录；未安装返回 false。
func (i *Installer) Home(toolType, version string) (string, bool) {
	dir := i.Dir(toolType, version)
	data, err := os.ReadFile(filepath.Join(dir, homeMarker))
	if err != nil {
		return "", false
	}
	rel := strings.TrimSpace(string(data))
	if rel == "" || rel == "." {
		return dir, true
	}
	home := filepath.Join(dir, filepath.FromSlash(rel))
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		return "", false
	}
	return home, true
}

func (i *Installer) key(toolType, version string) string {
	return sanitize(toolType) + "@" + normalizeVersion(version)
}

// Start 启动一个后台安装任务并立刻返回任务快照。
//
// 关键点：安装用独立的后台 context 执行，不挂在 HTTP 请求上 ——
// 用户点了「立即下载」就关掉页面，安装也必须继续并最终可用。
// 同一版本重复点击只会复用同一个任务（不会并发下载同一份包）。
func (i *Installer) Start(toolType, version string) (Task, error) {
	toolType = strings.ToLower(strings.TrimSpace(toolType))
	version = normalizeVersion(version)
	tool, ver, ok := FindVersion(toolType, version)
	if !ok {
		return Task{}, fmt.Errorf("未知的工具或版本：%s %s", toolType, version)
	}
	if !tool.AutoInstall {
		return Task{}, fmt.Errorf("%s 不支持自动安装，请手动填写主目录", tool.Title)
	}
	// 已装好：直接返回成功快照（幂等，重复点击不会重新下载）。
	if home, ok := i.Home(toolType, version); ok {
		return Task{
			Type: toolType, Version: version, State: TaskDone, Home: home,
			StartedAt: time.Now(), UpdatedAt: time.Now(),
		}, nil
	}

	key := i.key(toolType, version)

	// 先看是否已有任务：运行中/排队/已完成的直接复用（单飞），失败过的允许重试。
	i.mu.Lock()
	existing := i.tasks[key]
	i.mu.Unlock()
	if existing != nil {
		snapshot := existing.snapshot()
		if snapshot.State != TaskFailed {
			return snapshot, nil
		}
	}

	state := &taskState{
		task: Task{
			Type: toolType, Version: version, State: TaskQueued,
			StartedAt: time.Now(), UpdatedAt: time.Now(),
		},
		done:       make(chan struct{}),
		installCtx: i.bgCtx,
	}

	i.mu.Lock()
	// 再检查一次：并发点击时可能已被别的请求创建（以先创建者为准）。
	if current, ok := i.tasks[key]; ok && current != existing {
		snapshot := current.snapshot()
		i.mu.Unlock()
		return snapshot, nil
	}
	i.tasks[key] = state
	i.mu.Unlock()

	go i.run(state, tool, ver)
	return state.snapshot(), nil
}

// Ensure 确保某工具某版本已安装，返回主目录。
// 已有后台任务时等它结束（构建因此能复用页面触发的下载进度）；
// 没有任务时直接启动一个。ctx 用于构建取消，不影响后台安装本身。
func (i *Installer) Ensure(ctx context.Context, toolType, version string) (string, error) {
	if home, ok := i.Home(toolType, version); ok {
		return home, nil
	}
	if _, err := i.Start(toolType, version); err != nil {
		return "", err
	}
	state := i.taskState(i.key(toolType, version))
	if state == nil {
		// 竞态：任务刚被清理，退回同步执行一次。
		return i.Ensure(ctx, toolType, version)
	}
	select {
	case <-state.done:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	snapshot := state.snapshot()
	if snapshot.State == TaskDone {
		return snapshot.Home, nil
	}
	if snapshot.Error == "" {
		snapshot.Error = "安装未完成"
	}
	return "", errors.New(snapshot.Error)
}

// Task 返回指定工具版本的安装任务快照。
func (i *Installer) Task(toolType, version string) (Task, bool) {
	state := i.taskState(i.key(toolType, version))
	if state == nil {
		return Task{}, false
	}
	return state.snapshot(), true
}

// Tasks 返回全部安装任务（运行中 + 最近结束的），按开始时间倒序。
func (i *Installer) Tasks() []Task {
	i.mu.Lock()
	states := make([]*taskState, 0, len(i.tasks))
	for _, state := range i.tasks {
		states = append(states, state)
	}
	i.mu.Unlock()

	out := make([]Task, 0, len(states))
	for _, state := range states {
		out = append(out, state.snapshot())
	}
	sort.SliceStable(out, func(a, b int) bool {
		return out[a].StartedAt.After(out[b].StartedAt)
	})
	return out
}

func (i *Installer) taskState(key string) *taskState {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.tasks[key]
}

// run 是后台任务主体：下载 → 解压 → 写标记 → 更新状态。
func (i *Installer) run(state *taskState, tool Tool, ver Version) {
	defer close(state.done)
	ctx := state.installCtx

	url, err := ver.DownloadURL(i.mirror)
	if err != nil {
		i.fail(state, err)
		return
	}
	dir := i.Dir(tool.Type, ver.Version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		i.fail(state, fmt.Errorf("创建工具目录: %w", err))
		return
	}

	i.setState(state, TaskDownloading, 0, 0)
	i.logf("开始下载 %s %s：%s", tool.Title, ver.Version, url)
	wantSHA := ver.SHA256[platformKey()]
	archive, err := i.download(ctx, url, wantSHA, state)
	if err != nil {
		// 老版本（Maven 3.6.3、Ant 1.9.16 等）已从 dlcdn 下架，只在归档站上；
		// 有备用地址就再试一次，别让用户为了一个 URL 变更去手动填路径。
		if fallback := ver.FallbackDownloadURL(i.mirror); fallback != "" {
			i.logf("主地址下载失败，改用备用地址：%s", fallback)
			archive, err = i.download(ctx, fallback, wantSHA, state)
		}
		if err != nil {
			i.fail(state, err)
			return
		}
	}
	defer func() { _ = os.Remove(archive) }()

	i.setState(state, TaskExtracting, state.task.Downloaded, state.task.Total)
	i.logf("开始解压 %s %s", tool.Title, ver.Version)
	if err := extract(archive, dir); err != nil {
		// 解压失败就清空目录，避免留下半个工具被当成「已安装」。
		_ = os.RemoveAll(dir)
		i.fail(state, err)
		return
	}

	rel := detectHome(dir)
	if err := os.WriteFile(filepath.Join(dir, homeMarker), []byte(filepath.ToSlash(rel)), 0o644); err != nil {
		i.fail(state, fmt.Errorf("写入安装标记: %w", err))
		return
	}
	home := filepath.Join(dir, rel)

	state.mu.Lock()
	state.task.State = TaskDone
	state.task.Home = home
	state.task.Error = ""
	state.task.UpdatedAt = time.Now()
	state.mu.Unlock()

	i.logf("安装完成 %s %s → %s", tool.Title, ver.Version, home)
}

// setState 更新状态；total 只在新下载开始时设置。
func (i *Installer) setState(state *taskState, name string, downloaded, total int64) {
	state.mu.Lock()
	state.task.State = name
	state.task.Downloaded = downloaded
	if total > 0 {
		state.task.Total = total
	}
	state.task.UpdatedAt = time.Now()
	state.mu.Unlock()
}

// fail 记录失败原因（页面与构建日志都能看到，而不是只留一个 404）。
func (i *Installer) fail(state *taskState, err error) {
	state.mu.Lock()
	state.task.State = TaskFailed
	state.task.Error = err.Error()
	state.task.UpdatedAt = time.Now()
	state.mu.Unlock()
	i.logf("安装失败 %s %s：%v", state.task.Type, state.task.Version, err)
}

// progress 在写入磁盘的同时更新下载进度。
type progressWriter struct {
	state *taskState
	// lastFlush 限流：每个 4KB 都加锁会让大文件下载变慢。
	lastFlush time.Time
}

func (p *progressWriter) Write(data []byte) (int, error) {
	p.state.mu.Lock()
	p.state.task.Downloaded += int64(len(data))
	if time.Since(p.lastFlush) > 200*time.Millisecond {
		p.state.task.UpdatedAt = time.Now()
		p.lastFlush = time.Now()
	}
	p.state.mu.Unlock()
	return len(data), nil
}

// download 把 URL 下载到临时文件，返回其路径。
// wantSHA 非空时校验 SHA-256；发布清单没有内置校验和时跳过（兼容现有版本清单）。
func (i *Installer) download(ctx context.Context, url, wantSHA string, state *taskState) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := i.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败（%s）：%w；如无法直连外网，请配置 -tools-mirror 镜像或改用手动填写主目录", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败：%s 返回 HTTP %d", url, resp.StatusCode)
	}

	if err := os.MkdirAll(i.root, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(i.root, "download-*")
	if err != nil {
		return "", err
	}
	// Content-Length 缺失时（分块传输）Total 保持 0，前端显示不确定进度。
	if resp.ContentLength > 0 {
		state.mu.Lock()
		state.task.Total = resp.ContentLength
		state.mu.Unlock()
	}
	written, copyErr := io.Copy(f, io.LimitReader(io.TeeReader(resp.Body, &progressWriter{state: state}), maxDownloadBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("写入下载文件: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(f.Name())
		return "", closeErr
	}
	if written > maxDownloadBytes {
		_ = os.Remove(f.Name())
		return "", errors.New("工具包超过 1GB，已中止下载（检查镜像地址是否返回了错误的文件）")
	}
	if wantSHA != "" {
		if err := verifySHA256(f.Name(), wantSHA); err != nil {
			_ = os.Remove(f.Name())
			return "", err
		}
		i.logf("SHA-256 校验通过")
	}
	i.logf("下载完成：%d 字节", written)
	return f.Name(), nil
}

// verifySHA256 校验文件摘要，避免镜像/网络篡改工具包。
func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("计算 SHA-256: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	want = strings.ToLower(strings.TrimSpace(want))
	if got != want {
		return fmt.Errorf("工具包 SHA-256 校验失败：期望 %s，实际 %s", want, got)
	}
	return nil
}

// snapshot 返回线程安全的状态副本。
func (s *taskState) snapshot() Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.task
}

// extract 根据文件魔数选择解压方式（zip / tar.gz），
// 这样就不依赖下载地址的扩展名（Windows 下载器常把文件名弄丢）。
func extract(archive, dest string) error {
	if isZip(archive) {
		return extractZip(archive, dest)
	}
	return extractTarGz(archive, dest)
}

func isZip(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 2)
	if _, err := io.ReadFull(f, head); err != nil {
		return false
	}
	return head[0] == 'P' && head[1] == 'K'
}

func extractZip(archive, dest string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("打开 zip: %w", err)
	}
	defer func() { _ = r.Close() }()
	for _, file := range r.File {
		// 符号链接可能指向安装目录外，后续写入会跟随链接逃逸；直接拒绝。
		if file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("压缩包包含符号链接，已拒绝：%s", file.Name)
		}
		target, err := safeJoin(dest, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		mode := file.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		err = writeFile(target, rc, mode)
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("解压 gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("读取 tar: %w", err)
		}
		target, err := safeJoin(dest, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(header.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			if err := writeFile(target, tr, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// 保留发行版需要的符号链接（Node 的 bin/npm 等），但目标必须仍在
			// 安装目录内；绝对路径或指向目录外的链接直接跳过。
			if !symlinkWithin(dest, target, header.Linkname) {
				continue
			}
			_ = os.Remove(target)
			_ = os.Symlink(filepath.FromSlash(header.Linkname), target)
		case tar.TypeLink:
			// 硬链接目标语义在不同 tar 实现中不一致，直接跳过，避免链接到目录外。
		default:
			// 设备文件/硬链接等一律忽略。
		}
	}
}

// symlinkWithin 判断 tar 中的相对链接目标是否仍在安装目录内。
func symlinkWithin(base, linkPath, linkname string) bool {
	name := strings.TrimSpace(linkname)
	if name == "" || filepath.IsAbs(filepath.FromSlash(name)) {
		return false
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(linkPath), filepath.FromSlash(name)))
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	resolvedAbs, err := filepath.Abs(resolved)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(baseAbs, resolvedAbs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, r); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// safeJoin 阻止 zip/tar 里的 ../ 造成目录穿越。
func safeJoin(base, name string) (string, error) {
	cleaned := filepath.Clean(filepath.Join(base, filepath.FromSlash(name)))
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	if targetAbs != baseAbs && !strings.HasPrefix(targetAbs, baseAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("压缩包包含非法路径：%s", name)
	}
	return targetAbs, nil
}

// detectHome 找出真正的主目录：
// 大多数发行版解压后只有一个顶层目录（如 jdk-17.0.12+7 / apache-maven-3.9.9），
// 那就用它；否则用安装目录本身。返回相对安装目录的路径。
func detectHome(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "."
	}
	var dirs []string
	for _, e := range entries {
		name := e.Name()
		if name == homeMarker || strings.HasPrefix(name, "download-") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, name)
			continue
		}
		return "." // 顶层有普通文件 → 主目录就是安装目录本身
	}
	sort.Strings(dirs)
	if len(dirs) == 1 {
		return dirs[0]
	}
	return "."
}

// sanitize 把版本号里的非法路径字符替换掉（Windows 上 + : * ? " < > | 都不合法）。
func sanitize(s string) string {
	s = strings.TrimSpace(s)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_",
		"+", "-", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return replacer.Replace(s)
}

// MarshalTask 把任务序列化成 JSON（供持久化/测试使用）。
func MarshalTask(task Task) ([]byte, error) {
	return json.Marshal(task)
}
