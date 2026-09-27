package biz

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"cari.com.cn/framework/auditlog/model"
	"cari.com.cn/framework/auditlog/server/internal/testutil"
)

func TestCreate_NotReady(t *testing.T) {
	b := mustNewBiz(false, &testutil.MockModel{})
	_, err := b.Create(context.Background(), []CreateLog{validLog()})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("期望 ErrUnavailable，实际 %v", err)
	}
}

func TestCreate_Empty(t *testing.T) {
	b := mustNewBiz(true, &testutil.MockModel{})
	_, err := b.Create(context.Background(), nil)
	if !errors.Is(err, ErrEmptyLogs) {
		t.Fatalf("期望 ErrEmptyLogs，实际 %v", err)
	}
}

func TestCreate_EmptyServiceName(t *testing.T) {
	b := mustNewBiz(true, &testutil.MockModel{})
	_, err := b.Create(context.Background(), []CreateLog{{}})
	if !errors.Is(err, ErrEmptySvcName) {
		t.Fatalf("期望 ErrEmptySvcName，实际 %v", err)
	}
}

func TestCreate_BatchTooLarge(t *testing.T) {
	logs := make([]CreateLog, maxBatchSize+1)
	for i := range logs {
		logs[i] = validLog()
	}
	b := mustNewBiz(true, &testutil.MockModel{})
	_, err := b.Create(context.Background(), logs)
	if !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("期望 ErrBatchTooLarge，实际 %v", err)
	}
}

func TestCreate_AutoGenerateIDs(t *testing.T) {
	m := &testutil.MockModel{}
	b := mustNewBiz(true, m)
	ids, err := b.Create(context.Background(), []CreateLog{validLog(), validLog()})
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("期望 2 个 ID，实际 %d", len(ids))
	}
	if ids[0] == ids[1] {
		t.Fatal("两个自动生成的 ID 不应相同")
	}
	// UUIDv7 长度 36
	if len(ids[0]) != 36 {
		t.Fatalf("ID 长度应为 36，实际 %d", len(ids[0]))
	}
	// 确认落库数据 ID 与返回一致
	if m.InsertedRows[0].Id != ids[0] || m.InsertedRows[1].Id != ids[1] {
		t.Fatal("落库 ID 与返回 ID 不一致")
	}
}

func TestCreate_KeepProvidedID(t *testing.T) {
	m := &testutil.MockModel{}
	b := mustNewBiz(true, m)
	logs := []CreateLog{{Id: "custom-id-1", ServiceName: "auth"}}
	ids, err := b.Create(context.Background(), logs)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if ids[0] != "custom-id-1" {
		t.Fatalf("期望保留自定义 ID，实际 %s", ids[0])
	}
}

func TestCreate_NullStringConversion(t *testing.T) {
	m := &testutil.MockModel{}
	b := mustNewBiz(true, m)
	_, err := b.Create(context.Background(), []CreateLog{{
		ServiceName:  "auth",
		RequestBody:  `{"k":"v"}`,
		ResponseBody: "",
		Metadata:     "",
	}})
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	row := m.InsertedRows[0]
	if !row.RequestBody.Valid {
		t.Error("RequestBody 应为 Valid")
	}
	if row.ResponseBody.Valid {
		t.Error("ResponseBody 空串应为 Invalid（NULL）")
	}
	if row.Metadata.Valid {
		t.Error("Metadata 空串应为 Invalid（NULL）")
	}
}

func TestCreate_DBError(t *testing.T) {
	b := mustNewBiz(true, &testutil.MockModel{InsertErr: errDB})
	_, err := b.Create(context.Background(), []CreateLog{validLog()})
	if !errors.Is(err, ErrWriteFailed) {
		t.Fatalf("期望 ErrWriteFailed，实际 %v", err)
	}
}

// ---------- Search ----------

func TestSearch_NotReady(t *testing.T) {
	b := mustNewBiz(false, &testutil.MockModel{})
	_, _, err := b.Search(context.Background(), SearchFilter{})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("期望 ErrUnavailable，实际 %v", err)
	}
}

