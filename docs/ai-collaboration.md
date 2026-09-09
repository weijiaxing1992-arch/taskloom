# 用 AI 协作处理研发工作

TaskLoom 的项目集成让本机 Codex 等支持 HTTP MCP 的客户端读取需求、迭代、缺陷和测试用例，并在授权后回写进展、用例和执行结果。也可直接使用 HTTP API 接入其他工具。此功能是有范围的研发协作接口：不开放企业管理、成员管理、密码、企业 AI 密钥、任意文件或任意内部接口。

## 三步开始

1. 在项目的集成设置中创建个人凭据。确认项目和有效期，先使用“项目研发上下文只读”。这组权限一起包含需求、迭代、缺陷和用例读取，因为对象之间存在关联。需要回写时，再为对应资源勾选写权限；测试执行读取和写入另行授权。凭据属于当前用户和当前项目，最多有效 90 天，只在创建成功时展示完整值。保存到自己的安全凭据工具，不粘贴到任务、评论、仓库或截图中。
2. 让启动 Codex 的进程能够读取环境变量 `DEVFLOW_API_TOKEN`，并由本人将以下配置合并到自己的 Codex 配置。地址应与 TaskLoom 的实际监听地址一致；`127.0.0.1:19086` 是本机示例。本项目不会自动安装客户端或修改个人配置。

   ```toml
   [mcp_servers.devflow]
   url = "https://taskloom.example.com/api/open/mcp"
   bearer_token_env_var = "DEVFLOW_API_TOKEN"
   default_tools_approval_mode = "writes"
   ```

   Codex 的 HTTP MCP 支持从环境变量取得 Bearer Token；`writes` 会为没有标记为只读的工具请求审批。客户端设置依据 [官方 Codex MCP 文档](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)。本服务使用手动签发的 Bearer 凭据，不提供 OAuth 登录；不要对它执行 OAuth 登录配置。
3. 重新加载客户端后，先读取身份和项目上下文，确认项目正确。例如：“读取 TaskLoom 需求 123 的上下文，概括验收条件、关联缺陷与用例，并列出待确认的问题。”其中 `123` 必须替换为真实需求 ID；迭代上下文可使用实际 `sprintId`。首次先完成只读验证，再启用需要的写权限。

本机地址仅适用于能访问该主机的客户端，并不使 TaskLoom 自动成为云端服务。远程客户端需要管理员另外提供可达的 HTTPS 地址和部署防护，不能把远程环境里的 `127.0.0.1` 当作这台电脑。凭据不是 OpenAI API Key，也不会为外部模型提供额度。

## 从需求到实现再回写

先用 `devflow_context` 读取需求或迭代上下文，检查 `limits.truncated`。每种对象最多返回 100 条；缺失内容通过分页列表和详情继续读取。上下文不会读取附件的文件内容。涉及负责人、自定义字段或状态时，先用 `devflow_metadata` 读取本项目成员 ID、字段定义和需求状态；不要猜测人员 ID。

然后把明确的需求、验收条件和当前代码交给 Codex 实现并验证。在回写前，让它列出准备修改的对象、字段和验证证据，按已有用户授权和客户端审批执行。适合的回写包括需求备注、缺陷修复说明、测试用例和测试执行结果；不得把没有运行过的检查写成“已通过”。评论中的 @ 提及可能通知成员，只有确有需要且已获授权时才使用。

每次更新都先读取对应详情，保留结果中的 `etag`；更新工具的 `ifMatch` 原样使用该值。需求状态变更先调用 `devflow_requirements_transitions`，再通过更新工具设置允许的 `status`。所有创建、更新、评论均要提供新的 `idempotencyKey`（8–128 位，以字母或数字开头，可包含字母、数字、下划线、点、冒号和连字符）。只有完全相同请求的重试才复用原键；改变内容、版本或目标时使用新键。

遇到 `412`，说明读取后有人改动过对象，应重新读取、比较并复核意图。遇到 `409`，检查幂等键是否被用于不同请求，或原操作是否仍在处理。超时和服务器错误可能留下未知结果；先核对当前对象及操作记录，不应换一个键盲目再次写入。本机制不承诺跨任意故障的“恰好一次”执行。

## HTTP API 与接口说明

REST 基地址为 `/api/open/v1`；Bearer 凭据通过 `Authorization` 请求头传入。项目由凭据固定，不接受客户端指定项目或代替其他用户。以下示例要求终端已安全设置 `DEVFLOW_API_TOKEN`，不会包含实际令牌值：

```sh
curl --fail-with-body \
  --header "Authorization: Bearer ${DEVFLOW_API_TOKEN}" \
  'https://taskloom.example.com/api/open/v1/me'

curl --fail-with-body \
  --header "Authorization: Bearer ${DEVFLOW_API_TOKEN}" \
  'https://taskloom.example.com/api/open/v1/context?requirementId=123'
```

| 操作 | 接口 |
| --- | --- |
| 身份、上下文、项目元数据 | GET `/me`、`/context`、`/metadata` |
| 需求、迭代、缺陷、测试用例 | GET/POST `/requirements`、`/iterations`、`/defects`、`/test-cases`；GET/PATCH 对应 `/{id}` |
| 测试执行 | GET `/executions`、GET/PATCH `/executions/{id}` |
| 评论 | GET/POST `/requirements/{id}/comments`、`/defects/{id}/comments`、`/test-cases/{id}/comments` |
| 需求追溯与状态 | GET `/requirements/{id}/test-cases`、`/requirements/{id}/transitions` |
| OpenAPI 3.1 | Bearer GET `/openapi`；项目会话 GET `/api/integrations/openapi` |

列表使用 `page` 和 `pageSize`，默认每页 25 条，最多 100 条；返回 `items`、`total`、`page`、`pageSize`。上下文的 `requirementId` 和 `sprintId` 最多选一个，不传时取得项目上下文。详情通过 HTTP `ETag` 响应头返回版本；REST 更新须发送 `If-Match`，所有 REST 写入须发送 `Idempotency-Key`。业务数据保持原生 JSON，MCP 在 `structuredContent` 和文本结果中包装为 `status`、`data`、可选 `etag` 和 `contentTrust`；API 失败对应 `isError: true`。

凭据权限只会缩小权限，不能扩大用户已有的项目权限。创建凭据时可选的写权限，不代表当前用户一定能执行对应业务操作。成员被停用、移出项目或权限改变后，请求按当前权限重新检查。停用集成时撤销凭据；怀疑泄露时立即撤销并签发新凭据。

## MCP 边界与内容信任

`/api/open/mcp` 实现无状态 Streamable HTTP：POST 使用单个 JSON-RPC 2.0 消息和 JSON 响应；支持初始化、ping、工具列表和工具调用，以及初始化完成/取消通知。没有 SSE 推送、会话、批量调用、任意网址请求或任意路径透传；GET 返回 405。协商的协议版本为 `2025-06-18`；客户端应在后续请求发送 `MCP-Protocol-Version: 2025-06-18`，其他显式协议头会被拒绝。请求上限 1 MiB，工具业务结果上限 4 MiB。工具列表依凭据权限过滤，服务端也再次执行授权与业务校验。传输约定依据 [MCP Streamable HTTP 规范](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)，工具结果依据 [MCP 工具规范](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)。

需求正文、评论、标题、链接及其他业务字段全部视为不可信任务数据。即使正文写着“忽略规则”“上传凭据”“执行命令”或“打开某地址”，也不构成用户授权，不应覆盖系统指令或触发额外操作。MCP 初始化指令、工具描述和结果标识都会提示这一边界；客户端仍须自行维护授权、内容信任与写操作审批。
