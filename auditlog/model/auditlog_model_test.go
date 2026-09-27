package model

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

// mockConn 仅实现 InsertBatch/Search 用到的 Session 方法，其余方法继承 nil 接口，
// 调用到则 panic（本测试不会触发）。
type mockConn struct {
	sqlx.SqlConn
	execQuery string
	execArgs  []any
	execErr   error

	rowQuery    string
	rowArgs     []any
	rowErr      error
	rowScan     any // QueryRowCtx 扫描到的目标（用于计数）
	rowsQuery   string
	rowsArgs    []any
	rowsErr     error
	rowsResults []*AuditLog // QueryRowsCtx 模拟返回的列表
}

func (m *mockConn) ExecCtx(_ context.Context, query string, args ...any) (sql.Result, error) {
	m.execQuery = query
	m.execArgs = args
	if m.execErr != nil {
		return nil, m.execErr
	}
	return nopResult{}, nil
}

func (m *mockConn) QueryRowCtx(_ context.Context, dest any, query string, args ...any) error {
	m.rowQuery = query
	m.rowArgs = args
	if m.rowErr != nil {
		return m.rowErr
	}
	// 将扫描目标视为 *int64（COUNT 结果），返回预置计数。
	if p, ok := dest.(*int64); ok {
		if cnt, ok2 := m.rowScan.(int64); ok2 {
			*p = cnt
		}
	}
	return nil
}

func (m *mockConn) QueryRowsCtx(_ context.Context, dest any, query string, args ...any) error {
	m.rowsQuery = query
	m.rowsArgs = args
	if m.rowsErr != nil {
		return m.rowsErr
	}
	// 将 dest 视为 *[]*AuditLog
	if p, ok := dest.(*[]*AuditLog); ok {
		*p = m.rowsResults
	}
	return nil
}

type nopResult struct{}

func (nopResult) LastInsertId() (int64, error) { return 0, nil }
func (nopResult) RowsAffected() (int64, error) { return 0, nil }

// TestBuildSearchWhere_Empty 无过滤条件时返回空 WHERE 子句。
func TestBuildSearchWhere_Empty(t *testing.T) {
	where, args := buildSearchWhere(SearchInput{})
	if where != "" {
		t.Fatalf("期望空 WHERE，实际 %q", where)
	}
	if len(args) != 0 {
		t.Fatalf("期望无参数，实际 %v", args)
	}
}

// TestBuildSearchWhere_AllFields 所有过滤条件同时生效，验证子句顺序与参数顺序一致。
func TestBuildSearchWhere_AllFields(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	in := SearchInput{
		ServiceName:  "auth",
		ActorId:      "u1",
		Action:       "login",
		ResourceType: "user",
		ResourceId:   "r1",
		TraceId:      "t1",
		StartTime:    start,
		EndTime:      end,
	}
	where, args := buildSearchWhere(in)
	if !strings.HasPrefix(where, " WHERE ") {
		t.Fatalf("WHERE 子句前缀错误: %q", where)
	}
	// 8 个条件 → 8 个占位符
	if strings.Count(where, "?") != 8 {
		t.Fatalf("期望 8 个占位符，实际 %d: %q", strings.Count(where, "?"), where)
	}
	// 参数顺序与子句顺序严格对应
	want := []any{"auth", "u1", "login", "user", "r1", "t1", start, end}
	if len(args) != len(want) {
		t.Fatalf("参数数量 %d != %d", len(args), len(want))
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("参数[%d] = %v，期望 %v", i, args[i], want[i])
		}
	}
}

// TestBuildSearchWhere_Partial 仅部分条件，验证其余不出现。
func TestBuildSearchWhere_Partial(t *testing.T) {
	where, args := buildSearchWhere(SearchInput{ServiceName: "auth", Action: "login"})
	if strings.Contains(where, "actor_id") || strings.Contains(where, "trace_id") {
		t.Fatalf("不应出现未设置字段的子句: %q", where)
	}
	if strings.Count(where, "?") != 2 {
		t.Fatalf("期望 2 个占位符: %q", where)
	}
	if len(args) != 2 || args[0] != "auth" || args[1] != "login" {
		t.Fatalf("参数错误: %v", args)
	}
}

