package logic

import (
	"context"
	"database/sql"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cari.com.cn/framework/auditlog/model"
	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/pkg/id"
)

// maxBatchSize 单次请求允许写入的最大日志条数，防止超大请求压垮数据库。
const maxBatchSize = 500

// CreateAuditLogsLogic 批量写入审计日志的业务逻辑。
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
	// 依赖未就绪（启动中/MySQL 故障）时快速失败，避免请求堆积在数据库调用上。
	if !l.svcCtx.Health.IsReady() {
		return nil, status.Error(codes.Unavailable, "服务正在启动中或依赖暂不可用，请稍后重试")
	}

	if len(in.GetLogs()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "logs 不能为空")
	}
	if len(in.GetLogs()) > maxBatchSize {
		return nil, status.Errorf(codes.InvalidArgument, "单次最多写入 %d 条日志，当前 %d 条", maxBatchSize, len(in.GetLogs()))
	}

	now := time.Now().UTC()
	rows := make([]*model.AuditLog, 0, len(in.GetLogs()))
	ids := make([]string, 0, len(in.GetLogs()))

	for _, item := range in.GetLogs() {
		if len(item.GetServiceName()) == 0 {
			return nil, status.Error(codes.InvalidArgument, "service_name 不能为空")
		}

		logID := item.GetId()
		if len(logID) == 0 {
			logID = id.NewID()
		}

		rows = append(rows, toModelAuditLog(logID, item, now))
		ids = append(ids, logID)
	}

	if _, err := l.svcCtx.AuditLogModel.InsertBatch(l.ctx, rows); err != nil {
		l.Logger.Errorw("批量写入审计日志失败",
			logx.Field("count", len(rows)),
			logx.Field("error", err),
		)
		return nil, status.Error(codes.Internal, "写入审计日志失败")
	}

	l.Logger.Infow("审计日志写入成功",
		logx.Field("count", len(ids)),
		logx.Field("ids", ids),
	)

	return &pb.CreateAuditLogsResponse{Ids: ids}, nil
}

// toModelAuditLog 将 pb 实体转换为 model 实体；id/createdAt 由服务端统一填充。
func toModelAuditLog(logID string, src *pb.AuditLog, now time.Time) *model.AuditLog {
	return &model.AuditLog{
		Id:           logID,
		TraceId:      src.GetTraceId(),
		ServiceName:  src.GetServiceName(),
		Operation:    src.GetOperation(),
		ActorId:      src.GetActorId(),
		ActorType:    src.GetActorType(),
		Action:       src.GetAction(),
		ResourceType: src.GetResourceType(),
		ResourceId:   src.GetResourceId(),
		SourceIp:     src.GetSourceIp(),
		UserAgent:    src.GetUserAgent(),
		RequestUri:   src.GetRequestUri(),
		StatusCode:   int64(src.GetStatusCode()),
		RequestBody:  nullString(src.GetRequestBody()),
		ResponseBody: nullString(src.GetResponseBody()),
		Metadata:     nullString(src.GetMetadata()),
		ErrorMessage: src.GetErrorMessage(),
		DurationMs:   src.GetDurationMs(),
		CreatedAt:    now,
	}
}

// nullString 仅用于可空大字段：空串写为 NULL，非空才设置 Valid。
func nullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: len(v) > 0}
}
