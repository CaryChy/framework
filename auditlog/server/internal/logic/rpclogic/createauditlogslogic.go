package rpclogic

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"

	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/convert"
	"cari.com.cn/framework/auditlog/server/internal/svc"
)

// CreateAuditLogsLogic 批量写入审计日志的 gRPC 协议层，仅做编排与错误映射。
// 协议字段与领域模型的映射统一在 convert 包，业务规则统一在 biz 包。
type CreateAuditLogsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

// NewCreateAuditLogsLogic 创建批量写入 logic。
func NewCreateAuditLogsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateAuditLogsLogic {
	return &CreateAuditLogsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// CreateAuditLogs 写入一条或多条审计日志，返回生成的日志 ID。
func (l *CreateAuditLogsLogic) CreateAuditLogs(in *pb.CreateAuditLogsRequest) (*pb.CreateAuditLogsResponse, error) {
	ids, err := l.svcCtx.AuditLogBiz.Create(l.ctx, convert.CreateLogsFromRPC(in.GetLogs()))
	if err != nil {
		return nil, toGrpcErr(err)
	}
	return &pb.CreateAuditLogsResponse{Ids: ids}, nil
}
