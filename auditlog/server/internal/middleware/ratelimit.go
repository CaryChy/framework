// Package middleware 提供 gRPC 与 HTTP 共用的进程内限流中间件。
// 基于内存令牌桶（golang.org/x/time/rate），不依赖 Redis；
// 注意限流为单实例粒度，多实例部署时整体限流阈值 = 单实例值 × 实例数。
package middleware

import (
	"context"
	"net/http"

	"cari.com.cn/framework/auditlog/server/internal/resp"
	"github.com/zeromicro/go-zero/core/logx"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewGrpcRateLimit 返回 gRPC 一元拦截器：超过 rps 时返回 codes.ResourceExhausted。
// rps <= 0 时不限流（返回 nil，调用方应跳过注册）。
// 突发容量与 rps 相同：允许最多 1 秒量的瞬时突发。
func NewGrpcRateLimit(rps int) grpc.UnaryServerInterceptor {
	if rps <= 0 {
		return nil
	}
	limiter := rate.NewLimiter(rate.Limit(rps), rps)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		if !limiter.Allow() {
			// 限流是正常背压而非服务错误：Info 级记录，避免被刷时 Error 日志洪泛淹没真告警。
			logx.WithContext(ctx).Infow("gRPC 请求被限流",
				logx.Field("method", info.FullMethod),
				logx.Field("rps", rps),
			)
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(ctx, req)
	}
}

// NewHttpRateLimit 返回 rest 全局中间件：超过 rps 时返回 429。
// rps <= 0 时不限流（返回 nil，调用方应跳过 Use）。
func NewHttpRateLimit(rps int) func(next http.HandlerFunc) http.HandlerFunc {
	if rps <= 0 {
		return nil
	}
	limiter := rate.NewLimiter(rate.Limit(rps), rps)
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
				// 同 gRPC 侧：Info 级记录，避免被刷时 Error 日志洪泛。
				logx.WithContext(r.Context()).Infow("HTTP 请求被限流",
					logx.Field("path", r.URL.Path),
					logx.Field("rps", rps),
				)
				resp.ErrWithHeader(w, http.StatusTooManyRequests, "auditlog.too_many_requests",
					"rate limit exceeded", map[string]string{"Retry-After": "1"})
				return
			}
			next(w, r)
		}
	}
}
