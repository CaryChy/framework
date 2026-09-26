package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/zeromicro/go-zero/core/discov"
	"github.com/zeromicro/go-zero/core/service"
)

// envPrefix 配置文件环境变量插值前缀。写法：${AUDITLOG_MYSQL_DATASOURCE}，
// 加载配置时用同名环境变量替换，避免把真实凭证提交进版本库。
const envPrefix = "${"

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
}

// Validate 启动前的配置合法性校验：拦截会导致带病运行的错误组合，
// 在 main 中 conf.Load 之后调用，校验失败直接退出。
func (c *Config) Validate() error {
	if len(strings.TrimSpace(c.Mysql.DataSource)) == 0 {
		return fmt.Errorf("config: Mysql.DataSource 不能为空（生产环境请通过环境变量 AUDITLOG_MYSQL_DATASOURCE 注入）")
	}
	if len(strings.TrimSpace(c.Rpc.ListenOn)) == 0 {
		return fmt.Errorf("config: Rpc.ListenOn 不能为空")
	}
	if c.Http.Port <= 0 || c.Http.Port > 65535 {
		return fmt.Errorf("config: Http.Port 非法: %d", c.Http.Port)
	}
	if c.Admin.Port <= 0 || c.Admin.Port > 65535 {
		return fmt.Errorf("config: Admin.Port 非法: %d", c.Admin.Port)
	}
	if c.Admin.Port == c.Http.Port {
		return fmt.Errorf("config: Admin.Port 与 Http.Port 不得相同（管理端口必须与 mTLS 业务端口隔离）")
	}
	// TLS 证书成对校验：只配一半几乎必然是笔误，且会静默退化成不符合预期的安全形态。
	if err := checkCertPair("Rpc.TLS", c.Rpc.TLS.CertFile, c.Rpc.TLS.KeyFile); err != nil {
		return err
	}
	if err := checkCertPair("Http", c.Http.CertFile, c.Http.KeyFile); err != nil {
		return err
	}
	return nil
}

func checkCertPair(name, certFile, keyFile string) error {
	if (len(certFile) == 0) != (len(keyFile) == 0) {
		return fmt.Errorf("config: %s 的 CertFile/KeyFile 必须同时配置或同时为空", name)
	}
	return nil
}

// MysqlConf MySQL 连接配置。
type MysqlConf struct {
	// DataSource DSN。禁止在配置文件中写明文凭证，
	// 应使用 ${AUDITLOG_MYSQL_DATASOURCE} 由环境变量注入，例如：
	// auditlog_app:<password>@tcp(mysql:3306)/auditlog?charset=utf8mb4&parseTime=true&loc=Local
	DataSource string
}

// ServerTLSConf 服务端 TLS 证书配置；CACertFile 非空时强制校验对端客户端证书（mTLS）。
type ServerTLSConf struct {
	CertFile   string `json:",optional"`
	KeyFile    string `json:",optional"`
	CACertFile string `json:",optional"`
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
	MaxConns     int    `json:",default=10000"`
	MaxBytes     int64  `json:",default=1048576"`
	CpuThreshold int64  `json:",default=900,range=[0:1000)"`
	// CertFile/KeyFile 服务端 HTTPS 证书；为空时 HTTP 明文（仅限本地调试）。
	CertFile string `json:",optional"`
	KeyFile  string `json:",optional"`
	// ClientCACertFile 校验调用方客户端证书的 CA；非空时 HTTP 升级为 mTLS。
	ClientCACertFile string `json:",optional"`
}

// AdminSection 管理端口配置段：独立于业务端口的明文 HTTP 服务，
// 提供 /healthz（liveness）、/readyz（readiness）、/status（状态详情）供 k8s 探针使用。
// 刻意不配置 TLS/mTLS：k8s kubelet 探针不携带业务客户端证书。
// /status 含内部依赖信息，部署时必须仅集群内可达，禁止经 Ingress 对外暴露。
type AdminSection struct {
	Host string `json:",default=0.0.0.0"`
	// Port 管理端口，默认 8081。
	Port int `json:",default=8081"`
}

// LoadConfigRaw 读取配置文件原始字节，供 conf.LoadConfigFromBytes 做环境变量插值前使用。
func LoadConfigRaw(file string) ([]byte, error) {
	return os.ReadFile(file)
}

// ExpandEnv 将配置文本中的 ${VAR} 占位替换为环境变量值；未设置的变量保持原样以便报错定位。
func ExpandEnv(data []byte) []byte {
	return []byte(os.Expand(string(data), func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return envPrefix + key + "}"
	}))
}
