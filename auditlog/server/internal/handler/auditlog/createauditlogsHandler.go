package auditlog

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"

	"cari.com.cn/framework/auditlog/server/internal/core"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/auditlog/server/internal/types"
)

// CreateAuditLogsHandler 批量写入审计日志：POST /api/v1/auditlogs。
// HTTP 与 gRPC 共享 core 层实现，进程内直调（不再经本机 gRPC 回环）。
func CreateAuditLogsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.CreateAuditLogsRequest
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		resp, err := core.New(svcCtx).CreateAuditLogsHTTP(r.Context(), &req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
