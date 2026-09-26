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

// loadServerCertificate 加载服务端证书与私钥。
func loadServerCertificate(certFile, keyFile string) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("tls: load key pair (%q, %q): %w", certFile, keyFile, err)
	}

	return cert, nil
}

// NewServerTLSConfig 构造用于 HTTPS 服务端的 mTLS 配置。
// 当 caFile 非空时，强制校验客户端证书（双向认证）；为空时仅做服务端单向 TLS。
func NewServerTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
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
		pool, err := LoadCertPool(caFile)
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return cfg, nil
}

// NewServerCredentials 构造 gRPC 服务端的 mTLS 传输凭证。
func NewServerCredentials(certFile, keyFile, caFile string) (credentials.TransportCredentials, error) {
	cfg, err := NewServerTLSConfig(certFile, keyFile, caFile)
	if err != nil {
		return nil, err
	}

	return credentials.NewTLS(cfg), nil
}

// NewClientCredentials 构造 gRPC 客户端的 mTLS 传输凭证。
// clientCert/clientKey 为客户端证书；caFile 用于校验服务端；
// serverName 用于覆盖证书校验的 SNI/主机名（如经过 etcd 发现拿到的是 IP:Port）。
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
