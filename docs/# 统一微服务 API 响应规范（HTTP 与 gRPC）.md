# 统一微服务 API 响应规范（HTTP 与 gRPC）

适用范围：网关、微服务、中间件、前端、客户端。  
目标：统一成功/错误判断，消除重复字段，兼容多服务、多语言、可观测性与错误治理，同时支持 HTTP 与 gRPC 两种协议。

## 1. 核心原则

1. 需要返回响应体的 HTTP API，统一采用本文 JSON 结构。
2. 除明确规定无响应体的状态码，如 `204 No Content`，不得返回空 Body 或非 JSON 内容。
3. `error === "success"` 表示成功，其他值均表示错误。
4. HTTP Status Code 与 gRPC Status Code 表达协议层、基础设施层和通用语义。
5. `error` 表达稳定、机器可读的应用层错误标识。
6. `message` 仅用于日志、调试、排障，不属于稳定协议。
7. `data` 成功时放业务数据，错误时放结构化错误详情。
8. `ts` 为最终响应生成组件的 Unix 毫秒时间戳。
9. 不使用 `code`，避免与 `error` 重复。
10. 不使用 `isSuccessful`，`error === "success"` 已足够判断成功。
11. HTTP 与 gRPC 对同一个业务错误使用相同的 `service.error` 标识。

## 2. error 命名与定义

### 2.1 格式

```text
service.error
```

`service` 必须是实际产生错误的服务或组件名，例如：

```text
gateway
user
order
payment
auditlog
```

示例：

```text
gateway.invalid_token
gateway.too_many_requests
user.email_already_exists
user.not_found
order.not_found
payment.insufficient_balance
auditlog.invalid_token
```

错误标识应能明确表达“哪个服务出现了什么错误”。不要使用通用前缀，不要使用模糊的服务名。

### 2.2 保留值

```text
success
```

`success` 是协议级保留值，不属于任何服务的错误码命名空间。

禁止：

```text
user.success
gateway.success
success.xxx
```

### 2.3 要求

- 全小写
- 点号分隔
- 服务名在前，错误名在后
- 全局唯一、稳定不变
- 不得包含动态信息、参数值、用户信息、数据库错误、异常信息或自然语言
- 不得作为程序判断依据的 `message` 替代品

错误：

```text
error: "user 12345 not found"
error: "user.email_already_exists:foo@example.com"
error: "user.invalid_parameter_email"
```

正确：

```json
{
  "error": "user.not_found",
  "data": {
    "user_id": "12345"
  }
}
```

涉及敏感资源时，应结合安全规范决定是否统一返回 `not_found` 或 `access_denied`，避免通过错误响应探测资源是否存在。

### 2.4 服务错误定义

每个服务必须明确定义自己所有可能对外返回的错误，而不是由公司统一维护一份错误清单。

每个错误定义至少包含：

- `error` 标识
- 对应 HTTP 状态码
- 对应 gRPC 状态码
- 是否可重试
- 含义说明

错误定义由各服务自行维护，服务名前缀必须与实际服务或组件一致。网关、文档、客户端可以汇总展示，但错误的定义源头仍在各服务。

示例：`user` 服务错误定义

| error | http_status | grpc_status | retryable | description |
|---|---:|---|---|---|
| `user.not_found` | 404 | `NOT_FOUND` | false | 用户不存在 |
| `user.email_already_exists` | 409 | `ALREADY_EXISTS` | false | 邮箱已存在 |
| `user.invalid_parameter` | 400 | `INVALID_ARGUMENT` | false | 请求参数错误 |
| `user.internal_error` | 500 | `INTERNAL` | false | 用户服务内部错误 |

示例：`gateway` 服务错误定义

| error | http_status | grpc_status | retryable | description |
|---|---:|---|---|---|
| `gateway.invalid_token` | 401 | `UNAUTHENTICATED` | false | 令牌无效 |
| `gateway.too_many_requests` | 429 | `RESOURCE_EXHAUSTED` | true | 请求过多 |
| `gateway.bad_gateway` | 502 | `UNAVAILABLE` | true | 上游异常 |
| `gateway.service_unavailable` | 503 | `UNAVAILABLE` | true | 服务不可用 |
| `gateway.gateway_timeout` | 504 | `DEADLINE_EXCEEDED` | true | 上游超时 |

每个服务必须保证：

- 所有对外返回的 `error` 都已在自身错误定义中列出。
- 不得返回未定义的错误标识。
- 不得使用其他服务的错误前缀。
- 废弃错误必须保留标识并标记废弃，不得直接改名后复用。

## 3. HTTP 响应规范

