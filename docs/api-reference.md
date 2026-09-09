# TaskLoom 对外 API 参考

## 范围、版本与入口

本页描述当前实现的项目协作接口：需求、迭代、缺陷、测试用例和已有测试执行。面向自动化集成的版本化 REST 基地址是 `/api/open/v1`，MCP 地址是 `/api/open/mcp`。这不是企业管理 API，也不是所有站内功能的公开映射。

| 入口 | 身份 | 使用边界 |
| --- | --- | --- |
| `/api/open/v1` | 项目绑定的个人 Bearer 凭据 | 对外版本化 REST，使用本页列出的路径、字段和方法 |
| `/api/open/mcp` | 同一 Bearer 凭据 | 对外 MCP 工具，仅能调用已开放的项目协作能力 |
| `/api/integrations` 及其子路径 | 站内登录会话 | 本人凭据管理、调用记录、上下文和规范下载；不是 Bearer 管理接口 |
| `/api/session` | 站内登录会话 | 页面识别当前用户、项目和限制状态；不是 Bearer 身份接口 |
| 其他 `/api/...` | 各内部模块的身份与权限 | 站内实现接口，不具有对外 v1 的兼容性承诺，见[内部 API 参考](internal-api-reference.md) |

不开放删除工作项、创建测试执行/计划、完成并迁移迭代、成员和权限管理、密码、企业 AI 密钥、任意文件下载、任意 URL 请求或内部路径透传。不要把内部路径替换前缀后当作公开接口。

机器可读规范：[OpenAPI 3.1 文件](openapi.json)；站内请使用页面顶部“下载 OpenAPI”按钮。线上相同规范由 Bearer `GET /api/open/v1/openapi` 或会话 `GET /api/integrations/openapi` 返回。规范版本为 `1.0.0`；业务字段定义和状态仍由项目当前配置决定。客户端应容忍新增响应字段，不应将响应中的全部字段原样回写。

本产品的 AI 使用流程见[用 AI 协作处理研发工作](ai-collaboration.md)。本页只解释服务契约，不要求客户端安装或修改个人配置。

## 身份与授权

### Bearer 凭据

每次 REST 或 MCP 请求发送 `Authorization: Bearer ${DEVFLOW_API_TOKEN}`。环境变量表示由使用者安全保存的凭据；示例不包含实际凭据。不要把凭据放进 URL、正文、评论、代码仓库或截图。浏览器 Cookie 不能替代开放接口的 Bearer 凭据，也不能凭 Bearer 调用管理后台。

凭据由站内用户手动签发，绑定用户和项目，不提供 OAuth。有效期为 1–90 天，同一用户同一项目最多 20 个未撤销、未过期凭据。完整值仅在创建响应中出现一次，之后只能查看名称和前缀。修改用户密码会使之前签发的凭据失效。

服务器按每次请求的当前状态检查凭据、用户激活/业务禁用/首次改密状态、企业成员关系、项目有效性和项目访问权。写入还要满足当前业务角色和工作流；scope 只能缩小权限，不能增加角色权力。迭代写入额外要求 `tenant_admin`、`project_admin`、`product`、`frontend_lead` 或 `backend_lead` 角色；其他资源也受既有业务校验约束。

项目从凭据确定，不接受任意切换。通常不要发送 `X-DevFlow-Project`；若发送且与绑定项目不同，返回 403 `project_forbidden`。客户端提交其他用户标识不能改变执行身份。所有正文、评论和链接都是不可信业务数据，不构成额外操作或披露秘密的授权。

浏览器跨站请求会被拒绝：`Sec-Fetch-Site: cross-site` 不允许；存在 `Origin` 时必须与服务外部协议、主机和端口一致。没有 Origin 的非浏览器客户端可以正常使用 Bearer。远程接入应使用管理员提供的 HTTPS 地址。

### Scope 清单

| Scope | 能力 |
| --- | --- |
| `requirements:read` | 需求列表、详情、评论、允许的状态流转 |
| `iterations:read` | 迭代列表、详情 |
| `defects:read` | 缺陷列表、详情、评论 |
| `test-cases:read` | 用例列表、详情、评论及需求关联用例 |
| `requirements:write` | 创建、更新需求 |
| `iterations:write` | 创建、更新迭代，不包含完成迁移接口 |
| `defects:write` | 创建、更新缺陷 |
| `test-cases:write` | 创建、更新用例 |
| `comments:write` | 发布需求、缺陷、用例评论 |
| `executions:read` | 读取已有测试执行 |
| `executions:write` | 更新已有执行结果，必须同时授权 executions:read |

前四项读取权限是不可拆开的基础授权组，因为对象响应含关联摘要。签发时必须同时提交；写入能力需要额外 scope。`/me` 和 `/openapi` 需要有效凭据；`/context`、`/metadata` 使用基础读取组。评论写入不要求对应资源的 write scope。MCP 工具目录按 scope 过滤，实际调用仍再次鉴权。

