package main

import (
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"

	tlsutil "cari.com.cn/framework/auditlog/common/tls"
	"cari.com.cn/framework/auditlog/server/internal/config"
)

// mtlsPolicy 把调用方身份策略转换为 tls 包策略，HTTP 与 gRPC 共用。
// 组织/项目不经过配置：tls 包在启动时从本服务证书的 SPIFFE URI 派生基准。
func mtlsPolicy(p config.IdentityPolicy) tlsutil.Policy {
	return tlsutil.Policy{
		AllowedBusinessSystems: p.AllowedBusinessSystems,
		AllowedServices:        p.AllowedServices,
	}
}

// defaultRpcMiddlewares / defaultRestMiddlewares 显式声明框架中间件全开。
// 主程序以代码方式构造 ServerConf，框架 YAML 的 default=true 标签不生效，故在此对齐框架默认值。

func defaultRpcMiddlewares() zrpc.ServerMiddlewaresConf {
	return zrpc.ServerMiddlewaresConf{
		Trace:      true,
		Recover:    true,
		Stat:       true,
		Prometheus: true,
		Breaker:    true,
	}
}

func defaultRestMiddlewares() rest.MiddlewaresConf {
	return rest.MiddlewaresConf{
		Trace:      true,
		Log:        true,
		Prometheus: true,
		MaxConns:   true,
		Breaker:    true,
		Shedding:   true,
		Timeout:    true,
		Recover:    true,
		Metrics:    true,
		MaxBytes:   true,
		Gunzip:     true,
	}
}
