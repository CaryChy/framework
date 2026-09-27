package biz

import (
	"context"
	"fmt"
	"testing"

	"cari.com.cn/framework/auditlog/model"
	"cari.com.cn/framework/auditlog/server/internal/testutil"
)

// BenchmarkCreate_Single 单条写入性能（含 UUID 生成 + 结构体拷贝）。
func BenchmarkCreate_Single(b *testing.B) {
	m := &testutil.MockModel{}
	biz := mustNewBiz(true, m)
	ctx := context.Background()
	logs := []CreateLog{validLog()}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := biz.Create(ctx, logs); err != nil {
			b.Fatal(err)
		}
		m.InsertedRows = nil // 避免基准运行期切片无限增长
	}
}

// BenchmarkCreate_Batch 不同批量规模的写入性能。
func BenchmarkCreate_Batch(b *testing.B) {
	sizes := []int{1, 10, 100, 500}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("batch_%d", n), func(b *testing.B) {
			m := &testutil.MockModel{}
			biz := mustNewBiz(true, m)
			ctx := context.Background()
			logs := make([]CreateLog, n)
			for i := range logs {
				logs[i] = validLog()
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := biz.Create(ctx, logs); err != nil {
					b.Fatal(err)
				}
				m.InsertedRows = nil
			}
		})
	}
}

// BenchmarkSearch 查询路径性能（不含 DB，仅参数处理与结构体转换）。
func BenchmarkSearch(b *testing.B) {
	rows := make([]*model.AuditLog, 20)
	for i := range rows {
		rows[i] = &model.AuditLog{Id: "id", ServiceName: "auth"}
	}
	m := &testutil.MockModel{SearchTotal: 10000, SearchRows: rows}
	biz := mustNewBiz(true, m)
	ctx := context.Background()
	filter := SearchFilter{Page: 1, PageSize: 20}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := biz.Search(ctx, filter); err != nil {
			b.Fatal(err)
		}
	}
}
