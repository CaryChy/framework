package main

import (
	"testing"

	"cari.com.cn/framework/auditlog/server/internal/config"
)

// TestAdvertiseAddr 发布地址计算：AdvertiseHost 优先；空/未指定地址（0.0.0.0/::）
// 必须启动即失败，避免把下游不可达的地址发布到 etcd。
func TestAdvertiseAddr(t *testing.T) {
	tests := []struct {
		name    string
		section config.HttpSection
		want    string
		wantErr bool
	}{
		{
			name:    "host_fallback",
			section: config.HttpSection{Host: "127.0.0.1", Port: 8083},
			want:    "127.0.0.1:8083",
		},
		{
			name:    "advertise_host_priority",
			section: config.HttpSection{Host: "0.0.0.0", Port: 8083, AdvertiseHost: "10.0.0.8"},
			want:    "10.0.0.8:8083",
		},
		{
			name:    "unspecified_ipv4_rejected",
			section: config.HttpSection{Host: "0.0.0.0", Port: 8083},
			wantErr: true,
		},
		{
			name:    "unspecified_ipv6_rejected",
			section: config.HttpSection{Host: "::", Port: 8083},
			wantErr: true,
		},
		{
			name:    "empty_host_rejected",
			section: config.HttpSection{Port: 8083},
			wantErr: true,
		},
		{
			name:    "hostname_allowed",
			section: config.HttpSection{Host: "auditlog.svc.local", Port: 8083},
			want:    "auditlog.svc.local:8083",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := advertiseAddr(tt.section)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望配置错误，实际得到地址 %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if got != tt.want {
				t.Errorf("advertiseAddr = %q, 期望 %q", got, tt.want)
			}
		})
	}
}
