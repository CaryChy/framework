package auditlog

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tlsutil "cari.com.cn/framework/auditlog/common/tls"
	"cari.com.cn/framework/auditlog/common/tls/tlstest"
	"cari.com.cn/framework/auditlog/model"
	"cari.com.cn/framework/auditlog/server/internal/biz"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/auditlog/server/internal/testutil"
	"cari.com.cn/framework/auditlog/server/internal/types"
)

var errDBBoom = errors.New("db boom")

// envelope 统一响应结构（resp.Body 的测试视图，data 保留原始 JSON 供二次解析）。
type envelope struct {
	Error   string          `json:"error"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	TS      int64           `json:"ts"`
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

// newTestSvcCtx 用真实 biz + mock model 装配协议层测试容器。
func newTestSvcCtx(t *testing.T, ready bool, m *testutil.MockModel) *svc.ServiceContext {
	t.Helper()
	return &svc.ServiceContext{
		AuditLogBiz: biz.NewAuditLogBiz(&testutil.ReadyStub{Ready: ready}, m),
	}
}

// sampleRow 构造一条数据库层日志，覆盖可空字段的 NULL/非NULL 两种形态。
func sampleRow() *model.AuditLog {
	return &model.AuditLog{
		Id:           "log-1",
		TraceId:      "trace-1",
		ServiceName:  "auth",
		Operation:    "POST /login",
		ActorId:      "user-9",
		ActorType:    "user",
		Action:       "LOGIN",
		ResourceType: "session",
		ResourceId:   "s-1",
		SourceIp:     "10.0.0.1",
		UserAgent:    "go-test",
		RequestUri:   "/login",
		StatusCode:   200,
		RequestBody:  sql.NullString{}, // NULL -> HTTP 输出应为空串且省略
		ResponseBody: sql.NullString{String: `{"ok":true}`, Valid: true},
		Metadata:     sql.NullString{String: "{}", Valid: true},
		DurationMs:   12,
		CreatedAt:    time.UnixMilli(1700000000123).UTC(),
	}
}

// ---------- POST /api/v1/auditlogs ----------

// TestCreateAuditLogsHandler_Success 批量写入成功：201 + 成功 envelope（data.ids）。
func TestCreateAuditLogsHandler_Success(t *testing.T) {
	mdl := &testutil.MockModel{}
	svcCtx := newTestSvcCtx(t, true, mdl)
	body := `{"logs":[
		{"id":"log-1","service_name":"auth","action":"login","status_code":200,"request_body":"{\"u\":1}"},
		{"id":"log-2","service_name":"order","action":"create","status_code":201}
	]}`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auditlogs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	CreateAuditLogsHandler(svcCtx).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("期望 201，实际 %d，body=%s", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	if env.Error != "success" {
		t.Fatalf("error 应为 success，实际 %q", env.Error)
	}
	var resp types.CreateAuditLogsResponse
	if err := json.Unmarshal(env.Data, &resp); err != nil {
		t.Fatalf("data 不是合法 JSON: %v", err)
	}
	if len(resp.Ids) != 2 || resp.Ids[0] != "log-1" || resp.Ids[1] != "log-2" {
		t.Fatalf("返回 ID 与请求不一致: %+v", resp.Ids)
	}
	if len(mdl.InsertedRows) != 2 {
		t.Fatalf("期望落库 2 行，实际 %d", len(mdl.InsertedRows))
	}
	// 协议字段 -> 领域行的映射抽查（含可空大字段 NULL 写入规则）。
	first := mdl.InsertedRows[0]
	if first.ServiceName != "auth" || first.StatusCode != 200 {
		t.Fatalf("首行基础字段映射错误: %+v", first)
	}
	if !first.RequestBody.Valid || first.RequestBody.String != `{"u":1}` {
		t.Fatalf("非空 request_body 应落为 Valid NullString，实际 %+v", first.RequestBody)
	}
	if mdl.InsertedRows[1].RequestBody.Valid {
		t.Fatal("空 request_body 应落为 NULL（Valid=false）")
	}
}

func TestCreateAuditLogsHandler_ErrorStatus(t *testing.T) {
	cases := []struct {
		name       string
		ready      bool
		body       string
		InsertErr  error
		wantStatus int
		wantError  string
	}{
		{"malformed_json", true, `{not-json`, nil, http.StatusBadRequest, "auditlog.invalid_parameter"},
		{"empty_logs", true, `{"logs":[]}`, nil, http.StatusBadRequest, "auditlog.empty_logs"},
		{"empty_service_name", true, `{"logs":[{"action":"x"}]}`, nil, http.StatusBadRequest, "auditlog.empty_service_name"},
		{"not_ready", false, `{"logs":[{"id":"a","service_name":"auth"}]}`, nil, http.StatusServiceUnavailable, "auditlog.unavailable"},
		{"db_error", true, `{"logs":[{"id":"a","service_name":"auth"}]}`, errDBBoom, http.StatusInternalServerError, "auditlog.write_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mdl := &testutil.MockModel{InsertErr: tc.InsertErr}
			svcCtx := newTestSvcCtx(t, tc.ready, mdl)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auditlogs", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			CreateAuditLogsHandler(svcCtx).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("期望 %d，实际 %d，body=%s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if env := decodeEnvelope(t, rec); env.Error != tc.wantError {
				t.Errorf("error 应为 %q，实际 %q", tc.wantError, env.Error)
			}
		})
	}
}

func TestCreateAuditLogsHandler_BatchTooLarge(t *testing.T) {
	mdl := &testutil.MockModel{}
	svcCtx := newTestSvcCtx(t, true, mdl)

	var sb strings.Builder
	sb.WriteString(`{"logs":[`)
	for i := 0; i < 501; i++ { // 上限 500，第 501 条必须被拒
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"service_name":"auth"}`)
	}
	sb.WriteString(`]}`)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auditlogs", strings.NewReader(sb.String()))
	req.Header.Set("Content-Type", "application/json")
	CreateAuditLogsHandler(svcCtx).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("超大批请求应返回 400，实际 %d", rec.Code)
	}
	if env := decodeEnvelope(t, rec); env.Error != "auditlog.batch_too_large" {
		t.Errorf("error 应为 auditlog.batch_too_large，实际 %q", env.Error)
	}
	if len(mdl.InsertedRows) != 0 {
		t.Fatal("被拒绝的请求不应落库")
	}
}

