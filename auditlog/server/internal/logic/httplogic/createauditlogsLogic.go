package httplogic

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"

	"cari.com.cn/framework/auditlog/server/internal/convert"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/auditlog/server/internal/types"
)

// CreateAuditLogsLogic 批量写入审计日志的 HTTP 协议层，仅做编排与错误映射。
// 协议字段与领域模型的映射统一在 convert 包，业务规则统一在 biz 包。
type CreateAuditLogsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

// NewCreateAuditLogsLogic 创建批量写入 logic。
func NewCreateAuditLogsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateAuditLogsLogic {
	return &CreateAuditLogsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// CreateAuditLogs 将一条或多条审计日志交给领域服务写入。
func (l *CreateAuditLogsLogic) CreateAuditLogs(req *types.CreateAuditLogsRequest) (resp *types.CreateAuditLogsResponse, err error) {
	ids, err := l.svcCtx.AuditLogBiz.Create(l.ctx, convert.CreateLogsFromHTTP(req.Logs))
	if err != nil {
		return nil, err
	}
	return &types.CreateAuditLogsResponse{Ids: ids}, nil
}
