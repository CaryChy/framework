package auditlog

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"

	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/auditlog/server/internal/types"
)

// CreateAuditLogsLogic 批量写入审计日志的 HTTP 逻辑。
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

// CreateAuditLogs 将一条或多条审计日志转发给 gRPC 服务写入。
func (l *CreateAuditLogsLogic) CreateAuditLogs(req *types.CreateAuditLogsRequest) (resp *types.CreateAuditLogsResponse, err error) {
	logs := make([]*pb.AuditLog, 0, len(req.Logs))
	for i := range req.Logs {
		logs = append(logs, toProtoAuditLog(&req.Logs[i]))
	}

	l.Logger.Infow("HTTP 收到批量写入审计日志请求", logx.Field("count", len(logs)))

	rpcResp, err := l.svcCtx.AuditLogRpc.CreateAuditLogs(l.ctx, &pb.CreateAuditLogsRequest{Logs: logs})
	if err != nil {
		l.Logger.Errorw("调用 auditlog-rpc.CreateAuditLogs 失败", logx.Field("error", err))
		return nil, err
	}

	l.Logger.Infow("HTTP 批量写入审计日志成功",
		logx.Field("count", len(rpcResp.GetIds())),
		logx.Field("ids", rpcResp.GetIds()),
	)

	return &types.CreateAuditLogsResponse{Ids: rpcResp.GetIds()}, nil
}

// toProtoAuditLog HTTP 类型转 gRPC 类型。
func toProtoAuditLog(item *types.AuditLogItem) *pb.AuditLog {
	return &pb.AuditLog{
		Id:           item.Id,
		TraceId:      item.TraceId,
		ServiceName:  item.ServiceName,
		Operation:    item.Operation,
		ActorId:      item.ActorId,
		ActorType:    item.ActorType,
		Action:       item.Action,
		ResourceType: item.ResourceType,
		ResourceId:   item.ResourceId,
		SourceIp:     item.SourceIp,
		UserAgent:    item.UserAgent,
		RequestUri:   item.RequestUri,
		StatusCode:   item.StatusCode,
		RequestBody:  item.RequestBody,
		ResponseBody: item.ResponseBody,
		Metadata:     item.Metadata,
		ErrorMessage: item.ErrorMessage,
		DurationMs:   item.DurationMs,
	}
}
