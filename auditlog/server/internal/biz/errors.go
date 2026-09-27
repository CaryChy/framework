// 业务错误与错误分类：哨兵错误定义业务语义，Spec 错误定义表是本服务所有对外错误的唯一事实源，
// HTTP（resp.BizStatus / resp.FromBizErr）与 gRPC（toGrpcErr）的状态码、error 标识均由表派生，
// 对外文案统一经 MessageOf 脱敏，保证两端语义一致且不泄露内部细节。
//
// auditlog 服务错误定义（规范：docs/# 统一微服务 API 响应规范）：
//
//	| error                        | http | grpc              | retryable | 来源           |
//	|------------------------------|------|-------------------|-----------|----------------|
//	| auditlog.unavailable         | 503  | UNAVAILABLE       | true      | ErrUnavailable |
//	| auditlog.empty_logs          | 400  | INVALID_ARGUMENT  | false     | ErrEmptyLogs   |
//	| auditlog.empty_service_name  | 400  | INVALID_ARGUMENT  | false     | ErrEmptySvcName|
//	| auditlog.batch_too_large     | 400  | INVALID_ARGUMENT  | false     | ErrBatchTooLarge|
//	| auditlog.duplicate_id        | 400  | INVALID_ARGUMENT  | false     | ErrDuplicateID |
//	| auditlog.page_too_deep       | 400  | INVALID_ARGUMENT  | false     | ErrPageTooDeep |
//	| auditlog.write_failed        | 500  | INTERNAL          | false     | ErrWriteFailed |
//	| auditlog.query_failed        | 500  | INTERNAL          | false     | ErrQueryFailed |
//	| auditlog.internal_error      | 500  | INTERNAL          | false     | 未知错误兜底    |
//	| auditlog.invalid_parameter   | 400  | INVALID_ARGUMENT  | false     | HTTP 请求解析失败|
//
//	非 biz 哨兵错误（协议层专用，不在本表）：auditlog.unauthorized（/status 鉴权 401）、
//	auditlog.too_many_requests（限流 429）、auditlog.starting/not_ready/stopping（/readyz 503 三态）。
package biz

import "errors"

// 业务错误：供协议层映射为 gRPC codes / HTTP 状态码。
var (
	ErrUnavailable   = errors.New("服务正在启动中或依赖暂不可用，请稍后重试")
	ErrEmptyLogs     = errors.New("logs 不能为空")
	ErrEmptySvcName  = errors.New("service_name 不能为空")
	ErrBatchTooLarge = errors.New("单次写入条数超限")
	ErrDuplicateID   = errors.New("日志 ID 已存在，请勿重复写入")
	ErrPageTooDeep   = errors.New("分页过深，请缩小时间范围或加大过滤条件")
	ErrWriteFailed   = errors.New("写入审计日志失败")
	ErrQueryFailed   = errors.New("查询审计日志失败")
)

// ErrorKind 领域错误类别，与传输协议无关。
type ErrorKind int

const (
	// KindInternal 服务端内部错误。
	KindInternal ErrorKind = iota
	// KindUnavailable 依赖未就绪，调用方可安全重试。
	KindUnavailable
	// KindInvalidArgument 调用方参数或边界错误。
	KindInvalidArgument
)

// Spec 对外错误的稳定规格：error 标识、错误类别、是否可重试。
type Spec struct {
	// ID 稳定错误标识，格式 service.error（如 auditlog.unavailable）。
	ID string
	// Kind 错误类别，协议层据此翻译 HTTP/gRPC 状态码。
	Kind ErrorKind
	// Retryable 调用方是否可安全重试。
	Retryable bool
}

// errSpecs 哨兵错误 → 规格；未知错误一律兜底为 internalErrorSpec。
var errSpecs = map[error]Spec{
	ErrUnavailable:   {ID: "auditlog.unavailable", Kind: KindUnavailable, Retryable: true},
	ErrEmptyLogs:     {ID: "auditlog.empty_logs", Kind: KindInvalidArgument},
	ErrEmptySvcName:  {ID: "auditlog.empty_service_name", Kind: KindInvalidArgument},
	ErrBatchTooLarge: {ID: "auditlog.batch_too_large", Kind: KindInvalidArgument},
	ErrDuplicateID:   {ID: "auditlog.duplicate_id", Kind: KindInvalidArgument},
	ErrPageTooDeep:   {ID: "auditlog.page_too_deep", Kind: KindInvalidArgument},
	ErrWriteFailed:   {ID: "auditlog.write_failed", Kind: KindInternal},
	ErrQueryFailed:   {ID: "auditlog.query_failed", Kind: KindInternal},
}

// internalErrorSpec 未知错误的兜底规格。
var internalErrorSpec = Spec{ID: "auditlog.internal_error", Kind: KindInternal}

// InternalMessage 未知内部错误对外的统一脱敏文案：原始错误（可能含 SQL/表结构等
// 内部细节）只落服务端日志，不透传给调用方。
const InternalMessage = "服务内部错误"

// SpecOf 返回错误的稳定规格；未知错误一律视为 auditlog.internal_error。
func SpecOf(err error) Spec {
	for sentinel, spec := range errSpecs {
		if errors.Is(err, sentinel) {
			return spec
		}
	}
	return internalErrorSpec
}

// KindOf 返回领域错误的类别；未知错误一律视为内部错误。
func KindOf(err error) ErrorKind {
	return SpecOf(err).Kind
}

// MessageOf 返回错误对外展示文案：命中错误定义表的哨兵错误返回【哨兵自身的安全文案】
// （即使错误被 fmt.Errorf/errors.Join 包装，包装附加的文本也不对外，防止泄露内部细节）；
// 未知错误一律脱敏为 InternalMessage。HTTP（resp.FromBizErr）与 gRPC（toGrpcErr）
// 必须统一走本函数，保证两端对外文案一致且不泄露内部细节。
func MessageOf(err error) string {
	for sentinel := range errSpecs {
		if errors.Is(err, sentinel) {
			return sentinel.Error()
		}
	}
	return InternalMessage
}
