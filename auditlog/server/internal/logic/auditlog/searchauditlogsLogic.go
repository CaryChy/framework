package auditlog

import (
	"context"

	"github.com/zeromicro/go-zero/core/logx"

	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/auditlog/server/internal/types"
)

// SearchAuditLogsLogic 分页查询审计日志的 HTTP 逻辑。
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

// SearchAuditLogs 将查询条件转发给 gRPC 服务，并转换为 HTTP 响应。
func (l *SearchAuditLogsLogic) SearchAuditLogs(req *types.SearchAuditLogsRequest) (resp *types.SearchAuditLogsResponse, err error) {
	rpcResp, err := l.svcCtx.AuditLogRpc.SearchAuditLogs(l.ctx, &pb.SearchAuditLogsRequest{
		ServiceName:  req.ServiceName,
		ActorId:      req.ActorId,
		Action:       req.Action,
		ResourceType: req.ResourceType,
		ResourceId:   req.ResourceId,
		TraceId:      req.TraceId,
		StartTime:    req.StartTime,
		EndTime:      req.EndTime,
		Page:         req.Page,
		PageSize:     req.PageSize,
	})
	if err != nil {
		l.Logger.Errorw("调用 auditlog-rpc.SearchAuditLogs 失败", logx.Field("error", err))
		return nil, err
	}

	list := make([]types.AuditLogItem, 0, len(rpcResp.GetList()))
	for _, item := range rpcResp.GetList() {
		list = append(list, fromProtoAuditLog(item))
	}

	l.Logger.Infow("HTTP 查询审计日志成功",
		logx.Field("returned", len(list)),
		logx.Field("total", rpcResp.GetTotal()),
	)

	return &types.SearchAuditLogsResponse{
		List:  list,
		Total: rpcResp.GetTotal(),
	}, nil
}

// fromProtoAuditLog gRPC 类型转 HTTP 类型。
func fromProtoAuditLog(item *pb.AuditLog) types.AuditLogItem {
	return types.AuditLogItem{
		Id:           item.GetId(),
		TraceId:      item.GetTraceId(),
		ServiceName:  item.GetServiceName(),
		Operation:    item.GetOperation(),
		ActorId:      item.GetActorId(),
		ActorType:    item.GetActorType(),
		Action:       item.GetAction(),
		ResourceType: item.GetResourceType(),
		ResourceId:   item.GetResourceId(),
		SourceIp:     item.GetSourceIp(),
		UserAgent:    item.GetUserAgent(),
		RequestUri:   item.GetRequestUri(),
		StatusCode:   item.GetStatusCode(),
		RequestBody:  item.GetRequestBody(),
		ResponseBody: item.GetResponseBody(),
		Metadata:     item.GetMetadata(),
		ErrorMessage: item.GetErrorMessage(),
		DurationMs:   item.GetDurationMs(),
		CreatedAt:    item.GetCreatedAt(),
	}
}