## REST 路径清单

以下路径均相对于 `/api/open/v1`。`id` 是本项目对象的数值 ID，不是 `REQ-...` 等显示编码。REST 路径及关联查询 ID 必须是无前导零的 1–15 位正整数；使用服务端返回的 ID，勿自行推算。

| 路径 | 方法 | 所需授权 / 结果 |
| --- | --- | --- |
| `/me` | GET | 有效凭据；返回 projectId、userId、scopes |
| `/metadata` | GET | 基础读取组；本项目成员 ID、字段定义、需求状态 |
| `/context` | GET | 基础读取组；有界项目/需求/迭代关联快照 |
| `/openapi` | GET | 有效凭据；OpenAPI 3.1 文档 |
| `/requirements` | GET、POST | requirements:read；创建另需 requirements:write |
| `/requirements/{id}` | GET、PATCH | requirements:read；更新另需 requirements:write |
| `/iterations` | GET、POST | iterations:read；创建另需 iterations:write |
| `/iterations/{id}` | GET、PATCH | iterations:read；更新另需 iterations:write |
| `/defects` | GET、POST | defects:read；创建另需 defects:write |
| `/defects/{id}` | GET、PATCH | defects:read；更新另需 defects:write |
| `/test-cases` | GET、POST | test-cases:read；创建另需 test-cases:write |
| `/test-cases/{id}` | GET、PATCH | test-cases:read；更新另需 test-cases:write |
| `/executions` | GET | executions:read；不支持 POST |
| `/executions/{id}` | GET、PATCH | executions:read；更新另需 executions:write |
| `/requirements/{id}/comments` | GET、POST | requirements:read；发布另需 comments:write |
| `/defects/{id}/comments` | GET、POST | defects:read；发布另需 comments:write |
| `/test-cases/{id}/comments` | GET、POST | test-cases:read；发布另需 comments:write |
| `/requirements/{id}/transitions` | GET | requirements:read；只读，状态修改使用需求 PATCH |
| `/requirements/{id}/test-cases` | GET | requirements:read、test-cases:read；分页关联用例 |

未开放的路径或方法通常返回 404 `endpoint_not_exposed`，不要以内部服务支持某方法来推断它已开放。

### 列表与筛选

所有列表及评论列表返回 `{items,total,page,pageSize}`，按 ID 倒序。默认 `page=1`、`pageSize=25`；页码范围 1–1000000，每页 1–100。REST 接受 `limit` 作为 pageSize 的兼容别名；两者同时存在时非空 pageSize 优先，推荐只用 pageSize。没有游标分页、客户端排序参数或增量删除流。

| 资源 | 除 page/pageSize 外的 REST 筛选字段 |
| --- | --- |
| requirements | `q`、`status`、`priority`、`sprint`、`assigneeUserId`、`ownerUserId`、`category`、`updatedSince` |
| iterations | `q`、`status`、`updatedSince` |
| defects | `q`、`status`、`priority`、`sprint`、`assigneeUserId`、`verifierUserId`、`requirementId`、`updatedSince` |
| test-cases | `q`、`status`、`priority`、`ownerUserId`、`requirementId`、`caseType`、`category`、`updatedSince` |
| executions | `status`、`planId`、`updatedSince`；不支持 q |
| 需求关联用例 | 与 test-cases 相同，requirementId 由路径强制确定 |
| comments | 只允许 page、pageSize、limit |

`q` 在标题（迭代为名称）或显示编码上执行 LIKE 匹配，最多 200 字；`%`、`_` 具有 LIKE 通配含义，不是全文搜索。其余普通字段是单值精确匹配；需求人员筛选匹配主负责人/主处理人列，不等于多人数组中的任一成员。`sprint` 是保存的迭代名称，不是数值 sprintId。`updatedSince` 必须为 RFC3339 时间，含边界；它不承诺覆盖所有关联信息变化，不能替代 ETag。

任一查询参数重复或值超过 500 字节、未知筛选字段，会返回 400 `invalid_query`。整段查询最多 3000 字节。列表不是整个数据库的一致性快照；分页间发生写入可能改变 total 和成员位置，处理批量读取时按 ID 去重。详情读取过程中发生版本变化会返回 409 `read_conflict`。

当前 OpenAPI 和 MCP 的列表参数 schema 描述的是较小的基础筛选集；上表另列了 REST 实现接受的扩展筛选。MCP 不接受 limit、updatedSince 等未在其工具 schema 中列出的参数。

### 上下文、元数据与流转

`GET /context` 可不带参数，或只带 `requirementId`、`sprintId` 其中一个；不能同时指定，不能重复或添加其他参数。返回 `project`、`requirements`、`sprints`、`defects`、`testCases`、`generatedAt`、`contentTrust`、`limits`。每类最多 100 条，`limits={perType:100,truncated:boolean}`；truncated 为 true 时需要继续列表分页和详情读取。

