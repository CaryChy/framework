package auditlog

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"

	"cari.com.cn/framework/auditlog/server/internal/logic/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/auditlog/server/internal/types"
)

// CreateAuditLogsHandler 批量写入审计日志：POST /api/v1/auditlogs。
func CreateAuditLogsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.CreateAuditLogsRequest
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := auditlog.NewCreateAuditLogsLogic(r.Context(), svcCtx)
		resp, err := l.CreateAuditLogs(&req)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}
	}
}
