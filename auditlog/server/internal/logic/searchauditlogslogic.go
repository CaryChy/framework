package logic

import (
	"context"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cari.com.cn/framework/auditlog/model"
	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/svc"
)

const (
	defaultPageSize = 20
	maxPageSize     = 200
)

// SearchAuditLogsLogic 分页查询审计日志的业务逻辑。
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
	// 依赖未就绪（启动中/MySQL 故障）时快速失败。
	if !l.svcCtx.Health.IsReady() {
		return nil, status.Error(codes.Unavailable, "服务正在启动中或依赖暂不可用，请稍后重试")
	}

	page := in.GetPage()
	if page <= 0 {
		page = 1
	}
	pageSize := in.GetPageSize()
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	input := model.SearchInput{
		ServiceName:  in.GetServiceName(),
		ActorId:      in.GetActorId(),
		Action:       in.GetAction(),
		ResourceType: in.GetResourceType(),
		ResourceId:   in.GetResourceId(),
		TraceId:      in.GetTraceId(),
		StartTime:    unixMilliToTime(in.GetStartTime()),
		EndTime:      unixMilliToTime(in.GetEndTime()),
		Page:         page,
		PageSize:     pageSize,
	}

	rows, total, err := l.svcCtx.AuditLogModel.Search(l.ctx, input)
	if err != nil {
		l.Logger.Errorw("查询审计日志失败",
			logx.Field("filter", input),
			logx.Field("error", err),
		)
		return nil, status.Error(codes.Internal, "查询审计日志失败")
	}

	list := make([]*pb.AuditLog, 0, len(rows))
	for _, row := range rows {
		list = append(list, toProtoAuditLog(row))
	}

	l.Logger.Infow("审计日志查询成功",
		logx.Field("page", page),
		logx.Field("page_size", pageSize),
		logx.Field("returned", len(list)),
		logx.Field("total", total),
	)

	return &pb.SearchAuditLogsResponse{
		List:  list,
		Total: total,
	}, nil
}

// toProtoAuditLog model 实体转 pb 实体，时间统一输出为 Unix 毫秒。
func toProtoAuditLog(row *model.AuditLog) *pb.AuditLog {
	return &pb.AuditLog{
		Id:           row.Id,
		TraceId:      row.TraceId,
		ServiceName:  row.ServiceName,
		Operation:    row.Operation,
		ActorId:      row.ActorId,
		ActorType:    row.ActorType,
		Action:       row.Action,
		ResourceType: row.ResourceType,
		ResourceId:   row.ResourceId,
		SourceIp:     row.SourceIp,
		UserAgent:    row.UserAgent,
		RequestUri:   row.RequestUri,
		StatusCode:   int32(row.StatusCode),
		RequestBody:  row.RequestBody.String,
		ResponseBody: row.ResponseBody.String,
		Metadata:     row.Metadata.String,
		ErrorMessage: row.ErrorMessage,
		DurationMs:   row.DurationMs,
		CreatedAt:    row.CreatedAt.UnixMilli(),
	}
}

// unixMilliToTime 0 表示未传该过滤条件，返回 time 零值由 model 层跳过。
func unixMilliToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}

	return time.UnixMilli(ms).UTC()
}
