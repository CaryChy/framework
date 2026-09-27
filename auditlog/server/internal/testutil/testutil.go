// Package testutil 提供跨包复用的测试替身（mock model、就绪桩等），
// 供 biz / handler / server 等层的测试统一使用，避免各包重复定义。
package testutil

import (
	"context"
	"database/sql"

	"cari.com.cn/framework/auditlog/model"
)

// MockModel 可控的 model.AuditLogModel 替身，记录入参与返回预设数据。
type MockModel struct {
	InsertErr    error
	InsertedRows []*model.AuditLog // InsertBatch 收到的数据，供断言
	SearchErr    error
	SearchRows   []*model.AuditLog
	SearchTotal  int64
	LastInput    model.SearchInput // Search 收到的查询条件
}

// InsertBatch 实现 model.AuditLogModel。
func (m *MockModel) InsertBatch(_ context.Context, data []*model.AuditLog) (sql.Result, error) {
	if m.InsertErr != nil {
		return nil, m.InsertErr
	}
	m.InsertedRows = append(m.InsertedRows, data...)
	return fakeResult{affected: int64(len(data))}, nil
}

// Search 实现 model.AuditLogModel。
func (m *MockModel) Search(_ context.Context, in model.SearchInput) ([]*model.AuditLog, int64, error) {
	m.LastInput = in
	if m.SearchErr != nil {
		return nil, 0, m.SearchErr
	}
	return m.SearchRows, m.SearchTotal, nil
}

// Reset 清空已记录的调用数据（供同一 mock 在多次调用间复用）。
func (m *MockModel) Reset() {
	m.InsertedRows = nil
	m.LastInput = model.SearchInput{}
	m.InsertErr = nil
	m.SearchErr = nil
	m.SearchRows = nil
	m.SearchTotal = 0
}

type fakeResult struct{ affected int64 }

func (f fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (f fakeResult) RowsAffected() (int64, error) { return f.affected, nil }

// ReadyStub 可控的就绪检查，实现 biz.Readiness。
type ReadyStub struct{ Ready bool }

// IsReady 返回预设的就绪状态。
func (s *ReadyStub) IsReady() bool { return s.Ready }