需求上下文包含该需求、按 requirementId 关联的缺陷/用例、其迭代；不递归展开全部子需求。迭代上下文包含该迭代、归属需求/缺陷，以及这些需求关联的用例。无选择器时读取有界项目上下文。不含执行记录、完整评论流或附件文件内容；也不是全库备份或跨全部实体的一次原子快照。

`GET /metadata` 不接受查询参数，返回 `projectId`、`members`、`membersTruncated`、`requirementStatuses`、`fieldDefinitions`、`contentTrust`。members 每项是 `{id,name,role}`，只提供当前项目激活企业成员，最多 1000 条，不提供邮箱/凭据。这个目录不是最终可分配性保证，写入还会检查业务禁用、角色和字段规则。fieldDefinitions 是原生 `{items:[...]}` 字段响应，含 key、objectType、type、required、enabled、options、defaultValue 等；状态项以 key 为稳定值，显示名不能代替 key。

`GET /requirements/{id}/transitions` 不接受查询参数，返回 `{allowedTransitions:[状态key],currentStatus:状态key,version:工作流版本}`。这里的 version 是工作流版本，不是对象 ETag。先检查允许目标，再用需求 PATCH 提交 status；项目配置或权限变化仍可能令提交失败。

## 请求体与业务字段

REST 写入发送 `Content-Type: application/json` 和非空 JSON 对象，最大 2 MiB；不接受查询参数。创建使用 POST，更新使用 PATCH。所有写入必须带 Idempotency-Key，所有对象 PATCH 还必须带 If-Match。

不能写 `tenantId`、`projectId`、`id`、`code`、`createdAt`、`updatedAt`、`createdBy` 等系统字段；其他未在下列清单内的顶层字段也会被拒绝。名字、成员 ID、关联对象、字段值、状态流转、排期和必填自定义字段均由现有业务服务验证。先读取元数据；项目特有的必填项可能使下面的最小示例仍返回 422。

除特别注明的测试执行外，PATCH 只更新提供的普通字段；省略字段保留原值。显式数组通常是替换而非追加。不要随意发送 null；仅下文标注的可清空关系/对象按其业务语义处理。

### 需求 requirements

| 字段 | JSON 类型 / 说明 |
| --- | --- |
| `title` | string，创建必填，去空白后不能空 |
| `type`、`description`、`acceptance`、`category`、`sprint`、`status`、`priority`、`tags`、`remarks`、`discipline` | string；remarks 最多 20000 字，tags 是字符串而非数组 |
| `startDate`、`endDate` | string，YYYY-MM-DD；未排期允许空值，已有排期须符合业务日期约束 |
| `ownerUserIds`、`assigneeUserIds` | string[]，本项目成员 ID，顺序用于主负责人/主处理人；显式提交替换数组 |
| `descriptionMentionUserIds`、`remarksMentionUserIds` | string[]，提及可能通知对应成员；不是执行用户选择器 |
| `parentId` | integer 或 null；同项目父需求，不能形成循环；null 清除父级 |
| `progress` | integer，0–100 |
| `estimatedHours`、`actualHours` | number，非负有限数值 |
| `sensitive`、`authImpact` | boolean |
| `descriptionDoc` | object 或 null，现有结构化富文本；存在时从文档派生 description 和正文提及 |
| `roleWeights` | object 或 null，按角色维度保存评估及人员，见下文 |
| `tagColors` | object 或 null，标签名到 #RRGGBB 的映射，最多 100 项；标签名最多 64 字 |
| `customFields` | object，项目字段 key 到类型匹配的值；省略不提交自定义字段更新 |

创建时，省略 type/category/sprint/priority/discipline 分别使用产品需求、未分类、待规划、P2、product；省略 status 使用项目配置的初始状态，不应硬编码显示名称。对外需求写入没有单值 ownerUserId/assigneeUserId 或 owner/assignee 显示名字段，应使用复数 ID 数组；这些单值字段可能仍出现在响应中。

roleWeights 的键为 frontend、backend、algorithm、ui、product；每项形如 `{userIds:[成员ID],value:数值或null}`，兼容 userId。每个维度最多 50 位成员，value 为 0–1000000 的有限数值，null 表示未评估。多个人员数组的 MCP schema 上限为 100，业务层可有更严格的人选和提及限制。

descriptionDoc 使用 `{type:'doc',content:[节点]}` 结构（实际 JSON 使用双引号）。普通段落是 `{type:'paragraph',content:[{type:'text',text:'正文'}]}`。节点支持的 attrs、marks、链接、提及和资源引用由富文本验证器检查；最多 5000 节点、24 层深度、100000 字符，且仍受开放接口 2 MiB/MCP 1 MiB 总请求限制。不要把任意编辑器 JSON 或未经核实的资源引用直接回写。简单接入优先使用纯文本 description；不要用纯文本误覆盖需要保留的富文本格式。

