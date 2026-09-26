package config

import (
	"github.com/zeromicro/go-zero/core/discov"
	"github.com/zeromicro/go-zero/core/service"
)

// Config 单进程同时提供 gRPC 与 HTTP 的组合服务配置，全部字段从 YAML 配置文件加载。
type Config struct {
	service.ServiceConf

	// Mysql 本地 MySQL 数据源配置。
	Mysql MysqlConf

	// Rpc gRPC 服务端配置段。
	Rpc RpcSection

	// Http HTTP 业务服务端配置段（mTLS）。
	Http HttpSection

	// Admin 管理端口配置段（明文 HTTP，专供 k8s 探针/状态查询，不启用 mTLS）。
	Admin AdminSection

	// RpcTLS HTTP 网关回环直连本服务 gRPC 时使用的 mTLS 客户端证书；为空则明文访问（仅限本地调试）。
	// 单进程内 HTTP -> gRPC 走本机直连（dns:///Rpc.ListenOn），不经 etcd 服务发现，
	// 因此 etcd 故障不会影响同进程业务链路；未来跨服务调用（如 auth.rpc）再新增独立发现配置。
	RpcTLS ClientTLSConf `json:",optional"`
}

// MysqlConf MySQL 连接配置。
type MysqlConf struct {
	// DataSource DSN，例如：
	// root:root@tcp(127.0.0.1:3306)/auditlog?charset=utf8mb4&parseTime=true&loc=Local
	DataSource string
}

// ServerTLSConf 服务端 TLS 证书配置；CACertFile 非空时强制校验对端客户端证书（mTLS）。
type ServerTLSConf struct {
	CertFile   string `json:",optional"`
	KeyFile    string `json:",optional"`
	CACertFile string `json:",optional"`
}

// ClientTLSConf 客户端 mTLS 证书配置。
type ClientTLSConf struct {
	CertFile   string `json:",optional"`
	KeyFile    string `json:",optional"`
	CACertFile string `json:",optional"`
	// ServerName 覆盖 SNI/主机名校验（etcd 发现拿到 IP:Port 时使用）。
	ServerName string `json:",optional"`
}

// RpcSection gRPC 服务端配置段。
type RpcSection struct {
	// ListenOn gRPC 监听地址。
	ListenOn string
	// 单次 RPC 超时（毫秒）。
	Timeout int64 `json:",default=2000"`
	// CPU 过载保护阈值（900 = 90%）。
	CpuThreshold int64 `json:",default=900,range=[0:1000)"`
	// gRPC 健康检查开关。
	Health bool `json:",default=true"`
	// Etcd 注册中心；配置后服务启动即注册。
	Etcd discov.EtcdConf `json:",optional"`
	// TLS 服务端 mTLS 证书；为空时退化为明文 gRPC（仅限本地调试）。
	TLS ServerTLSConf `json:",optional"`
}

// HttpSection HTTP 服务端配置段。
type HttpSection struct {
	Host         string `json:",default=0.0.0.0"`
	Port         int
	Timeout      int64 `json:",default=3000"`
	MaxConns     int   `json:",default=10000"`
	MaxBytes     int64 `json:",default=1048576"`
	CpuThreshold int64 `json:",default=900,range=[0:1000)"`
	// CertFile/KeyFile 服务端 HTTPS 证书；为空时 HTTP 明文（仅限本地调试）。
	CertFile string `json:",optional"`
	KeyFile  string `json:",optional"`
	// ClientCACertFile 校验调用方客户端证书的 CA；非空时 HTTP 升级为 mTLS。
	ClientCACertFile string `json:",optional"`
}

// AdminSection 管理端口配置段：独立于业务端口的明文 HTTP 服务，
// 提供 /healthz（liveness）、/readyz（readiness）、/status（状态详情）供 k8s 探针使用。
// 刻意不配置 TLS/mTLS：k8s kubelet 探针不携带业务客户端证书。
type AdminSection struct {
	Host string `json:",default=0.0.0.0"`
	// Port 管理端口，默认 8081。
	Port int `json:",default=8081"`
}
