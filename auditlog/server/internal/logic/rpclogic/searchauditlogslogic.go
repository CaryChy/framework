package rpclogic

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"

	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/convert"
	"cari.com.cn/framework/auditlog/server/internal/svc"
)

// SearchAuditLogsLogic 分页查询审计日志的 gRPC 协议层，仅做编排与错误映射。
type SearchAuditLogsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

// NewSearchAuditLogsLogic 创建查询 logic。
func NewSearchAuditLogsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SearchAuditLogsLogic {
	return &SearchAuditLogsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// SearchAuditLogs 按可选条件分页查询审计日志，结果按创建时间倒序返回。
func (l *SearchAuditLogsLogic) SearchAuditLogs(in *pb.SearchAuditLogsRequest) (*pb.SearchAuditLogsResponse, error) {
	rows, total, err := l.svcCtx.AuditLogBiz.Search(l.ctx, convert.SearchFilterFromRPC(in))
	if err != nil {
		return nil, toGrpcErr(err)
	}
	return &pb.SearchAuditLogsResponse{
		List:  convert.AuditLogListToRPC(rows),
		Total: total,
	}, nil
}
