package middleware

import (
	"context"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// ResponseTimestamp 返回 gRPC 一元拦截器：成功响应在 trailing metadata 中
// 返回 x-response-ts（Unix 毫秒），对齐统一响应规范的 ts 字段。
func ResponseTimestamp() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		_ = grpc.SetTrailer(ctx, metadata.Pairs("x-response-ts",
			strconv.FormatInt(time.Now().UnixMilli(), 10)))
		return resp, err
	}
}
