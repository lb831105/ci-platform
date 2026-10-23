package pkg

// BuildInfo 承载编译期注入的版本信息，由 main 传入接口层，避免各包重复定义。
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}
