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

// With 返回携带字段的视图 core：zap.Logger.With(...) 注入的上下文字段
// （如 logger 名、endpoint 等）必须随后续每条日志一起转发给 logx，不能丢弃。
func (c *core) With(fields []zapcore.Field) zapcore.Core {
	return &withCore{parent: c, fields: fields}
}

func (c *core) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return ce.AddCore(ent, c)
}

func (c *core) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	c.write(ent, nil, fields)
	return nil
}

// write 将一条日志（含 With 上下文字段 + 调用点字段）转发给 logx。
func (c *core) write(ent zapcore.Entry, contextFields, callFields []zapcore.Field) {
	logFields := make([]logx.LogField, 0, len(contextFields)+len(callFields)+2)
	if len(ent.LoggerName) > 0 {
		logFields = append(logFields, logx.Field("logger", ent.LoggerName))
	}
	if ent.Caller.Defined {
		logFields = append(logFields, logx.Field("source", ent.Caller.TrimmedPath()))
	}
	for _, f := range contextFields {
		logFields = append(logFields, toLogxField(f))
	}
	for _, f := range callFields {
		logFields = append(logFields, toLogxField(f))
	}

	switch {
	case ent.Level >= zapcore.ErrorLevel:
		logx.Errorw(ent.Message, logFields...)
	default:
		// 本桥接的最低级别为 Warn：warn/error 分别映射到 Sloww/Errorw，
		// 与 go-zero 语义一致（慢日志/错误告警），info 以下不会到达这里。
		logx.Sloww(ent.Message, logFields...)
	}
}

// withCore 携带 With 字段的 core 视图：每条日志输出时合并上下文与调用点字段。
type withCore struct {
	parent *core
	fields []zapcore.Field
}

func (w *withCore) Enabled(lvl zapcore.Level) bool { return w.parent.Enabled(lvl) }

func (w *withCore) With(fields []zapcore.Field) zapcore.Core {
	merged := make([]zapcore.Field, 0, len(w.fields)+len(fields))
	merged = append(merged, w.fields...)
	merged = append(merged, fields...)
	return &withCore{parent: w.parent, fields: merged}
}

func (w *withCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return ce.AddCore(ent, w)
}

func (w *withCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	w.parent.write(ent, w.fields, fields)
	return nil
}

func (w *withCore) Sync() error { return nil }

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
