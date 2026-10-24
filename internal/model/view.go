package model

import "time"

// 视图类型，对齐 Jenkins New View 页的 Type 下拉。
const (
	// ViewTypeList 列表视图：显式选择任务（可叠加任务名正则）后展示。
	ViewTypeList = "list"
	// ViewTypeMy 我的视图：自动展示当前用户有权限访问的任务。
	ViewTypeMy = "my"
)

// 视图列 ID，对齐 Jenkins 列表视图的列配置。
const (
	ViewColumnStatus       = "status"       // 状态
	ViewColumnWeather      = "weather"      // 气象
	ViewColumnName         = "name"         // 名称
	ViewColumnLastSuccess  = "lastSuccess"  // 上次成功
	ViewColumnLastFailure  = "lastFailure"  // 上次失败
	ViewColumnLastDuration = "lastDuration" // 上次花费时间
	ViewColumnBuildButton  = "buildButton"  // 构建按钮
)

// ViewColumns 返回全部受支持的视图列（按 Jenkins 默认顺序）。
func ViewColumns() []string {
	return []string{
		ViewColumnStatus,
		ViewColumnWeather,
		ViewColumnName,
		ViewColumnLastSuccess,
		ViewColumnLastFailure,
		ViewColumnLastDuration,
		ViewColumnBuildButton,
	}
}

// View 是一个任务视图（本质是给任务归类，对齐 Jenkins Views）。
type View struct {
	Name string `json:"name"` // 视图名（唯一标识，也是 URL 中的视图 ID）

	// 描述（Jenkins: Describe the purpose of this view）。
	Description string `json:"description,omitempty"`

	Type string `json:"type"` // list | my

	// 列表视图：显式勾选的任务名；我的视图忽略该字段。
	Jobs []string `json:"jobs,omitempty"`

	// 列表视图：任务名正则（Jenkins「使用正则表达式在视图中显示任务」），
	// 与显式任务取并集；为空表示不启用。
	JobRegex string `json:"job_regex,omitempty"`

	// 展示列；为空时取全部默认列。
	Columns []string `json:"columns,omitempty"`

	// Widgets：仅把视图内任务显示在构建队列/构建执行状态中。
	FilterQueue     bool `json:"filter_queue,omitempty"`
	FilterExecutors bool `json:"filter_executors,omitempty"`

	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}
