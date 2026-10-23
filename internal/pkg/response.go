package pkg

import (
	"encoding/json"
	"errors"
	"log"
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
		log.Printf("write response failed: %v", err)
	}
}

// OK 写出成功响应。
func OK(w http.ResponseWriter, data interface{}) {
	JSON(w, CodeOK, "ok", data)
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
	log.Printf("internal error: %v", err)
	JSON(w, CodeInternal, err.Error(), nil)
}