// ---------- GET /api/v1/auditlogs ----------

// TestSearchAuditLogsHandler_Success 分页查询成功：200 + 成功 envelope（data.list/total）。
func TestSearchAuditLogsHandler_Success(t *testing.T) {
	mdl := &testutil.MockModel{SearchRows: []*model.AuditLog{sampleRow()}, SearchTotal: 1}
	svcCtx := newTestSvcCtx(t, true, mdl)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auditlogs?service_name=auth&actor_id=user-9&page=2&page_size=10", nil)
	SearchAuditLogsHandler(svcCtx).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d，body=%s", rec.Code, rec.Body.String())
	}
	// 查询条件必须透传到领域层。
	if mdl.LastInput.ServiceName != "auth" || mdl.LastInput.ActorId != "user-9" {
		t.Fatalf("过滤条件未透传: %+v", mdl.LastInput)
	}
	if mdl.LastInput.Page != 2 || mdl.LastInput.PageSize != 10 {
		t.Fatalf("分页参数未透传: page=%d page_size=%d", mdl.LastInput.Page, mdl.LastInput.PageSize)
	}

	env := decodeEnvelope(t, rec)
	if env.Error != "success" {
		t.Fatalf("error 应为 success，实际 %q", env.Error)
	}
	var resp types.SearchAuditLogsResponse
	if err := json.Unmarshal(env.Data, &resp); err != nil {
		t.Fatalf("data 不是合法 JSON: %v", err)
	}
	if resp.Total != 1 || len(resp.List) != 1 {
		t.Fatalf("响应分页数据错误: total=%d len=%d", resp.Total, len(resp.List))
	}
	item := resp.List[0]
	// 视图映射：NULL 解包为空串、时间转 Unix 毫秒。
	if item.Id != "log-1" || item.ServiceName != "auth" || item.StatusCode != 200 {
		t.Fatalf("条目基础字段错误: %+v", item)
	}
	if item.ResponseBody != `{"ok":true}` || item.Metadata != "{}" {
		t.Fatalf("非空可空字段映射错误: body=%q meta=%q", item.ResponseBody, item.Metadata)
	}
	if item.RequestBody != "" {
		t.Fatalf("NULL 字段应映射为空串，实际 %q", item.RequestBody)
	}
	if item.CreatedAt != 1700000000123 {
		t.Fatalf("created_at 应为 Unix 毫秒，实际 %d", item.CreatedAt)
	}
}