### 迭代 iterations

| 字段 | JSON 类型 / 说明 |
| --- | --- |
| `name` | string，创建必填，项目内名称/简称按现有规则校验 |
| `startDate`、`endDate` | string，创建均必填，YYYY-MM-DD，开始不能晚于结束 |
| `goal`、`status` | string，迭代目标与状态 |
| `capacity` | integer，非负 |

创建缺省状态是规划中；状态枚举为规划中、进行中、已完成、已取消。PATCH 允许保持原状态，或规划中→进行中/已取消、进行中→已取消；不开放“完成迭代并迁移未完成工作”的内部操作。改名会同步更新关联需求、缺陷和测试计划中的迭代名称；生命周期通知发送给有效的项目负责人。

### 缺陷 defects

| 字段 | JSON 类型 / 说明 |
| --- | --- |
| `title` | string，创建必填，1–200 字 |
| `description`、`steps`、`actual`、`expected`、`environment`、`foundVersion`、`fixVersion` | string |
| `severity`、`priority`、`status`、`sprint`、`discipline`、`tags` | string |
| `assigneeUserId`、`verifierUserId` | string，本项目有效处理人/验证人 ID；空字符串清空对应分配 |
| `requirementId` | integer 或 null，本项目需求；null 清空关联 |
| `progress`、`estimatedHours`、`actualHours` | integer 0–100、非负 number、非负 number |
| `customFields` | object，现有缺陷字段 key/value |

默认 status=新建、priority=P2、severity=一般。priority 可为 P0/P1/P2/P3；severity 为致命/严重/一般/轻微。status 为新建、已确认、修复中、已解决、待验证、已关闭、重新打开、已拒绝；更新必须符合详情中的 allowedTransitions（保持状态也可）。来源执行字段 sourceExecutionId 等只读，不支持通过开放接口伪造测试失败来源关系。

### 测试用例 test-cases

| 字段 | JSON 类型 / 说明 |
| --- | --- |
| `title` | string，创建必填，1–200 字 |
| `category`、`preconditions`、`steps`、`expected`、`priority`、`status`、`caseType`、`tags` | string |
| `ownerUserId` | string，本项目有效成员 ID；空字符串清空负责人 |
| `requirementId` | integer 或 null，本项目需求；null 清空关联 |
| `stepsDetail` | array，1–200 个步骤，每项有非空 action、expected；可带 order，服务器重排为连续序号 |
| `enabled` | boolean，仅 PATCH 可写；新用例默认启用 |
| `customFields` | object，现有用例字段 key/value |

创建还必须有 stepsDetail，或同时提供非空 steps 和 expected。提交 stepsDetail 时由它生成兼容 steps/expected 文本；更新平面 steps 或 expected 会按既有逻辑重建步骤表示，建议始终一致使用结构化步骤。

默认 priority=P2、status=草稿、caseType=功能测试。priority 为 P0/P1/P2/P3；status 为草稿、待评审、已通过、已废弃；caseType 为功能测试、接口测试、兼容性测试、安全测试、性能测试、自动化测试。测试工作区还可能配置必填条件；不能因为 schema 顶层只有 title 必填就省略实际步骤。响应中的 metadata、reviewStatus、requirement 摘要可读，但 metadata 顶层不属于开放写入白名单。

### 测试执行 executions

只有已有记录的 GET/PATCH；没有公开创建、删除、生成缺陷或历史子接口。

| 字段 | JSON 类型 / 说明 |
| --- | --- |
| `status` | string，PATCH 必填：未执行、通过、失败、阻塞、跳过；阻塞须项目已启用 |
| `note` | string，本次执行备注 |
| `actualResult` | string，本次实际结果 |

重要：执行 PATCH 是一次结果提交，**省略 note 或 actualResult 会将旧值清空**，不是普通字段合并。要保留时把详情中的原值一并提交。执行人自动记为凭据所属用户；不能指定 executorUserId。状态不是未执行时由服务端设置执行时间；每次提交写执行历史和活动，真实转为失败可能通知相关成员。不要把未运行的测试报告为通过。

### 评论

POST 请求字段：`body`（string，去空白后非空，最多 20000 字）、可选 `mentionUserIds`（string[]）、可选 `replyToId`（同一对象已有评论的 integer 或 null）。发布需要 Idempotency-Key，不需要 If-Match。提及和回复可能通知项目成员；真实提及目标最多 50 人，MCP 数组 schema 的 100 项上限不是业务通知人数保证。需求评论也可能返回 contentDoc，但开放评论写入不接受 contentDoc。

需求纯文本评论省略 mentionUserIds 时，兼容逻辑可能从正文的 @姓名推断提及；不打算提及时应显式传空数组。缺陷/用例评论只采用显式提及 ID，并要求这些成员实际以 @姓名 出现在 body 中，单独手输姓名不发送提及通知。replyToId 引发的回复通知仍独立存在。REST 支持 replyToId；**当前 MCP 评论工具只支持 body 和 mentionUserIds，不暴露回复参数**。