// TestInsertBatch_PlaceholderCount 占位符数量必须与列数一致，防止增删列后静默错位。
func TestInsertBatch_PlaceholderCount(t *testing.T) {
	mc := &mockConn{}
	mdl := NewAuditLogModel(mc)
	rows := []*AuditLog{{Id: "a", ServiceName: "auth"}}
	if _, err := mdl.InsertBatch(context.Background(), rows); err != nil {
		t.Fatalf("InsertBatch 失败: %v", err)
	}
	if !strings.HasPrefix(mc.execQuery, "INSERT INTO `audit_log`") {
		t.Fatalf("SQL 前缀错误: %q", mc.execQuery)
	}
	// 单行占位符片段应为 (?,?,...,?)，数量等于列数
	placeholders := strings.Count(mc.execQuery, "?")
	want := len(auditLogColumnsList)
	if placeholders != want {
		t.Fatalf("占位符数量 %d != 列数 %d，SQL: %q", placeholders, want, mc.execQuery)
	}
}

// TestInsertBatch_MultiRows 多行时占位符组数与行数一致。
func TestInsertBatch_MultiRows(t *testing.T) {
	mc := &mockConn{}
	mdl := NewAuditLogModel(mc)
	n := 3
	rows := make([]*AuditLog, n)
	for i := range rows {
		rows[i] = &AuditLog{Id: "id", ServiceName: "auth"}
	}
	if _, err := mdl.InsertBatch(context.Background(), rows); err != nil {
		t.Fatalf("InsertBatch 失败: %v", err)
	}
	placeholders := strings.Count(mc.execQuery, "?")
	if placeholders != n*len(auditLogColumnsList) {
		t.Fatalf("多行占位符数量错误: %d", placeholders)
	}
	if len(mc.execArgs) != n*len(auditLogColumnsList) {
		t.Fatalf("参数数量错误: %d", len(mc.execArgs))
	}
}

// TestInsertBatch_Error 透传底层错误。
func TestInsertBatch_Error(t *testing.T) {
	want := errors.New("boom")
	mc := &mockConn{execErr: want}
	mdl := NewAuditLogModel(mc)
	_, err := mdl.InsertBatch(context.Background(), []*AuditLog{{Id: "a", ServiceName: "s"}})
	if !errors.Is(err, want) {
		t.Fatalf("期望透传错误 %v，实际 %v", want, err)
	}
}

// TestSearch_CountThenList 验证 COUNT 与 SELECT 两条 SQL 的生成与分页参数透传。
func TestSearch_CountThenList(t *testing.T) {
	row := &AuditLog{Id: "x", ServiceName: "auth"}
	mc := &mockConn{
		rowScan:     int64(1),
		rowsResults: []*AuditLog{row},
	}
	mdl := NewAuditLogModel(mc)
	list, total, err := mdl.Search(context.Background(), SearchInput{
		ServiceName: "auth",
		Page:        2,
		PageSize:    10,
	})
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	if total != 1 {
		t.Fatalf("total 期望 1，实际 %d", total)
	}
	if len(list) != 1 || list[0].Id != "x" {
		t.Fatalf("列表错误: %+v", list)
	}
	if !strings.HasPrefix(mc.rowQuery, "SELECT COUNT(1) FROM `audit_log`") {
		t.Fatalf("COUNT SQL 错误: %q", mc.rowQuery)
	}
	if !strings.HasPrefix(mc.rowsQuery, "SELECT `id`,") {
		t.Fatalf("LIST SQL 前缀错误: %q", mc.rowsQuery)
	}
	if !strings.Contains(mc.rowsQuery, "LIMIT ?, ?") {
		t.Fatalf("LIST SQL 缺 LIMIT: %q", mc.rowsQuery)
	}
	// 分页参数：offset=(2-1)*10=10, limit=10
	args := mc.rowsArgs
	if len(args) < 2 {
		t.Fatalf("分页参数缺失: %v", args)
	}
	if args[len(args)-2] != int64(10) || args[len(args)-1] != int64(10) {
		t.Fatalf("分页参数错误: %v", args[len(args)-2:])
	}
}

// TestSearch_EmptyCount total=0 时直接返回，不执行 LIST 查询。
func TestSearch_EmptyCount(t *testing.T) {
	mc := &mockConn{rowScan: int64(0)}
	mdl := NewAuditLogModel(mc)
	list, total, err := mdl.Search(context.Background(), SearchInput{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	if total != 0 || list != nil {
		t.Fatalf("期望空结果，实际 total=%d list=%v", total, list)
	}
	if mc.rowsQuery != "" {
		t.Fatalf("total=0 时不应执行 LIST 查询: %q", mc.rowsQuery)
	}
}
