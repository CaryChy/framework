package config

import (
	"github.com/zeromicro/go-zero/core/discov"
	"github.com/zeromicro/go-zero/core/service"
)

// Config auditlog 服务配置：gRPC、HTTP、管理端口三段，全部从 YAML 加载。
type Config struct {
	service.ServiceConf

	// Mysql 本地 MySQL 数据源配置。
	Mysql MysqlConf
	// Rpc gRPC 服务端配置段。
	Rpc RpcSection
	// Http HTTP 业务服务端配置段（mTLS）。
	Http HttpSection
	// Admin 管理端口配置段（明文 HTTP，供 k8s 探针，不启用 mTLS）。
	Admin AdminSection
}

// MysqlConf MySQL 连接配置与连接池参数（<=0 表示不限制/使用默认值）。
type MysqlConf struct {
	// DataSource DSN，敏感项用 ${VAR} 环境变量占位符注入。
	DataSource string
	// MaxOpenConns 最大打开连接数。
	MaxOpenConns int `json:",default=0"`
	// MaxIdleConns 最大空闲连接数；<=0 时取 MaxOpenConns。
	MaxIdleConns int `json:",default=0"`
	// ConnMaxLifetime 连接最大存活时长（毫秒）。
	ConnMaxLifetime int64 `json:",default=0"`
	// ConnMaxIdleTime 连接最大空闲时长（毫秒）。
	ConnMaxIdleTime int64 `json:",default=0"`
}

// ServerTLSConf 服务端 TLS 证书配置；CACertFile 非空时强制校验客户端证书（mTLS）。
type ServerTLSConf struct {
	CertFile   string `json:",optional"`
	KeyFile    string `json:",optional"`
	CACertFile string `json:",optional"`
	// ClientIdentity 调用方 SPIFFE 身份白名单；启用 mTLS 时生效。
	ClientIdentity IdentityPolicy `json:",optional"`
}

// IdentityPolicy mTLS 调用方身份校验的可选白名单。
// 组织/项目不从配置读取：启动时从本服务证书的 SPIFFE URI 派生，调用方必须同组织同项目；
// 白名单留空表示该层不限制。
type IdentityPolicy struct {
	// AllowedBusinessSystems 允许的业务系统白名单；空表示不限制。
	AllowedBusinessSystems []string `json:",optional"`
	// AllowedServices 允许的具体服务白名单；空表示不限制。
	AllowedServices []string `json:",optional"`
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
	// RateLimit gRPC 全局限流（请求/秒，单实例内存令牌桶）；0 表示不限流。
	RateLimit int64 `json:",default=0"`
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
	// Etcd 注册中心；配置后 HTTP 服务就绪即注册（key 独立，与 gRPC 不冲突）。
	Etcd discov.EtcdConf `json:",optional"`
	// AdvertiseHost 发布到 etcd 的地址主机部分；留空时取 Host。
	// Host 为 0.0.0.0/:: 等未指定地址时必须配置本项为实例可达 IP/域名，否则启动失败。
	AdvertiseHost string `json:",optional"`
	// RateLimit HTTP 全局限流（请求/秒，单实例内存令牌桶）；0 表示不限流。
	RateLimit int64 `json:",default=0"`
	// CertFile/KeyFile 服务端 HTTPS 证书；为空时 HTTP 明文（仅限本地调试）。
	CertFile string `json:",optional"`
	KeyFile  string `json:",optional"`
	// ClientCACertFile 校验调用方客户端证书的 CA；非空时 HTTP 升级为 mTLS。
	ClientCACertFile string `json:",optional"`
	// ClientIdentity 调用方 SPIFFE 身份白名单；启用 mTLS 时生效。
	ClientIdentity IdentityPolicy `json:",optional"`
}

// AdminSection 管理端口配置段：明文 HTTP，供 k8s 探针使用，刻意不配置 TLS。
type AdminSection struct {
	Host string `json:",default=0.0.0.0"`
	// Port 管理端口，默认 8081。
	Port int `json:",default=8081"`
	// StatusToken 访问 /status 的 Bearer 令牌（建议经 ${AUDITLOG_ADMIN_TOKEN} 注入）；
	// 留空则 /status 不鉴权（仅限受信内网）。/healthz、/readyz 恒开放供 kubelet 使用。
	StatusToken string `json:",optional"`
}