## 响应与并发控制

### 成功响应

| 操作 | HTTP / JSON |
| --- | --- |
| 身份 | 200 `{projectId,userId,scopes}` |
| 列表与评论列表 | 200 `{items,total,page,pageSize}` |
| 五类资源详情 GET | 200 扁平原生对象，增加 `_etag`；响应头 ETag 为同一个带双引号的值 |
| 创建需求/迭代/缺陷/用例 | 201 原生对象；不是统一的 data 外层包装 |
| 更新需求/缺陷/用例 | 200 原生对象 |
| 更新迭代 | 200 `{sprint,items,weightSummary,summary}`，与扁平详情 GET 不同 |
| 更新执行 | 200 `{id,status,executor,executorUserId,executedAt,note,actualResult}`，不是完整执行详情 |
| 发布评论 | 201 原生评论对象，包括 id、author、authorUserId、body、createdAt、mentionUserIds 及回复字段 |

详情常见字段包括 id、code、title/name、status 和更新时间；需求带人员数组、状态显示信息、排期、评估、自定义字段，缺陷带 allowedTransitions 和来源摘要，用例带 requirement/metadata 摘要，执行带 planId、caseId、执行人/结果及可选 defectId。只读摘要和 `_etag` 都不能放入更新 body。

### ETag / If-Match

详情 GET 的 `ETag` 是不透明的带引号版本字符串，JSON 中对应 `_etag`。PATCH 将它原样放入 `If-Match`；不接受星号、弱标签、多个标签或自造版本。版本基于持久化修订计数，同秒页面编辑及自定义字段变更也会推动版本，不能用 updatedAt 代替。

缺少/格式无效返回 428 `precondition_required`；事务内版本不符返回 412 `precondition_failed`，并可能带当前 ETag。收到 412 后必须重新 GET、比较并重新确认修改意图，不应只替换响应头里的版本强行覆盖。该版本保护覆盖目标对象，不是上下文内所有关联记录的共同锁。

创建和更新成功不保证带新的 ETag。继续修改前重新读取详情；迭代内部名称变化等操作也可能改变关联对象的版本。

### 幂等键与未知结果

每次创建、更新、发布评论都需要 `Idempotency-Key`：8–128 位，以字母/数字开头，其余仅字母、数字、下划线、点、冒号、连字符。格式不符返回 400 `idempotency_key_required`。

幂等范围是同一凭据，记录持久化到数据库。指纹包含方法、路径、If-Match 和规范化顶层 JSON；推荐重试时保持同一正文，不依赖深层等价转换。相同键和相同请求返回原 HTTP 状态、响应正文及已保存 ETag，并带 `Idempotency-Replayed: true`；已保存失败结果也会重放。

同键不同目标/内容/版本返回 409 `idempotency_conflict`。仍在处理或结果未确认返回 409 `operation_pending`。5xx 也可能发生在业务提交后的回读/审计阶段：保留原键及 `X-DevFlow-Request-Id`，先检查对象和调用记录，不能换新键盲目重做。已明确解决旧请求且形成新的修改意图时才使用新键。这不是跨所有故障的“恰好一次”承诺；更换凭据也不共享原幂等空间。

### 通用错误

REST 错误通常为 `{error:{code,message}}`。HTTP 状态和 code 用于分支处理，message 是面向人的本地化说明；业务错误可能有额外字段，不应按中文文案判断成功。

| HTTP | 常见 code | 处理 |
| --- | --- | --- |
| 400 | invalid_request、invalid_query、invalid_json、idempotency_key_required | 修正格式/参数，确认之前是否已有写入 |
| 401 | invalid_api_token | 凭据无效、过期、撤销或当前身份失效；入口可能带 WWW-Authenticate |
| 403 | origin_forbidden、project_forbidden、insufficient_scope、forbidden、sprint_manager_required | 核对来源、绑定项目、scope 与当前业务角色 |
| 404 | endpoint_not_exposed、not_found | 接口未开放，或记录不存在/不属于项目 |
| 409 | read_conflict、idempotency_conflict、operation_pending | 重新读取或核查原请求，勿盲目重写 |
| 412 | precondition_failed | 重新读取和复核修改意图 |
| 413 | request_too_large、response_too_large | 缩小正文、上下文或分页范围 |
| 422 | readonly_field、unsupported_field、validation_error、custom_field_invalid、invalid_rich_document、invalid_mentions | 修正只读/未知字段、业务约束、字段值、提及或富文本；工作流可有专用错误码 |
| 428 | precondition_required | 先 GET，再带原样 If-Match 更新 |
| 429 | rate_limited | 遵循 Retry-After，延迟重试 |
| 503 | database_unavailable、service_unavailable、audit_unavailable、result_unconfirmed | 服务不可用或结果待确认；写请求先核查当前状态 |

