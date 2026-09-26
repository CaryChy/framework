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
// 最低输出级别为 Warn：过滤 etcd client 底层高频的 retrying info 噪音（连通性观测已由
// 健康追踪器的结构化日志承担），仅保留 warn/error 作为补充。
// logger 名称（etcd-client）与调用位置会作为字段一并输出，方便区分日志来源。
func NewEtcdLogger() *zap.Logger {
	return zap.New(
		&core{enab: zap.NewAtomicLevelAt(zapcore.WarnLevel)},
		zap.WithCaller(true),
	)
}

// core 实现 zapcore.Core，将每条日志转发给 logx。
type core struct {
	enab zapcore.LevelEnabler
}

func (c *core) Enabled(lvl zapcore.Level) bool { return c.enab.Enabled(lvl) }

func (c *core) With([]zapcore.Field) zapcore.Core { return c }

func (c *core) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return ce.AddCore(ent, c)
}

func (c *core) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	logFields := make([]logx.LogField, 0, len(fields)+2)
	if len(ent.LoggerName) > 0 {
		logFields = append(logFields, logx.Field("logger", ent.LoggerName))
	}
	if ent.Caller.Defined {
		logFields = append(logFields, logx.Field("source", ent.Caller.TrimmedPath()))
	}
	for _, f := range fields {
		logFields = append(logFields, toLogxField(f))
	}

	switch {
	case ent.Level >= zapcore.ErrorLevel:
		logx.Errorw(ent.Message, logFields...)
	case ent.Level == zapcore.WarnLevel:
		logx.Sloww(ent.Message, logFields...)
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
