package id

import (
	"github.com/google/uuid"
)

// NewID 生成一个 UUIDv7 字符串。
// UUIDv7 是基于 Unix 毫秒时间戳的、按时间有序的 UUID，
// 适合用作数据库主键，能提升 B-Tree 索引的写入性能。
//
// 返回 error 而非 panic：uuid.NewV7 在系统时钟异常（如时钟回拨且无法处理）时会失败，
// 由调用方决定如何处理（记录日志、拒绝请求或降级），避免单个请求导致整个进程崩溃。
func NewID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
