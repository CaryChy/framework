package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// appendURIs 将 SPIFFE 字符串解析为 url.URL 列表（解析失败立即让测试失败）。
func appendURIs(t *testing.T, raw ...string) []*url.URL {
	t.Helper()
	uris := make([]*url.URL, 0, len(raw))
	for _, s := range raw {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("测试用 SPIFFE URI 非法 %q: %v", s, err)
		}
		uris = append(uris, u)
	}
	return uris
}

// ---------- 内存 PKI 构造 ----------

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"cari"}, CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("生成 CA 失败: %v", err)
	}
	caCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("解析 CA 失败: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &testCA{cert: caCert, key: key, pool: pool}
}

// makeLeaf 由指定 CA 签发带 SPIFFE URI SAN 的叶子证书；spiffe 为空时不带 URI。
func (ca *testCA) makeLeaf(t *testing.T, cn, spiffe string, clientAuth bool) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"cari"}, CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if clientAuth {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.DNSNames = []string{"localhost"}
	}
	if spiffe != "" {
		tmpl.URIs = appendURIs(t, spiffe)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("签发叶子证书失败: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: mustParse(t, der)}
}

// makeLeafWithExtraURIs 签发带多个 URI SAN 的证书，用于唯一性反例。
func (ca *testCA) makeLeafWithExtraURIs(t *testing.T, spiffes []string) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "multi"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         appendURIs(t, spiffes...),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("签发叶子证书失败: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: mustParse(t, der)}
}

func mustParse(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("解析叶子证书失败: %v", err)
	}
	return c
}

// writeKeyPair 将 tls.Certificate 落盘为 PEM，供 NewServerTLSConfig 读取。
func writeKeyPair(t *testing.T, cert tls.Certificate) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()
	keyDER, err := x509.MarshalECPrivateKey(cert.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		t.Fatalf("序列化私钥失败: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		t.Fatalf("写证书文件失败: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("写私钥文件失败: %v", err)
	}
	return certFile, keyFile
}

func writeCA(t *testing.T, ca *testCA) string {
	t.Helper()
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
	if err := os.WriteFile(caFile, caPEM, 0o644); err != nil {
		t.Fatalf("写 CA 文件失败: %v", err)
	}
	return caFile
}

// ---------- ParseIdentity ----------

func TestParseIdentity_OK(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.makeLeaf(t, "caller", "spiffe://cari/platform/framework/auditlog-client", true)
	id, err := ParseIdentity(leaf.Leaf)
	if err != nil {
		t.Fatalf("解析身份失败: %v", err)
	}
	if id.TrustDomain != "cari" || id.Project != "platform" ||
		id.BusinessSystem != "framework" || id.Service != "auditlog-client" {
		t.Fatalf("身份字段错误: %+v", id)
	}
}

func TestParseIdentity_BadCases(t *testing.T) {
	ca := newTestCA(t)
	cases := []struct {
		name    string
		spiffe  string
		wantErr bool
	}{
		{"missing_san", "", true},
		{"path_too_short", "spiffe://cari/platform", true},
		{"empty_segment", "spiffe://cari/platform//svc", true},
		{"empty_trust_domain", "spiffe:///platform/framework/svc", true},
		{"with_query", "spiffe://cari/platform/framework/svc?x=1", true},
		{"with_fragment", "spiffe://cari/platform/framework/svc#f", true},
		{"with_port", "spiffe://cari:9443/platform/framework/svc", true},
		{"ok_with_instance_suffix", "spiffe://cari/platform/framework/svc/instance/pod-1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leaf := ca.makeLeaf(t, "c", tc.spiffe, true)
			_, err := ParseIdentity(leaf.Leaf)
			if tc.wantErr && err == nil {
				t.Fatalf("期望解析失败，实际通过")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望解析通过，实际失败: %v", err)
			}
		})
	}
}

