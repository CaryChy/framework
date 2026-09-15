package id

import (
	"github.com/google/uuid"
)

// NewID 生成一个 UUIDv7 字符串。
// UUIDv7 是基于 Unix 毫秒时间戳的、按时间有序的 UUID，
// 适合用作数据库主键，能提升 B-Tree 索引的写入性能。
func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// uuid.NewV7 仅在系统时钟异常时可能出错（如时钟回拨且无法处理）。
		// 在大多数生产环境中，可视为致命错误，直接 panic 以避免生成错误 ID。
		panic("id: failed to generate UUIDv7: " + err.Error())
	}
	return id.String()
}
