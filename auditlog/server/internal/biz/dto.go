// 协议无关的领域模型：gRPC 与 HTTP 的请求/响应均通过 convert 层与这三个结构体互转，
// 业务规则与语义换算（NULL 解包、时间戳）只在这一层实现一次。
package biz

// CreateLog 待写入的单条日志。
type CreateLog struct {
	Id           string
	TraceId      string
	ServiceName  string
	Operation    string
	ActorId      string
	ActorType    string
	Action       string
	ResourceType string
	ResourceId   string
	SourceIp     string
	UserAgent    string
	RequestUri   string
	StatusCode   int32
	RequestBody  string
	ResponseBody string
	Metadata     string
	ErrorMessage string
	DurationMs   int64
}

// SearchFilter 查询过滤条件，分页字段 <=0 时由 biz 归一化。
type SearchFilter struct {
	ServiceName  string
	ActorId      string
	Action       string
	ResourceType string
	ResourceId   string
	TraceId      string
	StartTimeMs  int64
	EndTimeMs    int64
	Page         int64
	PageSize     int64
}

// AuditLogView 审计日志输出视图；NULL→"" 与时间转毫秒的规则在 toView 统一实现。
type AuditLogView struct {
	Id           string
	TraceId      string
	ServiceName  string
	Operation    string
	ActorId      string
	ActorType    string
	Action       string
	ResourceType string
	ResourceId   string
	SourceIp     string
	UserAgent    string
	RequestUri   string
	StatusCode   int32
	RequestBody  string
	ResponseBody string
	Metadata     string
	ErrorMessage string
	DurationMs   int64
	CreatedAtMs  int64
}
