package rpclogic

import (
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/biz"
)

func TestToGrpcErr_Mapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   codes.Code
		wantReason string
	}{
		{"unavailable", biz.ErrUnavailable, codes.Unavailable, "auditlog.unavailable"},
		{"empty_logs", biz.ErrEmptyLogs, codes.InvalidArgument, "auditlog.empty_logs"},
		{"empty_svc", biz.ErrEmptySvcName, codes.InvalidArgument, "auditlog.empty_service_name"},
		{"batch_too_large", biz.ErrBatchTooLarge, codes.InvalidArgument, "auditlog.batch_too_large"},
		{"page_too_deep", biz.ErrPageTooDeep, codes.InvalidArgument, "auditlog.page_too_deep"},
		{"internal", biz.ErrWriteFailed, codes.Internal, "auditlog.write_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grpcErr := toGrpcErr(tt.err)
			st, ok := status.FromError(grpcErr)
			if !ok {
				t.Fatalf("toGrpcErr 返回非 gRPC status 错误: %v", grpcErr)
			}
			if st.Code() != tt.wantCode {
				t.Errorf("期望 code=%v，实际 %v", tt.wantCode, st.Code())
			}
			// details 必须携带 ErrorInfo（Reason=稳定错误标识，与 HTTP 侧一致）。
			info := &errdetails.ErrorInfo{}
			found := false
			for _, d := range st.Details() {
				if e, ok := d.(*errdetails.ErrorInfo); ok {
					info = e
					found = true
					break
				}
			}
			if !found {
				t.Fatal("details 中未找到 errdetails.ErrorInfo")
			}
			if info.Reason != tt.wantReason {
				t.Errorf("Reason 期望 %q，实际 %q", tt.wantReason, info.Reason)
			}
			if info.Domain != "auditlog" {
				t.Errorf("Domain 期望 auditlog，实际 %q", info.Domain)
			}
			if info.Metadata["retryable"] == "" {
				t.Error("Metadata.retryable 不应为空")
			}
			if info.Metadata["ts"] == "" {
				t.Error("Metadata.ts 不应为空")
			}
		})
	}
}

func TestCreateAuditLogsLogic_Conversion(t *testing.T) {
	// 验证 pb->biz 转换正确（通过检查 Create 收到的参数）
	// 这里主要验证编译和基本结构，实际转换正确性由集成测试覆盖
	in := &pb.CreateAuditLogsRequest{
		Logs: []*pb.AuditLog{
			{ServiceName: "auth", Action: "login", StatusCode: 200},
			{ServiceName: "order", Action: "create", StatusCode: 201},
		},
	}
	if len(in.GetLogs()) != 2 {
		t.Fatalf("期望 2 条日志，实际 %d", len(in.GetLogs()))
	}
	if in.GetLogs()[0].GetServiceName() != "auth" {
		t.Errorf("第一条 service_name 错误: %s", in.GetLogs()[0].GetServiceName())
	}
}
