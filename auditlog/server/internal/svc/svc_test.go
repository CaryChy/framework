package svc

import (
	"testing"

	"github.com/zeromicro/go-zero/core/discov"
	"github.com/zeromicro/go-zero/core/service"

	"cari.com.cn/framework/auditlog/server/internal/config"
)

// TestNewServiceContext_DSNPlaceholder 环境变量未展开时应返回明确错误，
// 而非让 sqlx 去解析一个含 ${...} 的非法 DSN。
func TestNewServiceContext_DSNPlaceholder(t *testing.T) {
	c := config.Config{
		ServiceConf: service.ServiceConf{Name: "test"},
		Mysql: config.MysqlConf{
			DataSource: "${AUDITLOG_DB_USER}:pass@tcp(127.0.0.1:3306)/auditlog",
		},
	}
	svcCtx, err := NewServiceContext(c)
	if err == nil {
		if svcCtx != nil {
			svcCtx.Stop()
		}
		t.Fatal("DSN 含占位符时应返回错误")
	}
}

// TestNewServiceContext_NilEtcdNoHosts 未配置 etcd 时不应创建 etcd 客户端。
func TestNewServiceContext_NilEtcdNoHosts(t *testing.T) {
	// 使用一个真实可达但不存在的 DSN 让 sqlx 连接（连接失败不影响 etcd 客户端是否创建的判断）。
	c := config.Config{
		ServiceConf: service.ServiceConf{Name: "test"},
		Mysql: config.MysqlConf{
			DataSource: "root:pass@tcp(127.0.0.1:3306)/auditlog?parseTime=true",
		},
	}
	svcCtx, err := NewServiceContext(c)
	if err != nil {
		t.Fatalf("无 etcd 配置时 NewServiceContext 不应返回错误: %v", err)
	}
	defer svcCtx.Stop()
	if svcCtx.etcdCli != nil {
		t.Fatal("未配置 etcd Hosts 时 etcdCli 应为 nil")
	}
}

// TestEtcdClientConfig_TLSFailFast 声明了 etcd TLS 但证书文件缺失时必须返回错误，
// 不允许静默降级为明文连接。
func TestEtcdClientConfig_TLSFailFast(t *testing.T) {
	conf := discov.EtcdConf{
		Hosts:       []string{"127.0.0.1:2379"},
		CertFile:    "/nonexistent/cert.pem",
		CertKeyFile: "/nonexistent/key.pem",
		CACertFile:  "/nonexistent/ca.pem",
	}
	if _, err := etcdClientConfig(conf); err == nil {
		t.Fatal("etcd TLS 证书缺失时 etcdClientConfig 应返回错误（fail-fast）")
	}
}

// TestPickEtcdConf 探活配置选取：优先 Rpc 段，Rpc 为空时回退 Http 段，均无则不探活。
func TestPickEtcdConf(t *testing.T) {
	rpcOnly := config.Config{}
	rpcOnly.Rpc.Etcd.Hosts = []string{"127.0.0.1:2379"}
	if _, ok := pickEtcdConf(rpcOnly); !ok {
		t.Fatal("Rpc.Etcd 配置后应启用探活")
	}

	httpOnly := config.Config{}
	httpOnly.Http.Etcd.Hosts = []string{"127.0.0.1:2379"}
	if _, ok := pickEtcdConf(httpOnly); !ok {
		t.Fatal("仅 Http.Etcd 配置时也应启用探活")
	}

	if _, ok := pickEtcdConf(config.Config{}); ok {
		t.Fatal("两段均未配置时不应启用探活")
	}
}
