# auditlog 错误返回对齐《统一微服务 API 响应规范》

## Context

按 `docs/# 统一微服务 API 响应规范（HTTP 与 gRPC）.md` 改造 auditlog 所有对外错误/响应出口：
- HTTP 统一包裹 `{"error","message","data","ts"}`，成功 `error="success"`，错误为 `auditlog.xxx`
- gRPC 错误在 `status.details` 携带 `errdetails.ErrorInfo{Reason: service.error, Domain: auditlog}`，成功加 trailing metadata `x-response-ts`
- admin 健康端口三接口同样包裹（kubelet 只看状态码，包裹不破坏探针语义）

**关键设计决策**：
- gRPC details 载体选 `google.golang.org/genproto/googleapis/rpc/errdetails.ErrorInfo`（文档 §4.2 明确允许），**不改 proto、不动 goctl 生成物**
- envelope 构造收敛在新包 `server/internal/resp`，handler/admin/middleware 三处共用
- 错误定义表（service.error → Kind/Retryable）收敛在 biz 层，成为唯一事实源，`KindOf`/`HttpStatusOf`/`toGrpcErr` 全部由表派生，消除双端漂移

## auditlog 服务错误定义表（写入 biz/errors.go 注释）

| error | http | grpc | retryable | 来源 |
|---|---|---|---|---|
| auditlog.unavailable | 503 | UNAVAILABLE | true | ErrUnavailable |
| auditlog.empty_logs | 400 | INVALID_ARGUMENT | false | ErrEmptyLogs |
| auditlog.empty_service_name | 400 | INVALID_ARGUMENT | false | ErrEmptySvcName |
| auditlog.batch_too_large | 400 | INVALID_ARGUMENT | false | ErrBatchTooLarge |
| auditlog.page_too_deep | 400 | INVALID_ARGUMENT | false | ErrPageTooDeep |
| auditlog.write_failed | 500 | INTERNAL | false | ErrWriteFailed |
| auditlog.query_failed | 500 | INTERNAL | false | ErrQueryFailed |
| auditlog.internal_error | 500 | INTERNAL | false | 未知错误兜底 |
| auditlog.unauthorized | 401 | UNAUTHENTICATED | false | admin /status 鉴权 |
| auditlog.too_many_requests | 429 | RESOURCE_EXHAUSTED | true | 限流中间件 |
| auditlog.starting / auditlog.not_ready / auditlog.stopping | 503 | UNAVAILABLE | true | /readyz 503 三态 |

## 改动内容

### 1. biz/errors.go：错误规格表（唯一事实源）

```go
type Spec struct {
    ID        string    // "auditlog.xxx"
    Kind      ErrorKind
    Retryable bool
}
func SpecOf(err error) Spec // errors.Is 逐项匹配哨兵；未知 → auditlog.internal_error
```
- `KindOf` 保留为导出 API，实现改为 `SpecOf(err).Kind`（TestKindOf 无需改动）
- 文件顶部注释附上面的错误定义表（服务错误定义源头）

### 2. 新包 server/internal/resp：HTTP envelope

```go
type Body struct {
    Error   string `json:"error"`
    Message string `json:"message"`
    Data    any    `json:"data"`
    TS      int64  `json:"ts"` // Unix 毫秒
}
func OK(w, data)                  // 200 error=success message=""
func Created(w, data)             // 201 error=success
func Err(w, status, id, msg)      // 通用错误，data={}
func FromBizErr(w, err)           // 查 SpecOf→Kind→HTTP 状态码；message=err.Error()
```
统一 `Content-Type: application/json; charset=utf-8`。

### 3. HTTP handler（goctl 生成骨架内手写区）

- [createauditlogsHandler.go](auditlog/server/internal/handler/auditlog/createauditlogsHandler.go)：成功 `resp.Created(resp, data=CreateAuditLogsResponse)`；错误 `resp.FromBizErr`
- [searchauditlogsHandler.go](auditlog/server/internal/handler/auditlog/searchauditlogsHandler.go)：成功 `resp.OK`；错误同上
- [httplogic/errors.go](auditlog/server/internal/logic/httplogic/errors.go) `HttpStatusOf` 改为查 `biz.SpecOf(err).Kind`（逻辑不变，来源收敛）

### 4. gRPC（rpclogic）

- [rpclogic/errors.go](auditlog/server/internal/logic/rpclogic/errors.go) `toGrpcErr`：`status.New(code).WithDetails(&errdetails.ErrorInfo{Reason: spec.ID, Domain: "auditlog", Metadata: {"retryable": "true"/"false", "ts": "..."}})`，`Message` 仍为 err.Error()
- 成功 trailing metadata：新增 10 行 unary 拦截器（`grpc.SetTrailer(ctx, metadata.Pairs("x-response-ts", ...))`），在 [grpc.go](auditlog/server/grpc.go) `AddUnaryInterceptors` 挂载
- 依赖：`go get google.golang.org/genproto/googleapis/rpc/errdetails`

### 5. admin 健康端口（用户重点关注）

[admin.go](auditlog/server/internal/admin/admin.go) 三端点改用 resp 包，状态码不变（kubelet 兼容）：
- `/healthz` 200 → `{error:"success", data:{status:"ok"}}`
- `/readyz` 200 → `{error:"success", data:{status:"ready", components}}`；503 → error 按 phase 映射 `auditlog.starting`/`auditlog.not_ready`/`auditlog.stopping`，`data:{status, components}`
- `/status` 200 → `{error:"success", data:Snapshot}`；401 → `{error:"auditlog.unauthorized"}` + 响应头 `WWW-Authenticate: Bearer`
- 原 `writeJSON` 删除，统一走 resp 包

### 6. 限流中间件

[middleware/ratelimit.go](auditlog/server/internal/middleware/ratelimit.go) 429 响应体改 `{error:"auditlog.too_many_requests", message, data:{}, ts}`，保留 `Retry-After: 1`

### 7. 测试同步（不删用例，只改断言结构）

- [admin_test.go](auditlog/server/internal/admin/admin_test.go)：三端点断言 `error` 字段与 `data` 嵌套
- [auditlog_handler_test.go](auditlog/server/internal/handler/auditlog/auditlog_handler_test.go)：成功/失败断言改 envelope
- [ratelimit_test.go](auditlog/server/internal/middleware/ratelimit_test.go)：429 body 断言
- [rpclogic_test.go](auditlog/server/internal/logic/rpclogic/rpclogic_test.go)：新增 details（ErrorInfo.Reason）断言
- biz：新增 `TestSpecOf`（每个哨兵错误 + 未知错误兜底）
- [auditlogserver_test.go](auditlog/server/internal/server/auditlogserver_test.go)：错误 message 断言不变；可补 details 断言

## 不做的事

- 不改 proto / 不重新生成 rpc 代码（types.go、rpc/auditlog 原样）
- 不改 biz 业务逻辑、ErrorKind 判定语义
- 不动 /healthz /readyz 状态码语义（k8s 兼容）
- 不创建新的文档文件（错误定义表以代码注释为准）

## 验证

1. `go build ./... && go vet ./... && gofmt -l auditlog/` 全绿
2. `go test -race -count=1 ./auditlog/...` 全部通过
3. 冒烟（etcd 可用，MySQL 假凭据）：`/healthz` 200+success、`/readyz` 503+auditlog.starting、`/status` 401+auditlog.unauthorized；带合法 client 证书调 `POST /api/v1/auditlogs`（写一条）与 `GET /api/v1/auditlogs` 验证 envelope；gRPC 侧触发错误验证 `status.details` 中 `ErrorInfo.Reason`