func TestParseIdentity_MultipleURIs(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.makeLeafWithExtraURIs(t, []string{
		"spiffe://cari/platform/framework/a",
		"spiffe://cari/platform/framework/b",
	})
	if _, err := ParseIdentity(leaf.Leaf); err == nil {
		t.Fatal("存在多个 spiffe URI 时必须拒绝")
	}
}

// ---------- 身份基准派生自服务端证书 ----------

// newVerifierFrom 便捷构造：以指定 SPIFFE 的服务端证书作为组织/项目基准。
func newVerifierFrom(t *testing.T, ca *testCA, serverSPIFFE string, p Policy) *identityVerifier {
	t.Helper()
	serverCert := ca.makeLeaf(t, "server", serverSPIFFE, false)
	v, err := p.newVerifier(serverCert.Leaf)
	if err != nil {
		t.Fatalf("从服务端证书派生基准失败: %v", err)
	}
	return v
}

func TestVerifier_BaselineIsCariPlatform(t *testing.T) {
	ca := newTestCA(t)
	// 本服务证书身份：cari/platform/framework/auditlog。
	policy := Policy{
		AllowedBusinessSystems: []string{"framework"},
		AllowedServices:        []string{"auditlog-client"},
	}
	v := newVerifierFrom(t, ca, "spiffe://cari/platform/framework/auditlog", policy)
	if v.self.TrustDomain != "cari" || v.self.Project != "platform" {
		t.Fatalf("基准派生错误: %+v", v.self)
	}

	cases := []struct {
		name    string
		spiffe  string
		wantErr bool
	}{
		{"exact_match", "spiffe://cari/platform/framework/auditlog-client", false},
		{"wrong_organization", "spiffe://other/platform/framework/auditlog-client", true},
		{"wrong_project", "spiffe://cari/mining-platform/framework/auditlog-client", true},
		{"wrong_business_system", "spiffe://cari/platform/gateway/auditlog-client", true},
		{"wrong_service", "spiffe://cari/platform/framework/evil-service", true},
		{"trust_domain_case_insensitive", "spiffe://CARI/platform/framework/auditlog-client", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leaf := ca.makeLeaf(t, "c", tc.spiffe, true)
			_, err := v.verify(leaf.Leaf)
			if tc.wantErr && err == nil {
				t.Fatalf("期望拒绝身份 %s", tc.spiffe)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望放行身份 %s，实际: %v", tc.spiffe, err)
			}
		})
	}
}

// 关键语义：组织/项目不是写死的 cari/platform，而是随服务端证书变化。
// 当本服务证书属于 acme/mining-ops 时，同属该组织/项目的调用方放行，
// 即便它持有 cari/platform 的"平台身份"也必须拒绝。
func TestVerifier_BaselineDerivedFromServerCert(t *testing.T) {
	ca := newTestCA(t)
	v := newVerifierFrom(t, ca, "spiffe://acme/mining-ops/iot/ingestor", Policy{})
	if v.self.TrustDomain != "acme" || v.self.Project != "mining-ops" {
		t.Fatalf("基准应随服务端证书为 acme/mining-ops，实际: %+v", v.self)
	}

	accepted := ca.makeLeaf(t, "sensor", "spiffe://acme/mining-ops/iot/sensor-1", true)
	if _, err := v.verify(accepted.Leaf); err != nil {
		t.Fatalf("与本服务同组织同项目的调用方应放行: %v", err)
	}
	rejected := ca.makeLeaf(t, "platform-svc", "spiffe://cari/platform/framework/x", true)
	if _, err := v.verify(rejected.Leaf); err == nil {
		t.Fatal("cari/platform 身份对于 acme/mining-ops 服务属于跨组织，必须拒绝（证明未写死）")
	}
}

