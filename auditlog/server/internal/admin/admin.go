// Package admin 提供明文 HTTP 管理端口，专供 k8s 探针与运维状态查询：
// /healthz（liveness，恒 200）、/readyz（readiness，必需依赖就绪才 200）、
// /status（详细状态，可配 Bearer token）。刻意不启用 TLS（kubelet 不带客户端证书），
// 与 mTLS 业务端口物理隔离，部署时勿对集群外暴露。
// 响应体遵循统一响应规范（error/message/data/ts），状态码语义不变。
package admin

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"cari.com.cn/framework/auditlog/server/internal/health"
	"cari.com.cn/framework/auditlog/server/internal/resp"
)

// 管理端口超时：优雅关闭、读、写、空闲连接。
const (
	shutdownTimeout = 5 * time.Second
	readTimeout     = 10 * time.Second
	writeTimeout    = 10 * time.Second
	idleTimeout     = 60 * time.Second
)

// Server 管理端口 HTTP 服务，实现 service.Service（Start/Stop）可加入 ServiceGroup。
type Server struct {
	addr        string
	tracker     *health.Tracker
	statusToken string // 非空时 /status 需 Bearer 鉴权
	server      *http.Server
}

// NewServer 创建管理端口服务；statusToken 非空时 /status 需凭 "Authorization: Bearer <token>" 访问。
func NewServer(host string, port int, tracker *health.Tracker, statusToken string) *Server {
	s := &Server{
		addr:        fmt.Sprintf("%s:%d", host, port),
		tracker:     tracker,
		statusToken: statusToken,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/status", s.handleStatus)

	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	return s
}

// Start 阻塞启动管理端口，由 ServiceGroup 在独立 goroutine 中调用。
func (s *Server) Start() {
	logx.Infof("管理端口启动（明文 HTTP，供 k8s 探针）：http://%s（/healthz /readyz /status）", s.addr)
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logx.Errorw("管理端口异常退出",
			logx.Field("addr", s.addr),
			logx.Field("error", err),
		)
	}
}

// Stop 优雅关闭管理端口。
func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := s.server.Shutdown(ctx); err != nil {
		logx.Errorw("管理端口关闭异常", logx.Field("error", err))
		return
	}
	logx.Info("管理端口已停止")
}

// handleHealthz liveness：进程能响应请求即 200。
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	resp.OK(w, map[string]string{"status": "ok"})
}

// handleReadyz readiness：必需依赖（MySQL）实时健康才 200；
// 启动中、依赖故障或停止中均 503（error 标识区分三态），k8s 据此摘流量。
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	snap := s.tracker.Snapshot()
	if snap.Ready {
		resp.OK(w, map[string]any{"status": "ready", "components": snap.Components})
		return
	}

	// 503 下按稳定 error 标识区分：启动中 / 依赖故障 / 停止中。
	errorID := "auditlog." + string(snap.Phase)
	status := string(snap.Phase)
	if snap.Phase == health.PhaseReady {
		errorID = "auditlog.not_ready" // 启动完成但依赖运行中故障
		status = "not_ready"
	}
	resp.ErrData(w, http.StatusServiceUnavailable, errorID,
		"服务暂不可接收流量", map[string]any{"status": status, "components": snap.Components})
}

// handleStatus 返回完整状态详情（恒 200）；配置了 StatusToken 时须携带 Bearer token。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if s.statusToken != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		provided := ""
		if strings.HasPrefix(auth, prefix) {
			provided = auth[len(prefix):]
		}
		// 常量时间比较，避免通过响应时间差异泄露 token。
		if subtle.ConstantTimeCompare([]byte(provided), []byte(s.statusToken)) != 1 {
			resp.ErrWithHeader(w, http.StatusUnauthorized, "auditlog.unauthorized",
				"未授权访问 /status，请提供正确的 Bearer token",
				map[string]string{"WWW-Authenticate": `Bearer realm="status"`})
			return
		}
	}
	resp.OK(w, s.tracker.Snapshot())
}
