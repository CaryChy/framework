package logbridge

import (
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// TestToLogxField 覆盖 etcd 客户端日志常见字段类型到 logx 的转换。
func TestToLogxField(t *testing.T) {
	tests := []struct {
		name  string
		field zapcore.Field
		want  any
	}{
		{"string", zap.String("k", "v"), "v"},
		{"int64", zap.Int64("k", 42), int64(42)},
		{"int32", zap.Int32("k", 7), int32(7)},
		{"bool", zap.Bool("k", true), true},
		{"bool_false", zap.Bool("k", false), false},
		{"duration", zap.Duration("k", time.Second), "1s"},
		{"error", zap.NamedError("k", errors.New("boom")), "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := toLogxField(tt.field)
			if f.Key != "k" {
				t.Errorf("key 错误: %s", f.Key)
			}
			if f.Value != tt.want {
				t.Errorf("value = %v (%T)，期望 %v (%T)", f.Value, f.Value, tt.want, tt.want)
			}
		})
	}
}

// TestNewEtcdLogger 构造的 logger 可正常记录 error/warn，且不 panic。
func TestNewEtcdLogger(t *testing.T) {
	lg := NewEtcdLogger()
	if lg == nil {
		t.Fatal("NewEtcdLogger 不应返回 nil")
	}
	// Warn 为最低输出级别，Info 应被过滤；调用 Write 不应 panic。
	lg.Warn("warn message", zap.String("component", "etcd"))
	lg.Error("error message", zap.Error(errors.New("x")))
}

// TestCore_WithAccumulatesFields With 注入的字段应在 Write 时合并。
func TestCore_WithAccumulatesFields(t *testing.T) {
	c := &core{enab: zap.NewAtomicLevelAt(zapcore.InfoLevel)}
	c2 := c.With([]zapcore.Field{zap.String("a", "1")}).(*core)
	if len(c2.fields) != 1 || c2.fields[0].Key != "a" {
		t.Fatalf("With 未累积字段: %+v", c2.fields)
	}
	c3 := c2.With([]zapcore.Field{zap.String("b", "2")}).(*core)
	if len(c3.fields) != 2 {
		t.Fatalf("二次 With 应累积为 2 个字段，实际 %d", len(c3.fields))
	}
}