func TestSearch_DefaultPage(t *testing.T) {
	m := &testutil.MockModel{SearchTotal: 100}
	b := mustNewBiz(true, m)
	rows, total, err := b.Search(context.Background(), SearchFilter{})
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	_ = rows
	if total != 100 {
		t.Fatalf("期望 total=100，实际 %d", total)
	}
}

func TestSearch_PageTooDeep(t *testing.T) {
	b := mustNewBiz(true, &testutil.MockModel{})
	// page=500, page_size=200 → offset=99800 > maxPageOffset
	_, _, err := b.Search(context.Background(), SearchFilter{Page: 500, PageSize: 200})
	if !errors.Is(err, ErrPageTooDeep) {
		t.Fatalf("期望 ErrPageTooDeep，实际 %v", err)
	}
}

func TestSearch_MaxPageSizeCap(t *testing.T) {
	m := &testutil.MockModel{SearchTotal: 1000}
	b := mustNewBiz(true, m)
	// page_size=500 应被截断到 maxPageSize=200，page=1 时 offset=0，不触发深度限制
	rows, _, err := b.Search(context.Background(), SearchFilter{Page: 1, PageSize: 500})
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	_ = rows
}

func TestSearch_DBError(t *testing.T) {
	b := mustNewBiz(true, &testutil.MockModel{SearchErr: errDB})
	_, _, err := b.Search(context.Background(), SearchFilter{})
	if !errors.Is(err, ErrQueryFailed) {
		t.Fatalf("期望 ErrQueryFailed，实际 %v", err)
	}
}

func TestUnixMilliToTime(t *testing.T) {
	if !unixMilliToTime(0).IsZero() {
		t.Error("0 应返回零值 time")
	}
	ts := unixMilliToTime(1720000000000)
	if ts.Year() != 2024 {
		t.Errorf("时间转换错误: %v", ts)
	}
}

// TestKindOf 守护唯一的错误分类规则：HTTP 状态码与 gRPC code 均由该分类翻译而来。
func TestKindOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"unavailable", ErrUnavailable, KindUnavailable},
		{"empty_logs", ErrEmptyLogs, KindInvalidArgument},
		{"empty_svc", ErrEmptySvcName, KindInvalidArgument},
		{"batch_too_large", ErrBatchTooLarge, KindInvalidArgument},
		{"duplicate_id", ErrDuplicateID, KindInvalidArgument},
		{"page_too_deep", ErrPageTooDeep, KindInvalidArgument},
		{"write_failed", ErrWriteFailed, KindInternal},
		{"query_failed", ErrQueryFailed, KindInternal},
		{"unknown", errors.New("boom"), KindInternal},
		{"nil", nil, KindInternal},
		{"wrapped", errors.Join(ErrEmptyLogs, errors.New("extra")), KindInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := KindOf(tt.err); got != tt.want {
				t.Errorf("KindOf(%v) = %d，期望 %d", tt.err, got, tt.want)
			}
		})
	}
}

// TestSpecOf 守护错误定义表：error 标识（auditlog.xxx）与 retryable 必须与错误定义表一致，
// HTTP 与 gRPC 两端的稳定错误标识均派生自该表。
func TestSpecOf(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantID        string
		wantRetryable bool
	}{
		{"unavailable", ErrUnavailable, "auditlog.unavailable", true},
		{"empty_logs", ErrEmptyLogs, "auditlog.empty_logs", false},
		{"empty_svc", ErrEmptySvcName, "auditlog.empty_service_name", false},
		{"batch_too_large", ErrBatchTooLarge, "auditlog.batch_too_large", false},
		{"duplicate_id", ErrDuplicateID, "auditlog.duplicate_id", false},
		{"page_too_deep", ErrPageTooDeep, "auditlog.page_too_deep", false},
		{"write_failed", ErrWriteFailed, "auditlog.write_failed", false},
		{"query_failed", ErrQueryFailed, "auditlog.query_failed", false},
		{"unknown", errors.New("boom"), "auditlog.internal_error", false},
		{"nil", nil, "auditlog.internal_error", false},
		{"wrapped", errors.Join(ErrEmptyLogs, errors.New("extra")), "auditlog.empty_logs", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := SpecOf(tt.err)
			if spec.ID != tt.wantID {
				t.Errorf("SpecOf(%v).ID = %q，期望 %q", tt.err, spec.ID, tt.wantID)
			}
			if spec.Retryable != tt.wantRetryable {
				t.Errorf("SpecOf(%v).Retryable = %v，期望 %v", tt.err, spec.Retryable, tt.wantRetryable)
			}
		})
	}
}