### 3.1 响应结构

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `error` | string | 是 | 成功固定为 `"success"`；错误为 `service.error` |
| `message` | string | 是 | 非稳定诊断信息，仅供服务端日志、调试、排障 |
| `data` | any | 是 | 成功放业务数据；错误建议放结构化对象 |
| `ts` | int64 | 是 | 最终 HTTP 响应生成组件的 Unix 毫秒时间戳 |

响应头要求：

```http
Content-Type: application/json; charset=utf-8
```

### 3.2 成功响应

一般成功：

```json
{
  "error": "success",
  "message": "",
  "data": {
    "id": 123,
    "name": "张三"
  },
  "ts": 1790486978678
}
```

HTTP 状态码：

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
```

创建成功：

```json
{
  "error": "success",
  "message": "",
  "data": {
    "id": 123
  },
  "ts": 1790486978678
}
```

HTTP 状态码：

```http
HTTP/1.1 201 Created
Content-Type: application/json; charset=utf-8
```

无响应体成功：

```http
HTTP/1.1 204 No Content
```

`204 No Content` 不返回 JSON Body，属于明确例外。

### 3.3 错误响应

```json
{
  "error": "gateway.invalid_token",
  "message": "token is invalid or expired",
  "data": {
    "fields": []
  },
  "ts": 1790486978678
}
```

HTTP 状态码：

```http
HTTP/1.1 401 Unauthorized
Content-Type: application/json; charset=utf-8
```

### 3.4 HTTP Status 与 error 分工

- HTTP Status 表示错误类别。
- `error` 表示具体错误原因。
- 不得使用 `error = "409"`、`error = "HTTP_409"` 这类形式。
- 同一个 HTTP Status 下可以有多个稳定 `error`。

示例：

```text
409
├── user.email_already_exists
├── order.already_exists
└── resource.version_conflict
```

推荐映射：

| 场景 | HTTP | error |
|---|---:|---|
| 成功 | 200 | `success` |
| 创建成功 | 201 | `success` |
| 无响应体成功 | 204 | 无 Body |
| 请求格式/参数错误 | 400 | `user.invalid_parameter` |
| 未认证 | 401 | `gateway.invalid_token` |
| 无权限 | 403 | `user.forbidden` |
| 资源不存在 | 404 | `user.not_found` |
| 请求方法不允许 | 405 | `gateway.method_not_allowed` |
| 请求冲突 | 409 | `user.email_already_exists` |
| 语义校验失败 | 422 | `payment.insufficient_balance` |
| 限流 | 429 | `gateway.too_many_requests` |
| 服务端异常 | 500 | `user.internal_error` |
| 上游异常 | 502 | `gateway.bad_gateway` |
| 服务不可用 | 503 | `gateway.service_unavailable` |
| 上游超时 | 504 | `gateway.gateway_timeout` |

注意：

- `502/503/504` 对网关、监控、运维有不同诊断意义，不要全部合并为同一个 `error`。
- `422` 表示请求格式和语法正确，但服务器无法处理其中语义。不要形成“只要业务错误就是 422”的习惯。
- 标准名称建议使用 `422 Unprocessable Content`，历史名称为 `Unprocessable Entity`。
- 使用 `401 Unauthorized` 时，应根据认证方案返回 `WWW-Authenticate`。
- `429`、`503` 可配合 `Retry-After`。

## 4. gRPC 响应规范

### 4.1 成功响应

gRPC 成功时：

- `status.code = OK`
- 业务数据直接使用方法定义的 Response 消息，不额外包裹 `error/message/data`
- 如需统一返回响应生成时间，可在 trailing metadata 中返回 `x-response-ts`，值为 Unix 毫秒时间戳字符串

示例 metadata：

```text
x-response-ts: 1790486978678
```

gRPC 是强类型协议，成功响应应由 proto 明确定义，不建议为了统一而强制所有 Response 都包含 `data` 字段。

### 4.2 错误响应

gRPC 错误时：

- `status.code` 使用标准 gRPC 状态码，且不能为 `OK`
- `status.message` 放非稳定诊断信息，对应 HTTP 的 `message`
- `status.details` 中携带统一错误详情

统一错误详情建议使用以下 protobuf 结构：

```proto
message ErrorDetail {
  string error = 1;                 // service.error
  google.protobuf.Struct data = 2;  // 结构化错误详情
  int64 ts = 3;                     // 最终响应生成时间 Unix 毫秒
}
```

也可以结合标准 `google.rpc.ErrorInfo`：

- `reason` 放 `service.error`
- `domain` 放服务名
- `metadata` 放字符串化错误详情

无论采用哪种方式，必须保证：

- 每个错误都能取到稳定的 `service.error`
- `message` 不作为客户端程序判断依据
- `data` 为结构化对象
- `ts` 为最终响应生成组件的 Unix 毫秒时间戳

### 4.3 gRPC Status 与 error 分工

- gRPC Status Code 表示错误类别。
- `error` 表示具体错误原因。
- 同一个 gRPC Status Code 下可以有多个稳定 `error`。

推荐映射：

| 场景 | gRPC Status | HTTP | error 示例 |
|---|---|---|---|
| 成功 | `OK` | 200 | `success` |
| 请求格式/参数错误 | `INVALID_ARGUMENT` | 400 | `user.invalid_parameter` |
| 未认证 | `UNAUTHENTICATED` | 401 | `gateway.invalid_token` |
| 无权限 | `PERMISSION_DENIED` | 403 | `user.forbidden` |
| 资源不存在 | `NOT_FOUND` | 404 | `user.not_found` |
| 请求方法不允许 | `UNIMPLEMENTED` | 405/501 | `gateway.method_not_allowed` |
| 请求冲突 | `ALREADY_EXISTS` | 409 | `user.email_already_exists` |
| 语义校验失败 | `FAILED_PRECONDITION` | 422 | `payment.insufficient_balance` |
| 限流 | `RESOURCE_EXHAUSTED` | 429 | `gateway.too_many_requests` |
| 服务端异常 | `INTERNAL` | 500 | `user.internal_error` |
| 上游异常 | `UNAVAILABLE` | 502 | `gateway.bad_gateway` |
| 服务不可用 | `UNAVAILABLE` | 503 | `gateway.service_unavailable` |
| 上游超时 | `DEADLINE_EXCEEDED` | 504 | `gateway.gateway_timeout` |

注意：

- gRPC 没有与 HTTP 502 完全一一对应的状态码，上游异常通常映射为 `UNAVAILABLE`。
- `FAILED_PRECONDITION` 适合表示请求语义正确但当前系统状态不允许执行。
- `RESOURCE_EXHAUSTED` 适合限流、配额耗尽等场景。
- `UNAVAILABLE` 可配合重试和退避策略，但不代表客户端可以无脑重试。

### 4.4 gRPC 错误示例

`status.code`：

```text
UNAUTHENTICATED
```

`status.message`：

```text
token is invalid or expired
```

`status.details`：

```json
{
  "error": "gateway.invalid_token",
  "data": {
    "fields": []
  },
  "ts": 1790486978678
}
```

### 4.5 gRPC 服务错误定义

每个服务必须明确定义自己所有可能对外返回的 gRPC 错误。

每个错误定义至少包含：

- `error` 标识
- gRPC Status Code
- HTTP 状态码
- 是否可重试
- 含义说明

示例：`payment` 服务错误定义

| error | grpc_status | http_status | retryable | description |
|---|---|---|---|---|
| `payment.insufficient_balance` | `FAILED_PRECONDITION` | 422 | false | 余额不足 |
| `payment.order_not_found` | `NOT_FOUND` | 404 | false | 支付单不存在 |
| `payment.duplicate_request` | `ALREADY_EXISTS` | 409 | false | 重复请求 |
| `payment.internal_error` | `INTERNAL` | 500 | false | 支付服务内部错误 |

## 5. 跨协议一致性

1. 同一个业务错误在 HTTP 和 gRPC 中使用相同的 `service.error`。
2. HTTP Status Code 与 gRPC Status Code 按映射表对应。
3. `message` 在两个协议中都是非稳定诊断信息。
4. `data` 在两个协议中都是结构化错误详情。
5. `ts` 在两个协议中都表示最终响应生成组件的 Unix 毫秒时间戳。
6. 成功时，HTTP 使用 `error = "success"`，gRPC 使用 `status.code = OK`。
7. 错误时，HTTP 使用非 `success` 的 `error`，gRPC 使用非 `OK` 的 `status.code` 并在 details 中携带 `service.error`。
8. 网关在 HTTP 与 gRPC 之间转换时，必须保留 `service.error` 语义，不得丢失或改写为通用错误。

## 6. message 定位

`message` 用于服务端日志、开发调试和故障排查，不属于稳定 API 协议。

必须遵守：

- 不得作为客户端程序判断依据
- 不得直接展示给最终用户
- 不保证内容稳定
- 后端可随时调整语言、措辞和详细程度

错误做法：

```javascript
if (response.message === "token expired") {
  // 错误：依赖非稳定字段
}
```

正确做法：

```javascript
if (response.error === "gateway.invalid_token") {
  // 正确：依赖稳定错误标识
}
```

前端多语言必须根据 `error` 映射：

```js
const i18n = {
  "zh-CN": {
    "gateway.invalid_token": "登录凭证无效",
    "user.not_found": "用户不存在",
    "user.internal_error": "服务器内部错误，请稍后重试"
  },
  "en-US": {
    "gateway.invalid_token": "Invalid token",
    "user.not_found": "User not found",
    "user.internal_error": "Internal server error"
  }
};

