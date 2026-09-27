# auditlog 可读性整体重构（稳健清理）

## Context

用户反馈代码"晦涩难懂"，痛点全选：注释太密太长、样板拷贝太多、调用链层级太多、大文件混职责。
重构策略经确认采用**稳健清理**：目录结构与调用链基本不动、对外行为零变化、goctl 兼容保留（logic/handler/types 目录约定不动）、全部现有测试保持通过。

核心思路：提升**信噪比**——注释从"论述文"变"提示语"，大文件按职责拆小，装配逻辑一眼看懂。

**不做的事**（明确出界）：
- 不砍 convert 包 / 不合并 logic 层（调用链结构不变；"层级太多"痛点靠每层薄到一眼看穿来缓解）
- 不合并/删除任何同构 DTO（反射或代码生成反而更晦涩）
- 不改任何配置字段、etcd key、端口、路由、日志语义、错误语义

## 改动内容

### 1. 注释瘦身（全局，收益最大）

规则：
- 包注释 ≤3 行：只说"这是什么 + 关键约定"
- 导出符号 godoc 一行；函数内"为什么"注释 ≤2 行
- **删除**：历史决策叙述（"上一轮重构…"式）、架构论述段、解释显而易见行为的注释
- **保留**：安全/语义警告（mTLS fail-fast、NULL 语义、深度分页防护、敏感数据脱敏原因）

热点文件：`server/server.go`（ETCD_CLIENT_DEBUG 6 行→2 行、DisableStmtLog 5 行→2 行）、
`server/internal/svc/servicecontext.go`（顶部 6 行约定→3 行）、
`biz/auditlog_biz.go`、`convert/convert.go`（顶部 3 行论述→1 行）、`health/health.go`、`model/auditlog_model.go`

### 2. biz/auditlog_biz.go（313 行）拆为 3 个文件

| 新文件 | 内容 | 约行数 |
|---|---|---|
| `biz/errors.go` | 哨兵错误 + ErrorKind + KindOf | ~60 |
| `biz/dto.go` | CreateLog + SearchFilter + AuditLogView（协议无关领域模型） | ~70 |
| `biz/auditlog.go` | AuditLogBiz + Create + Search + toView/nullString/unixMilliToTime | ~180 |

另外把 Create 中 19 字段的 model 行构造提取为 `buildRow(item, logID, now) *model.AuditLog`，
Create 主体收敛为：校验 → 逐条生成 ID → 构造 → 批量写 → 日志。

### 3. server/server.go（291 行）拆为 4 个文件

| 新文件 | 内容 |
|---|---|
| `server/main.go` | main：配置→日志→依赖→装配→ServiceGroup，目标 ≤80 行 |
| `server/defaults.go` | defaultRpcMiddlewares + defaultRestMiddlewares + mtlsPolicy |
| `server/grpc.go` | setupGRPC |
| `server/http.go` | setupHTTP |

### 4. svc/servicecontext.go（179 行）拆为 2 个文件

| 新文件 | 内容 |
|---|---|
| `svc/servicecontext.go` | ServiceContext + NewServiceContext + applyMysqlPool（~110 行） |
| `svc/etcd.go` | etcdClientConfig + etcdTLSConfig + 相关常量（~70 行） |

### 5. 其余文件仅注释压缩，结构不动

`config/config.go`、`convert/convert.go`、`health/health.go`、`admin/admin.go`、`common/tls/*.go`、`common/logbridge/logbridge.go`、`model/auditlog_model.go`、`middleware/ratelimit.go`、`testutil/testutil.go`、logic 层各文件。

### 6. 测试文件

- 逻辑零改动，`biz` 包测试文件**无需改动**（同包内拆文件不影响）
- 其余测试仅按需压缩测试内部的冗长注释（不删用例、不改断言）

## 关键约束

- goctl 兼容假设：`logic/`、`handler/`、`types/`、`server/internal/server/` 目录结构与文件名保持 goctl 约定，仅允许手写内容注释微调
- 项目既有约定不破坏：gRPC/HTTP 共用 biz、错误分类唯一收敛于 `biz.KindOf`、SPIFFE 身份校验逻辑（`common/tls/identity.go`）只注释不动逻辑

## 验证

1. `go build ./... && go vet ./... && gofmt -l .` 全绿
2. `go test -race -count=1 ./...` 全部通过（含 benchmark 编译）
3. 冒烟：`go run server.go` 启动（本地 etcd/MySQL 可用时），确认启动日志顺序与之前一致、`/healthz` `/readyz` `/status` 响应结构不变
4. 人工 diff 复核：确认无任何导出符号改名/删除、无行为性代码变更（仅移动+注释）
