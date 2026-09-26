package main

import (
	"flag"
	"os"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/proc"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/rest"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"

	tlsutil "cari.com.cn/framework/auditlog/common/tls"
	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/admin"
	"cari.com.cn/framework/auditlog/server/internal/config"
	"cari.com.cn/framework/auditlog/server/internal/handler"
	rpcserver "cari.com.cn/framework/auditlog/server/internal/server"
	"cari.com.cn/framework/auditlog/server/internal/svc"
)

var configFile = flag.String("f", "etc/auditlog.yaml", "the config file")

// exitOnErr 启动阶段致命错误的统一出口：记录日志并优雅退出，避免 panic。
func exitOnErr(msg string, err error) {
	logx.Errorw(msg, logx.Field("error", err))
	logx.Close()
	os.Exit(1)
}

// defaultRpcMiddlewares 显式声明 gRPC 服务端中间件全开。
// 主程序以代码方式构造 RpcServerConf，框架 YAML 的 default=true 标签不会生效，故在此对齐框架默认值。
func defaultRpcMiddlewares() zrpc.ServerMiddlewaresConf {
	return zrpc.ServerMiddlewaresConf{
		Trace:      true,
		Recover:    true,
		Stat:       true,
		Prometheus: true,
		Breaker:    true,
	}
}

// defaultRestMiddlewares 显式声明 HTTP 服务端中间件全开，原因同上。
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