if (resp.error !== "success") {
  const text = i18n[lang][resp.error] || "未知错误";
  showError(text);
}
```

## 7. data 规范

### 7.1 成功响应

`data` 可以是任意合法 JSON 数据：

```json
{}
```

```json
[]
```

```json
{
  "id": 1
}
```

### 7.2 错误响应

错误响应的 `data` 建议统一为对象：

```json
"data": {
  "fields": [
    {
      "field": "page",
      "reason": "必须大于 0",
      "value": "-1"
    }
  ]
}
```

不要：

```json
"data": "invalid parameter"
```

## 8. ts 规范

`ts` 表示生成最终响应的组件的 Unix 毫秒时间戳。

- HTTP：放在响应 JSON 的 `ts` 字段中。
- gRPC：错误时放在 `ErrorDetail.ts` 中；成功时可通过 trailing metadata `x-response-ts` 返回。
- 经过网关的外部请求，由网关生成。
- 直接访问服务的请求，由服务生成。
- 使用 UTC 时间，不受客户端时区影响。

## 9. 微服务与网关规则

1. 对外格式由网关统一，各服务内部可自行实现。
2. 每个服务必须明确定义自己的所有对外错误返回，错误前缀为服务名，避免不同服务冲突。
3. `error` 前缀必须是实际产生错误的服务或组件名。
4. 下游错误不要无脑透传，尤其是 500、SQL、堆栈、内部 IP。
5. 聚合接口提前定义错误优先级和降级规则。
6. 最外层 `ts` 建议由网关最终生成。
7. 内部 RPC 可使用 RPC 错误模型，gRPC 服务按本文 gRPC 规范返回错误。
8. 网关、中间件按标准 HTTP Code 或 gRPC Status Code 返回。
9. 业务错误使用合适的 4xx 或 gRPC 状态码，具体原因由 `error` 区分。
10. 不推荐业务逻辑错误一律返回 HTTP 200 或 gRPC OK。若团队历史约定必须如此，只能限于可预期业务规则错误，且认证、权限、限流、系统异常仍用标准状态码。

## 10. 安全与信息泄露

1. 5xx / `INTERNAL` 不返回堆栈、SQL、内部 IP、依赖服务地址。
2. `error` 不包含用户输入、数据库错误、异常信息。
3. 敏感资源应避免通过 `not_found` 和 `access_denied` 的差异探测资源存在性。
4. `message` 不直接展示给用户。
5. `data` 中返回资源 ID 时，应遵循安全策略。

## 11. 检查清单

- [ ] 成功判断只看 `error === "success"` 或 gRPC `status.code = OK`。
- [ ] 已去掉 `code`。
- [ ] 已去掉 `isSuccessful`。
- [ ] `success` 是协议级保留值。
- [ ] `error` 格式为 `service.error`。
- [ ] `service` 是实际产生错误的服务或组件名。
- [ ] 不使用通用前缀。
- [ ] 每个服务已明确定义所有对外错误返回。
- [ ] 未返回未定义的错误标识。
- [ ] 错误前缀为实际服务名。
- [ ] HTTP Status 正确。
- [ ] gRPC Status 正确。
- [ ] HTTP 与 gRPC 对同一业务错误使用相同 `service.error`。
- [ ] `message` 不直接展示给用户。
- [ ] 多语言根据 `error` 映射。
- [ ] `ts` 为最终响应生成组件的 Unix 毫秒时间戳。
- [ ] 5xx / `INTERNAL` 不暴露堆栈、SQL、内部 IP。
- [ ] `204 No Content` 不返回 JSON Body。
- [ ] 错误响应 `data` 为对象。
- [ ] `502/503/504` 不合并。
- [ ] 401 按认证方案返回 `WWW-Authenticate`。
- [ ] 429/503 按需返回 `Retry-After`。

## 12. 最终格式

### 12.1 HTTP 成功

```json
{
  "error": "success",
  "message": "",
  "data": {},
  "ts": 1790486978678
}
```

### 12.2 HTTP 错误

```json
{
  "error": "service.error",
  "message": "internal description",
  "data": {},
  "ts": 1790486978678
}
```

### 12.3 HTTP 无响应体成功

```http
HTTP/1.1 204 No Content
```

### 12.4 gRPC 成功

```text
status.code = OK
响应消息按 proto 定义
trailing metadata 可返回 x-response-ts
```

### 12.5 gRPC 错误

```text
status.code != OK
status.message = 非稳定诊断信息
status.details 包含 ErrorDetail
ErrorDetail.error = service.error
ErrorDetail.data = 结构化错误详情
ErrorDetail.ts = Unix 毫秒时间戳
```

核心规则：

> HTTP Status 与 gRPC Status 给基础设施、网关、监控看；error 给业务、前端、多语言看。成功固定 `"success"` 或 `OK`，其他均为错误。