func TestVerifier_OptionalLayers(t *testing.T) {
	ca := newTestCA(t)
	// 无白名单：只要求与本服务同组织同项目，业务系统/服务不同也放行。
	v := newVerifierFrom(t, ca, "spiffe://cari/platform/framework/auditlog", Policy{})

	otherProject := ca.makeLeaf(t, "c", "spiffe://cari/mining-x/platform-x/anything", true)
	if _, err := v.verify(otherProject.Leaf); err == nil {
		t.Fatal("项目与本服务不一致必须拒绝")
	}
	sameScope := ca.makeLeaf(t, "c", "spiffe://cari/platform/any-system/any-service", true)
	if _, err := v.verify(sameScope.Leaf); err != nil {
		t.Fatalf("同组织同项目且无白名单时应放行，实际: %v", err)
	}
}

func TestNewVerifier_ServerCertWithoutSPIFFE(t *testing.T) {
	ca := newTestCA(t)
	serverCert := ca.makeLeaf(t, "server", "", false)
	if _, err := (Policy{}).newVerifier(serverCert.Leaf); err == nil {
		t.Fatal("服务端证书缺少 SPIFFE URI 时必须无法派生基准并报错")
	}
}

// ---------- 真实 mTLS 握手集成 ----------

// startMTLSServer 用 NewServerTLSConfig 启动一次性 TLS 监听，返回地址与关闭函数。
func startMTLSServer(t *testing.T, ca *testCA, serverCert tls.Certificate, caFile string, policy Policy) (string, func()) {
	t.Helper()
	certFile, keyFile := writeKeyPair(t, serverCert)
	cfg, err := NewServerTLSConfig(certFile, keyFile, caFile, policy)
	if err != nil {
		t.Fatalf("构造服务端 TLS 配置失败: %v", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				// 显式完成握手：TLS1.3 客户端证书为 post-handshake 认证，
				// 身份拒绝发生在服务端 Handshake 阶段；随后读一个字节再关闭。
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				if err := c.(*tls.Conn).Handshake(); err != nil {
					_ = c.Close()
					return
				}
				buf := make([]byte, 4)
				_, _ = c.Read(buf)
				_ = c.Close()
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

// pingMTLS 建立 mTLS 连接并做一次应用层往返。
// 注意 TLS1.3 下客户端证书认证在握手后进行：tls.Dial 可能先成功，
// 服务端拒绝时发出的 bad_certificate 告警会在随后的 Read/Write 中暴露，
// 因此不能只断言 Dial 错误。被接受时服务端读完即关闭，客户端收到 io.EOF。
func pingMTLS(t *testing.T, addr string, ca *testCA, clientCert *tls.Certificate) error {
	t.Helper()
	cfg := &tls.Config{
		RootCAs:    ca.pool,
		ServerName: "localhost",
		MinVersion: tls.VersionTLS12,
	}
	if clientCert != nil {
		cfg.Certificates = []tls.Certificate{*clientCert}
	}
	conn, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		return err
	}
	buf := make([]byte, 4)
	if _, err := conn.Read(buf); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // 服务端接受后正常关闭
		}
		return err // 收到 bad_certificate 等告警
	}
	return nil
}

// 业务系统白名单策略；组织/项目基准由服务端证书 spiffe://cari/platform/framework/auditlog 派生。
var serverPolicy = Policy{
	AllowedBusinessSystems: []string{"framework"},
}

func TestMTLSHandshake(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeCA(t, ca)
	serverCert := ca.makeLeaf(t, "auditlog", "spiffe://cari/platform/framework/auditlog", false)
	addr, shutdown := startMTLSServer(t, ca, serverCert, caFile, serverPolicy)
	defer shutdown()

	cases := []struct {
		name     string
		spiffe   string
		sameCA   bool
		withCert bool
		wantErr  bool
	}{
		{"same_project_caller", "spiffe://cari/platform/framework/auditlog-client", true, true, false},
		{"outsider_project", "spiffe://cari/mining-platform/device-system/device-service", true, true, true},
		{"outsider_organization", "spiffe://evil/platform/framework/intruder", true, true, true},
		{"other_business_system", "spiffe://cari/platform/gateway/gateway-service", true, true, true},
		{"cert_without_san", "", true, true, true},
		{"no_client_cert", "", true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var clientCert *tls.Certificate
			if tc.withCert {
				c := ca.makeLeaf(t, "caller", tc.spiffe, true)
				clientCert = &c
			}
			err := pingMTLS(t, addr, ca, clientCert)
			if tc.wantErr && err == nil {
				t.Fatalf("期望握手失败（%s），实际成功", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望握手成功（%s），实际: %v", tc.name, err)
			}
		})
	}
}