func main() {
	flag.Parse()

	var c config.Config
	if err := conf.Load(*configFile, &c); err != nil {
		exitOnErr("加载配置文件失败", err)
	}
	// 按配置文件初始化日志（级别/编码/输出/保留时长）。
	if err := logx.SetUp(c.Log); err != nil {
		exitOnErr("初始化日志失败", err)
	}

	logx.Info("================ auditlog 服务正在启动 ================")

	// 抑制 go-zero 内部 etcd client 默认 zap logger 的 info/warn 刷屏（如 retrying/Auto sync，
	// 该 client 在 discov/internal 中创建，无法从外部注入 logger）：仅放行 error 级别原始日志；
	// etcd 连通性的结构化观测由本服务健康追踪器经 logx 统一输出（首次失败/中断/恢复/周期摘要）。
	// 必须在任何 etcd client（zrpc.NewServer 也会创建）之前设置，client 连接时实时读取该变量。
	_ = os.Setenv("ETCD_CLIENT_DEBUG", "error")

	// ---------------- 公共依赖：MySQL + gRPC 客户端 ----------------
	// 注意：MySQL/etcd 连接失败不会退出，Health 保持 starting 并后台重试，
	// 由管理端口 /readyz 对外反映就绪状态。
	svcCtx, err := svc.NewServiceContext(c)
	if err != nil {
		exitOnErr("初始化服务依赖失败", err)
	}

	// ---------------- gRPC 服务（写库 + etcd 注册 + mTLS） ----------------
	rpcServerConf := zrpc.RpcServerConf{
		ServiceConf:  c.ServiceConf,
		ListenOn:     c.Rpc.ListenOn,
		Etcd:         c.Rpc.Etcd,
		Timeout:      c.Rpc.Timeout,
		CpuThreshold: c.Rpc.CpuThreshold,
		Health:       c.Rpc.Health,
		Middlewares:  defaultRpcMiddlewares(),
	}
	rpcSrv, err := zrpc.NewServer(rpcServerConf, func(grpcServer *grpc.Server) {
		pb.RegisterAuditLogServiceServer(grpcServer, rpcserver.NewAuditLogServer(svcCtx))
	})
	if err != nil {
		exitOnErr("创建 gRPC 服务失败", err)
	}

	// 配置了证书即为 gRPC server 挂载 mTLS 传输凭证，未配置时退化为明文（仅限本地调试）。
	if len(c.Rpc.TLS.CertFile) > 0 && len(c.Rpc.TLS.KeyFile) > 0 {
		creds, err := tlsutil.NewServerCredentials(c.Rpc.TLS.CertFile, c.Rpc.TLS.KeyFile, c.Rpc.TLS.CACertFile)
		if err != nil {
			exitOnErr("加载 gRPC mTLS 证书失败", err)
		}
		rpcSrv.AddOptions(grpc.Creds(creds))
		logx.Infow("gRPC mTLS 已启用",
			logx.Field("cert", c.Rpc.TLS.CertFile),
			logx.Field("ca", c.Rpc.TLS.CACertFile),
		)
	} else {
		logx.Info("未配置 TLS 证书，gRPC 以明文模式启动")
	}

	// ---------------- HTTP 网关（HTTPS/mTLS，经 etcd 发现调用 gRPC） ----------------
	restConf := rest.RestConf{
		ServiceConf:  c.ServiceConf,
		Host:         c.Http.Host,
		Port:         c.Http.Port,
		CertFile:     c.Http.CertFile,
		KeyFile:      c.Http.KeyFile,
		Timeout:      c.Http.Timeout,
		MaxConns:     c.Http.MaxConns,
		MaxBytes:     c.Http.MaxBytes,
		CpuThreshold: c.Http.CpuThreshold,
		Middlewares:  defaultRestMiddlewares(),
	}

	var httpOpts []rest.RunOption
	// go-zero v1.10.3 仅在 RestConf.CertFile/KeyFile 非空时才走 HTTPS 分支（engine.start），
	// WithTLSConfig 只负责注入含客户端 CA 校验（mTLS）的 tls.Config，两者缺一不可。
	// 故证书路径需同时写入 RestConf；未配置证书时走明文 HTTP（仅限本地调试）。
	if len(c.Http.CertFile) > 0 && len(c.Http.KeyFile) > 0 {
		tlsConfig, err := tlsutil.NewServerTLSConfig(c.Http.CertFile, c.Http.KeyFile, c.Http.ClientCACertFile)
		if err != nil {
			exitOnErr("加载 HTTPS/mTLS 证书失败", err)
		}
		httpOpts = append(httpOpts, rest.WithTLSConfig(tlsConfig))

		if len(c.Http.ClientCACertFile) > 0 {
			logx.Infow("HTTP mTLS 已启用，将强制校验调用方客户端证书",
				logx.Field("cert", c.Http.CertFile),
				logx.Field("ca", c.Http.ClientCACertFile),
			)
		} else {
			logx.Info("HTTP 单向 TLS 已启用（未配置客户端 CA 校验）")
		}
	} else {
		logx.Info("未配置 TLS 证书，HTTP 以明文模式启动")
	}

	httpSrv, err := rest.NewServer(restConf, httpOpts...)
	if err != nil {
		exitOnErr("创建 HTTP 服务失败", err)
	}
	handler.RegisterHandlers(httpSrv, svcCtx)

	// ---------------- 管理端口（明文 HTTP：/healthz /readyz /status，不启用 mTLS） ----------------
	adminSrv := admin.NewServer(c.Admin.Host, c.Admin.Port, svcCtx.Health)

	// ---------------- 同一进程同时启动 gRPC、HTTP 与管理端口，统一优雅退出 ----------------
	group := service.NewServiceGroup()
	group.Add(rpcSrv)
	group.Add(httpSrv)
	group.Add(adminSrv)

	// 该监听器先于 ServiceGroup 内部的停止监听器注册，收到 SIGINT/SIGTERM 时最先执行。
	proc.AddShutdownListener(func() {
		logx.Info("收到退出信号，auditlog 服务开始优雅停止（停止接收新请求，等待存量请求处理完）...")
		svcCtx.Health.MarkStopping()
	})

	logx.Infof("auditlog 服务启动完成：gRPC %s（etcd 注册 key: %s），HTTP(mTLS) %s:%d，管理端口(明文) %s:%d",
		c.Rpc.ListenOn, c.Rpc.Etcd.Key, c.Http.Host, c.Http.Port, c.Admin.Host, c.Admin.Port)
	// Start 阻塞至收到退出信号：期间 MySQL/etcd 即使不可用也不影响进程存活。
	group.Start()

	// 走到这里说明信号已触发、各 server 已完成 Shutdown；group.Stop 幂等兜底。
	group.Stop()
	svcCtx.Stop()
	logx.Info("================ auditlog 服务已完全停止 ================")
	logx.Close()
}
