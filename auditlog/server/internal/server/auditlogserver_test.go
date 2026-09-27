package server

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	tlsutil "cari.com.cn/framework/auditlog/common/tls"
	"cari.com.cn/framework/auditlog/common/tls/tlstest"
	"cari.com.cn/framework/auditlog/model"
	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/biz"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/auditlog/server/internal/testutil"
)

var errDBBoom = errors.New("db boom")

const bufSize = 1024 * 1024

// startGRPC 基于 bufconn 启动注册了 AuditLogService 的内存 gRPC 服务，返回就绪客户端。
func startGRPC(t *testing.T, ready bool, mdl *testutil.MockModel) (pb.AuditLogServiceClient, func()) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	svcCtx := &svc.ServiceContext{
		AuditLogBiz: biz.NewAuditLogBiz(&testutil.ReadyStub{Ready: ready}, mdl),
	}
	pb.RegisterAuditLogServiceServer(srv, NewAuditLogServer(svcCtx))
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		srv.Stop()
		t.Fatalf("创建 bufconn 客户端失败: %v", err)
	}
	cleanup := func() {
		_ = conn.Close()
		srv.Stop()
	}
	return pb.NewAuditLogServiceClient(conn), cleanup
}

func rpcCode(t *testing.T, err error) codes.Code {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("期望 gRPC status 错误，实际 %v", err)
	}
	return st.Code()
}

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
		RequestBody:  sql.NullString{}, // NULL -> RPC 输出应为空串
		ResponseBody: sql.NullString{String: `{"ok":true}`, Valid: true},
		Metadata:     sql.NullString{String: "{}", Valid: true},
		DurationMs:   12,
		CreatedAt:    time.UnixMilli(1700000000123).UTC(),
	}
}

// ---------- CreateAuditLogs ----------

func TestGRPCCreateAuditLogs_Success(t *testing.T) {
	mdl := &testutil.MockModel{}
	cli, cleanup := startGRPC(t, true, mdl)
	defer cleanup()

	resp, err := cli.CreateAuditLogs(context.Background(), &pb.CreateAuditLogsRequest{
		Logs: []*pb.AuditLog{
			{Id: "log-1", ServiceName: "auth", Action: "login", StatusCode: 200, RequestBody: `{"u":1}`},
			{Id: "log-2", ServiceName: "order", Action: "create", StatusCode: 201},
		},
	})
	if err != nil {
		t.Fatalf("期望调用成功，实际 %v", err)
	}
	if len(resp.GetIds()) != 2 || resp.GetIds()[0] != "log-1" || resp.GetIds()[1] != "log-2" {
		t.Fatalf("返回 ID 与请求不一致: %v", resp.GetIds())
	}
	if len(mdl.InsertedRows) != 2 {
		t.Fatalf("期望落库 2 行，实际 %d", len(mdl.InsertedRows))
	}
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

func TestGRPCCreateAuditLogs_ErrorCodes(t *testing.T) {
	cases := []struct {
		name      string
		ready     bool
		req       *pb.CreateAuditLogsRequest
		InsertErr error
		want      codes.Code
	}{
		{
			name:  "empty_logs",
			ready: true,
			req:   &pb.CreateAuditLogsRequest{},
			want:  codes.InvalidArgument,
		},
		{
			name:  "empty_service_name",
			ready: true,
			req:   &pb.CreateAuditLogsRequest{Logs: []*pb.AuditLog{{Id: "a", Action: "x"}}},
			want:  codes.InvalidArgument,
		},
		{
			name:  "not_ready",
			ready: false,
			req:   &pb.CreateAuditLogsRequest{Logs: []*pb.AuditLog{{Id: "a", ServiceName: "auth"}}},
			want:  codes.Unavailable,
		},
		{
			name:      "db_error",
			ready:     true,
			req:       &pb.CreateAuditLogsRequest{Logs: []*pb.AuditLog{{Id: "a", ServiceName: "auth"}}},
			InsertErr: errDBBoom,
			want:      codes.Internal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mdl := &testutil.MockModel{InsertErr: tc.InsertErr}
			cli, cleanup := startGRPC(t, tc.ready, mdl)
			defer cleanup()

			_, err := cli.CreateAuditLogs(context.Background(), tc.req)
			if code := rpcCode(t, err); code != tc.want {
				t.Fatalf("期望 code=%v，实际 %v（err=%v）", tc.want, code, err)
			}
		})
	}
}