func TestSearchAuditLogsHandler_DefaultPaging(t *testing.T) {
	mdl := &testutil.MockModel{}
	svcCtx := newTestSvcCtx(t, true, mdl)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auditlogs", nil)
	SearchAuditLogsHandler(svcCtx).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d，body=%s", rec.Code, rec.Body.String())
	}
	// biz 层负责默认值：page=1、page_size=20。
	if mdl.LastInput.Page != 1 || mdl.LastInput.PageSize != 20 {
		t.Fatalf("未传分页时应使用默认值 1/20，实际 page=%d page_size=%d",
			mdl.LastInput.Page, mdl.LastInput.PageSize)
	}
}

// ---------- HTTPS + mTLS 端到端 ----------

// startMTLSHTTPServer 构建带 mTLS 校验的 HTTPS 服务器，返回客户端配置与关闭函数。
// 服务端身份 spiffe://cari/platform/framework/auditlog，组织/项目基准从其派生。
func startMTLSHTTPServer(t *testing.T, svcCtx *svc.ServiceContext) (*http.Client, func()) {
	t.Helper()

	ca, err := tlstest.NewTestCA()
	if err != nil {
		t.Fatalf("创建测试 CA 失败: %v", err)
	}

	dir := t.TempDir()
	serverCert, err := ca.MakeLeaf("auditlog", "spiffe://cari/platform/framework/auditlog", false)
	if err != nil {
		t.Fatalf("生成服务端证书失败: %v", err)
	}
	certFile, keyFile, err := tlstest.WriteKeyPair(dir, serverCert, "server")
	if err != nil {
		t.Fatalf("写服务端证书失败: %v", err)
	}
	caFile, err := ca.WriteCertPool(dir)
	if err != nil {
		t.Fatalf("写 CA 文件失败: %v", err)
	}

	cfg, err := tlsutil.NewServerTLSConfig(certFile, keyFile, caFile, tlsutil.Policy{
		AllowedBusinessSystems: []string{"framework"},
	})
	if err != nil {
		t.Fatalf("构造服务端 TLS 配置失败: %v", err)
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 将 handler 逻辑与 mTLS 结合：只有 mTLS 通过的请求才能进入业务 handler。
		// 这里用多路复用模拟服务端的路由分发。
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v1/auditlogs", CreateAuditLogsHandler(svcCtx))
		mux.ServeHTTP(w, r)
	}))
	server.TLS = cfg
	server.StartTLS()

	return server.Client(), func() { server.Close() }
}