// TestToView 守护唯一的输出映射规则：NULL 解包为 ""、时间序列化为 Unix 毫秒，
// gRPC/HTTP 两端响应均派生自该视图，不允许各自再实现一遍。
func TestToView(t *testing.T) {
	created := time.Date(2024, 7, 3, 12, 0, 0, 0, time.UTC)
	row := &model.AuditLog{
		Id:           "id-1",
		ServiceName:  "auth",
		StatusCode:   201,
		RequestBody:  sql.NullString{String: "body", Valid: true},
		ResponseBody: sql.NullString{}, // NULL 必须解包为空串
		Metadata:     sql.NullString{String: "", Valid: false},
		ErrorMessage: "",
		DurationMs:   42,
		CreatedAt:    created,
	}
	v := toView(row)
	if v.Id != "id-1" || v.ServiceName != "auth" {
		t.Errorf("基础字段拷贝错误: %+v", v)
	}
	if v.StatusCode != 201 {
		t.Errorf("StatusCode 期望 201，实际 %d", v.StatusCode)
	}
	if v.RequestBody != "body" {
		t.Errorf("RequestBody 期望 body，实际 %q", v.RequestBody)
	}
	if v.ResponseBody != "" {
		t.Errorf("NULL ResponseBody 应解包为空串，实际 %q", v.ResponseBody)
	}
	if v.Metadata != "" {
		t.Errorf("NULL Metadata 应解包为空串，实际 %q", v.Metadata)
	}
	if v.CreatedAtMs != created.UnixMilli() {
		t.Errorf("CreatedAtMs 期望 %d，实际 %d", created.UnixMilli(), v.CreatedAtMs)
	}
}

// TestCreate_DuplicateID 调用方自带重复 id 触发 MySQL 主键冲突（Error 1062）时，
// 应映射为客户端输入错误 ErrDuplicateID（400），而非 500 写入失败。
func TestCreate_DuplicateID(t *testing.T) {
	dupErr := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'id-1' for key 'PRIMARY'"}
	b := mustNewBiz(true, &testutil.MockModel{InsertErr: dupErr})
	_, err := b.Create(context.Background(), []CreateLog{validLog()})
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("期望 ErrDuplicateID，实际 %v", err)
	}
	if got := KindOf(err); got != KindInvalidArgument {
		t.Fatalf("duplicate_id 应为客户端输入错误类别，实际 %d", got)
	}
}

// TestIsDuplicateEntry 非 1062 的 MySQL 错误与普通错误不应误判为主键冲突。
func TestIsDuplicateEntry(t *testing.T) {
	if isDuplicateEntry(errors.New("boom")) {
		t.Fatal("普通错误不应判定为主键冲突")
	}
	if isDuplicateEntry(&mysql.MySQLError{Number: 1213, Message: "Deadlock"}) {
		t.Fatal("死锁错误（1213）不应判定为主键冲突")
	}
}

// TestMessageOf 对外文案：哨兵错误透传安全文案，未知错误脱敏为 InternalMessage。
func TestMessageOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"sentinel", ErrEmptyLogs, ErrEmptyLogs.Error()},
		{"wrapped_sentinel", errors.Join(ErrEmptyLogs, errors.New("extra")), ErrEmptyLogs.Error()},
		{"unknown", errors.New("Error 1062: Duplicate entry 'x' for key 'PRIMARY'"), InternalMessage},
		{"nil", nil, InternalMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MessageOf(tt.err); got != tt.want {
				t.Errorf("MessageOf = %q，期望 %q", got, tt.want)
			}
		})
	}
}
