package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func okHandler(_ context.Context, req any) (any, error) { return req, nil }

func TestGrpcRateLimit_Disabled(t *testing.T) {
	if NewGrpcRateLimit(0) != nil {
		t.Fatal("rps=0 应返回 nil（不限流）")
	}
}

func TestGrpcRateLimit_UnderLimit(t *testing.T) {
	interceptor := NewGrpcRateLimit(100)
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}
	for i := 0; i < 100; i++ {
		if _, err := interceptor(context.Background(), nil, info, okHandler); err != nil {
			t.Fatalf("第 %d 个请求不应被限流: %v", i+1, err)
		}
	}
}

func TestGrpcRateLimit_OverLimit(t *testing.T) {
	interceptor := NewGrpcRateLimit(1)
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

	// 突发容量=1：第一个请求放行
	if _, err := interceptor(context.Background(), nil, info, okHandler); err != nil {
		t.Fatalf("第一个请求应放行: %v", err)
	}
	// 立即第二个：令牌耗尽，应被限流
	_, err := interceptor(context.Background(), nil, info, okHandler)
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.ResourceExhausted {
		t.Fatalf("超限应返回 ResourceExhausted，实际 %v", err)
	}
}

func TestHttpRateLimit_Disabled(t *testing.T) {
	if NewHttpRateLimit(0) != nil {
		t.Fatal("rps=0 应返回 nil（不限流）")
	}
}

func TestHttpRateLimit(t *testing.T) {
	mw := NewHttpRateLimit(1)
	next := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	handler := mw(next)

	// 第一个请求：放行
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("第一个请求应 200，实际 %d", rec.Code)
	}

	// 立即第二个：429 + Retry-After + 规范错误体
	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超限应 429，实际 %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 响应应携带 Retry-After 头")
	}
	var body struct {
		Error string `json:"error"`
		TS    int64  `json:"ts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("429 响应非合法 JSON: %v，body=%s", err, rec.Body.String())
	}
	if body.Error != "auditlog.too_many_requests" {
		t.Errorf("error 应为 auditlog.too_many_requests，实际 %q", body.Error)
	}
	if body.TS <= 0 {
		t.Errorf("ts 应为 Unix 毫秒时间戳，实际 %d", body.TS)
	}
}