// TestHTTPS_mTLS_ValidClient 合法客户端证书：同组织同项目，且业务系统在允许列表。
func TestHTTPS_mTLS_ValidClient(t *testing.T) {
	mdl := &testutil.MockModel{}
	svcCtx := newTestSvcCtx(t, true, mdl)

	// 用 deploy/tls 中的真实证书
	const deployTLS = "../../../deploy/tls"
	if _, err := os.Stat(deployTLS + "/client.pem"); err != nil {
		t.Skip("deploy/tls 证书不存在，跳过真实证书测试")
	}

	caPEM, _ := os.ReadFile(deployTLS + "/ca.pem")
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)

	clientCert, err := tls.LoadX509KeyPair(deployTLS+"/client.pem", deployTLS+"/client-key.pem")
	if err != nil {
		t.Fatalf("加载客户端证书失败: %v", err)
	}

	realClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:      pool,
			Certificates: []tls.Certificate{clientCert},
			MinVersion:   tls.VersionTLS12,
		},
	}}

	// 用 server.Client() 的地址
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v1/auditlogs", CreateAuditLogsHandler(svcCtx))
		mux.ServeHTTP(w, r)
	}))
	cfg, err := tlsutil.NewServerTLSConfig(
		deployTLS+"/server.pem",
		deployTLS+"/server-key.pem",
		deployTLS+"/ca.pem",
		tlsutil.Policy{AllowedBusinessSystems: []string{"framework"}},
	)
	if err != nil {
		t.Fatalf("构造 mTLS 配置失败: %v", err)
	}
	server.TLS = cfg
	server.StartTLS()
	defer server.Close()

	body := `{"logs":[{"id":"mtls-1","service_name":"auth","action":"login","status_code":200}]}`
	resp, err := realClient.Post(server.URL+"/api/v1/auditlogs", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("mTLS 请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("期望 201，实际 %d", resp.StatusCode)
	}
	if len(mdl.InsertedRows) != 1 || mdl.InsertedRows[0].Id != "mtls-1" {
		t.Fatalf("期望落库 1 条，实际 %d", len(mdl.InsertedRows))
	}
}