func TestMTLSHandshake_UntrustedRoot(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeCA(t, ca)
	serverCert := ca.makeLeaf(t, "auditlog", "spiffe://cari/platform/framework/auditlog", false)
	addr, shutdown := startMTLSServer(t, ca, serverCert, caFile, serverPolicy)
	defer shutdown()

	// 另一套 CA 签发的调用方证书：即使身份字符串正确，Root 验证也必须失败。
	otherCA := newTestCA(t)
	rogue := otherCA.makeLeaf(t, "caller", "spiffe://cari/platform/framework/auditlog-client", true)
	if err := pingMTLS(t, addr, ca, &rogue); err == nil {
		t.Fatal("非受信 Root 签发的客户端证书必须被拒绝")
	}
}

// 端到端证明组织/项目基准随服务端证书变化：
// 服务端证书属于 acme/mining-ops 时，只接受同组织同项目调用方，cari/platform 被拒。
func TestMTLSHandshake_BaselineDerivedFromServerCert(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeCA(t, ca)
	serverCert := ca.makeLeaf(t, "ingestor", "spiffe://acme/mining-ops/iot/ingestor", false)
	addr, shutdown := startMTLSServer(t, ca, serverCert, caFile, Policy{})
	defer shutdown()

	inScope := ca.makeLeaf(t, "sensor", "spiffe://acme/mining-ops/iot/sensor-1", true)
	if err := pingMTLS(t, addr, ca, &inScope); err != nil {
		t.Fatalf("与本服务同组织同项目的调用方应握手成功: %v", err)
	}
	outOfScope := ca.makeLeaf(t, "platform", "spiffe://cari/platform/framework/x", true)
	if err := pingMTLS(t, addr, ca, &outOfScope); err == nil {
		t.Fatal("组织/项目基准来自服务端证书：cari/platform 对 acme/mining-ops 必须被拒")
	}
}

func TestNewServerTLSConfig_RequiresServerSPIFFE(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeCA(t, ca)

	// 启用 mTLS（caFile 非空）但服务端证书没有 SPIFFE URI：
	// 无法派生组织/项目基准，启动期必须失败，防止退化为链通过即放行。
	serverCertNoID := ca.makeLeaf(t, "auditlog", "", false)
	certFile, keyFile := writeKeyPair(t, serverCertNoID)
	if _, err := NewServerTLSConfig(certFile, keyFile, caFile, Policy{}); err == nil {
		t.Fatal("服务端证书缺少 SPIFFE URI 时启用 mTLS 必须报错")
	}

	// 带合法 SPIFFE 的服务端证书 + mTLS：正常构造。
	serverCert := ca.makeLeaf(t, "auditlog", "spiffe://cari/platform/framework/auditlog", false)
	certFile2, keyFile2 := writeKeyPair(t, serverCert)
	if _, err := NewServerTLSConfig(certFile2, keyFile2, caFile, Policy{}); err != nil {
		t.Fatalf("合法服务端证书应成功构造 mTLS 配置: %v", err)
	}
	// 单向 TLS（caFile 为空）不需要身份基准，即使证书无 SPIFFE 也允许。
	if _, err := NewServerTLSConfig(certFile, keyFile, "", Policy{}); err != nil {
		t.Fatalf("单向 TLS 不应要求服务端 SPIFFE 身份: %v", err)
	}
}
