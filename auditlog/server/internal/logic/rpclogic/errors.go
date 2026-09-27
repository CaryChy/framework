package rpclogic

import (
	"strconv"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cari.com.cn/framework/auditlog/server/internal/biz"
)

// kindGrpcCode 领域错误类别 → gRPC 状态码。
func kindGrpcCode(k biz.ErrorKind) codes.Code {
	switch k {
	case biz.KindUnavailable:
		return codes.Unavailable
	case biz.KindInvalidArgument:
		return codes.InvalidArgument
	default:
		return codes.Internal
	}
}

// toGrpcErr 将业务错误翻译为 gRPC 错误：status.code 表达错误类别，
// status.details 携带 errdetails.ErrorInfo（Reason = auditlog.xxx 稳定标识，
// 与 HTTP 侧共用 biz.SpecOf 错误定义表）；status.message 走 biz.MessageOf，
// 未知内部错误脱敏为固定文案，原始错误只落服务端日志。
func toGrpcErr(err error) error {
	spec := biz.SpecOf(err)
	st := status.New(kindGrpcCode(spec.Kind), biz.MessageOf(err))
	withDetails, derr := st.WithDetails(&errdetails.ErrorInfo{
		Reason: spec.ID,
		Domain: "auditlog",
		Metadata: map[string]string{
			"retryable": boolStr(spec.Retryable),
			"ts":        strconv.FormatInt(time.Now().UnixMilli(), 10),
		},
	})
	if derr != nil {
		// details 构造失败不影响错误语义本身。
		return st.Err()
	}
	return withDetails.Err()
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
