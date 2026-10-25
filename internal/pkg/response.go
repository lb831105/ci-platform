package pkg

import (
	"ci-platform/internal/pkg/logger"
	"encoding/json"
	"errors"
	"net/http"
)

// Response 是全局统一响应体。
type Response struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data"`
}

// JSON 写出统一响应，HTTP 状态码恒为 200，业务态由 code 表达。
func JSON(w http.ResponseWriter, code int, msg string, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(Response{Code: code, Msg: msg, Data: data}); err != nil {
		logger.Errorf("write response failed: %v", err)
	}
}

// OK 写出成功响应。
func OK(w http.ResponseWriter, data interface{}) {
	JSON(w, CodeOK, "ok", data)
}

// FailStatus 写出失败响应，并把业务错误码映射为**真实 HTTP 状态码**。
//
// 只用于需要与外部系统按 HTTP 语义交互的接口（如代码平台 Webhook）：
// 平台自身的 UI/脚本依赖「HTTP 200 + code」这一约定，不能全局改成状态码。
// 响应体格式保持一致，调用方两种判读方式都能用。
func FailStatus(w http.ResponseWriter, err error) {
	if err == nil {
		OK(w, nil)
		return
	}
	code := CodeInternal
	msg := err.Error()
	var appErr *AppError
	if errors.As(err, &appErr) {
		code, msg = appErr.Code, appErr.Msg
	} else {
		logger.Errorf("internal error: %v", err)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(HTTPStatusOf(code))
	if encErr := json.NewEncoder(w).Encode(Response{Code: code, Msg: msg, Data: nil}); encErr != nil {
		logger.Errorf("write response failed: %v", encErr)
	}
}

// Fail 写出失败响应，自动识别 AppError。
func Fail(w http.ResponseWriter, err error) {
	if err == nil {
		OK(w, nil)
		return
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		JSON(w, appErr.Code, appErr.Msg, nil)
		return
	}
	logger.Errorf("internal error: %v", err)
	JSON(w, CodeInternal, err.Error(), nil)
}