列表、详情、写入和规范等业务派发按每个凭据每分钟最多 180 次计数，超限带 `Retry-After: 60`。`/me` 和 MCP 初始化/ping/工具目录本身不进入该业务计数。请求路径最多 200 字节；REST 返回缓冲最多 16 MiB。响应可能因审计保存失败返回 503，即使业务处理曾经完成。

## REST 调用示例

所有名称、编号和正文均为演示占位。先确认实际项目；`${DEVFLOW_API_TOKEN}`、`${DEVFLOW_ETAG}` 由调用方安全提供，不把秘密写进命令历史里的字面量。

```sh
curl --fail-with-body \
  --header "Authorization: Bearer ${DEVFLOW_API_TOKEN}" \
  'https://taskloom.example.com/api/open/v1/me'

curl --fail-with-body --include \
  --header "Authorization: Bearer ${DEVFLOW_API_TOKEN}" \
  'https://taskloom.example.com/api/open/v1/requirements/123'

# DEVFLOW_ETAG 必须是上一条详情实际返回的完整 ETag，包含双引号。
# 新的修改意图使用新的请求键；相同请求重试沿用原键。
curl --fail-with-body --request PATCH \
  --header "Authorization: Bearer ${DEVFLOW_API_TOKEN}" \
  --header 'Content-Type: application/json' \
  --header "If-Match: ${DEVFLOW_ETAG}" \
  --header 'Idempotency-Key: example-req-123-note-001' \
  --data '{"remarks":"已完成本次检查；详细证据见关联提交。"}' \
  'https://taskloom.example.com/api/open/v1/requirements/123'
```

创建用例的 body 示例（POST `/test-cases`，另带 Bearer 和新的 Idempotency-Key）：

```json
{
  "title": "示例：提交前必填校验",
  "priority": "P2",
  "status": "草稿",
  "caseType": "功能测试",
  "preconditions": "已进入待测表单",
  "stepsDetail": [
    {"action": "清空必填字段并提交", "expected": "显示必填提示，不保存无效数据"}
  ]
}
```

## MCP 协议与工具

### HTTP 与 JSON-RPC

使用 POST `/api/open/mcp`、相同 Bearer、`Content-Type: application/json`。Accept 应允许 application/json（客户端可以同时声明 text/event-stream）。服务为无状态 Streamable HTTP 的 JSON 响应模式，没有 SSE、会话 ID、批量 JSON-RPC、任意资源 URL 或代理转发。GET/DELETE 等返回 405，Allow: POST。

协商协议为 `2025-06-18`；后续请求建议带 `MCP-Protocol-Version: 2025-06-18`，显式传其他版本返回 HTTP 400 / -32600。每次只接受一个 JSON-RPC 2.0 对象，请求 id 为整数或最多 256 字节字符串，不能为 null；params/arguments 是对象。请求体最多 1 MiB。

| 方法 | 参数 / 返回 |
| --- | --- |
| initialize | 必填 protocolVersion、capabilities 对象、clientInfo.name/version；返回协议版本、tools 能力、serverInfo 和内容信任说明 |
| notifications/initialized | 无 id；202，无响应体 |
| notifications/cancelled | 无 id；202，仅接受通知，不提供持久任务取消保证 |
| ping | 返回空对象 |
| tools/list | 所有有权工具一次返回，无下一页；非空 cursor 被拒绝 |
| tools/call | params 为 `{name,arguments}`；按工具输入 schema 严格校验 |

