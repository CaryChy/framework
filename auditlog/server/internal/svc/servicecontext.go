package svc

import (
	"context"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	clientv3 "go.etcd.io/etcd/client/v3"

	"cari.com.cn/framework/auditlog/common/logbridge"
	"cari.com.cn/framework/auditlog/model"
	"cari.com.cn/framework/auditlog/server/internal/config"
	"cari.com.cn/framework/auditlog/server/internal/health"
)

// 依赖探活周期。
const (
	mysqlProbeInterval = 3 * time.Second
	etcdProbeInterval  = 5 * time.Second
	etcdDialTimeout    = 2 * time.Second
)

// ServiceContext 组合服务的依赖容器：gRPC 与 HTTP 两种协议入口共用。
//
// 架构说明：单进程组合服务内，HTTP 入口不再通过 gRPC 回环调用本进程业务逻辑，
// 而是与 gRPC 入口共享 internal/core 层的同一份实现（见 server/internal/core）。
// 这消除了双份 logic/mapper、本机 RPC 往返延迟叠加，以及 RpcTLS 回环客户端证书。
type ServiceContext struct {
	Config        config.Config
	Health        *health.Tracker
	AuditLogModel model.AuditLogModel // MySQL 数据访问（core 层使用）

	// 仅供 Stop 时释放的外部资源。
	etcdCli *clientv3.Client
}

// NewServiceContext 初始化公共依赖。
// 关键约定：MySQL/etcd 等外部依赖连接失败【不会】导致进程退出——
// 健康追踪器初始状态为 starting，后台 goroutine 周期重试，依赖恢复后自动转 ready，
// 期间 /readyz 返回 503、业务请求返回 Unavailable。仅配置类错误才返回错误。
func NewServiceContext(c config.Config) (*ServiceContext, error) {
	tracker := health.NewTracker()
	svcCtx := &ServiceContext{
		Config: c,
		Health: tracker,
	}

	// ---------------- MySQL（必需组件，决定 readiness） ----------------
	// sqlx.NewMysql 只封装 DSN，不建立真实连接，不会失败。
	conn := sqlx.NewMysql(c.Mysql.DataSource)
	svcCtx.AuditLogModel = model.NewAuditLogModel(conn)
	tracker.AddComponent("mysql", true, mysqlProbeInterval, func(ctx context.Context) error {
		db, err := conn.RawDB()
		if err != nil {
			return err
		}
		return db.PingContext(ctx)
	})

	// ---------------- etcd（非必需组件，仅展示状态，不阻断 readiness） ----------------
	// etcd 故障只影响本服务对【其他服务】的注册可见性，不影响本进程 HTTP/gRPC 双入口，故不阻断 ready。
	if len(c.Rpc.Etcd.Hosts) > 0 {
		etcdCli, err := clientv3.New(clientv3.Config{
			Endpoints:   c.Rpc.Etcd.Hosts,
			DialTimeout: etcdDialTimeout,
			// 将 etcd client 的 zap 日志桥接到 logx，统一 JSON 输出格式。
			Logger: logbridge.NewEtcdLogger(),
		})
		if err != nil {
			// 仅配置类错误（endpoint 非法等）才可能走到这里。
			return nil, err
		}
		svcCtx.etcdCli = etcdCli
		endpoints := c.Rpc.Etcd.Hosts
		tracker.AddComponent("etcd", false, etcdProbeInterval, func(ctx context.Context) error {
			// Status 走最廉价的连通性检查；多 endpoint 时探测首个即可反映本地链路。
			_, err := etcdCli.Status(ctx, endpoints[0])
			return err
		})
	}

	return svcCtx, nil
}

// Stop 释放后台探活 goroutine 与 etcd 探活客户端。
func (s *ServiceContext) Stop() {
	s.Health.Stop()
	if s.etcdCli != nil {
		if err := s.etcdCli.Close(); err != nil {
			logx.Errorw("关闭 etcd 探活客户端失败", logx.Field("error", err))
		}
	}
}
