// Package core 承载 auditlog 服务的核心业务逻辑：gRPC 与 HTTP 两种接入方式
// 共享同一份实现（单进程组合服务内不再经 HTTP->gRPC 回环调用，直接进程内调用本包）。
//
// 依赖方向：rpc server / http handler -> core -> model，协议层只做类型转换与转发。
package core

import (
	"context"
	"database/sql"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cari.com.cn/framework/auditlog/common/mask"
	"cari.com.cn/framework/auditlog/model"
	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/auditlog/server/internal/types"
	"cari.com.cn/framework/pkg/id"
)

const (
	// maxBatchSize 单次请求允许写入的最大日志条数，防止超大请求压垮数据库。
	maxBatchSize = 500
	// defaultPageSize 查询未指定分页大小时的默认值。
	defaultPageSize = 20
	// maxPageSize 单次查询允许的最大返回条数。
	maxPageSize = 200
	// maxOffset 深度分页保护：(page-1)*pageSize 超过该值的查询直接拒绝，
	// 避免 MySQL 在大偏移量 LIMIT 扫描时拖垮实例（需要翻更深的页请缩小时间范围）。
	maxOffset = 100000
	// logBodyLimit 运行日志中记录 body/metadata 摘要时的最大字节数（脱敏前先行截断）。
	logBodyLimit = 512
)

// Core 审计日志核心用例集合，持有服务依赖。
type Core struct {
	svcCtx *svc.ServiceContext
}

// New 创建 Core。
func New(svcCtx *svc.ServiceContext) *Core {
	return &Core{svcCtx: svcCtx}
}

