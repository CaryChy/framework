// Package tls 提供 auditlog 服务（HTTP/gRPC）双向 TLS 所需的证书加载与配置构造能力。
package tls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

// LoadCertPool 读取 CA 证书并构造证书池，用于校验对端证书。
func LoadCertPool(caFile string) (*x509.CertPool, error) {
	if len(caFile) == 0 {
		return nil, errors.New("tls: ca cert file is empty")
	}

	caPem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("tls: read ca cert %q: %w", caFile, err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPem) {
		return nil, fmt.Errorf("tls: failed to append ca cert from %q", caFile)
	}

	return pool, nil
}

// loadServerCertificate 加载服务端证书与私钥，并确保 Leaf 已解析，
// 供启动期从证书 SPIFFE URI 派生组织/项目身份基准。
func loadServerCertificate(certFile, keyFile string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("tls: load key pair (%q, %q): %w", certFile, keyFile, err)
	}
	if cert.Leaf == nil {
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("tls: parse server leaf cert %q: %w", certFile, err)
		}
		cert.Leaf = leaf
	}

	return cert, nil
}

// NewServerTLSConfig 构造 HTTPS 服务端的 mTLS 配置。
// caFile 非空时：Go TLS 验证证书链后，再校验调用方 SPIFFE 身份——组织/项目基准从
// 本服务证书派生，调用方必须同组织同项目；caFile 为空时仅单向 TLS，policy 不生效。
func NewServerTLSConfig(certFile, keyFile, caFile string, policy Policy) (*tls.Config, error) {
	cert, err := loadServerCertificate(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		ClientCAs:    x509.NewCertPool(),
	}

	if len(caFile) > 0 {
		// 启动期 fail-fast：服务端证书缺少合法 SPIFFE URI 时无法界定信任边界，
		// 直接拒绝启动，避免退化为"证书链通过即放行"。
		verifier, err := policy.newVerifier(cert.Leaf)
		if err != nil {
			return nil, err
		}
		pool, err := LoadCertPool(caFile)
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.VerifyPeerCertificate = verifier.verifyPeerCertificate
	}

	return cfg, nil
}

// NewServerCredentials 构造 gRPC 服务端的 mTLS 传输凭证，身份策略与 HTTP 端口共用同一份实现。
func NewServerCredentials(certFile, keyFile, caFile string, policy Policy) (credentials.TransportCredentials, error) {
	cfg, err := NewServerTLSConfig(certFile, keyFile, caFile, policy)
	if err != nil {
		return nil, err
	}

	return credentials.NewTLS(cfg), nil
}

// NewClientCredentials 构造 gRPC 客户端的 mTLS 传输凭证。
// serverName 覆盖 SNI/主机名校验（如经 etcd 发现拿到的是 IP:Port）。
func NewClientCredentials(clientCert, clientKey, caFile, serverName string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(clientCert, clientKey)
	if err != nil {
		return nil, fmt.Errorf("tls: load client key pair (%q, %q): %w", clientCert, clientKey, err)
	}

	pool, err := LoadCertPool(caFile)
	if err != nil {
		return nil, err
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
		ServerName:   serverName,
	}

	return credentials.NewTLS(cfg), nil
}
