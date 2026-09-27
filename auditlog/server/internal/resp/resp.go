// Package resp 构造符合《统一微服务 API 响应规范》的 HTTP 响应体：
// {"error","message","data","ts"}，成功 error="success"，错误为 auditlog.xxx 稳定标识。
// 业务 handler、admin 管理端口、限流中间件共用，保证全服务响应结构一致。
package resp

import (
	"encoding/json"
	"net/http"
	"time"

	"cari.com.cn/framework/auditlog/server/internal/biz"
)

// SuccessError 成功响应的协议级保留值。
const SuccessError = "success"

// Body 统一响应结构。
type Body struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Data    any    `json:"data"`
	TS      int64  `json:"ts"`
}

// OK 写出 200 成功响应。
func OK(w http.ResponseWriter, data any) {
	write(w, http.StatusOK, SuccessError, "", data)
}

// Created 写出 201 创建成功响应。
func Created(w http.ResponseWriter, data any) {
	write(w, http.StatusCreated, SuccessError, "", data)
}

// Err 写出指定状态码与 error 标识的错误响应，data 固定为空对象。
func Err(w http.ResponseWriter, status int, errorID, message string) {
	write(w, status, errorID, message, struct{}{})
}

// ErrData 同 Err，但 data 为结构化错误详情（规范 §7.2）。
func ErrData(w http.ResponseWriter, status int, errorID, message string, data any) {
	write(w, status, errorID, message, data)
}

// ErrWithHeader 同 Err，附加响应头（如 WWW-Authenticate、Retry-After）。
func ErrWithHeader(w http.ResponseWriter, status int, errorID, message string, header map[string]string) {
	for k, v := range header {
		w.Header().Set(k, v)
	}
	write(w, status, errorID, message, struct{}{})
}

// BizStatus 返回业务错误对应的 HTTP 状态码（503/400/500）。
func BizStatus(err error) int {
	return kindStatus(biz.SpecOf(err).Kind)
}

// FromBizErr 将业务错误翻译为规范错误响应：查 biz.SpecOf 得 error 标识与类别，
// 再映射为 HTTP 状态码；对外文案走 biz.MessageOf，未知内部错误脱敏为固定文案。
func FromBizErr(w http.ResponseWriter, err error) {
	spec := biz.SpecOf(err)
	write(w, kindStatus(spec.Kind), spec.ID, biz.MessageOf(err), struct{}{})
}

func kindStatus(k biz.ErrorKind) int {
	switch k {
	case biz.KindUnavailable:
		return http.StatusServiceUnavailable
	case biz.KindInvalidArgument:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func write(w http.ResponseWriter, status int, errorID, message string, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Body{
		Error:   errorID,
		Message: message,
		Data:    data,
		TS:      time.Now().UnixMilli(),
	})
}
