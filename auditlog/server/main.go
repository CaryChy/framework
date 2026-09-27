package main

import (
	"flag"
	"os"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/proc"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/core/stores/sqlx"

	"cari.com.cn/framework/auditlog/server/internal/admin"
	"cari.com.cn/framework/auditlog/server/internal/config"
	"cari.com.cn/framework/auditlog/server/internal/svc"
)

var configFile = flag.String("f", "etc/auditlog.yaml", "the config file")

// exitOnErr 启动阶段致命错误的统一出口：记录日志并优雅退出，避免 panic。
func exitOnErr(msg string, err error) {
	logx.Errorw(msg, logx.Field("error", err))
	logx.Close()
	os.Exit(1)
}

func main() {
	flag.Parse()

	var c config.Config
	// UseEnv：展开配置中的 ${VAR} 环境变量占位符，敏感项（凭据/token）不写死配置文件。
	if err := conf.Load(*configFile, &c, conf.UseEnv()); err != nil {
		exitOnErr("加载配置文件失败", err)
	}
	if err := logx.SetUp(c.Log); err != nil {
		exitOnErr("初始化日志失败", err)
	}

	logx.Info("================ auditlog 服务正在启动 ================")

	// go-zero 内部 etcd client 的日志无法从外部注入 logger，只能用环境变量降噪到 error；
	// etcd 连通性的结构化观测由健康追踪器输出。
	_ = os.Setenv("ETCD_CLIENT_DEBUG", "error")

	// 审计表含敏感字段，关闭 sqlx 的 SQL 明文日志，避免敏感数据落盘；
	// 慢查询与执行失败的日志不受影响。
	sqlx.DisableStmtLog()

	// MySQL/etcd 连接失败不会退出进程：健康保持 starting 并后台重试，由 /readyz 对外反映。
	svcCtx, err := svc.NewServiceContext(c)
	if err != nil {
		exitOnErr("初始化服务依赖失败", err)
	}

	rpcSrv, err := setupGRPC(c, svcCtx)
	if err != nil {
		exitOnErr("创建 gRPC 服务失败", err)
	}

	httpSrv, httpPub, err := setupHTTP(c, svcCtx)
	if err != nil {
		exitOnErr("创建 HTTP 服务失败", err)
	}
	if httpPub != nil {
		defer httpPub.Stop()
	}

	adminSrv := admin.NewServer(c.Admin.Host, c.Admin.Port, svcCtx.Health, c.Admin.StatusToken)
	if c.Admin.StatusToken == "" {
		// 非致命但需显式告知：/status 将无鉴权开放，环境变量缺失时不能静默放行。
		logx.Infow("管理端口 /status 未配置访问令牌（AUDITLOG_ADMIN_TOKEN），接口无鉴权，仅限受信内网",
			logx.Field("level", "warn"))
	}

	group := service.NewServiceGroup()
	group.Add(rpcSrv)
	group.Add(httpSrv)
	group.Add(adminSrv)

	// 先于 ServiceGroup 内部的停止监听器注册，收到信号时最先标记停止中（readiness 摘流量），
	// 并同步把 HTTP 地址从 etcd 摘除，避免存量请求处理期间仍有新流量经服务发现进入。
	proc.AddShutdownListener(func() {
		logx.Info("收到退出信号，auditlog 服务开始优雅停止（停止接收新请求，等待存量请求处理完）...")
		svcCtx.Health.MarkStopping()
		if httpPub != nil {
			httpPub.Stop()
		}
	})

	logx.Infof("auditlog 服务启动完成：gRPC %s（etcd key: %s），HTTP(mTLS) %s:%d（etcd key: %s），管理端口(明文) %s:%d",
		c.Rpc.ListenOn, c.Rpc.Etcd.Key, c.Http.Host, c.Http.Port, c.Http.Etcd.Key, c.Admin.Host, c.Admin.Port)
	group.Start()

	// 走到这里说明信号已触发、各 server 已 Shutdown；group.Stop 幂等兜底。
	group.Stop()
	svcCtx.Stop()
	logx.Info("================ auditlog 服务已完全停止 ================")
	logx.Close()
}
