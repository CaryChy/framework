package model

import "github.com/zeromicro/go-zero/core/stores/sqlx"

// ErrNotFound 未查询到记录时返回，与 go-zero sqlx 保持一致。
var ErrNotFound = sqlx.ErrNotFound
