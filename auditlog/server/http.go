package main

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/core/discov"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest"

	tlsutil "cari.com.cn/framework/auditlog/common/tls"
	"cari.com.cn/framework/auditlog/server/internal/config"
	"cari.com.cn/framework/auditlog/server/internal/handler"
	"cari.com.cn/framework/auditlog/server/internal/health"
	"cari.com.cn/framework/auditlog/server/internal/middleware"
	"cari.com.cn/framework/auditlog/server/internal/svc"
)

// readyPollInterval 等待服务就绪以注册 etcd 的轮询周期。
const readyPollInterval = 500 * time.Millisecond

// setupHTTP 装配 HTTP 服务端：路由、限流、mTLS 凭证与 etcd 注册。
// 返回的注册器为 nil 表示未配置 etcd 注册，调用方负责在退出时 Stop。
func setupHTTP(c config.Config, svcCtx *svc.ServiceContext) (*rest.Server, *httpRegistration, error) {
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
	// go-zero 仅在 RestConf.CertFile/KeyFile 非空时才走 HTTPS 分支，
	// WithTLSConfig 负责注入含客户端 CA 校验（mTLS）的 tls.Config，两者缺一不可。
	if len(c.Http.CertFile) > 0 && len(c.Http.KeyFile) > 0 {
		tlsConfig, err := tlsutil.NewServerTLSConfig(c.Http.CertFile, c.Http.KeyFile, c.Http.ClientCACertFile,
			mtlsPolicy(c.Http.ClientIdentity))
		if err != nil {
			return nil, nil, err
		}
		httpOpts = append(httpOpts, rest.WithTLSConfig(tlsConfig))

		if len(c.Http.ClientCACertFile) > 0 {
			logx.Infow("HTTP mTLS 已启用，将强制校验调用方客户端证书及 SPIFFE 身份（同组织/项目）",
				logx.Field("cert", c.Http.CertFile),
				logx.Field("ca", c.Http.ClientCACertFile),
				logx.Field("allowed_business_systems", c.Http.ClientIdentity.AllowedBusinessSystems),
				logx.Field("allowed_services", c.Http.ClientIdentity.AllowedServices),
			)
		} else {
			logx.Info("HTTP 单向 TLS 已启用（未配置客户端 CA 校验）")
		}
	} else {
		logx.Info("未配置 TLS 证书，HTTP 以明文模式启动")
	}

	httpSrv, err := rest.NewServer(restConf, httpOpts...)
	if err != nil {
		return nil, nil, err
	}

	// HTTP 全局限流（内存令牌桶，单实例粒度）；0 表示不限流。
	if c.Http.RateLimit > 0 {
		if mw := middleware.NewHttpRateLimit(int(c.Http.RateLimit)); mw != nil {
			httpSrv.Use(mw)
			logx.Infow("HTTP 限流已启用", logx.Field("rps", c.Http.RateLimit))
		}
	}
	handler.RegisterHandlers(httpSrv, svcCtx)

	// 注册到 etcd（独立 key，与 gRPC 注册共存）：服务首次就绪后才发布，
	// 未就绪期间下游经服务发现看不到本实例；发布地址必须是对外可达地址。
	var reg *httpRegistration
	if len(c.Http.Etcd.Hosts) > 0 {
		addr, err := advertiseAddr(c.Http)
		if err != nil {
			return nil, nil, err
		}
		reg = newHTTPRegistration(c.Http.Etcd, addr, svcCtx.Health)
		logx.Infow("HTTP 服务将在就绪后注册到 etcd",
			logx.Field("key", c.Http.Etcd.Key),
			logx.Field("addr", addr),
		)
	}

	return httpSrv, reg, nil
}

// advertiseAddr 计算发布到 etcd 的 HTTP 地址：AdvertiseHost 优先，其次 Host。
// 空或 0.0.0.0/:: 等未指定地址对下游不可达，属于配置类错误，启动即失败。
func advertiseAddr(h config.HttpSection) (string, error) {
	host := h.AdvertiseHost
	if host == "" {
		host = h.Host
	}
	if host == "" {
		return "", fmt.Errorf("HTTP 注册 etcd 需要对外可达地址：Host 为空，请配置 Http.AdvertiseHost")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return "", fmt.Errorf("HTTP Host=%s 为未指定地址，发布到 etcd 后下游无法拨号，请配置 Http.AdvertiseHost 为本实例可达 IP/域名", host)
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", h.Port)), nil
}

// httpRegistration HTTP etcd 延迟注册器：服务首次就绪后才发布地址；
// Stop 幂等，停止等待并注销已发布的租约。
type httpRegistration struct {
	etcdConf discov.EtcdConf
	addr     string
	health   *health.Tracker

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	mu  sync.Mutex
	pub *discov.Publisher
}

func newHTTPRegistration(etcdConf discov.EtcdConf, addr string, tracker *health.Tracker) *httpRegistration {
	r := &httpRegistration{
		etcdConf: etcdConf,
		addr:     addr,
		health:   tracker,
		stopCh:   make(chan struct{}),
	}
	r.wg.Add(1)
	go r.loop()
	return r
}

// loop 轮询等待服务就绪，就绪后立即发布并退出 goroutine（publisher 自维持租约）。
func (r *httpRegistration) loop() {
	defer r.wg.Done()
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			if !r.health.IsReady() {
				continue
			}
			r.publish()
			return
		}
	}
}

// publish 创建 etcd publisher（账号/mTLS 参数与健康探测客户端一致）。
func (r *httpRegistration) publish() {
	pubOpts := []discov.PubOption{}
	if r.etcdConf.HasAccount() {
		pubOpts = append(pubOpts, discov.WithPubEtcdAccount(r.etcdConf.User, r.etcdConf.Pass))
	}
	if r.etcdConf.HasTLS() {
		pubOpts = append(pubOpts, discov.WithPubEtcdTLS(
			r.etcdConf.CertFile, r.etcdConf.CertKeyFile, r.etcdConf.CACertFile,
			r.etcdConf.InsecureSkipVerify,
		))
	}
	pub := discov.NewPublisher(r.etcdConf.Hosts, r.etcdConf.Key, r.addr, pubOpts...)
	r.mu.Lock()
	r.pub = pub
	r.mu.Unlock()
	logx.Infow("HTTP 服务已注册到 etcd",
		logx.Field("key", r.etcdConf.Key),
		logx.Field("addr", r.addr),
	)
}

// Stop 停止等待并注销已发布的 etcd 租约；幂等。
func (r *httpRegistration) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	r.wg.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pub != nil {
		r.pub.Stop()
		r.pub = nil
	}
}