func TestGRPCCreateAuditLogs_BatchTooLarge(t *testing.T) {
	mdl := &testutil.MockModel{}
	cli, cleanup := startGRPC(t, true, mdl)
	defer cleanup()

	logs := make([]*pb.AuditLog, 501) // 上限 500
	for i := range logs {
		logs[i] = &pb.AuditLog{ServiceName: "auth"}
	}
	_, err := cli.CreateAuditLogs(context.Background(), &pb.CreateAuditLogsRequest{Logs: logs})
	if code := rpcCode(t, err); code != codes.InvalidArgument {
		t.Fatalf("超大批请求应返回 InvalidArgument，实际 %v", code)
	}
	if len(mdl.InsertedRows) != 0 {
		t.Fatal("被拒绝的请求不应落库")
	}
}

// ---------- SearchAuditLogs ----------

func TestGRPCSearchAuditLogs_Success(t *testing.T) {
	mdl := &testutil.MockModel{SearchRows: []*model.AuditLog{sampleRow()}, SearchTotal: 1}
	cli, cleanup := startGRPC(t, true, mdl)
	defer cleanup()

	resp, err := cli.SearchAuditLogs(context.Background(), &pb.SearchAuditLogsRequest{
		ServiceName: "auth",
		ActorId:     "user-9",
		Page:        2,
		PageSize:    10,
	})
	if err != nil {
		t.Fatalf("期望调用成功，实际 %v", err)
	}
	if resp.GetTotal() != 1 || len(resp.GetList()) != 1 {
		t.Fatalf("响应分页数据错误: total=%d len=%d", resp.GetTotal(), len(resp.GetList()))
	}

	// 查询条件透传到领域层。
	if mdl.LastInput.ServiceName != "auth" || mdl.LastInput.ActorId != "user-9" {
		t.Fatalf("过滤条件未透传: %+v", mdl.LastInput)
	}
	if mdl.LastInput.Page != 2 || mdl.LastInput.PageSize != 10 {
		t.Fatalf("分页参数未透传: page=%d page_size=%d", mdl.LastInput.Page, mdl.LastInput.PageSize)
	}

	item := resp.GetList()[0]
	if item.GetId() != "log-1" || item.GetServiceName() != "auth" || item.GetStatusCode() != 200 {
		t.Fatalf("条目基础字段错误: %+v", item)
	}
	if item.GetResponseBody() != `{"ok":true}` || item.GetMetadata() != "{}" {
		t.Fatalf("非空可空字段映射错误: body=%q meta=%q", item.GetResponseBody(), item.GetMetadata())
	}
	if item.GetRequestBody() != "" {
		t.Fatalf("NULL 字段应映射为空串，实际 %q", item.GetRequestBody())
	}
	if item.GetCreatedAt() != 1700000000123 {
		t.Fatalf("created_at 应为 Unix 毫秒，实际 %d", item.GetCreatedAt())
	}
}

func TestGRPCSearchAuditLogs_DefaultPaging(t *testing.T) {
	mdl := &testutil.MockModel{}
	cli, cleanup := startGRPC(t, true, mdl)
	defer cleanup()

	if _, err := cli.SearchAuditLogs(context.Background(), &pb.SearchAuditLogsRequest{}); err != nil {
		t.Fatalf("期望调用成功，实际 %v", err)
	}
	if mdl.LastInput.Page != 1 || mdl.LastInput.PageSize != 20 {
		t.Fatalf("未传分页时应使用默认值 1/20，实际 page=%d page_size=%d",
			mdl.LastInput.Page, mdl.LastInput.PageSize)
	}
}

// ---------- gRPC over mTLS（真实证书链 + SPIFFE 身份） ----------

