// Package logbridge 把第三方库（当前为 etcd clientv3）的 zap 结构化日志
// 桥接到 go-zero logx，使全部日志统一为 logx JSON 格式，便于集中采集与检索。
package logbridge

import (
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewEtcdLogger 构造一个写入 logx 的 zap.Logger，供 clientv3.Config.Logger 使用。
// 最低级别 Warn：过滤 etcd client 高频的 retrying info 噪音（连通性观测由健康追踪器承担）。
func NewEtcdLogger() *zap.Logger {
	return zap.New(
		&core{enab: zap.NewAtomicLevelAt(zapcore.WarnLevel)},
		zap.WithCaller(true),
	)
}

// core 实现 zapcore.Core，将每条日志转发给 logx。
type core struct {
	enab   zapcore.LevelEnabler
	fields []zapcore.Field // With 累积的上下文字段
}

func (c *core) Enabled(lvl zapcore.Level) bool { return c.enab.Enabled(lvl) }

// With 返回携带新增上下文字段的新 core，保证 zap.With 注入的字段（如 component 名）不丢失。
func (c *core) With(fields []zapcore.Field) zapcore.Core {
	merged := make([]zapcore.Field, 0, len(c.fields)+len(fields))
	merged = append(merged, c.fields...)
	merged = append(merged, fields...)
	return &core{enab: c.enab, fields: merged}
}

func (c *core) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return ce.AddCore(ent, c)
}

func (c *core) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	all := make([]zapcore.Field, 0, len(c.fields)+len(fields))
	all = append(all, c.fields...)
	all = append(all, fields...)

	logFields := make([]logx.LogField, 0, len(all)+2)
	if len(ent.LoggerName) > 0 {
		logFields = append(logFields, logx.Field("logger", ent.LoggerName))
	}
	if ent.Caller.Defined {
		logFields = append(logFields, logx.Field("source", ent.Caller.TrimmedPath()))
	}
	for _, f := range all {
		logFields = append(logFields, toLogxField(f))
	}

	// logx 无独立 Warn 级别：第三方 warn（如重试失败）按 Infow 输出并标注 level=warn，
	// 避免误入慢日志流（Sloww 是慢日志语义，并非 warn）。
	switch {
	case ent.Level >= zapcore.ErrorLevel:
		logx.Errorw(ent.Message, logFields...)
	case ent.Level == zapcore.WarnLevel:
		logx.Infow(ent.Message, append(logFields, logx.Field("level", "warn"))...)
	default:
		logx.Infow(ent.Message, logFields...)
	}
	return nil
}

func (c *core) Sync() error { return nil }

// toLogxField 将 zap 字段值转换为 logx 字段，覆盖 etcd 客户端日志的常见类型；
// 其余类型退化为反射值，确保信息不丢失。
func toLogxField(f zapcore.Field) logx.LogField {
	switch f.Type {
	case zapcore.StringType, zapcore.BinaryType, zapcore.ByteStringType:
		return logx.Field(f.Key, f.String)
	case zapcore.ErrorType:
		if err, ok := f.Interface.(error); ok {
			return logx.Field(f.Key, err.Error())
		}
		return logx.Field(f.Key, f.Interface)
	case zapcore.Int64Type, zapcore.Uint64Type, zapcore.Uint32Type:
		return logx.Field(f.Key, f.Integer)
	case zapcore.Int32Type:
		return logx.Field(f.Key, int32(f.Integer))
	case zapcore.BoolType:
		return logx.Field(f.Key, f.Integer == 1)
	case zapcore.DurationType:
		return logx.Field(f.Key, time.Duration(f.Integer).String())
	case zapcore.TimeType:
		return logx.Field(f.Key, time.Unix(0, f.Integer).Format(time.RFC3339Nano))
	default:
		return logx.Field(f.Key, f.Interface)
	}
}
