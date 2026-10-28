package pkg

import (
	"errors"
	"fmt"
	"net/http"
)

// 业务错误码。与 HTTP 状态码解耦，响应体始终 HTTP 200。
const (
	CodeOK              = 0
	CodeBadRequest      = 40000
	CodeUnauthorized    = 40100
	CodeForbidden       = 40300
	CodeNotFound        = 40400
	CodeConflict        = 40900
	CodeTooManyRequests = 42900
	CodeInternal        = 50000
)

// AppError 是携带业务错误码的错误类型。
type AppError struct {
	Code int
	Msg  string
}

func (e *AppError) Error() string { return e.Msg }

// NewError 构造业务错误。
func NewError(code int, format string, args ...interface{}) *AppError {
	return &AppError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// 常用错误快捷构造。
func ErrBadRequest(format string, args ...interface{}) *AppError {
	return NewError(CodeBadRequest, format, args...)
}

func ErrNotFound(format string, args ...interface{}) *AppError {
	return NewError(CodeNotFound, format, args...)
}

func ErrConflict(format string, args ...interface{}) *AppError {
	return NewError(CodeConflict, format, args...)
}

// ErrForbidden 权限不足或操作被安全策略拒绝。
func ErrForbidden(format string, args ...interface{}) *AppError {
	return NewError(CodeForbidden, format, args...)
}

// ErrInternal 内部错误（HTTP 500 语义）：用于把底层错误包装成可读提示。
func ErrInternal(format string, args ...interface{}) *AppError {
	return NewError(CodeInternal, format, args...)
}

// Wrap 保留业务错误码并附加上下文。
func Wrap(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return &AppError{Code: appErr.Code, Msg: fmt.Sprintf(format, args...) + ": " + appErr.Msg}
	}
	return fmt.Errorf(format+": %w", append(args, err)...)
}

// HTTPStatusOf 将业务错误码映射为 HTTP 状态码（仅用于日志与中间件）。
func HTTPStatusOf(code int) int {
	switch code {
	case CodeBadRequest:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodeTooManyRequests:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}