// grpcMTLSDial 用指定客户端证书建立 gRPC mTLS 连接。
// 通过自定义 dialer 在 bufconn 上完成 TLS 握手。
func grpcMTLSDial(t *testing.T, lis *bufconn.Listener, ca *tlstest.TestCA, clientCert *tls.Certificate) (*grpc.ClientConn, error) {
	t.Helper()

	clientTLS := &tls.Config{
		RootCAs:    ca.Pool,
		ServerName: "localhost",
		MinVersion: tls.VersionTLS12,
	}
	if clientCert != nil {
		clientTLS.Certificates = []tls.Certificate{*clientCert}
	}

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)),
	)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// TestGRPC_mTLS_ValidClient 合法客户端：同组织同项目，业务系统在允许列表。
func TestGRPC_mTLS_ValidClient(t *testing.T) {
	mdl := &testutil.MockModel{}

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

	creds, err := tlsutil.NewServerCredentials(certFile, keyFile, caFile, tlsutil.Policy{
		AllowedBusinessSystems: []string{"framework"},
	})
	if err != nil {
		t.Fatalf("构造 mTLS 凭证失败: %v", err)
	}

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer(grpc.Creds(creds))
	svcCtx := &svc.ServiceContext{
		AuditLogBiz: biz.NewAuditLogBiz(&testutil.ReadyStub{Ready: true}, mdl),
	}
	pb.RegisterAuditLogServiceServer(srv, NewAuditLogServer(svcCtx))
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	// 生成与服务端同 CA、同组织同项目的合法客户端证书
	clientCert, err := ca.MakeLeaf("caller", "spiffe://cari/platform/framework/caller", true)
	if err != nil {
		t.Fatalf("生成客户端证书失败: %v", err)
	}

	conn, err := grpcMTLSDial(t, lis, ca, &clientCert)
	if err != nil {
		t.Fatalf("创建连接失败: %v", err)
	}
	defer conn.Close()

	cli := pb.NewAuditLogServiceClient(conn)
	resp, err := cli.CreateAuditLogs(context.Background(), &pb.CreateAuditLogsRequest{
		Logs: []*pb.AuditLog{{Id: "mtls-1", ServiceName: "auth", Action: "login"}},
	})
	if err != nil {
		t.Fatalf("mTLS gRPC 调用失败: %v", err)
	}
	if len(resp.GetIds()) != 1 || resp.GetIds()[0] != "mtls-1" {
		t.Fatalf("返回 ID 错误: %v", resp.GetIds())
	}
}

