// Package svc 组装服务的依赖容器。
// 关键约定：MySQL/etcd 连接失败【不会】导致进程退出——健康追踪器初始为 starting，
// 后台周期重试，依赖恢复后自动转 ready；期间 /readyz 返回 503、业务返回 Unavailable。
// 仅配置类错误（如 DSN 占位符未展开）才返回错误终止启动。
package svc

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/discov"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	clientv3 "go.etcd.io/etcd/client/v3"

	"cari.com.cn/framework/auditlog/model"
	"cari.com.cn/framework/auditlog/server/internal/biz"
	"cari.com.cn/framework/auditlog/server/internal/config"
	"cari.com.cn/framework/auditlog/server/internal/health"
)

// mysqlProbeInterval 依赖探活周期；etcd 探活周期与拨号超时见 etcd.go。
const mysqlProbeInterval = 3 * time.Second

// ServiceContext 依赖容器：gRPC 与 HTTP 两个协议层共用同一套领域服务。
type ServiceContext struct {
	Config config.Config
	Health *health.Tracker
	// AuditLogBiz 审计日志领域服务：gRPC server 与 HTTP handler 共用，不经进程内回环。
	AuditLogBiz *biz.AuditLogBiz

	// etcdCli 仅供 Stop 时释放的健康探活客户端（服务注册的 etcd 连接由 go-zero 框架自管）。
	etcdCli *clientv3.Client
}

// NewServiceContext 初始化公共依赖。
func NewServiceContext(c config.Config) (*ServiceContext, error) {
	tracker := health.NewTracker()
	svcCtx := &ServiceContext{Config: c, Health: tracker}

	// ---------------- MySQL（必需组件，决定 readiness） ----------------
	// 环境变量未注入时 DSN 中仍含 ${...} 占位符，给出明确错误而非晦涩的解析失败。
	if strings.Contains(c.Mysql.DataSource, "${") {
		return nil, errors.New("MySQL DSN 含未展开的环境变量占位符，请先设置 AUDITLOG_DB_USER/AUDITLOG_DB_PASSWORD/AUDITLOG_DB_HOST/AUDITLOG_DB_PORT/AUDITLOG_DB_NAME")
	}
	conn := sqlx.NewMysql(c.Mysql.DataSource)
	applyMysqlPool(conn, c.Mysql)
	svcCtx.AuditLogBiz = biz.NewAuditLogBiz(tracker, model.NewAuditLogModel(conn))
	tracker.AddComponent("mysql", true, mysqlProbeInterval, func(ctx context.Context) error {
		db, err := conn.RawDB()
		if err != nil {
			return err
		}
		return db.PingContext(ctx)
	})

	// ---------------- etcd（非必需组件，不阻断 readiness） ----------------
	// etcd 故障只影响本服务在注册中心的可见性，不影响 gRPC 直连与同进程 HTTP。
	// Rpc/Http 两段都可能配置 EtcdConf：任一段配置了即建立探活（优先 Rpc 段）。
	etcdConf, etcdConfigured := pickEtcdConf(c)
	if etcdConfigured {
		cliCfg, err := etcdClientConfig(etcdConf)
		if err != nil {
			// 配置类错误（证书缺失/非法等）：终止启动。
			return nil, err
		}
		etcdCli, err := clientv3.New(cliCfg)
		if err != nil {
			// 仅配置类错误（endpoint 非法等）才会走到这里。
			return nil, err
		}
		svcCtx.etcdCli = etcdCli
		tracker.AddComponent("etcd", false, etcdProbeInterval, func(ctx context.Context) error {
			// Status 是最廉价的连通性检查；多 endpoint 时探测首个即可反映本地链路。
			_, err := etcdCli.Status(ctx, etcdConf.Hosts[0])
			return err
		})
	}

	return svcCtx, nil
}

// pickEtcdConf 选取用于健康探活的 etcd 配置：优先 Rpc 段，其次 Http 段。
func pickEtcdConf(c config.Config) (discov.EtcdConf, bool) {
	if len(c.Rpc.Etcd.Hosts) > 0 {
		return c.Rpc.Etcd, true
	}
	if len(c.Http.Etcd.Hosts) > 0 {
		return c.Http.Etcd, true
	}
	return discov.EtcdConf{}, false
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

// applyMysqlPool 应用 MySQL 连接池参数；<=0 的配置项保持默认行为，避免误覆盖框架合理默认值。
func applyMysqlPool(conn sqlx.SqlConn, c config.MysqlConf) {
	if c.MaxOpenConns <= 0 && c.MaxIdleConns <= 0 && c.ConnMaxLifetime <= 0 && c.ConnMaxIdleTime <= 0 {
		return
	}
	db, err := conn.RawDB()
	if err != nil {
		logx.Errorw("获取 MySQL 原始连接池失败，跳过连接池配置", logx.Field("error", err))
		return
	}
	if c.MaxOpenConns > 0 {
		db.SetMaxOpenConns(c.MaxOpenConns)
	}
	if c.MaxIdleConns > 0 {
		db.SetMaxIdleConns(c.MaxIdleConns)
	} else if c.MaxOpenConns > 0 {
		db.SetMaxIdleConns(c.MaxOpenConns)
	}
	if c.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(time.Duration(c.ConnMaxLifetime) * time.Millisecond)
	}
	if c.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(time.Duration(c.ConnMaxIdleTime) * time.Millisecond)
	}
}
