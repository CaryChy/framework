package auditlog

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"

	httplogic "cari.com.cn/framework/auditlog/server/internal/logic/httplogic"
	"cari.com.cn/framework/auditlog/server/internal/resp"
	"cari.com.cn/framework/auditlog/server/internal/svc"
	"cari.com.cn/framework/auditlog/server/internal/types"
)

// SearchAuditLogsHandler 分页查询审计日志：GET /api/v1/auditlogs。
// 错误按统一响应规范包裹（auditlog.xxx / 503/400/500）。
func SearchAuditLogsHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.SearchAuditLogsRequest
		if err := httpx.Parse(r, &req); err != nil {
			resp.Err(w, http.StatusBadRequest, "auditlog.invalid_parameter", err.Error())
			return
		}

		l := httplogic.NewSearchAuditLogsLogic(r.Context(), svcCtx)
		data, err := l.SearchAuditLogs(&req)
		if err != nil {
			resp.FromBizErr(w, err)
			return
		}
		resp.OK(w, data)
	}
}
