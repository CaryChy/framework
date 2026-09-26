package auditlog

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"

	"cari.com.cn/framework/auditlog/server/internal/logic/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/auditlog/server/internal/types"
)

// SearchAuditLogsHandler 分页查询审计日志：GET /api/v1/auditlogs。
func SearchAuditLogsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.SearchAuditLogsRequest
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := auditlog.NewSearchAuditLogsLogic(r.Context(), svcCtx)
		resp, err := l.SearchAuditLogs(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
