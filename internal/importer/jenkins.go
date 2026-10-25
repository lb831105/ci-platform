// Package importer 把外部 CI 的任务定义转换成平台的 model.Pipeline。
//
// 设计取舍：平台存储始终是自己的 JSON（config.json），**不复制 Jenkins 的
// XML 结构**。Jenkins 的 XML 是「插件类名 + 字段名」的序列化结果，既不是稳定
// 契约，也无法直接驱动我们的执行器；因此这里做的是「语义翻译」：
// 把 Jenkins 的 builder/trigger/parameter/publisher 翻译成平台字段，
// 并把无法翻译的部分以告警形式交给用户，绝不静默丢弃。
package importer

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// 告警类型与级别统一由 pkg 提供：Jenkins 导入与平台配置导入共用同一份契约，
// 前端只需一种渲染逻辑。
type Warning = pkg.Warning

const (
	LevelError = pkg.LevelError
	LevelWarn  = pkg.LevelWarn
	LevelInfo  = pkg.LevelInfo
)

// SshServerRef 是平台已配置的 SSH 服务器摘要，用于把 Jenkins 的
// Publish Over SSH「服务器名」自动映射到 server_id。
type SshServerRef struct {
	ID   string
	Name string
	Host string
}

// warnings 是解析过程中的告警收集器。
type warnings struct {
	list []Warning
}

func (w *warnings) add(level, field, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	for _, item := range w.list {
		// 同一字段的同类告警只保留第一条，避免 50 个参数刷屏。
		if item.Level == level && item.Field == field && item.Message == msg {
			return
		}
	}
	w.list = append(w.list, Warning{Field: field, Level: level, Message: msg})
}

func (w *warnings) errorf(field, format string, args ...interface{}) {
	w.add(LevelError, field, format, args...)
}

func (w *warnings) warnf(field, format string, args ...interface{}) {
	w.add(LevelWarn, field, format, args...)
}

func (w *warnings) infof(field, format string, args ...interface{}) {
	w.add(LevelInfo, field, format, args...)
}

// FromJenkinsXML 解析 Jenkins 的 config.xml，返回平台任务定义与告警列表。
// servers 用于把 SSH 发布目标映射到平台已配置的服务器（可为空）。
func FromJenkinsXML(data []byte, servers []SshServerRef) (*model.Pipeline, []Warning, error) {
	root, err := parse(data)
	if err != nil {
		return nil, nil, err
	}
	var w warnings
	// 任务类型：Jenkinsfile / 多分支 / 文件夹 无法用「步骤列表」表达。
	switch kind := strings.ToLower(root.XMLName.Local); {
	case strings.Contains(kind, "flow-definition"):
		return nil, nil, pkg.ErrBadRequest("这是 Jenkinsfile/Pipeline 任务（flow-definition），平台不支持 pipeline-as-code，无法自动迁移；请把脚本拆成 shell 步骤后手工创建")
	case strings.Contains(kind, "multibranch"):
		return nil, nil, pkg.ErrBadRequest("这是多分支流水线（multibranch），平台是「一个任务一个分支」，请为每个分支分别创建任务")
	case strings.Contains(kind, "folder"):
		return nil, nil, pkg.ErrBadRequest("这是一个文件夹（folder），请直接导入其中的具体任务")
	}

	p := &model.Pipeline{GitBranch: "main"}
	if p.Description = strings.TrimSpace(root.value("description")); p.Description != "" {
		// Jenkins 描述常含 HTML，这里不做渲染（前端按纯文本展示）。
	}
	scanScm(root, p, &w)
	scanBuilders(root, p, &w)
	scanTriggers(root, p, &w)
	scanParams(root, p, &w)
	scanArtifacts(root, p, &w)
	scanRetention(root, p, &w)
	scanSshPublishers(root, p, servers, &w)
	scanMisc(root, &w)

	if len(p.Steps) == 0 {
		// 没有可识别的 builder（如纯插件构建）：给出占位步骤并明确告警，
		// 保证导入后任务可打开、可编辑，而不是静默变成一个空任务。
		p.Steps = []model.Step{{Name: "todo", Run: "echo \"TODO: Jenkins 构建步骤无法自动转换，请在此补充命令\""}}
		w.errorf("builders", "没有可识别的构建步骤（可能使用了未支持的插件），已插入占位步骤，请补充实际命令")
	}
	return p, w.list, nil
}

// ---- XML 通用节点 ----

// node 是「与插件类名无关」的通用 XML 节点：Jenkins 的标签名带着插件类名，
// 用固定 struct tag 解析会被插件版本差异击穿，逐节点匹配更稳。
type node struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Text     string     `xml:",chardata"`
	Children []node     `xml:",any"`
}