// CreateAuditLogs 批量写入审计日志（gRPC pb 入参/出参），返回生成的日志 ID 列表。
func (c *Core) CreateAuditLogs(ctx context.Context, in *pb.CreateAuditLogsRequest) (*pb.CreateAuditLogsResponse, error) {
	// 依赖未就绪（启动中/MySQL 故障）时快速失败，避免请求堆积在数据库调用上。
	if !c.svcCtx.Health.IsReady() {
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

	if _, err := c.svcCtx.AuditLogModel.InsertBatch(ctx, rows); err != nil {
		logx.WithContext(ctx).Errorw("批量写入审计日志失败",
			logx.Field("count", len(rows)),
			logx.Field("error", err),
		)
		return nil, status.Error(codes.Internal, "写入审计日志失败")
	}

	// 仅记录数量与 ID（UUID，不含业务内容）；绝不落 request_body/response_body 等敏感字段。
	logx.WithContext(ctx).Infow("审计日志写入成功",
		logx.Field("count", len(ids)),
		logx.Field("ids", ids),
	)

	return &pb.CreateAuditLogsResponse{Ids: ids}, nil
}

// SearchAuditLogs 按可选条件分页查询审计日志（gRPC pb 入参/出参），结果按创建时间倒序。
func (c *Core) SearchAuditLogs(ctx context.Context, in *pb.SearchAuditLogsRequest) (*pb.SearchAuditLogsResponse, error) {
	if !c.svcCtx.Health.IsReady() {
		return nil, status.Error(codes.Unavailable, "服务正在启动中或依赖暂不可用，请稍后重试")
	}

	input, err := buildSearchInput(in.GetServiceName(), in.GetActorId(), in.GetAction(),
		in.GetResourceType(), in.GetResourceId(), in.GetTraceId(),
		in.GetStartTime(), in.GetEndTime(), in.GetPage(), in.GetPageSize())
	if err != nil {
		return nil, err
	}

	rows, total, err := c.svcCtx.AuditLogModel.Search(ctx, input)
	if err != nil {
		logx.WithContext(ctx).Errorw("查询审计日志失败",
			logx.Field("filter", describeFilter(input)),
			logx.Field("error", err),
		)
		return nil, status.Error(codes.Internal, "查询审计日志失败")
	}

	list := make([]*pb.AuditLog, 0, len(rows))
	for _, row := range rows {
		list = append(list, ToProtoAuditLog(row))
	}

	logx.WithContext(ctx).Infow("审计日志查询成功",
		logx.Field("page", input.Page),
		logx.Field("page_size", input.PageSize),
		logx.Field("returned", len(list)),
		logx.Field("total", total),
	)

	return &pb.SearchAuditLogsResponse{
		List:  list,
		Total: total,
	}, nil
}

// CreateAuditLogsHTTP 批量写入（HTTP types 入参/出参），复用 gRPC 路径的校验与落库逻辑。
func (c *Core) CreateAuditLogsHTTP(ctx context.Context, req *types.CreateAuditLogsRequest) (*types.CreateAuditLogsResponse, error) {
	logs := make([]*pb.AuditLog, 0, len(req.Logs))
	for i := range req.Logs {
		item := &req.Logs[i]
		logs = append(logs, &pb.AuditLog{
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
		})
	}

	rpcResp, err := c.CreateAuditLogs(ctx, &pb.CreateAuditLogsRequest{Logs: logs})
	if err != nil {
		return nil, err
	}
	return &types.CreateAuditLogsResponse{Ids: rpcResp.GetIds()}, nil
}

// SearchAuditLogsHTTP 分页查询（HTTP types 入参/出参），复用 gRPC 路径的查询逻辑。
func (c *Core) SearchAuditLogsHTTP(ctx context.Context, req *types.SearchAuditLogsRequest) (*types.SearchAuditLogsResponse, error) {
	if !c.svcCtx.Health.IsReady() {
		return nil, status.Error(codes.Unavailable, "服务正在启动中或依赖暂不可用，请稍后重试")
	}

	input, err := buildSearchInput(req.ServiceName, req.ActorId, req.Action,
		req.ResourceType, req.ResourceId, req.TraceId,
		req.StartTime, req.EndTime, req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}

	rows, total, err := c.svcCtx.AuditLogModel.Search(ctx, input)
	if err != nil {
		logx.WithContext(ctx).Errorw("查询审计日志失败",
			logx.Field("filter", describeFilter(input)),
			logx.Field("error", err),
		)
		return nil, status.Error(codes.Internal, "查询审计日志失败")
	}

	list := make([]types.AuditLogItem, 0, len(rows))
	for _, row := range rows {
		p := ToProtoAuditLog(row)
		list = append(list, types.AuditLogItem{
			Id:           p.Id,
			TraceId:      p.TraceId,
			ServiceName:  p.ServiceName,
			Operation:    p.Operation,
			ActorId:      p.ActorId,
			ActorType:    p.ActorType,
			Action:       p.Action,
			ResourceType: p.ResourceType,
			ResourceId:   p.ResourceId,
			SourceIp:     p.SourceIp,
			UserAgent:    p.UserAgent,
			RequestUri:   p.RequestUri,
			StatusCode:   p.StatusCode,
			RequestBody:  p.RequestBody,
			ResponseBody: p.ResponseBody,
			Metadata:     p.Metadata,
			ErrorMessage: p.ErrorMessage,
			DurationMs:   p.DurationMs,
			CreatedAt:    p.CreatedAt,
		})
	}

	logx.WithContext(ctx).Infow("审计日志查询成功",
		logx.Field("page", input.Page),
		logx.Field("page_size", input.PageSize),
		logx.Field("returned", len(list)),
		logx.Field("total", total),
	)

	return &types.SearchAuditLogsResponse{List: list, Total: total}, nil
}

// buildSearchInput 归一化并校验分页参数（两种协议入口共用）。
func buildSearchInput(serviceName, actorID, action, resourceType, resourceID, traceID string,
	startTime, endTime, page, pageSize int64) (model.SearchInput, error) {

	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	// 深度分页保护：大偏移量 LIMIT 会触发 MySQL 大范围扫描，明确拒绝并引导缩小过滤范围。
	if (page-1)*pageSize > maxOffset {
		return model.SearchInput{}, status.Errorf(codes.InvalidArgument,
			"翻页过深（offset 上限 %d），请缩小时间范围或过滤条件", maxOffset)
	}

	return model.SearchInput{
		ServiceName:  serviceName,
		ActorId:      actorID,
		Action:       action,
		ResourceType: resourceType,
		ResourceId:   resourceID,
		TraceId:      traceID,
		StartTime:    unixMilliToTime(startTime),
		EndTime:      unixMilliToTime(endTime),
		Page:         page,
		PageSize:     pageSize,
	}, nil
}

// describeFilter 生成可安全输出的过滤条件摘要（仅白名单键，不含任何 body 内容）。
func describeFilter(in model.SearchInput) string {
	return mask.Text(`{"service_name":"` + in.ServiceName +
		`","actor_id":"` + in.ActorId +
		`","action":"` + in.Action +
		`","trace_id":"` + in.TraceId + `"}`)
}

// LogBodyForDebug 供协议层输出调试日志用的安全摘要：先截断再按键脱敏。
func LogBodyForDebug(body string) string {
	return mask.Body(body, logBodyLimit)
}

// ToProtoAuditLog model 实体转 pb 实体，时间统一输出为 Unix 毫秒。
func ToProtoAuditLog(row *model.AuditLog) *pb.AuditLog {
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

// unixMilliToTime 0 表示未传该过滤条件，返回 time 零值由 model 层跳过。
func unixMilliToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
