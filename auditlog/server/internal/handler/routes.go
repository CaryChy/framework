package handler

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest"

	auditloghandler "cari.com.cn/framework/auditlog/server/internal/handler/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/svc"
)

// RegisterHandlers 注册 HTTP 路由，统一挂载 /api/v1 前缀。
func RegisterHandlers(server *rest.Server, serverCtx *svc.ServiceContext) {
	server.AddRoutes(
		[]rest.Route{
			{
				Method:  http.MethodPost,
				Path:    "/auditlogs",
				Handler: auditloghandler.CreateAuditLogsHandler(serverCtx),
			},
			{
				Method:  http.MethodGet,
				Path:    "/auditlogs",
				Handler: auditloghandler.SearchAuditLogsHandler(serverCtx),
			},
		},
		rest.WithPrefix("/api/v1"),
	)
}
