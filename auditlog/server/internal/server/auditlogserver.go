package server

import (
	"context"

	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/logic"
	"cari.com.cn/framework/auditlog/server/internal/svc"
)

// AuditLogServer gRPC 服务实现，仅做协议层转发，业务逻辑在 logic 层。
type AuditLogServer struct {
	svcCtx *svc.ServiceContext
	pb.UnimplementedAuditLogServiceServer
}

// NewAuditLogServer 创建 gRPC server 实例。
func NewAuditLogServer(svcCtx *svc.ServiceContext) *AuditLogServer {
	return &AuditLogServer{svcCtx: svcCtx}
}

// CreateAuditLogs 写入一条或多条审计日志。
func (s *AuditLogServer) CreateAuditLogs(ctx context.Context, in *pb.CreateAuditLogsRequest) (*pb.CreateAuditLogsResponse, error) {
	return logic.NewCreateAuditLogsLogic(ctx, s.svcCtx).CreateAuditLogs(in)
}

// SearchAuditLogs 分页查询审计日志。
func (s *AuditLogServer) SearchAuditLogs(ctx context.Context, in *pb.SearchAuditLogsRequest) (*pb.SearchAuditLogsResponse, error) {
	return logic.NewSearchAuditLogsLogic(ctx, s.svcCtx).SearchAuditLogs(in)
}
