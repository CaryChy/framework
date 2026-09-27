package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/logx"

	"cari.com.cn/framework/auditlog/server/internal/health"
)

// TestMain 抑制探活失败日志，避免测试输出被刷屏。
func TestMain(m *testing.M) {
	logx.SetLevel(logx.SevereLevel)
	m.Run()
}

// freePort 返回一个当前空闲的 TCP 端口（存在极小 TOCTOU 竞态，测试可接受）。
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("分配空闲端口失败: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// checker 可在运行期切换健康结果的探活函数，用于驱动 tracker 状态机。
type checker struct {
	mu      sync.Mutex
	healthy bool
}

func (c *checker) set(h bool) { c.mu.Lock(); c.healthy = h; c.mu.Unlock() }
func (c *checker) check(context.Context) error {
	c.mu.Lock()
	h := c.healthy
	c.mu.Unlock()
	if h {
		return nil
	}
	return errors.New("unhealthy")
}

// newReq 构造一个 GET 请求（多数 handler 忽略 method/body）。
func newReq() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/", nil)
}

// envelope 统一响应结构（resp.Body 的测试视图，data 解为对象）。
type envelope struct {
	Error   string         `json:"error"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
	TS      int64          `json:"ts"`
}

// decodeEnvelope 解析统一响应 envelope，校验 ts 必填。
func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应非合法 JSON: %v，body=%s", err, rec.Body.String())
	}
	if env.TS <= 0 {
		t.Errorf("ts 应为 Unix 毫秒时间戳，实际 %d", env.TS)
	}
	return env
}

// waitForReady 轮询直到 tracker.IsReady 等于 want，超时则失败。
func waitForReady(t *testing.T, tr *health.Tracker, want bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tr.IsReady() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("tracker.IsReady 未在超时内变为 %v", want)
}

// TestHealthz_OK liveness 探针恒返回 200，成功 envelope（error=success）。
func TestHealthz_OK(t *testing.T) {
	s := NewServer("127.0.0.1", 0, health.NewTracker(), "")
	w := httptest.NewRecorder()
	s.handleHealthz(w, newReq())

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type 错误: %q", ct)
	}
	env := decodeEnvelope(t, w)
	if env.Error != "success" {
		t.Errorf("error 应为 success，实际 %q", env.Error)
	}
	if env.Data["status"] != "ok" {
		t.Errorf("data.status 字段错误: %v", env.Data["status"])
	}
}

// TestReadyz_Ready 阶段 ready 且必需依赖健康时返回 200。
func TestReadyz_Ready(t *testing.T) {
	tr := health.NewTracker()
	defer tr.Stop()
	c := &checker{healthy: true}
	tr.AddComponent("mysql", true, time.Millisecond, c.check)
	waitForReady(t, tr, true)

	s := NewServer("127.0.0.1", 0, tr, "")
	w := httptest.NewRecorder()
	s.handleReadyz(w, newReq())

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}
	env := decodeEnvelope(t, w)
	if env.Error != "success" {
		t.Errorf("error 应为 success，实际 %q", env.Error)
	}
	if env.Data["status"] != "ready" {
		t.Errorf("data.status 应为 ready，实际 %v", env.Data["status"])
	}
	if _, ok := env.Data["components"]; !ok {
		t.Error("data 应包含 components 字段")
	}
}

// TestReadyz_Starting 启动中（必需依赖未就绪）返回 503，error=auditlog.starting。
func TestReadyz_Starting(t *testing.T) {
	tr := health.NewTracker()
	defer tr.Stop()
	c := &checker{healthy: false}
	tr.AddComponent("mysql", true, time.Millisecond, c.check)
	waitForReady(t, tr, false)

	s := NewServer("127.0.0.1", 0, tr, "")
	w := httptest.NewRecorder()
	s.handleReadyz(w, newReq())

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("期望 503，实际 %d", w.Code)
	}
	env := decodeEnvelope(t, w)
	if env.Error != "auditlog.starting" {
		t.Errorf("error 应为 auditlog.starting，实际 %q", env.Error)
	}
	if env.Data["status"] != "starting" {
		t.Errorf("data.status 应为 starting，实际 %v", env.Data["status"])
	}
}

// TestReadyz_RunningFailure 已进入 ready 阶段但必需依赖运行中故障，返回 503 且 error=auditlog.not_ready。
func TestReadyz_RunningFailure(t *testing.T) {
	tr := health.NewTracker()
	defer tr.Stop()
	c := &checker{healthy: true}
	tr.AddComponent("mysql", true, time.Millisecond, c.check)
	waitForReady(t, tr, true)

	// 依赖在运行期故障：阶段保持 ready，但 IsReady 变 false。
	c.set(false)
	waitForReady(t, tr, false)

	s := NewServer("127.0.0.1", 0, tr, "")
	w := httptest.NewRecorder()
	s.handleReadyz(w, newReq())

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("期望 503，实际 %d", w.Code)
	}
	env := decodeEnvelope(t, w)
	if env.Error != "auditlog.not_ready" {
		t.Errorf("error 应为 auditlog.not_ready，实际 %q", env.Error)
	}
	if env.Data["status"] != "not_ready" {
		t.Errorf("data.status 应为 not_ready，实际 %v", env.Data["status"])
	}
}

// TestReadyz_Stopping 停止阶段返回 503，error=auditlog.stopping。
func TestReadyz_Stopping(t *testing.T) {
	tr := health.NewTracker()
	defer tr.Stop()
	tr.MarkStopping()

	s := NewServer("127.0.0.1", 0, tr, "")
	w := httptest.NewRecorder()
	s.handleReadyz(w, newReq())

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("期望 503，实际 %d", w.Code)
	}
	env := decodeEnvelope(t, w)
	if env.Error != "auditlog.stopping" {
		t.Errorf("error 应为 auditlog.stopping，实际 %q", env.Error)
	}
	if env.Data["status"] != "stopping" {
		t.Errorf("data.status 应为 stopping，实际 %v", env.Data["status"])
	}
}

// TestStatus_NoToken 未配置 token 时 /status 直接返回完整快照，恒 200。
func TestStatus_NoToken(t *testing.T) {
	tr := health.NewTracker()
	defer tr.Stop()
	s := NewServer("127.0.0.1", 0, tr, "")
	w := httptest.NewRecorder()
	s.handleStatus(w, newReq())

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}
	env := decodeEnvelope(t, w)
	if env.Error != "success" {
		t.Errorf("error 应为 success，实际 %q", env.Error)
	}
	if env.Data["phase"] != string(health.PhaseStarting) {
		t.Errorf("data.phase 应为 starting，实际 %v", env.Data["phase"])
	}
}

// TestStatus_Authorized 配置 token 且携带正确 Bearer 时返回 200。
func TestStatus_Authorized(t *testing.T) {
	tr := health.NewTracker()
	defer tr.Stop()
	s := NewServer("127.0.0.1", 0, tr, "secret-token")

	req := newReq()
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", w.Code)
	}
}

// TestStatus_MissingAuth 配置 token 但缺失 Authorization 头时返回 401，
// error=auditlog.unauthorized 且携带 WWW-Authenticate。
func TestStatus_MissingAuth(t *testing.T) {
	tr := health.NewTracker()
	defer tr.Stop()
	s := NewServer("127.0.0.1", 0, tr, "secret-token")

	w := httptest.NewRecorder()
	s.handleStatus(w, newReq())

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d", w.Code)
	}
	env := decodeEnvelope(t, w)
	if env.Error != "auditlog.unauthorized" {
		t.Errorf("error 应为 auditlog.unauthorized，实际 %q", env.Error)
	}
	if wa := w.Header().Get("WWW-Authenticate"); wa == "" {
		t.Error("401 响应应携带 WWW-Authenticate 头")
	}
}

// TestStatus_WrongToken 配置 token 但 Bearer 值错误时返回 401。
func TestStatus_WrongToken(t *testing.T) {
	tr := health.NewTracker()
	defer tr.Stop()
	s := NewServer("127.0.0.1", 0, tr, "secret-token")

	req := newReq()
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d", w.Code)
	}
}

// TestStatus_WrongScheme Authorization 头非 Bearer 前缀时返回 401。
func TestStatus_WrongScheme(t *testing.T) {
	tr := health.NewTracker()
	defer tr.Stop()
	s := NewServer("127.0.0.1", 0, tr, "secret-token")

	req := newReq()
	req.Header.Set("Authorization", "Basic secret-token")
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d", w.Code)
	}
}

// TestNewServer_AddrFormat 验证监听地址格式化。
func TestNewServer_AddrFormat(t *testing.T) {
	s := NewServer("0.0.0.0", 9091, health.NewTracker(), "")
	if s.addr != "0.0.0.0:9091" {
		t.Errorf("addr 格式错误: %q", s.addr)
	}
	if s.statusToken != "" {
		t.Errorf("statusToken 应为空，实际 %q", s.statusToken)
	}
	if s.server.ReadTimeout != readTimeout {
		t.Errorf("ReadTimeout 配置错误: %v", s.server.ReadTimeout)
	}
	if s.server.WriteTimeout != writeTimeout {
		t.Errorf("WriteTimeout 配置错误: %v", s.server.WriteTimeout)
	}
}

// TestStartStop_Lifecycle 启动后能正常接受请求，Stop 后 Shutdown 不阻塞。
func TestStartStop_Lifecycle(t *testing.T) {
	port := freePort(t)
	tr := health.NewTracker()
	defer tr.Stop()
	s := NewServer("127.0.0.1", port, tr, "")

	done := make(chan struct{})
	go func() {
		s.Start()
		close(done)
	}()

	// 轮询直到端口可连接（Start 异步，需要等 ListenAndServe 绑定）。
	deadline := time.Now().Add(2 * time.Second)
	connected := false
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + s.addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				connected = true
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !connected {
		t.Fatal("管理端口启动后无法连接 /healthz")
	}

	s.Stop()
	select {
	case <-done:
	case <-time.After(shutdownTimeout + time.Second):
		t.Fatal("Stop 后 Start 未在超时内返回")
	}
}
