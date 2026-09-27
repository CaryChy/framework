package httplogic

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"

	"cari.com.cn/framework/auditlog/server/internal/convert"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/auditlog/server/internal/types"
)

// SearchAuditLogsLogic 分页查询审计日志的 HTTP 协议层，仅做编排与错误映射。
type SearchAuditLogsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewSearchAuditLogsLogic 创建查询 logic。
func NewSearchAuditLogsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SearchAuditLogsLogic {
	return &SearchAuditLogsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// SearchAuditLogs 将查询条件交给领域服务，并转换为 HTTP 响应。
func (l *SearchAuditLogsLogic) SearchAuditLogs(req *types.SearchAuditLogsRequest) (resp *types.SearchAuditLogsResponse, err error) {
	rows, total, err := l.svcCtx.AuditLogBiz.Search(l.ctx, convert.SearchFilterFromHTTP(req))
	if err != nil {
		return nil, err
	}
	return &types.SearchAuditLogsResponse{
		List:  convert.AuditLogListToHTTP(rows),
		Total: total,
	}, nil
}
