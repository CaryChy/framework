// 审计日志领域服务：gRPC server 与 HTTP handler 共用，只做协议编解码与错误映射，不重复实现业务。
package biz

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/zeromicro/go-zero/core/logx"

	"cari.com.cn/framework/auditlog/model"
	"cari.com.cn/framework/pkg/id"
)

const (
	maxBatchSize    = 500
	defaultPageSize = 20
	maxPageSize     = 200
	// maxPageOffset 深度分页上限：offset 超过该值拒绝，避免深翻页全表扫描。
	maxPageOffset = 10000
)

// Readiness 抽象就绪检查，避免 biz 反向依赖 svc（由 svc 注入 *health.Tracker）。
type Readiness interface {
	IsReady() bool
}

// AuditLogBiz 审计日志领域服务。
type AuditLogBiz struct {
	readiness Readiness
	mdl       model.AuditLogModel
}

// NewAuditLogBiz 创建领域服务。
func NewAuditLogBiz(readiness Readiness, mdl model.AuditLogModel) *AuditLogBiz {
	return &AuditLogBiz{readiness: readiness, mdl: mdl}
}

// Create 写入一条或多条审计日志，返回生成的日志 ID。
func (b *AuditLogBiz) Create(ctx context.Context, logs []CreateLog) ([]string, error) {
	if !b.readiness.IsReady() {
		return nil, ErrUnavailable
	}
	if len(logs) == 0 {
		return nil, ErrEmptyLogs
	}
	if len(logs) > maxBatchSize {
		return nil, ErrBatchTooLarge
	}

	lg := logx.WithContext(ctx)
	now := time.Now().UTC()
	rows := make([]*model.AuditLog, 0, len(logs))
	ids := make([]string, 0, len(logs))

	for i := range logs {
		item := &logs[i]
		if item.ServiceName == "" {
			return nil, ErrEmptySvcName
		}
		logID := item.Id
		if logID == "" {
			genID, err := id.NewID()
			if err != nil {
				lg.Errorw("生成日志 ID 失败（系统时钟异常）", logx.Field("error", err))
				return nil, ErrWriteFailed
			}
			logID = genID
		}
		rows = append(rows, buildRow(item, logID, now))
		ids = append(ids, logID)
	}

	if _, err := b.mdl.InsertBatch(ctx, rows); err != nil {
		lg.Errorw("批量写入审计日志失败", logx.Field("count", len(rows)), logx.Field("error", err))
		if isDuplicateEntry(err) {
			return nil, ErrDuplicateID
		}
		return nil, ErrWriteFailed
	}

	// 全量 ids 仅在 debug 级输出：批量 500 条时单行约 19KB，info 级会造成日志体积压力。
	lg.Infow("审计日志写入成功", logx.Field("count", len(ids)))
	logx.WithContext(ctx).Debugw("审计日志写入成功（全量 IDs）", logx.Field("ids", ids))
	return ids, nil
}

// Search 按可选条件分页查询审计日志，结果按创建时间倒序返回。
func (b *AuditLogBiz) Search(ctx context.Context, f SearchFilter) ([]AuditLogView, int64, error) {
	if !b.readiness.IsReady() {
		return nil, 0, ErrUnavailable
	}

	page, pageSize := f.Page, f.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	if int64(page-1)*int64(pageSize) > maxPageOffset {
		return nil, 0, ErrPageTooDeep
	}

	input := model.SearchInput{
		ServiceName:  f.ServiceName,
		ActorId:      f.ActorId,
		Action:       f.Action,
		ResourceType: f.ResourceType,
		ResourceId:   f.ResourceId,
		TraceId:      f.TraceId,
		StartTime:    unixMilliToTime(f.StartTimeMs),
		EndTime:      unixMilliToTime(f.EndTimeMs),
		Page:         page,
		PageSize:     pageSize,
	}

	rows, total, err := b.mdl.Search(ctx, input)
	if err != nil {
		logx.WithContext(ctx).Errorw("查询审计日志失败", logx.Field("filter", input), logx.Field("error", err))
		return nil, 0, ErrQueryFailed
	}

	views := make([]AuditLogView, 0, len(rows))
	for _, row := range rows {
		views = append(views, toView(row))
	}

	logx.WithContext(ctx).Infow("审计日志查询成功",
		logx.Field("page", page),
		logx.Field("page_size", pageSize),
		logx.Field("returned", len(views)),
		logx.Field("total", total),
	)
	return views, total, nil
}

// buildRow 构造单条待写入的存储实体。
func buildRow(item *CreateLog, logID string, now time.Time) *model.AuditLog {
	return &model.AuditLog{
		Id:           logID,
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
		StatusCode:   int64(item.StatusCode),
		RequestBody:  nullString(item.RequestBody),
		ResponseBody: nullString(item.ResponseBody),
		Metadata:     nullString(item.Metadata),
		ErrorMessage: item.ErrorMessage,
		DurationMs:   item.DurationMs,
		CreatedAt:    now,
	}
}

// toView 存储实体 → 输出视图，唯一负责 NULL 解包与时间戳序列化。
func toView(row *model.AuditLog) AuditLogView {
	return AuditLogView{
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
		CreatedAtMs:  row.CreatedAt.UnixMilli(),
	}
}

// nullString 可空大字段：空串写为 NULL。
func nullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: len(v) > 0}
}

// isDuplicateEntry 判断是否为 MySQL 主键/唯一键冲突（Error 1062）：
// 调用方自带重复 id 属于客户端输入错误，映射为 ErrDuplicateID（400）而非 500。
func isDuplicateEntry(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// unixMilliToTime 0 表示未传该过滤条件，返回零值由 model 层跳过。
func unixMilliToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
