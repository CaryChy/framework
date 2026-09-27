package resp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"cari.com.cn/framework/auditlog/server/internal/biz"
)

func TestBizStatus_Mapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"unavailable", biz.ErrUnavailable, http.StatusServiceUnavailable},
		{"empty_logs", biz.ErrEmptyLogs, http.StatusBadRequest},
		{"empty_svc", biz.ErrEmptySvcName, http.StatusBadRequest},
		{"batch_too_large", biz.ErrBatchTooLarge, http.StatusBadRequest},
		{"page_too_deep", biz.ErrPageTooDeep, http.StatusBadRequest},
		{"write_failed", biz.ErrWriteFailed, http.StatusInternalServerError},
		{"query_failed", biz.ErrQueryFailed, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BizStatus(tt.err); got != tt.want {
				t.Errorf("BizStatus(%v) = %d, 期望 %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestBizStatus_Nil(t *testing.T) {
	// nil 不应 panic
	if got := BizStatus(nil); got != http.StatusInternalServerError {
		t.Errorf("nil 错误应映射为 500，实际 %d", got)
	}
}

func TestBizStatus_Wrapped(t *testing.T) {
	// 包装后的错误仍应正确映射
	wrapped := errors.Join(biz.ErrEmptyLogs, errors.New("extra info"))
	if got := BizStatus(wrapped); got != http.StatusBadRequest {
		t.Errorf("包装错误应映射为 400，实际 %d", got)
	}
}

// TestFromBizErr_KnownSentinel 命中错误定义表的哨兵错误：透传安全文案与稳定标识。
func TestFromBizErr_KnownSentinel(t *testing.T) {
	rec := httptest.NewRecorder()
	FromBizErr(rec, biz.ErrEmptyLogs)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, 期望 400", rec.Code)
	}
	var body Body
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if body.Error != "auditlog.empty_logs" {
		t.Errorf("error 标识 = %q, 期望 auditlog.empty_logs", body.Error)
	}
	if body.Message != biz.ErrEmptyLogs.Error() {
		t.Errorf("message = %q, 期望透传哨兵错误文案 %q", body.Message, biz.ErrEmptyLogs.Error())
	}
}

// TestFromBizErr_MasksUnknownError 未知错误：error 标识兜底为 internal_error，
// message 脱敏为固定文案，原始错误细节（如 SQL/表结构）不得泄露给调用方。
func TestFromBizErr_MasksUnknownError(t *testing.T) {
	rec := httptest.NewRecorder()
	FromBizErr(rec, errors.New("Error 1062: Duplicate entry 'abc' for key 'audit_log.PRIMARY'"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d, 期望 500", rec.Code)
	}
	var body Body
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if body.Error != "auditlog.internal_error" {
		t.Errorf("error 标识 = %q, 期望 auditlog.internal_error", body.Error)
	}
	if body.Message != biz.InternalMessage {
		t.Errorf("message = %q, 期望脱敏文案 %q", body.Message, biz.InternalMessage)
	}
}
