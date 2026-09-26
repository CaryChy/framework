// Package admin 提供独立的明文 HTTP 管理端口，专供 k8s 探针与运维状态查询：
//   - GET /healthz：liveness 探针，进程存活即返回 200；
//   - GET /readyz：readiness 探针，必需依赖全部就绪才返回 200，否则 503；
//   - GET /status：服务阶段与各依赖组件的详细状态。
//
// 该端口刻意不启用 TLS/mTLS（kubelet 探针不携带业务客户端证书），
// 与强制 mTLS 的业务 HTTP 端口物理隔离，部署时不要对集群外暴露。
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"cari.com.cn/framework/auditlog/server/internal/health"
)

// shutdownTimeout 管理端口优雅关闭超时。
const shutdownTimeout = 5 * time.Second

// Server 管理端口 HTTP 服务，实现 service.Service（Start/Stop）可加入 ServiceGroup。
type Server struct {
	addr    string
	tracker *health.Tracker
	server  *http.Server
}

// NewServer 创建管理端口服务。
func NewServer(host string, port int, tracker *health.Tracker) *Server {
	s := &Server{
		addr:    fmt.Sprintf("%s:%d", host, port),
		tracker: tracker,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/status", s.handleStatus)

	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: time.Second,
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

// handleHealthz liveness：只要进程还在运行并能处理请求即 200。
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz readiness：服务阶段为 ready 且必需依赖（MySQL）实时健康才 200；
// 启动中、依赖故障或停止中均返回 503，k8s 据此摘流量。
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	snap := s.tracker.Snapshot()
	code := http.StatusOK
	status := "ready"
	if !snap.Ready {
		code = http.StatusServiceUnavailable
		status = string(snap.Phase)
		if status == string(health.PhaseReady) {
			// 已完成启动但必需依赖运行中故障。
			status = "not_ready"
		}
	}
	writeJSON(w, code, map[string]any{
		"status":     status,
		"components": snap.Components,
	})
}

// handleStatus 返回完整状态详情，供运维排查；不区分就绪与否，恒返回 200。
func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.tracker.Snapshot())
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logx.Errorw("管理端口响应编码失败", logx.Field("error", err))
	}
}
