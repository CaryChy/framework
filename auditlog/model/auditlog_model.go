package model

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

// auditLogColumnsList 审计日志表全字段列列表；InsertBatch 占位符数量由 len() 派生，
// 增删列时只需维护本列表一处。
var auditLogColumnsList = []string{
	"`id`",
	"`trace_id`",
	"`service_name`",
	"`operation`",
	"`actor_id`",
	"`actor_type`",
	"`action`",
	"`resource_type`",
	"`resource_id`",
	"`source_ip`",
	"`user_agent`",
	"`request_uri`",
	"`status_code`",
	"`request_body`",
	"`response_body`",
	"`metadata`",
	"`error_message`",
	"`duration_ms`",
	"`created_at`",
}

var auditLogColumns = strings.Join(auditLogColumnsList, ",")

// AuditLog 与 audit_log 表一一对应的实体；仅可空大字段用 sql.NullString 写入真正的 NULL。
type AuditLog struct {
	Id           string         `db:"id"`
	TraceId      string         `db:"trace_id"`
	ServiceName  string         `db:"service_name"`
	Operation    string         `db:"operation"`
	ActorId      string         `db:"actor_id"`
	ActorType    string         `db:"actor_type"`
	Action       string         `db:"action"`
	ResourceType string         `db:"resource_type"`
	ResourceId   string         `db:"resource_id"`
	SourceIp     string         `db:"source_ip"`
	UserAgent    string         `db:"user_agent"`
	RequestUri   string         `db:"request_uri"`
	StatusCode   int64          `db:"status_code"`
	RequestBody  sql.NullString `db:"request_body"`
	ResponseBody sql.NullString `db:"response_body"`
	Metadata     sql.NullString `db:"metadata"`
	ErrorMessage string         `db:"error_message"`
	DurationMs   int64          `db:"duration_ms"`
	CreatedAt    time.Time      `db:"created_at"`
}

// SearchInput 分页查询入参，所有过滤条件均可选；时间为闭区间。
type SearchInput struct {
	ServiceName  string
	ActorId      string
	Action       string
	ResourceType string
	ResourceId   string
	TraceId      string
	StartTime    time.Time
	EndTime      time.Time
	Page         int64
	PageSize     int64
}

// AuditLogModel 审计日志数据访问接口。
type AuditLogModel interface {
	// InsertBatch 在单条 SQL 中批量写入审计日志。
	InsertBatch(ctx context.Context, data []*AuditLog) (sql.Result, error)
	// Search 按条件分页查询，返回当前页数据与满足条件的总数。
	Search(ctx context.Context, in SearchInput) ([]*AuditLog, int64, error)
}

type customAuditLogModel struct {
	conn sqlx.SqlConn
}

// NewAuditLogModel 创建审计日志 Model。
func NewAuditLogModel(conn sqlx.SqlConn) AuditLogModel {
	return &customAuditLogModel{conn: conn}
}

// InsertBatch 批量插入：使用多行 VALUES 的单条 SQL，减少网络往返与事务开销。
func (m *customAuditLogModel) InsertBatch(ctx context.Context, data []*AuditLog) (sql.Result, error) {
	if len(data) == 0 {
		return nil, nil
	}

	var (
		placeholders []string
		args         []any
	)

	rowPlaceholder := fmt.Sprintf("(%s)", strings.TrimSuffix(strings.Repeat("?,", len(auditLogColumnsList)), ","))
	for _, row := range data {
		placeholders = append(placeholders, rowPlaceholder)
		args = append(args,
			row.Id,
			row.TraceId,
			row.ServiceName,
			row.Operation,
			row.ActorId,
			row.ActorType,
			row.Action,
			row.ResourceType,
			row.ResourceId,
			row.SourceIp,
			row.UserAgent,
			row.RequestUri,
			row.StatusCode,
			row.RequestBody,
			row.ResponseBody,
			row.Metadata,
			row.ErrorMessage,
			row.DurationMs,
			row.CreatedAt,
		)
	}

	query := fmt.Sprintf(
		"INSERT INTO `audit_log` (%s) VALUES %s",
		auditLogColumns,
		strings.Join(placeholders, ","),
	)

	return m.conn.ExecCtx(ctx, query, args...)
}

// Search 动态条件查询：先 COUNT 总数，再按时间倒序分页取数。
func (m *customAuditLogModel) Search(ctx context.Context, in SearchInput) ([]*AuditLog, int64, error) {
	whereClause, args := buildSearchWhere(in)

	var total int64
	countQuery := "SELECT COUNT(1) FROM `audit_log`" + whereClause
	if err := m.conn.QueryRowCtx(ctx, &total, countQuery, args...); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	// 分页默认值与边界由 biz 层统一保证，model 层不重复处理。
	listQuery := fmt.Sprintf(
		"SELECT %s FROM `audit_log`%s ORDER BY `created_at` DESC, `id` DESC LIMIT ?, ?",
		auditLogColumns,
		whereClause,
	)
	queryArgs := append([]any{}, args...)
	queryArgs = append(queryArgs, (in.Page-1)*in.PageSize, in.PageSize)

	var list []*AuditLog
	if err := m.conn.QueryRowsCtx(ctx, &list, listQuery, queryArgs...); err != nil {
		return nil, 0, err
	}

	return list, total, nil
}

// buildSearchWhere 拼装动态 WHERE 子句，参数化查询防止 SQL 注入。
func buildSearchWhere(in SearchInput) (string, []any) {
	var (
		clauses []string
		args    []any
	)

	if len(in.ServiceName) > 0 {
		clauses = append(clauses, "`service_name` = ?")
		args = append(args, in.ServiceName)
	}
	if len(in.ActorId) > 0 {
		clauses = append(clauses, "`actor_id` = ?")
		args = append(args, in.ActorId)
	}
	if len(in.Action) > 0 {
		clauses = append(clauses, "`action` = ?")
		args = append(args, in.Action)
	}
	if len(in.ResourceType) > 0 {
		clauses = append(clauses, "`resource_type` = ?")
		args = append(args, in.ResourceType)
	}
	if len(in.ResourceId) > 0 {
		clauses = append(clauses, "`resource_id` = ?")
		args = append(args, in.ResourceId)
	}
	if len(in.TraceId) > 0 {
		clauses = append(clauses, "`trace_id` = ?")
		args = append(args, in.TraceId)
	}
	if !in.StartTime.IsZero() {
		clauses = append(clauses, "`created_at` >= ?")
		args = append(args, in.StartTime)
	}
	if !in.EndTime.IsZero() {
		clauses = append(clauses, "`created_at` <= ?")
		args = append(args, in.EndTime)
	}

	if len(clauses) == 0 {
		return "", args
	}

	return " WHERE " + strings.Join(clauses, " AND "), args
}