// TestGRPC_mTLS_Rejected 非法客户端证书场景。
func TestGRPC_mTLS_Rejected(t *testing.T) {
	mdl := &testutil.MockModel{}

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

	creds, err := tlsutil.NewServerCredentials(certFile, keyFile, caFile, tlsutil.Policy{
		AllowedBusinessSystems: []string{"framework"},
	})
	if err != nil {
		t.Fatalf("构造 mTLS 凭证失败: %v", err)
	}

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer(grpc.Creds(creds))
	svcCtx := &svc.ServiceContext{
		AuditLogBiz: biz.NewAuditLogBiz(&testutil.ReadyStub{Ready: true}, mdl),
	}
	pb.RegisterAuditLogServiceServer(srv, NewAuditLogServer(svcCtx))
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	req := &pb.CreateAuditLogsRequest{
		Logs: []*pb.AuditLog{{Id: "x", ServiceName: "auth"}},
	}

	// 场景1：无客户端证书 → 握手失败。
	t.Run("no_client_cert", func(t *testing.T) {
		conn, err := grpcMTLSDial(t, lis, ca, nil)
		if err != nil {
			t.Fatalf("创建连接失败: %v", err)
		}
		defer conn.Close()
		_, err = pb.NewAuditLogServiceClient(conn).CreateAuditLogs(context.Background(), req)
		if err == nil {
			t.Fatal("无客户端证书应握手失败")
		}
	})

	// 场景2：无 SPIFFE → 身份解析失败。
	t.Run("no_spiffe", func(t *testing.T) {
		cert, err := ca.MakeLeaf("caller", "", true)
		if err != nil {
			t.Fatalf("生成证书失败: %v", err)
		}
		conn, err := grpcMTLSDial(t, lis, ca, &cert)
		if err != nil {
			t.Fatalf("创建连接失败: %v", err)
		}
		defer conn.Close()
		_, err = pb.NewAuditLogServiceClient(conn).CreateAuditLogs(context.Background(), req)
		if err == nil {
			t.Fatal("无 SPIFFE 的客户端证书应被拒绝")
		}
	})

	// 场景3：跨组织（evil/platform）→ 组织不匹配。
	t.Run("cross_org", func(t *testing.T) {
		cert, err := ca.MakeLeaf("caller", "spiffe://evil/platform/framework/x", true)
		if err != nil {
			t.Fatalf("生成证书失败: %v", err)
		}
		conn, err := grpcMTLSDial(t, lis, ca, &cert)
		if err != nil {
			t.Fatalf("创建连接失败: %v", err)
		}
		defer conn.Close()
		_, err = pb.NewAuditLogServiceClient(conn).CreateAuditLogs(context.Background(), req)
		if err == nil {
			t.Fatal("跨组织证书应被拒绝")
		}
	})

	// 场景4：跨项目（cari/mining-platform）→ 项目不匹配。
	t.Run("cross_project", func(t *testing.T) {
		cert, err := ca.MakeLeaf("caller", "spiffe://cari/mining-platform/device/x", true)
		if err != nil {
			t.Fatalf("生成证书失败: %v", err)
		}
		conn, err := grpcMTLSDial(t, lis, ca, &cert)
		if err != nil {
			t.Fatalf("创建连接失败: %v", err)
		}
		defer conn.Close()
		_, err = pb.NewAuditLogServiceClient(conn).CreateAuditLogs(context.Background(), req)
		if err == nil {
			t.Fatal("跨项目证书应被拒绝")
		}
	})

	// 场景5：业务系统不在白名单（cari/platform/gateway）→ 白名单拒绝。
	t.Run("not_in_whitelist", func(t *testing.T) {
		cert, err := ca.MakeLeaf("caller", "spiffe://cari/platform/gateway/x", true)
		if err != nil {
			t.Fatalf("生成证书失败: %v", err)
		}
		conn, err := grpcMTLSDial(t, lis, ca, &cert)
		if err != nil {
			t.Fatalf("创建连接失败: %v", err)
		}
		defer conn.Close()
		_, err = pb.NewAuditLogServiceClient(conn).CreateAuditLogs(context.Background(), req)
		if err == nil {
			t.Fatal("业务系统不在白名单应被拒绝")
		}
	})

	// 场景6：同组织同项目且业务系统在白名单 → 通过。
	t.Run("valid", func(t *testing.T) {
		cert, err := ca.MakeLeaf("caller", "spiffe://cari/platform/framework/caller", true)
		if err != nil {
			t.Fatalf("生成证书失败: %v", err)
		}
		conn, err := grpcMTLSDial(t, lis, ca, &cert)
		if err != nil {
			t.Fatalf("创建连接失败: %v", err)
		}
		defer conn.Close()
		resp, err := pb.NewAuditLogServiceClient(conn).CreateAuditLogs(context.Background(), req)
		if err != nil {
			t.Fatalf("合法证书应通过: %v", err)
		}
		if len(resp.GetIds()) != 1 {
			t.Fatalf("期望 1 个 ID，实际 %d", len(resp.GetIds()))
		}
	})
}

func TestGRPCSearchAuditLogs_ErrorCodes(t *testing.T) {
	cases := []struct {
		name      string
		ready     bool
		req       *pb.SearchAuditLogsRequest
		SearchErr error
		want      codes.Code
	}{
		{
			name:  "page_too_deep",
			ready: true,
			req:   &pb.SearchAuditLogsRequest{Page: 99999, PageSize: 200},
			want:  codes.InvalidArgument,
		},
		{
			name:  "not_ready",
			ready: false,
			req:   &pb.SearchAuditLogsRequest{},
			want:  codes.Unavailable,
		},
		{
			name:      "db_error",
			ready:     true,
			req:       &pb.SearchAuditLogsRequest{},
			SearchErr: errDBBoom,
			want:      codes.Internal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mdl := &testutil.MockModel{SearchErr: tc.SearchErr}
			cli, cleanup := startGRPC(t, tc.ready, mdl)
			defer cleanup()

			_, err := cli.SearchAuditLogs(context.Background(), tc.req)
			if code := rpcCode(t, err); code != tc.want {
				t.Fatalf("期望 code=%v，实际 %v（err=%v）", tc.want, code, err)
			}
		})
	}
}
