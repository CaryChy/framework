// Package tlstest 提供跨包测试复用的 mTLS 测试辅助：内存 CA、签发带 SPIFFE URI
// 的临时叶子证书、写临时证书文件。独立于 tls 包存放，避免测试代码编进生产二进制。
package tlstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// TestCA 内存测试 CA，供跨包测试签发带 SPIFFE URI 的临时证书。
type TestCA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	Pool *x509.CertPool
}

// NewTestCA 创建内存测试 CA。
func NewTestCA() (*TestCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成 CA 密钥失败: %w", err)
	}
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
		return nil, fmt.Errorf("创建 CA 证书失败: %w", err)
	}
	caCert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("解析 CA 证书失败: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &TestCA{Cert: caCert, Key: key, Pool: pool}, nil
}

// MakeLeaf 签发带 SPIFFE URI SAN 的叶子证书。
func (ca *TestCA) MakeLeaf(cn, spiffe string, clientAuth bool) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("生成叶子密钥失败: %w", err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("生成序列号失败: %w", err)
	}
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
		u, err := url.Parse(spiffe)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("解析 SPIFFE URI 失败: %w", err)
		}
		tmpl.URIs = []*url.URL{u}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("创建叶子证书失败: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("解析叶子证书失败: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}

// WriteCertPool 将 CA 证书写入临时目录，返回文件路径。
func (ca *TestCA) WriteCertPool(dir string) (string, error) {
	path := filepath.Join(dir, "ca.pem")
	pemData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw})
	if err := os.WriteFile(path, pemData, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// WriteKeyPair 将证书和私钥写入临时目录，返回 (certPath, keyPath)。
func WriteKeyPair(dir string, cert tls.Certificate, prefix string) (string, string, error) {
	certPath := filepath.Join(dir, prefix+".pem")
	keyPath := filepath.Join(dir, prefix+"-key.pem")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return "", "", err
	}

	keyDER, err := x509.MarshalECPrivateKey(cert.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		return "", "", err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", "", err
	}

	return certPath, keyPath, nil
}