func parse(data []byte) (*node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, pkg.ErrBadRequest("XML 内容为空")
	}
	data = normalizeXMLDeclaration(data)
	dec := xml.NewDecoder(bytes.NewReader(data))
	// 非严格模式：Jenkins 导出的 XML 可能包含未声明的实体引用。
	dec.Strict = false
	dec.CharsetReader = func(label string, _ io.Reader) (io.Reader, error) {
		return nil, fmt.Errorf("不支持的编码 %q，请以 UTF-8 导出 config.xml", label)
	}
	var root node
	if err := dec.Decode(&root); err != nil {
		return nil, pkg.ErrBadRequest("config.xml 解析失败：%v", err)
	}
	return &root, nil
}

// normalizeXMLDeclaration 把 XML 声明里的版本号改写为 1.0。
//
// Jenkins 2.x 起导出的 config.xml 用 `<?xml version='1.1' encoding='UTF-8'?>`，
// 而 Go 的 encoding/xml 只接受 1.0（报 "unsupported version \"1.1\""）。
// 1.1 相比 1.0 主要是放宽了控制字符与行尾规则，Jenkins 实际写出的内容
// 并不依赖 1.1 独有语义，因此改写声明是安全的（不改写会直接导入失败）。
func normalizeXMLDeclaration(data []byte) []byte {
	// 去掉声明前的空白（含 BOM 后的换行），再判断是否是 XML 声明。
	trimmed := bytes.TrimLeftFunc(data, func(r rune) bool {
		return r == 9 || r == 10 || r == 13 || r == 32
	})
	if !bytes.HasPrefix(trimmed, []byte("<?xml")) {
		return data
	}
	end := bytes.Index(trimmed, []byte("?>"))
	if end < 0 {
		return data
	}
	decl := string(trimmed[:end])
	start := strings.Index(decl, "version")
	if start < 0 {
		return data
	}
	rest := decl[start+len("version"):]
	eq := strings.Index(rest, "=")
	if eq < 0 {
		return data
	}
	// 引号前允许空白；去掉空白后第一个字符必须是单/双引号。
	value := strings.TrimSpace(rest[eq+1:])
	if len(value) == 0 {
		return data
	}
	quote := value[0]
	if quote != byte(39) && quote != byte(34) {
		return data
	}
	closeIdx := strings.IndexByte(value[1:], quote)
	if closeIdx < 0 {
		return data
	}
	// 保留引号后的其它伪属性（encoding 等）。closeIdx 是 value[1:] 中的位置，
	// 因此闭合引号在 value[1+closeIdx]，其后内容从 value[2+closeIdx] 开始。
	fixed := decl[:start] + "version=" + string(quote) + "1.0" + string(quote) + value[2+closeIdx:]
	return []byte(fixed + string(trimmed[end:]))
}

func (n *node) name() string { return strings.ToLower(n.XMLName.Local) }

// matches 判断节点名是否以给定后缀结尾（忽略大小写）。
// Jenkins 用完整类名做标签，例如 jenkins.plugins.publish__over__ssh.BapSshPublisher。
func (n *node) matches(suffix string) bool {
	return strings.HasSuffix(n.name(), strings.ToLower(suffix))
}

// child 返回第一个名称匹配的子节点。
func (n *node) child(suffixes ...string) *node {
	for i := range n.Children {
		for _, suffix := range suffixes {
			if n.Children[i].matches(suffix) {
				return &n.Children[i]
			}
		}
	}
	return nil
}

// children 返回所有名称匹配的子节点。
func (n *node) children(suffixes ...string) []*node {
	out := make([]*node, 0, len(n.Children))
	for i := range n.Children {
		for _, suffix := range suffixes {
			if n.Children[i].matches(suffix) {
				out = append(out, &n.Children[i])
				break
			}
		}
	}
	return out
}

// value 返回第一个匹配子节点的文本（顺带 trim）。
func (n *node) value(suffixes ...string) string {
	if c := n.child(suffixes...); c != nil {
		return strings.TrimSpace(c.Text)
	}
	return ""
}

