package types

// AuditLogItem 审计日志条目，字段与 gRPC AuditLog 保持一致。
type AuditLogItem struct {
	Id           string `json:"id,optional,omitempty"`
	TraceId      string `json:"trace_id,optional,omitempty"`
	ServiceName  string `json:"service_name,optional,omitempty"`
	Operation    string `json:"operation,optional,omitempty"`
	ActorId      string `json:"actor_id,optional,omitempty"`
	ActorType    string `json:"actor_type,optional,omitempty"`
	Action       string `json:"action,optional,omitempty"`
	ResourceType string `json:"resource_type,optional,omitempty"`
	ResourceId   string `json:"resource_id,optional,omitempty"`
	SourceIp     string `json:"source_ip,optional,omitempty"`
	UserAgent    string `json:"user_agent,optional,omitempty"`
	RequestUri   string `json:"request_uri,optional,omitempty"`
	StatusCode   int32  `json:"status_code,optional"`
	RequestBody  string `json:"request_body,optional,omitempty"`
	ResponseBody string `json:"response_body,optional,omitempty"`
	Metadata     string `json:"metadata,optional,omitempty"`
	ErrorMessage string `json:"error_message,optional,omitempty"`
	DurationMs   int64  `json:"duration_ms,optional"`
	CreatedAt    int64  `json:"created_at,optional"`
}

// CreateAuditLogsRequest 批量写入请求，Logs 支持一条或多条。
type CreateAuditLogsRequest struct {
	Logs []AuditLogItem `json:"logs"`
}

// CreateAuditLogsResponse 批量写入响应。
type CreateAuditLogsResponse struct {
	Ids []string `json:"ids"`
}

// SearchAuditLogsRequest 分页查询请求，所有过滤条件均可选。
type SearchAuditLogsRequest struct {
	ServiceName  string `form:"service_name,optional"`
	ActorId      string `form:"actor_id,optional"`
	Action       string `form:"action,optional"`
	ResourceType string `form:"resource_type,optional"`
	ResourceId   string `form:"resource_id,optional"`
	TraceId      string `form:"trace_id,optional"`
	StartTime    int64  `form:"start_time,optional"`
	EndTime      int64  `form:"end_time,optional"`
	Page         int64  `form:"page,optional"`
	PageSize     int64  `form:"page_size,optional"`
}

// SearchAuditLogsResponse 分页查询响应。
type SearchAuditLogsResponse struct {
	List  []AuditLogItem `json:"list"`
	Total int64          `json:"total"`
}
