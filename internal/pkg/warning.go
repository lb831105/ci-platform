package pkg

// Warning 描述「配置迁移 / 导入」过程中一处无法完整处理的配置。
//
// 三级语义（与 Jenkins 迁移场景对齐）：
//   - LevelError：不人工处理就不可用（如凭据丢失、目标主机不存在）
//   - LevelWarn：已降级处理，需要用户知晓（如多分支只取第一个）
//   - LevelInfo：语义不同但无需操作（如时间戳已内置）
//
// 之所以要把差异显式返回给调用方：迁移最容易出的事故不是「报错」，
// 而是「导入成功但配置悄悄丢了」。
type Warning struct {
	Field   string `json:"field"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

const (
	LevelError = "error"
	LevelWarn  = "warn"
	LevelInfo  = "info"
)