// attr 返回属性值（Jenkins 有些字段用属性而非子节点）。
func (n *node) attr(name string) string {
	for _, a := range n.Attrs {
		if strings.EqualFold(a.Name.Local, name) {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

// find 深度优先查找第一个匹配后缀的节点。
func (n *node) find(suffix string) *node {
	if n.matches(suffix) {
		return n
	}
	for i := range n.Children {
		if found := n.Children[i].find(suffix); found != nil {
			return found
		}
	}
	return nil
}

// findAll 深度优先收集所有匹配后缀的节点。
func (n *node) findAll(suffix string) []*node {
	out := make([]*node, 0, 4)
	var walk func(*node)
	walk = func(cur *node) {
		if cur.matches(suffix) {
			out = append(out, cur)
		}
		for i := range cur.Children {
			walk(&cur.Children[i])
		}
	}
	walk(n)
	return out
}

// ---- 各配置段翻译 ----

// scanScm 把 GitSCM 映射为平台唯一的 git_url / git_branch / 凭据引用。
func scanScm(root *node, p *model.Pipeline, w *warnings) {
	scm := root.child("scm")
	if scm == nil {
		return
	}
	if strings.Contains(scm.name(), "nullscm") {
		return
	}
	git := strings.Contains(scm.name(), "gitscm") || scm.find("userremoteconfigs") != nil
	if !git {
		w.errorf("scm", "不支持的源码管理类型 %q（平台仅支持 Git），构建步骤仍会创建，请手工补充仓库地址", shortClass(scm.XMLName.Local))
		return
	}

	urls := scm.findAll("url")
	if len(urls) == 0 {
		w.warnf("scm", "GitSCM 未配置仓库地址（<url>），任务将按「无源码管理」创建")
		return
	}
	if len(urls) > 1 {
		w.warnf("scm", "配置了 %d 个远端仓库，只迁移第一个 %q", len(urls), urls[0].Text)
	}
	p.GitURL = strings.TrimSpace(urls[0].Text)

	// 凭据不可解密：Jenkins 用 master key 加密，导入时只能提示重建。
	// credentialsId 在 userRemoteConfigs/*/credentialsId 下（不是 scm 的直接子节点）。
	if el := scm.find("credentialsid"); el != nil {
		id := strings.TrimSpace(el.Text)
		if id != "" {
			w.warnf("scm", "Git 凭据 credentialsId=%s 无法迁移（Jenkins 加密存储），请在「系统管理 → 凭据」重建并在任务里重新选择", id)
		}
	}

	// 分支写在 <branches><hudson.plugins.git.BranchSpec><name>*/main</name> 里。
	branches := make([]string, 0, len(scm.findAll("branchspec")))
	for _, spec := range scm.findAll("branchspec") {
		if name := spec.value("name"); name != "" {
			branches = append(branches, name)
		}
	}
	if len(branches) > 0 {
		if len(branches) > 1 {
			w.warnf("scm", "配置了多个分支 %v，平台一个任务只跟一个分支，已取 %q；其余分支请另建任务", branches, branches[0])
		}
		p.GitBranch = normalizeBranch(branches[0])
	}
	if strings.TrimSpace(p.GitBranch) == "" {
		p.GitBranch = "main"
	}
}

// normalizeBranch 去掉 Jenkins 常见的前缀写法（*/main、refs/heads/main）。
func normalizeBranch(spec string) string {
	spec = strings.TrimSpace(spec)
	for _, prefix := range []string{"*/", "refs/heads/", "origin/"} {
		if strings.HasPrefix(spec, prefix) {
			spec = strings.TrimPrefix(spec, prefix)
		}
	}
	// 排除规则（:refs/heads/xxx）与通配符无法表达，交给调用方处理；
	// 这里只保留第一段，避免把「main :pr-*」整串塞进分支名。
	if i := strings.IndexAny(spec, " \t"); i >= 0 {
		spec = spec[:i]
	}
	return strings.TrimSpace(spec)
}

// scanBuilders 把 builders 下的插件 builder 逐个翻译成平台的步骤。
func scanBuilders(root *node, p *model.Pipeline, w *warnings) {
	builders := root.child("builders")
	if builders == nil {
		return
	}
	counter := map[string]int{}
	nextName := func(kind string) string {
		counter[kind]++
		return fmt.Sprintf("%s-%d", kind, counter[kind])
	}
	for i := range builders.Children {
		b := &builders.Children[i]
		// Publish Over SSH 也可能作为「构建步骤」出现（BapSshBuilderPlugin）。
		// 它由 scanSshPublishers 统一翻译成发布目标，这里跳过，避免报「不受支持的步骤」。
		if b.find("bapssh"+"publisher") != nil {
			continue
		}
		kind := stepKind(b)
		switch kind {
		case "shell":
			if cmd := b.value("command"); cmd != "" {
				p.Steps = append(p.Steps, model.Step{Name: nextName("shell"), Run: cmd})
			}
		case "batch":
			if cmd := b.value("command"); cmd != "" {
				// Windows 批处理：平台在 Windows 上以 cmd 执行多行脚本。
				p.Steps = append(p.Steps, model.Step{Name: nextName("batch"), Run: cmd})
			}
		case "powershell":
			if cmd := b.value("command"); cmd != "" {
				w.warnf("builders", "PowerShell 构建步骤依赖「系统配置 → 执行 shell」为 PowerShell；若默认是 cmd.exe 请先切换，否则该步骤会失败")
				p.Steps = append(p.Steps, model.Step{Name: nextName("powershell"), Run: cmd})
			}
		case "maven":
			run := "mvn " + strings.TrimSpace(b.value("targets"))
			if pom := b.value("pom"); pom != "" {
				run += " -f " + pom
			}
			if name := b.value("mavenName"); name != "" {
				w.infof("builders", "Maven 安装项 %q 无法迁移，请确认「全局工具配置」中的 Maven 主目录已填写（构建用 mvn 命令）", name)
			}
			if bt := b.value("buildSuccessThreshold"); bt != "" {
				w.warnf("builders", "Maven 成功阈值 %s（UNSTABLE 语义）不受支持，构建只会是成功/失败", bt)
			}
			p.Steps = append(p.Steps, model.Step{Name: nextName("maven"), Run: run})
		case "gradle":
			exe := "gradle"
			if wrapper := b.value("useWrapper"); strings.EqualFold(wrapper, "true") {
				exe = "./gradlew"
			}
			run := exe + " " + strings.TrimSpace(b.value("tasks"))
			if name := b.value("gradleName"); name != "" {
				w.infof("builders", "Gradle 安装项 %q 无法迁移，请确认「全局工具配置」中的 Gradle 主目录已填写", name)
			}
			p.Steps = append(p.Steps, model.Step{Name: nextName("gradle"), Run: run})
		case "ant":
			run := "ant " + strings.TrimSpace(b.value("targets"))
			if buildFile := b.value("buildFile"); buildFile != "" {
				run += " -f " + buildFile
			}
			if name := b.value("antName"); name != "" {
				w.infof("builders", "Ant 安装项 %q 无法迁移，请确认「全局工具配置」中的 Ant 主目录已填写", name)
			}
			p.Steps = append(p.Steps, model.Step{Name: nextName("ant"), Run: run})
		default:
			w.warnf("builders", "构建步骤 %q 不受支持，已跳过（如需保留请改为 shell 命令）", shortClass(b.XMLName.Local))
		}
	}
}

// stepKind 把插件类名归类为平台能表达的步骤类型。
func stepKind(n *node) string {
	name := n.name()
	// 去掉命名空间前缀，只看类名尾部。
	base := name[strings.LastIndex(name, ".")+1:]
	switch {
	case strings.Contains(base, "shell") && strings.HasSuffix(base, "shell"):
		return "shell"
	case strings.Contains(base, "batchfile"):
		return "batch"
	case strings.Contains(base, "powershell"):
		return "powershell"
	case strings.Contains(base, "maven"):
		return "maven"
	case strings.Contains(base, "gradle"):
		return "gradle"
	case strings.Contains(base, "ant"):
		return "ant"
	}
	return ""
}

// shortClass 只保留类名最后一段，避免告警里塞满完整包名。
func shortClass(tag string) string {
	if i := strings.LastIndex(tag, "."); i >= 0 && i+1 < len(tag) {
		return tag[i+1:]
	}
	return tag
}

// scanTriggers 翻译定时触发器；其它触发器（SCM 轮询等）在平台由 Webhook 代替。
func scanTriggers(root *node, p *model.Pipeline, w *warnings) {
	triggers := root.child("triggers")
	if triggers == nil {
		return
	}
	var specs []string
	for i := range triggers.Children {
		t := &triggers.Children[i]
		switch {
		case strings.Contains(t.name(), "timertrigger"):
			if spec := t.value("spec"); spec != "" {
				specs = append(specs, spec)
			}
		case strings.Contains(t.name(), "scmtrigger"):
			w.infof("triggers", "SCM 轮询（Poll SCM）已忽略：平台通过 Webhook 触发，请在任务配置里生成 Webhook 地址")
		default:
			w.warnf("triggers", "触发器 %q 不受支持，已忽略（可用 Webhook 或定时构建替代）", shortClass(t.XMLName.Local))
		}
	}
	if len(specs) == 0 {
		return
	}
	if len(specs) > 1 {
		w.warnf("triggers", "配置了 %d 个定时触发器，平台每个任务只有一条 cron 规则，已取第一个 %q", len(specs), specs[0])
	}
	if spec, changed := normalizeCronSpec(specs[0], w); spec != "" {
		p.CronSpec = spec
		if changed {
			w.infof("triggers", "Jenkins cron %q 已转换为 %q", specs[0], spec)
		}
	}
}

// normalizeCronSpec 处理 Jenkins 特有的写法：TZ= 前缀与 @daily 之类别名。
func normalizeCronSpec(spec string, w *warnings) (string, bool) {
	original := spec
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", false
	}
	// TZ=Asia/Shanghai H 2 * * * → 平台按服务器本地时区执行，时区前缀只能丢弃。
	if strings.HasPrefix(strings.ToUpper(spec), "TZ=") {
		fields := strings.Fields(spec)
		if len(fields) > 1 {
			w.warnf("triggers", "定时触发器带时区前缀 %q：平台按服务器本地时区执行，请确认服务器时区一致", fields[0])
			spec = strings.Join(fields[1:], " ")
		}
	}
	aliases := map[string]string{
		"@hourly":   "H * * * *",
		"@daily":    "H H * * *",
		"@midnight": "H H * * *",
		"@weekly":   "H H * * 0",
		"@monthly":  "H H 1 * *",
		"@yearly":   "H H 1 1 *",
		"@annually": "H H 1 1 *",
	}
	if mapped, ok := aliases[strings.ToLower(spec)]; ok {
		return mapped, true
	}
	return spec, spec != original
}

// scanParams 把 parameterDefinitions 翻译成平台的 params（类型/默认值/可选值）。
func scanParams(root *node, p *model.Pipeline, w *warnings) {
	defs := root.find("parameterdefinitions")
	if defs == nil {
		return
	}
	used := map[string]bool{}
	for i := range defs.Children {
		d := &defs.Children[i]
		rawName := strings.TrimSpace(d.value("name"))
		if rawName == "" {
			continue
		}
		name, ok := sanitizeParamName(rawName)
		if !ok {
			w.warnf("params", "参数名 %q 含非法字符，已重命名为 %q（参数会作为环境变量注入，必须是合法标识符）", rawName, name)
		}
		name = dedupeParamName(name, used)
		used[strings.ToUpper(name)] = true

		def := model.ParamDef{Name: name}
		if desc := strings.TrimSpace(d.value("description")); desc != "" {
			def.Description = desc
		}
		kind := d.name()
		switch {
		case strings.Contains(kind, "booleanparameter"):
			def.Type = model.ParamBool
			def.Default = "true"
			if v := d.value("defaultvalue"); v != "" {
				def.Default = strings.ToLower(v)
			}
		case strings.Contains(kind, "choiceparameter"):
			def.Type = model.ParamChoice
			for _, opt := range d.findAll("string") {
				if v := strings.TrimSpace(opt.Text); v != "" {
					def.Options = append(def.Options, v)
				}
			}
			if v := strings.TrimSpace(d.value("defaultvalue")); v != "" {
				def.Default = v
			} else if len(def.Options) > 0 {
				// Jenkins 的 Choice 没有默认值字段，运行时默认选中第一项。
				def.Default = def.Options[0]
			}
			if len(def.Options) == 0 {
				w.errorf("params", "参数 %q 是 Choice 类型但没有可选值，已降级为字符串", rawName)
				def.Type = model.ParamString
			}
		case strings.Contains(kind, "textparameter"):
			def.Type = model.ParamText
			def.Default = d.value("defaultvalue")
		case strings.Contains(kind, "passwordparameter"):
			def.Type = model.ParamString
			def.Default = d.value("defaultvalue")
			// 我们的参数值会明文进环境变量与构建记录，不做遮罩。
			w.errorf("params", "参数 %q 是密码类型：平台暂不支持参数脱敏（值会出现在构建记录/日志中），已降级为字符串，请改用凭据或从外部注入", rawName)
		case strings.Contains(kind, "fileparameter"):
			w.errorf("params", "参数 %q 是文件参数（File Parameter），平台不支持上传文件参数，已跳过", rawName)
			continue
		case strings.Contains(kind, "runparameter"):
			w.errorf("params", "参数 %q 是 Run Parameter（引用其它任务构建），平台不支持，已跳过", rawName)
			continue
		case strings.Contains(kind, "stringparameter"):
			def.Type = model.ParamString
			def.Default = d.value("defaultvalue")
		default:
			def.Type = model.ParamString
			def.Default = d.value("defaultvalue")
			w.warnf("params", "参数 %q 的类型 %q 不受支持，已按字符串处理", rawName, shortClass(d.XMLName.Local))
		}
		p.Params = append(p.Params, def)
	}
	if len(p.Params) > 0 {
		w.infof("params", "参数值会以「参数名」与 CI_PARAM_<大写名> 两种环境变量注入步骤，脚本里请按需替换（$NAME 或 %%NAME%%）")
	}
}

// sanitizeParamName 把任意名字转成合法标识符（字母/数字/下划线，非数字开头）。
func sanitizeParamName(raw string) (string, bool) {
	var b strings.Builder
	changed := false
	for i, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteString("P_")
				changed = true
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
			changed = true
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		name = "PARAM"
		changed = true
	}
	// CI_ 前缀是平台保留命名空间（会覆盖 CI_BUILD_ID 等内置变量）。
	if strings.HasPrefix(strings.ToUpper(name), "CI_") {
		name = "J_" + name
		changed = true
	}
	if len(name) > 64 {
		name = name[:64]
		changed = true
	}
	return name, !changed
}

// dedupeParamName 处理大小写不敏感的重名（Windows 环境变量不区分大小写）。
func dedupeParamName(name string, used map[string]bool) string {
	candidate := name
	for i := 2; used[strings.ToUpper(candidate)]; i++ {
		candidate = fmt.Sprintf("%s_%d", name, i)
	}
	return candidate
}

// scanArtifacts 把 ArtifactArchiver 的逗号分隔 glob 翻译成平台产物归档配置。
func scanArtifacts(root *node, p *model.Pipeline, w *warnings) {
	archiver := root.find("artifactarchiver")
	if archiver == nil {
		return
	}
	raw := archiver.value("artifacts")
	files := splitCommaList(raw)
	if len(files) == 0 {
		w.warnf("artifacts", "产物归档配置为空，已跳过")
		return
	}
	p.Artifacts = model.ArtifactConfig{Enabled: true, Files: files}
	if v := archiver.value("allowEmptyArchive"); strings.EqualFold(v, "true") {
		w.infof("artifacts", "Jenkins 允许空归档（allowEmptyArchive），平台要求至少匹配一个文件，否则构建失败")
	}
}

// scanRetention 翻译 logRotator（丢弃旧的构建）。
func scanRetention(root *node, p *model.Pipeline, w *warnings) {
	rot := root.find("logrotator")
	if rot == nil {
		// 新版导出的结构是 <strategy class="hudson.tasks.LogRotator">。
		for _, candidate := range root.findAll("strategy") {
			if strings.Contains(strings.ToLower(candidate.attr("class")), "logrotator") {
				rot = candidate
				break
			}
		}
	}
	if rot == nil {
		return
	}
	numToKeep := firstInt(rot.value("numToKeep"), rot.attr("numToKeep"))
	daysToKeep := firstInt(rot.value("daysToKeep"), rot.attr("daysToKeep"))
	if numToKeep > 0 {
		p.BuildRetentionMaxPerJob = numToKeep
	}
	if daysToKeep > 0 {
		p.BuildRetentionMaxAgeDays = daysToKeep
	}
	if v := rot.attr("artifactNumToKeep"); v != "" {
		w.infof("retention", "产物保留数量 %s 不受支持：平台的产物随构建一起保留/删除", v)
	}
}

// firstInt 取第一个可解析的正整数。
func firstInt(values ...string) int {
	for _, v := range values {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// scanSshPublishers 把 Publish Over SSH 翻译成平台的 ssh_publishes（可多目标）。
// Jenkins 里「服务器名 + transfers」是一个 publisher；平台里一个目标 = 服务器 + 文件 + 远程目录 + 命令，
// 因此每个 transfer 变成一个目标。
func scanSshPublishers(root *node, p *model.Pipeline, servers []SshServerRef, w *warnings) {
	// 扫描整棵文档：Publish Over SSH 既可能是「构建后操作」（publishers），
	// 也可能是「构建步骤」（builders 里的 BapSshBuilderPlugin）。
	var targets []*node
	inBuilders := false
	var walk func(*node, bool)
	walk = func(cur *node, underBuilders bool) {
		if cur.matches("bapssh" + "publisher") {
			targets = append(targets, cur)
			if underBuilders {
				inBuilders = true
			}
			return
		}
		for i := range cur.Children {
			walk(&cur.Children[i], underBuilders || cur.matches("builders"))
		}
	}
	walk(root, false)
	if len(targets) == 0 {
		return
	}
	if inBuilders {
		w.infof("ssh", "Jenkins 中该发布位于「构建步骤」中间，平台统一在构建成功后发布；如需严格顺序请拆成两个任务")
	}

	index := 0
	for _, pub := range targets {
		serverName := pub.value("configName")
		server, matched := matchSshServer(serverName, servers)
		transfers := pub.findAll("bapsshtransfer")
		if len(transfers) == 0 {
			// 老版本直接把字段放在 publisher 上。
			transfers = []*node{pub}
		}
		for _, t := range transfers {
			index++
			cfg := model.SshPublishConfig{
				Name:          strings.TrimSpace(serverName),
				RemoteBaseDir: t.value("remoteDirectory"),
				RemovePrefix:  t.value("removePrefix"),
				ExecCommand:   t.value("execCommand"),
				SourceFiles:   splitCommaList(t.value("sourceFiles")),
				CleanRemote:   boolValue(t.value("cleanRemote")),
				Flatten:       boolValue(t.value("flatten")),
			}
			// execTimeout 在 Jenkins 里是毫秒；平台统一用秒（0 表示沿用构建级超时）。
			if raw := t.value("execTimeout"); raw != "" {
				if ms, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && ms > 0 {
					cfg.ExecTimeoutSec = (ms + 999) / 1000
					if ms%1000 != 0 {
						w.infof("ssh", "发布目标 %q 的命令超时 %dms 已向上取整为 %d 秒", cfg.Name, ms, cfg.ExecTimeoutSec)
					}
				}
			}
			warnUnsupportedTransferOptions(t, cfg.Name, w)
			if cfg.Name == "" {
				cfg.Name = fmt.Sprintf("发布 %d", index)
			} else if len(transfers) > 1 {
				// 同一服务器多个 transfer：平台的目标名必须唯一。
				cfg.Name = fmt.Sprintf("%s-%d", cfg.Name, index)
			}
			if len(cfg.SourceFiles) == 0 {
				w.warnf("ssh", "发布目标 %q 没有配置 sourceFiles，已跳过", cfg.Name)
				continue
			}
			if matched {
				cfg.ServerID = server.ID
				cfg.Enabled = true
			} else {
				// 找不到服务器时**不启用**：没有目标主机的发布配置一旦启用，
				// 下一次构建就会因为「服务器为空」失败。
				cfg.Enabled = false
				cfg.Host = ""
				w.errorf("ssh", "发布目标 %q 引用的 Jenkins 服务器 %q 在平台「系统配置 → SSH 服务器」中不存在，"+
					"该目标已保存为停用状态，请先创建同名服务器再启用", cfg.Name, serverName)
			}
			p.SshPublishes = append(p.SshPublishes, cfg)
		}
	}
	if len(p.SshPublishes) > 0 {
		w.infof("ssh", "SSH 发布已迁移为 %d 个目标；远程目录仍按「服务器默认目录 + 任务目录」拼接规则解析", len(p.SshPublishes))
	}
}

// boolValue 把 Jenkins 的 true/false 文本转成 bool（缺失字段视为 false）。
func boolValue(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "true")
}

// warnUnsupportedTransferOptions 逐条提示「Jenkins 有、平台无」的 transfer 选项。
// 这些选项会改变部署结果（如日期目录、保留权限），静默忽略会让用户以为已经生效。
func warnUnsupportedTransferOptions(t *node, target string, w *warnings) {
	options := []struct {
		field string
		label string
	}{
		{"remoteDirectorySDF", "Remote directory is a date format（按日期建目录）"},
		{"usePty", "Exec PTY"},
		{"useSftpForExec", "Exec in SFTP"},
		{"keepFilePermissions", "Keep file permissions（保留文件权限）"},
		{"makeEmptyDirs", "Make empty dirs（保留空目录）"},
		{"noDefaultExcludes", "No default excludes"},
	}
	for _, opt := range options {
		if boolValue(t.value(opt.field)) {
			w.warnf("ssh", "发布目标 %q 的选项 %s 不受支持，已忽略；如有需要请在远程命令里自行处理", target, opt.label)
		}
	}
	if sep := strings.TrimSpace(t.value("patternSeparator")); sep != "" && sep != "[, ]+" && sep != "[,]+" {
		w.warnf("ssh", "发布目标 %q 使用了自定义文件分隔符 %q：平台的 sourceFiles 按逗号/分号/换行拆分", target, sep)
	}
	if boolValue(t.value("cleanRemote")) {
		w.infof("ssh", "发布目标 %q 勾选了「发布前清空远程目录」：该操作不可逆，请确认远程目录正确", target)
	}
}

// matchSshServer 先按名称、再按主机匹配平台已配置的 SSH 服务器。
func matchSshServer(name string, servers []SshServerRef) (SshServerRef, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return SshServerRef{}, false
	}
	for _, s := range servers {
		if strings.EqualFold(strings.TrimSpace(s.Name), name) {
			return s, true
		}
	}
	for _, s := range servers {
		if strings.EqualFold(strings.TrimSpace(s.Host), name) {
			return s, true
		}
	}
	return SshServerRef{}, false
}

// scanMisc 处理「平台没有对应能力」的常见配置，逐条给出告警。
func scanMisc(root *node, w *warnings) {
	if v := root.value("disabled"); strings.EqualFold(v, "true") {
		w.errorf("disabled", "该任务在 Jenkins 中处于禁用状态；平台没有「禁用任务」概念，导入后请确认是否需要手动触发")
	}
	if v := strings.ToLower(root.value("concurrentBuild", "concurrentBuilds")); v == "true" {
		w.warnf("concurrent", "Jenkins 允许并发构建；平台同一任务串行执行（避免多个构建同时写工作区）")
	}
	if v := root.value("jdk"); v != "" && !strings.EqualFold(v, "(System)") {
		w.infof("jdk", "JDK %q 未迁移：平台按「全局工具配置」中的 JDK 主目录（JAVA_HOME）选择版本", v)
	}
	if v := root.value("assignedNode"); v != "" {
		w.warnf("agent", "任务绑定节点 %q：平台只有单机执行，所有步骤都在服务器本地运行", v)
	}
	if v := root.value("quietPeriod"); v != "" && v != "5" {
		w.infof("quietPeriod", "静默期 %s 秒由平台全局「系统配置 → 静默期」统一控制", v)
	}
	for _, prop := range root.findAll("properties") {
		for i := range prop.Children {
			item := &prop.Children[i]
			switch {
			case strings.Contains(item.name(), "parametersdefinition"):
				// 已在 scanParams 处理。
			case strings.Contains(item.name(), "builddiscarder"):
				// 已在 scanRetention 处理。
			case strings.Contains(item.name(), "pathignore"):
				// 无副作用，忽略。
			case strings.Contains(item.name(), "connectionproperty"):
				w.infof("properties", "GitLab/GitHub 连接配置未迁移：平台用任务级 Webhook 地址触发，无需连接凭据")
			case strings.Contains(item.name(), "gitlab"), strings.Contains(item.name(), "github"),
				strings.Contains(item.name(), "gitee"), strings.Contains(item.name(), "webhook"):
				w.infof("properties", "代码平台推送触发器已忽略：请在任务配置里使用平台生成的 Webhook 地址")
			case strings.Contains(item.name(), "envinject"):
				w.warnf("properties", "Environment Injector 的环境变量未迁移，请改用「系统配置 → 全局环境变量」或写进构建步骤")
			default:
				if short := shortClass(item.XMLName.Local); short != "Properties" {
					w.warnf("properties", "任务属性 %q 不受支持，已忽略", short)
				}
			}
		}
	}
	for _, wrapper := range root.findAll("buildwrappers") {
		for i := range wrapper.Children {
			item := &wrapper.Children[i]
			switch {
			case strings.Contains(item.name(), "timestamper"):
				w.infof("wrappers", "Timestamper 已内置：构建日志自带时间戳")
			case strings.Contains(item.name(), "nodejs"):
				name := item.value("nodeJSInstallationName")
				w.infof("wrappers",
					"NodeJS 安装项 %q 未迁移：平台按「全局工具配置」中的 NodeJS 主目录与 PATH 选择版本", name)
			case strings.Contains(item.name(), "maskpasswords"):
				w.errorf("wrappers", "Mask Passwords 不可迁移：平台目前不会对日志里的密码做遮罩，请避免在步骤里 echo 密码")
			default:
				if short := shortClass(item.XMLName.Local); short != "BuildWrappers" {
					w.warnf("wrappers", "构建包装器 %q 不受支持（如注入环境变量/自定义工具），已忽略", short)
				}
			}
		}
	}
	for _, step := range root.findAll("input") {
		_ = step
		w.errorf("approval", "任务包含 input（人工确认）步骤：平台暂无审批门，请改为构建前人工触发")
		break
	}
	if mailer := root.find("hudson.tasks.mailer"); mailer != nil {
		w.infof("notify", "Jenkins 邮件通知未迁移：请在任务「构建后操作 → 构建通知」中配置收件人/群")
	}
}

// splitCommaList 按逗号/空白拆分 Jenkins 的多值字段（sourceFiles、artifacts）。
func splitCommaList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ';'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if v := strings.TrimSpace(f); v != "" {
			out = append(out, v)
		}
	}
	return out
}
