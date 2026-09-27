package main

import (
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"

	tlsutil "cari.com.cn/framework/auditlog/common/tls"
	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/config"
	"cari.com.cn/framework/auditlog/server/internal/middleware"
	rpcserver "cari.com.cn/framework/auditlog/server/internal/server"
	"cari.com.cn/framework/auditlog/server/internal/svc"
)

// setupGRPC 装配 gRPC 服务端：注册服务、限流、mTLS 凭证。
func setupGRPC(c config.Config, svcCtx *svc.ServiceContext) (*zrpc.RpcServer, error) {
	rpcSrv, err := zrpc.NewServer(zrpc.RpcServerConf{
		ServiceConf:  c.ServiceConf,
		ListenOn:     c.Rpc.ListenOn,
		Etcd:         c.Rpc.Etcd,
		Timeout:      c.Rpc.Timeout,
		CpuThreshold: c.Rpc.CpuThreshold,
		Health:       c.Rpc.Health,
		Middlewares:  defaultRpcMiddlewares(),
	}, func(grpcServer *grpc.Server) {
		pb.RegisterAuditLogServiceServer(grpcServer, rpcserver.NewAuditLogServer(svcCtx))
	})
	if err != nil {
		return nil, err
	}

	// 写入/查询的入参含 request_body 等敏感字段，关闭 stat 拦截器的完整入参日志。
	zrpc.DontLogContentForMethod("/auditlog.AuditLogService/CreateAuditLogs")
	zrpc.DontLogContentForMethod("/auditlog.AuditLogService/SearchAuditLogs")

	// 成功响应 trailing metadata 返回 x-response-ts（统一响应规范的 ts 字段）。
	rpcSrv.AddUnaryInterceptors(middleware.ResponseTimestamp())

	// gRPC 全局限流（内存令牌桶，单实例粒度）；0 表示不限流。
	if c.Rpc.RateLimit > 0 {
		if interceptor := middleware.NewGrpcRateLimit(int(c.Rpc.RateLimit)); interceptor != nil {
			rpcSrv.AddUnaryInterceptors(interceptor)
			logx.Infow("gRPC 限流已启用", logx.Field("rps", c.Rpc.RateLimit))
		}
	}

	// 配置了证书即挂载 mTLS 传输凭证，未配置时退化为明文（仅限本地调试）。
	if len(c.Rpc.TLS.CertFile) > 0 && len(c.Rpc.TLS.KeyFile) > 0 {
		creds, err := tlsutil.NewServerCredentials(c.Rpc.TLS.CertFile, c.Rpc.TLS.KeyFile, c.Rpc.TLS.CACertFile,
			mtlsPolicy(c.Rpc.TLS.ClientIdentity))
		if err != nil {
			return nil, err
		}
		rpcSrv.AddOptions(grpc.Creds(creds))
		if len(c.Rpc.TLS.CACertFile) > 0 {
			logx.Infow("gRPC mTLS 已启用，将强制校验调用方客户端证书及 SPIFFE 身份（同组织/项目）",
				logx.Field("cert", c.Rpc.TLS.CertFile),
				logx.Field("ca", c.Rpc.TLS.CACertFile),
				logx.Field("allowed_business_systems", c.Rpc.TLS.ClientIdentity.AllowedBusinessSystems),
				logx.Field("allowed_services", c.Rpc.TLS.ClientIdentity.AllowedServices),
			)
		} else {
			logx.Info("gRPC 单向 TLS 已启用（未配置客户端 CA 校验）")
		}
	} else {
		logx.Info("未配置 TLS 证书，gRPC 以明文模式启动")
	}

	return rpcSrv, nil
}