// TestHTTPS_mTLS_Rejected 非法客户端证书场景：无证书 / 无 SPIFFE / 跨组织 / 跨项目。
func TestHTTPS_mTLS_Rejected(t *testing.T) {
	mdl := &testutil.MockModel{}
	svcCtx := newTestSvcCtx(t, true, mdl)

	ca, err := tlstest.NewTestCA()
	if err != nil {
		t.Fatalf("创建测试 CA 失败: %v", err)
	}
	dir := t.TempDir()
	serverCert, err := ca.MakeLeaf("auditlog", "spiffe://cari/platform/framework/auditlog", false)
	if err != nil {
		t.Fatalf("生成服务端证书失败: %v", err)
	}
	certFile, keyFile, err := tlstest.WriteKeyPair(dir, serverCert, "server")
	if err != nil {
		t.Fatalf("写服务端证书失败: %v", err)
	}
	caFile, err := ca.WriteCertPool(dir)
	if err != nil {
		t.Fatalf("写 CA 文件失败: %v", err)
	}

	cfg, err := tlsutil.NewServerTLSConfig(certFile, keyFile, caFile, tlsutil.Policy{
		AllowedBusinessSystems: []string{"framework"},
	})
	if err != nil {
		t.Fatalf("构造服务端 TLS 配置失败: %v", err)
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v1/auditlogs", CreateAuditLogsHandler(svcCtx))
		mux.ServeHTTP(w, r)
	}))
	server.TLS = cfg
	server.StartTLS()
	defer server.Close()

	body := `{"logs":[{"id":"x","service_name":"auth"}]}`
	url := server.URL + "/api/v1/auditlogs"

	// 辅助：发起带指定证书的 POST。
	doPost := func(clientCert *tls.Certificate) (*http.Response, error) {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS12,
			},
		}
		if clientCert != nil {
			tr.TLSClientConfig.Certificates = []tls.Certificate{*clientCert}
		}
		c := &http.Client{Transport: tr}
		return c.Post(url, "application/json", strings.NewReader(body))
	}

	// 场景1：无客户端证书 → 握手失败。
	t.Run("no_client_cert", func(t *testing.T) {
		resp, err := doPost(nil)
		if err == nil {
			resp.Body.Close()
			t.Fatal("无客户端证书应握手失败")
		}
		if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "tls") {
			t.Logf("错误信息: %v", err)
		}
	})

	// 场景2：证书无 SPIFFE URI → 身份解析失败。
	t.Run("no_spiffe", func(t *testing.T) {
		cert, err := ca.MakeLeaf("caller", "", true)
		if err != nil {
			t.Fatalf("生成证书失败: %v", err)
		}
		resp, err := doPost(&cert)
		if err == nil {
			resp.Body.Close()
			t.Fatal("无 SPIFFE 的客户端证书应被拒绝")
		}
	})

	// 场景3：跨组织（evil/platform）→ 组织不匹配。
	t.Run("cross_org", func(t *testing.T) {
		cert, err := ca.MakeLeaf("caller", "spiffe://evil/platform/framework/x", true)
		if err != nil {
			t.Fatalf("生成证书失败: %v", err)
		}
		resp, err := doPost(&cert)
		if err == nil {
			resp.Body.Close()
			t.Fatal("跨组织证书应被拒绝")
		}
	})

	// 场景4：跨项目（cari/mining-platform）→ 项目不匹配。
	t.Run("cross_project", func(t *testing.T) {
		cert, err := ca.MakeLeaf("caller", "spiffe://cari/mining-platform/device/x", true)
		if err != nil {
			t.Fatalf("生成证书失败: %v", err)
		}
		resp, err := doPost(&cert)
		if err == nil {
			resp.Body.Close()
			t.Fatal("跨项目证书应被拒绝")
		}
	})

	// 场景5：业务系统不在白名单（cari/platform/gateway）→ 白名单拒绝。
	t.Run("not_in_whitelist", func(t *testing.T) {
		cert, err := ca.MakeLeaf("caller", "spiffe://cari/platform/gateway/x", true)
		if err != nil {
			t.Fatalf("生成证书失败: %v", err)
		}
		resp, err := doPost(&cert)
		if err == nil {
			resp.Body.Close()
			t.Fatal("业务系统不在白名单应被拒绝")
		}
	})

	// 场景6：同组织同项目且业务系统在白名单 → 通过。
	t.Run("valid", func(t *testing.T) {
		cert, err := ca.MakeLeaf("caller", "spiffe://cari/platform/framework/caller", true)
		if err != nil {
			t.Fatalf("生成证书失败: %v", err)
		}
		resp, err := doPost(&cert)
		if err != nil {
			t.Fatalf("合法证书应通过: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("期望 201，实际 %d", resp.StatusCode)
		}
	})
}

func TestSearchAuditLogsHandler_ErrorStatus(t *testing.T) {
	cases := []struct {
		name       string
		ready      bool
		target     string
		SearchErr  error
		wantStatus int
		wantError  string
	}{
		{"bad_page_type", true, "/api/v1/auditlogs?page=abc", nil, http.StatusBadRequest, "auditlog.invalid_parameter"},
		{"page_too_deep", true, "/api/v1/auditlogs?page=99999&page_size=200", nil, http.StatusBadRequest, "auditlog.page_too_deep"},
		{"not_ready", false, "/api/v1/auditlogs", nil, http.StatusServiceUnavailable, "auditlog.unavailable"},
		{"db_error", true, "/api/v1/auditlogs", errDBBoom, http.StatusInternalServerError, "auditlog.query_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mdl := &testutil.MockModel{SearchErr: tc.SearchErr}
			svcCtx := newTestSvcCtx(t, tc.ready, mdl)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			SearchAuditLogsHandler(svcCtx).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("期望 %d，实际 %d，body=%s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if env := decodeEnvelope(t, rec); env.Error != tc.wantError {
				t.Errorf("error 应为 %q，实际 %q", tc.wantError, env.Error)
			}
		})
	}
}
