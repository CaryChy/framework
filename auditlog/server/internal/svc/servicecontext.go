package svc

import (
	"context"
	"time"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"github.com/zeromicro/go-zero/zrpc"
	clientv3 "go.etcd.io/etcd/client/v3"

	"cari.com.cn/framework/auditlog/common/logbridge"
	tlsutil "cari.com.cn/framework/auditlog/common/tls"
	pb "cari.com.cn/framework/auditlog/rpc/auditlog"

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

// ServiceContext 组合服务的依赖容器：gRPC 业务层与 HTTP 网关共用。
type ServiceContext struct {
	Config        config.Config
	Health        *health.Tracker
	AuditLogModel model.AuditLogModel      // gRPC 业务层：MySQL 数据访问
	AuditLogRpc   pb.AuditLogServiceClient // HTTP 网关：回环直连本进程 gRPC（mTLS，不经 etcd）

	// 仅供 Stop 时释放的外部资源。
	etcdCli *clientv3.Client
}

// NewServiceContext 初始化公共依赖。
// 关键约定：MySQL/etcd 等外部依赖连接失败【不会】导致进程退出——
// 健康追踪器初始状态为 starting，后台 goroutine 周期重试，依赖恢复后自动转 ready，
// 期间 /readyz 返回 503、业务请求返回 Unavailable。仅配置类错误（如证书文件缺失）才返回错误。
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
	// etcd 故障只影响本服务对【其他服务】的注册可见性，不影响 gRPC 直连与同进程 HTTP 回环，故不阻断 ready。
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

	// ---------------- HTTP 网关 -> 本进程 gRPC：mTLS 本机直连（不经 etcd） ----------------
	// 单进程组合服务内回环调用直连 Rpc.ListenOn，避免 etcd 故障时 resolver 地址列表清空导致 HTTP 503；
	// 外部服务（未来的 auth.rpc 等）仍按标准方式经 etcd 发现。
	clientOpts := make([]zrpc.ClientOption, 0, 1)
	if len(c.RpcTLS.CertFile) > 0 && len(c.RpcTLS.KeyFile) > 0 {
		creds, err := tlsutil.NewClientCredentials(
			c.RpcTLS.CertFile, c.RpcTLS.KeyFile, c.RpcTLS.CACertFile, c.RpcTLS.ServerName,
		)
		if err != nil {
			return nil, err
		}
		clientOpts = append(clientOpts, zrpc.WithTransportCredentials(creds))
		logx.Infow("HTTP 回环访问本服务 gRPC 已启用 mTLS（本机直连，不经 etcd）",
			logx.Field("cert", c.RpcTLS.CertFile),
			logx.Field("server_name", c.RpcTLS.ServerName),
		)
	} else {
		logx.Info("未配置 mTLS 客户端证书，将以明文回环访问本服务 gRPC")
	}

	// 代码方式构造配置需显式填充默认值（YAML default 标签不生效）。
	// FillDefault 要求 optional 字段（Target）保持零值，故先填充再覆盖 Target（同 go-zero NewClientWithTarget）。
	var clientConf zrpc.RpcClientConf
	if err := conf.FillDefault(&clientConf); err != nil {
		return nil, err
	}
	clientConf.Target = "dns:///" + c.Rpc.ListenOn
	clientConf.Timeout = c.Rpc.Timeout
	client, err := zrpc.NewClient(clientConf, clientOpts...)
	if err != nil {
		return nil, err
	}
	svcCtx.AuditLogRpc = pb.NewAuditLogServiceClient(client.Conn())

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