initialize 示例：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18",
    "capabilities": {},
    "clientInfo": {"name": "example-client", "version": "1.0"}
  }
}
```

### 完整工具目录

read/write 为对应资源的 scope；实际目录仅包含有权工具。基础四项读取权限签发时必须一同拥有。

| 工具 | arguments | 授权 |
| --- | --- | --- |
| `devflow_me` | `{}` | 有效凭据 |
| `devflow_metadata` | `{}` | 基础读取组 |
| `devflow_context` | 可选 requirementId 或 sprintId，至多一个 | 基础读取组 |
| `devflow_requirements_list` | 可选 query | requirements:read |
| `devflow_requirements_get` | id | requirements:read |
| `devflow_requirements_create` | data、idempotencyKey | requirements:read/write |
| `devflow_requirements_update` | id、data、ifMatch、idempotencyKey | requirements:read/write |
| `devflow_requirements_comments` | id、可选 query | requirements:read |
| `devflow_requirements_comment` | id、body、idempotencyKey；可选 mentionUserIds | requirements:read、comments:write |
| `devflow_requirements_transitions` | id | requirements:read |
| `devflow_requirements_test_cases` | id、可选 query | requirements:read、test-cases:read |
| `devflow_iterations_list` | 可选 query | iterations:read |
| `devflow_iterations_get` | id | iterations:read |
| `devflow_iterations_create` | data、idempotencyKey | iterations:read/write |
| `devflow_iterations_update` | id、data、ifMatch、idempotencyKey | iterations:read/write |
| `devflow_defects_list` | 可选 query | defects:read |
| `devflow_defects_get` | id | defects:read |
| `devflow_defects_create` | data、idempotencyKey | defects:read/write |
| `devflow_defects_update` | id、data、ifMatch、idempotencyKey | defects:read/write |
| `devflow_defects_comments` | id、可选 query | defects:read |
| `devflow_defects_comment` | id、body、idempotencyKey；可选 mentionUserIds | defects:read、comments:write |
| `devflow_test_cases_list` | 可选 query | test-cases:read |
| `devflow_test_cases_get` | id | test-cases:read |
| `devflow_test_cases_create` | data、idempotencyKey | test-cases:read/write |
| `devflow_test_cases_update` | id、data、ifMatch、idempotencyKey | test-cases:read/write |
| `devflow_test_cases_comments` | id、可选 query | test-cases:read |
| `devflow_test_cases_comment` | id、body、idempotencyKey；可选 mentionUserIds | test-cases:read、comments:write |
| `devflow_executions_list` | 可选 query | executions:read |
| `devflow_executions_get` | id | executions:read |
| `devflow_executions_update` | id、data（必须含 status）、ifMatch、idempotencyKey | executions:read/write |

MCP 的 data 使用本页各资源写入字段和共用 OpenAPI schema，不允许系统字段或未知顶层字段。id/关联 ID 是 JSON 整数，仍须通过 REST 项目边界和 ID 校验。ifMatch 对应 REST If-Match，必须含原始双引号；idempotencyKey 对应 REST Idempotency-Key。写入参数不是 HTTP 头名称。

query 的可选字段：

| 工具资源 | query 字段 |
| --- | --- |
| requirements、defects | page、pageSize、q、status、priority、sprint |
| iterations | page、pageSize、q、status |
| test-cases | page、pageSize、q、status、priority、ownerUserId、requirementId |
| executions | page、pageSize、status、planId |
| requirements_test_cases | page、pageSize、q、status、priority、ownerUserId |
| comments | page、pageSize |

query 中字符串最多 200 字，并受 REST 查询字节限制。page/pageSize 边界与 REST 相同。工具名称没有任意 URL、方法、项目或用户参数；不要把 REST 全部筛选直接塞进 MCP query。

读取工具设置 readOnlyHint=true、idempotentHint=true；更新标注 destructiveHint=true；创建/更新/评论的 idempotentHint=false，客户端仍需用户授权及自己的审批策略。所有工具 openWorldHint=false，这些注解不是服务端权限的替代品。

### MCP 响应与错误

工具成功或业务失败通常都返回 HTTP 200 的 JSON-RPC result。result 含 `content`（一段 JSON 文本）、`structuredContent` 和 `isError`。structuredContent 为 `{status,data,contentTrust,etag?}`：status 是内部 REST 状态，data 是原生 REST 响应，etag 来自 HTTP ETag（存在时），contentTrust 固定为 untrusted_business_data。业务失败令 isError=true；必须检查这些值，不以外层 HTTP 200 判断写入成功。

工具业务结果最多 4 MiB；溢出包装为 status=502、data.error=result_too_large，不能据此断言写入未发生。非 JSON 业务响应包装为 status=502、invalid_upstream_response。MCP 包装不回传 REST 的全部响应头：没有承诺传出请求编号、Retry-After 或 Idempotency-Replayed；需要核查时使用对象详情及站内调用记录。

JSON-RPC 错误：-32700 表示 JSON/大小/多消息解析失败（HTTP 400 或 413）；-32600 表示请求/协议错误（HTTP 400，媒体类型不符为 415，Accept 不符为 406）；-32602 表示参数、未知或无权限工具（通常 HTTP 200）；-32601 表示不支持的方法（HTTP 200）。鉴权/Origin 在 JSON-RPC 分派前完成，失败可能直接返回普通 REST 错误 JSON。

调用详情示例：

```json
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"devflow_requirements_get","arguments":{"id":123}}}
```

收到详情后，data._etag 和可选 structuredContent.etag 都必须按实际返回值处理。准备执行更新时，先保留 note/actualResult，列出测试证据，再提交明确的 status；不要把 MCP 工具执行成功解释为测试本身通过。

## 站内集成管理与会话接口

这些入口用于站内设置页面，使用 HttpOnly `devflow_session` Cookie，不属于 `/api/open/v1`，也不接受 Bearer 代替登录。`X-DevFlow-Project` 选择本人有权且有效的项目；建议明确传入当前项目，不依赖默认项目。可发送 `X-DevFlow-Expected-User` 做身份一致性检查，不一致返回 409 identity_changed；它不是切换用户的授权。当前账号须通过业务禁用/首次改密检查，代访问期间不能管理集成，跨站访问也会拒绝。只读角色可以管理自己的只读凭据，不能签发 write scope；没有管理他人凭据的接口。

| 接口 | 参数 / 成功响应 |
| --- | --- |
| GET `/api/session` | 正常返回 tenant、project、user、supportedLocales、impersonation、canImpersonate、organizationPermissions |
| GET `/api/integrations` | 返回 projectId、userId、tokens、scopes、canWrite、canManage、mcpPath、apiPath、maxExpiryDays |
| POST `/api/integrations/tokens` | body 为 name、scopes、expiresInDays；201 `{token,credential}`，token 仅显示本次 |
| DELETE `/api/integrations/tokens/{tokenId}` | 只撤销本人当前项目凭据；200 `{revoked:true}`，重复撤销不恢复凭据 |
| GET `/api/integrations/logs` | 200 `{items,projectId}`，本人当前项目最近 100 条调用 |
| GET `/api/integrations/context` | 与开放 context 相同的 requirementId/sprintId 选择与返回边界 |
| GET `/api/integrations/openapi` | 与开放 openapi 相同的 OpenAPI 3.1 内容 |

创建凭据的 name 去除首尾空白后须为 1–80 字；expiresInDays 为整数 1–90；scopes 必须是上述合法键组成的数组、无重复、包含完整基础读取组。executions:write 必须另含 executions:read。请求按 2 MiB 读取；错误/过大 JSON 返回 400 invalid_json。此会话管理接口不使用开放写入的 If-Match/Idempotency-Key，也没有凭据编辑、续期、恢复或轮换 endpoint。创建结果不明时先查看凭据列表，再处理可能已签发的凭据，不能取回曾经遗漏的明文。

credential/tokens 每项字段为 id、name、prefix、scopes、createdAt、expiresAt、lastUsedAt、revokedAt；未使用/未撤销时相应时间为空字符串。列表最多最近 100 项，包括已过期和已撤销项；不是有效凭据数量，状态需结合时间判断。lastUsedAt 是近似使用时间，最多约每分钟更新一次，不能作为完整活动证明。

日志项为 id、tokenName、method、path、status、createdAt、replayed，不记录 Authorization、请求正文、查询词或密码。status=0 表示审计占位未完成/结果待核实。MCP 的业务调用记录为派发的 REST 路径，不是任意工具参数；握手、工具列表、/me 不出现在该调用日志。日志和凭据列表均没有分页参数，不是全量审计导出。

管理特有错误包括 403 impersonation_forbidden/origin_forbidden/scope_forbidden/forbidden、422 validation_error/invalid_scope/context_scope_required、409 credential_limit、404 not_found、405 method_not_allowed、503 token_unavailable/database_unavailable。会话入口也可能返回 401 unauthorized、403 project_forbidden 和账号操作限制错误。

正常 session.user 含 id、name、role、avatarColor、locale、timezone、operationDisabled、mustChangePassword。首次改密或业务禁用账号仍可能得到 HTTP 200 的受限 session，但 project 为空、组织权限为空；没有活动项目的管理员也可能得到 project 为空的会话。必须读取这些状态，不能只因 session=200 就创建凭据或访问业务。具体登录、退出、初次改密和项目目录契约见[内部 API 参考](internal-api-reference.md)。

## 规范生成与回归检查

`docs/openapi.json` 不是手写的第二套 schema。REST 下载入口、站内下载入口、写入字段白名单、MCP 写入 schema 和导出检查复用 `cmd/server/integration_openapi.go` 中的 `integrationOpenAPISpec()` / `integrationOpenAPIWriteSchema()`。规范有意保留原生业务对象的可扩展字段；本页明确列出的响应包装、业务必填项和 REST 扩展筛选仍需一起阅读。

在仓库根目录运行：

```sh
# 重新导出（只生成文档；不启动服务、不创建 App 或打开数据库）
node scripts/export-openapi.mjs

# CI/发布前只检查，不改文档
node scripts/export-openapi.mjs --check

# 文档覆盖、schema 边界及全部集成回归
go test ./cmd/server -run 'TestIntegration' -count=1
```

脚本优先使用仓库已有 `work/toolchain/runtime/go/bin/go`，否则使用 PATH 中的 Go；可用 `DEVFLOW_GO_BINARY` 指定工具链。既有 GOCACHE/GOMODCACHE 会保留。导出通过只运行文档快照测试并显式设置 `DEVFLOW_UPDATE_OPENAPI=1` 实现；--check 会移除继承的该标志，避免检查意外写文件。

一致性测试逐字节比较生成结果与下载文件，并检查重复生成稳定性、公开路径、scope、MCP 工具、写入字段是否在本页出现。业务测试只使用测试夹具，不能据此宣称已验证任何真实项目数据或实际外部客户端连接。站内文档和静态下载发布后无需新增数据库权限或公共管理入口。
