package convert

import (
	"testing"
	"time"

	"cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/biz"
	"cari.com.cn/framework/auditlog/server/internal/types"
)

// TestCreateLogFromHTTP_FieldMapping 验证 HTTP 请求到领域对象的字段拷贝。
func TestCreateLogFromHTTP_FieldMapping(t *testing.T) {
	in := types.AuditLogItem{
		Id:           "id-1",
		TraceId:      "trace-1",
		ServiceName:  "auth",
		Operation:    "login",
		ActorId:      "u1",
		ActorType:    "user",
		Action:       "login",
		ResourceType: "user",
		ResourceId:   "r1",
		SourceIp:     "1.2.3.4",
		UserAgent:    "ua",
		RequestUri:   "/api/login",
		StatusCode:   200,
		RequestBody:  `{"k":"v"}`,
		ResponseBody: `{"ok":true}`,
		Metadata:     `{"m":1}`,
		ErrorMessage: "",
		DurationMs:   42,
	}
	got := CreateLogFromHTTP(&in)
	if got.Id != "id-1" || got.ServiceName != "auth" || got.StatusCode != 200 {
		t.Errorf("基础字段映射错误: %+v", got)
	}
	if got.DurationMs != 42 {
		t.Errorf("DurationMs 映射错误: %d", got.DurationMs)
	}
	if got.RequestBody != `{"k":"v"}` {
		t.Errorf("RequestBody 映射错误: %s", got.RequestBody)
	}
}

// TestCreateLogsFromHTTP_Empty 空切片返回空切片。
func TestCreateLogsFromHTTP_Empty(t *testing.T) {
	if got := CreateLogsFromHTTP(nil); len(got) != 0 {
		t.Fatalf("期望空切片，实际 %v", got)
	}
}

// TestCreateLogFromRPC_FieldMapping 验证 gRPC 请求到领域对象的字段拷贝。
func TestCreateLogFromRPC_FieldMapping(t *testing.T) {
	in := &auditlog.AuditLog{
		Id:           "id-2",
		TraceId:      "trace-2",
		ServiceName:  "order",
		Operation:    "create",
		ActorId:      "u2",
		ActorType:    "user",
		Action:       "create",
		ResourceType: "order",
		ResourceId:   "o1",
		SourceIp:     "5.6.7.8",
		UserAgent:    "grpc",
		RequestUri:   "/order",
		StatusCode:   201,
		RequestBody:  "req",
		ResponseBody: "resp",
		Metadata:     "meta",
		ErrorMessage: "err",
		DurationMs:   99,
	}
	got := CreateLogFromRPC(in)
	if got.ServiceName != "order" || got.StatusCode != 201 || got.ErrorMessage != "err" {
		t.Errorf("字段映射错误: %+v", got)
	}
}

// TestSearchFilterFromHTTP 验证 HTTP 查询参数到领域过滤条件。
func TestSearchFilterFromHTTP(t *testing.T) {
	in := types.SearchAuditLogsRequest{
		ServiceName:  "auth",
		ActorId:      "u1",
		Action:       "login",
		ResourceType: "user",
		ResourceId:   "r1",
		TraceId:      "t1",
		StartTime:    1000,
		EndTime:      2000,
		Page:         3,
		PageSize:     50,
	}
	got := SearchFilterFromHTTP(&in)
	if got.ServiceName != "auth" || got.ActorId != "u1" || got.Page != 3 || got.PageSize != 50 {
		t.Errorf("过滤条件映射错误: %+v", got)
	}
	if got.StartTimeMs != 1000 || got.EndTimeMs != 2000 {
		t.Errorf("时间字段映射错误: start=%d end=%d", got.StartTimeMs, got.EndTimeMs)
	}
}

// TestAuditLogToHTTP_NullFields 验证 NULL 字段解包为空串。
func TestAuditLogToHTTP_NullFields(t *testing.T) {
	created := time.Date(2024, 7, 3, 12, 0, 0, 0, time.UTC)
	v := biz.AuditLogView{
		Id:           "id",
		ServiceName:  "auth",
		StatusCode:   200,
		RequestBody:  "body",
		ResponseBody: "",
		Metadata:     "",
		CreatedAtMs:  created.UnixMilli(),
	}
	got := AuditLogToHTTP(v)
	if got.RequestBody != "body" || got.ResponseBody != "" || got.Metadata != "" {
		t.Errorf("NULL 字段未正确解包: body=%q resp=%q meta=%q", got.RequestBody, got.ResponseBody, got.Metadata)
	}
	if got.CreatedAt != created.UnixMilli() {
		t.Errorf("CreatedAt 映射错误: %d", got.CreatedAt)
	}
}

// TestAuditLogToRPC 验证 gRPC 响应字段映射。
func TestAuditLogToRPC(t *testing.T) {
	v := biz.AuditLogView{
		Id:          "id",
		ServiceName: "auth",
		StatusCode:  200,
		DurationMs:  10,
		CreatedAtMs: 1720000000000,
	}
	got := AuditLogToRPC(v)
	if got.GetId() != "id" || got.GetStatusCode() != 200 || got.GetDurationMs() != 10 {
		t.Errorf("gRPC 响应映射错误: %+v", got)
	}
}

// TestAuditLogListToHTTP_Empty 空列表返回空切片而非 nil（JSON 序列化为 []）。
func TestAuditLogListToHTTP_Empty(t *testing.T) {
	got := AuditLogListToHTTP(nil)
	if got == nil {
		t.Fatal("空列表应返回非 nil 切片")
	}
	if len(got) != 0 {
		t.Fatalf("期望空，实际 %d", len(got))
	}
}
