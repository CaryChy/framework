package svc

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/zeromicro/go-zero/core/discov"
	clientv3 "go.etcd.io/etcd/client/v3"

	"cari.com.cn/framework/auditlog/common/logbridge"
)

const (
	etcdProbeInterval = 5 * time.Second
	etcdDialTimeout   = 2 * time.Second
)

// etcdClientConfig 从 discov.EtcdConf 构造健康探活的 etcd 客户端配置，支持账号密码与 mTLS。
// 与 gRPC/HTTP 服务注册使用同一套 EtcdConf，保证探测与注册的连接参数一致。
// 声明了 TLS（HasTLS）但证书构造失败属于配置类错误：返回 error 终止启动，
// 不允许静默降级为明文连接（与业务端口 mTLS fail-fast 姿态一致）。
func etcdClientConfig(conf discov.EtcdConf) (clientv3.Config, error) {
	cfg := clientv3.Config{
		Endpoints:   conf.Hosts,
		DialTimeout: etcdDialTimeout,
		// 把 etcd client 的 zap 日志桥接到 logx，统一 JSON 输出格式。
		Logger: logbridge.NewEtcdLogger(),
	}
	if conf.User != "" {
		cfg.Username = conf.User
		cfg.Password = conf.Pass
	}
	if conf.HasTLS() {
		tlsCfg, err := etcdTLSConfig(conf)
		if err != nil {
			return clientv3.Config{}, fmt.Errorf("etcd 已声明 TLS 但配置构造失败，拒绝降级为明文连接: %w", err)
		}
		cfg.TLS = tlsCfg
	}
	return cfg, nil
}

// etcdTLSConfig 由 EtcdConf 的证书字段构造 etcd 客户端 TLS 配置。
func etcdTLSConfig(conf discov.EtcdConf) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: conf.InsecureSkipVerify,
		MinVersion:         tls.VersionTLS12,
	}
	if conf.CACertFile != "" {
		caPEM, err := os.ReadFile(conf.CACertFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("etcd CA 证书解析失败")
		}
		tlsCfg.RootCAs = pool
	}
	if conf.CertFile != "" && conf.CertKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(conf.CertFile, conf.CertKeyFile)
		if err != nil {
			return nil, err
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}
