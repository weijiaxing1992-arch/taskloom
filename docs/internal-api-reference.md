# TaskLoom 内部 API 参考

## 微信扫码登录与绑定

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/auth/wechat/status` | 公开 | 返回 `enabled,origin`，不含 AppSecret。 |
| POST | `/api/auth/wechat/start` | 未登录浏览器 | 同源 JSON `{}`，返回微信官方 `url,expiresIn`，设置 5 分钟 Secure/HttpOnly/Lax 状态 Cookie。 |
| GET | `/api/auth/wechat/callback` | 一次性授权 | 微信返回 `code,state`；校验浏览器 Cookie、有效期、配置版本，兑换 OpenID 后绑定或签发会话，303 到固定站内路径。 |
| GET | `/api/profile/wechat` | 本人，禁止代访问 | 返回 `bound,boundAt,enabled,origin`；不返回 OpenID、令牌或密钥。 |
| POST | `/api/profile/wechat/bind` | 本人，禁止代访问 | 同源 JSON `{password}`，验证当前密码后发起绑定；回调重查原会话与账号凭据。 |
| POST | `/api/profile/wechat/unbind` | 本人，禁止代访问 | 同源 JSON `{password}`，解除当前绑定、作废本人待完成授权，撤销其他浏览器会话，返回 `{bound:false}`。 |
| GET | `/api/organization/wechat-login` | 企业管理员，禁止代访问 | 返回 `appId,origin,callbackUrl,enabled,secretConfigured,version`。 |
| PATCH | `/api/organization/wechat-login` | 企业管理员，禁止代访问 | 同源 JSON `{appId,origin,secret?,clearSecret?,enabled,version}`；Secret 空字符串保持原值；版本冲突 409。 |

正式回调地址为 https://taskloom.example.com/api/auth/wechat/callback。网站应用 AppID 为 wx 前缀及 16 位十六进制字符，AppSecret 为 32 位字母数字；origin 仅允许无端口、路径、查询或凭据的 HTTPS 域名。已存在绑定时拒绝更换 AppID（409），启用但未配置密钥返回 422。禁用配置不撤销已有登录会话。

身份按当前应用下 OpenID 的摘要匹配已绑定用户，绝不按昵称或邮箱自动开户。扫码不能绕过停用与强制改密。授权 state 与发起浏览器绑定，5 分钟后失效、一次消费；回调页面设置 no-referrer，不向页面回传微信 code、access_token 或内部错误。完成后 URL 只带预定义结果标识，不能指定任意跳转地址。

登录发起按可信连接 IP 每分钟 60 次；绑定/解绑密码确认按账号与连接 IP 每分钟 10 次，错误密码也计数，超限 429。反向代理共享连接 IP 的部署应另配可信边缘限流，不得直接信任客户端伪造的转发头。跨域或缺失 Origin 的写请求拒绝（403）；普通成员不能读取或修改管理员配置（403）。

本轮接口属于 Web 会话边界，不属于开放 REST/MCP Token 权限，不支持第三方通过 Bearer 给人员代绑微信。

## AI 可读导出与 Markdown 结构

需求 JSON/Markdown 导出保持原路径和 v1 原始 data 字段，新增 aiContext、descriptionMarkdown、bodyMarkdown、remarksMarkdown，以及 codeBlocks（source、language、code）。这些字段仅供阅读，不是 PATCH 输入。完整子需求、公开评论和测试资料仍使用同一授权只读快照，超限明确失败。

富文本 heading.level 扩展为 1–6；tableCell/tableHeader 可选 textAlign 为 left/center/right。其他任意样式、脚本属性仍拒绝。备注沿用字符串格式，以语言围栏保存代码，不增加代码执行接口。

## 2026-09-08 关联筛选补充

`GET /api/requirements?mine=1`、`GET /api/defects?mine=1` 根据当前会话筛选全部有效人员字段、角色、明确 @、创建与评论参与关系。参数接受 1/true、0/false；不接受指定他人账号。结果继续受当前租户与项目权限限制。

迭代与待规划工作项返回只读 `relatedToMe`。保存视图可选 `config.relatedToMe` 布尔值与其他条件取交集，旧 `assigneeMode=me` 语义不变。代码块沿用富文本 JSON 和纯文本 Markdown 围栏接口，不增加代码执行接口。

## 文档范围与使用边界

本页对应代码包中的 Go 服务与 Vue 站点，核对日期为 2026-09-06。覆盖站点使用的会话接口、企业管理、项目、需求、迭代、缺陷、测试协作、通知、报表和配置接口。路径均相对于当前站点，例如 `https://taskloom.example.com`。示例中的 ID、姓名、日期为说明用途，必须替换成当前环境中真实、已授权的对象。

**面向 Codex、脚本及第三方集成，优先使用 [开放 API 与 MCP 文档](api-reference.md)。** 本页的 `/api/...` 是站点内部会话接口，不承诺独立于产品版本的兼容性；不可把管理员 Cookie 交给第三方，更不可把开放平台 Bearer 凭证当作后台管理凭证。`/api/open/v1/...` 与 `/api/open/mcp` 使用独立认证边界，详细契约在开放 API 文档。

本页按服务端实际处理器整理，并非将界面按钮简单映射成假想 CRUD。尤其注意：当前没有需求/缺陷/测试用例的通用 `DELETE` 接口，没有独立上传测试附件的接口，也没有任意 SQL、数据库管理、任意 URL Webhook 执行接口。部分旧处理器宽松接受其它 HTTP 方法不是受支持契约，请仅使用本页列出的方法。

## 请求、响应与权限约定

### 2026-09-08 日常协作扩展

- `GET /api/notifications` 新增可选 `group=mentions|handoffs|changes|activity`，空值为全部，未知值返回 400。分组与既有 `read,eventType,project` 条件组合，在分页之前筛选。响应新增 `total`（当前组合筛选总数）、`hasMore` 和 `groupUnread`（按所选项目统计四组未读数，不受已读/事件筛选影响）；原 `unread` 仍为本人所有可见项目的未读总数。所有统计遵循消息及项目可见权限。
- `GET /api/my-work?sprintId={正整数编号}` 支持按迭代筛选，与项目、关键词、类型、状态和分类条件取交集；收藏视图同样支持。返回 `sprints` 是当前账号可访问且符合项目筛选的迭代元数据（id、name、projectId、projectName、status），进行中排在前面。跨项目同名迭代不会混入；编号非法返回 422，不存在或不在所选项目范围返回 404。计数按迭代过滤后的基础范围计算，测试用例按关联需求的迭代归属。
- `GET /api/my-work?category=active` 返回尚未完成、尚未终止的工作项；`counts.active` 为相同基础范围的未结束总数。需求参与关系增加职能权重绑定账号，以及启用的预置测试/运维/组长/管理人员字段，使用稳定账号 ID，不按姓名猜测。
- 内部需求 `descriptionDoc`、需求评论 `contentDoc` 支持 `table → tableRow → tableCell/tableHeader` 文档节点，最多 300 行、30 列，每行列数一致，单元格内容为合法块节点。禁止任意表格属性或 HTML 事件属性。开放 API 的写入能力仍以开放 API 文档为准，不因内部富文本扩展而自动开放。

### TAPD PDF 导入扩展

`POST /api/requirements` 接受可选 `tapdImport` 对象，仅企业管理员或项目管理员可提交。该对象包含 `workspaceId`、`sourceId`、`fileName`、`pdfBase64`、`pageCount`、`fields:[{label,value}]`、`rawText`、`warnings:string[]`、`reviewed:true`。其他字段仍按普通需求创建校验，不能绕过项目成员、分类、状态及自定义字段权限。完整契约和字段映射说明保存在代码包 `docs/tapd-migration.md`。

原始 PDF、字段对照 JSON 与需求在同一事务中保存。201 表示创建成功；200 且 `alreadyImported:true` 表示相同文件此前已导入，返回已有 `id/code`；409 `tapd_source_exists` 表示相同来源但文件不同，不覆盖已有需求。请求最多 30 MiB，PDF 最多 10 MiB，原文字段 1 MiB，字段最多 128 项，合并后备注最多 20,000 字；不截断超限内容。

### 身份与作用域

| 项目 | 规范 |
| --- | --- |
| 登录身份 | `POST /api/auth/login` 后由浏览器维护 HttpOnly 会话 Cookie；请求使用同源凭据。退出、停用或移除成员会使会话失效。 |
| 项目选择 | 项目业务接口使用 `X-DevFlow-Project: <projectId>`；建议始终显式传递，不依赖演示环境的默认项目。 |
| 多标签页身份检查 | `X-DevFlow-Expected-User: <userId>` 应使用当前 `/api/session` 返回值。与会话不一致时返回 `409 identity_changed`。首次改密必须传此头。 |
| 企业接口 | `/api/organization/...`、个人企业微信配置和企业工作量报表不依赖当前项目选择，但分别复核企业权限。 |
| 项目生命周期 | `/api/projects` 及子资源按目标项目独立鉴权；归档项目不允许正常业务读写。 |
| 语言 | `Accept-Language` 支持 `zh-CN`、`en-US`；响应错误文案可本地化，业务判断使用错误 `code`，不要匹配中文消息。 |
| 数据格式 | 一般写入使用 `Content-Type: application/json`。附件使用单文件 `multipart/form-data`；导出按入口支持 PDF、CSV 或单条需求完整 JSON/Markdown。 |
| 缓存 | API 默认 `Cache-Control: private, no-store`；浏览器页面顺序等纯本地布局不属于服务端接口。 |

### 权限缩写

表中的权限是必要条件，不表示绕过处理器内部的对象、状态、版本或字段校验。

| 缩写 | 含义 |
| --- | --- |
| 公开 | 不要求登录；邀请资源仍需要不可猜测的有效邀请 token。 |
| 本人 | 有效会话，仅操作自己的资料、偏好、通知或草稿；部分旧偏好接口仍经过项目作用域检查。 |
| 项目读 | 当前活动项目成员或企业管理员。资源必须属于相同企业和项目。 |
| 项目写 | 项目读 + 非只读、非操作禁用角色；状态流转、人员与关联对象继续校验。 |
| 项目管理 | 服务端认可的项目管理员或企业管理员；某些设置具有单独的事务内检查。 |
| 迭代管理 | `tenant_admin`、`project_admin`、`product`、`frontend_lead`、`backend_lead`。 |
| 企业权限 | `organization.read`、`members.manage`、`departments.manage`、`groups.manage`、`invitations.manage`、`applications.review`、`reports.view` 等明确能力；以 `/api/organization/permissions` 的目录为准。 |
| 企业管理员 | 当前活动 `tenant_admin`；不可通过用户组委派的敏感操作仍要求此身份。 |
| AI 生成 | 当前项目写入能力及企业 AI 使用权限，配置必须可用；请求必须显式确认发送内容。 |

项目角色枚举：`project_admin`、`product`、`frontend`、`backend`、`algorithm`、`ui`、`frontend_lead`、`backend_lead`、`qa`、`viewer`。企业角色与项目角色不是同一套授权，姓名、部门名称、截图中的角色都不能代替服务端角色。

### 返回格式、错误与重试

成功创建通常返回 `201`，读取和更新通常返回 `200`。对象详情直接返回对象，列表通常返回 `{"items":[]}`，但各模块的分页字段不同，见下文。`id`、`code`、创建时间及计算统计由服务端生成，客户端不要尝试覆盖。

```json
{
  "error": {
    "code": "validation_error",
    "message": "请求字段不符合要求"
  }
}
```

| HTTP 状态 | 处理建议 |
| --- | --- |
| 400 / 415 / 422 | 检查 JSON、内容类型、枚举、人员、日期、工作流或必填字段；不要原样自动重试。 |
| 401 | 会话过期、账号不可用或凭据错误；重新登录。 |
| 403 | 缺少企业/项目权限、只读或操作禁用；不要尝试换 ID 绕过。 |
| 404 | 对象不存在，或不属于当前可访问作用域。 |
| 409 | 身份、状态、版本或预览发生变化；重新读取并让用户确认，不能覆盖其他人的修改。 |
| 413 | 超过请求大小限制；缩减正文/附件或分批处理。 |
| 429 / 503 | 限流或服务繁忙，有限退避后重试。写入超时先读取验证，不要盲目重复创建。 |

一般请求体上限为 2 MiB；需求富文本写入与私有草稿有独立的 30 MiB 级限额；需求附件单文件为 10 MiB，上传请求允许少量协议开销。正文、AI、批量导入等还会执行更细的内容限制。

内部 API **没有统一幂等键或统一 ETag 契约**。不同资源分别使用 `version`、`baseVersion`、`orderVersion`、`requirementUpdatedAt`、`caseUpdatedAt` 等。稳定的对外幂等与条件写入应使用开放 API。

## 登录、个人资料与偏好

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET | `/api/health` | 公开 | 返回 `{"status":"ok"}`，仅证明 HTTP 进程存活，不证明数据库可写或通知已送达。 |
| POST | `/api/auth/login` | 公开 | `{email,password}`；返回 `authenticated,user,supportedLocales` 并签发 Cookie。 |
| POST | `/api/auth/logout` | 公开 | 撤销当前 Cookie 对应会话，返回 `{loggedOut:true}`；数据库撤销失败返回 503 并保留 Cookie，便于重试，不报告成功。 |
| GET | `/api/session` | 本人/项目读 | 返回 `tenant,project,user`、权限及代访问信息；首次改密账号得到受限会话视图。 |
| GET | `/api/watermark` | 本人 | 不依赖项目选择，返回当前 `userId,accountName,serverTime,ipAddress,ipSource,refreshSeconds`；连接 IP 仅取服务端连接地址，响应不缓存。 |
| POST | `/api/auth/initial-password` | 本人 | `{currentPassword,newPassword,confirmPassword}` + 必须的身份检查头；首次改密成功后须重新登录。 |
| GET | `/api/auth/impersonation` | 本人 | 返回 `{active,impersonation}`，检查当前是否处于代访问。 |
| POST | `/api/auth/impersonation` | 企业管理员 | `{userId,reason}`，理由 4–500 字；创建有审计记录的代访问，不允许代访自己或嵌套代访问。 |
| POST | `/api/auth/impersonation/stop` | 原管理员会话 | 结束当前代访问，返回 `stopped,projectId`。 |
| GET / PATCH | `/api/profile` | 本人 | GET 返回个人资料和 `memberships`；PATCH 接受下表中的资料字段。 |
| POST | `/api/profile/password` | 本人 | `{currentPassword,newPassword,confirmPassword}`；校验现有密码，修改凭据。 |
| GET / PATCH | `/api/profile/wecom-webhook` | 本人 | PATCH `{url?,enabled?,clear?}`；配置个人通知机器人。GET 仅提供配置状态，不回传密钥。 |
| GET / PATCH | `/api/preferences/display` | 本人 | `{fontSize,themeMode}`；fontSize 为 `small,standard,large,extraLarge`，themeMode 为 `light,dark,auto`。 |
| GET / PATCH | `/api/preferences/locale` | 本人 | `{locale}`；保存语言。 |
| GET / PATCH | `/api/preferences/requirement-list` | 本人/项目读 | 查询 `view=requirement-list/sprint-list`；PATCH `{columns:[...]}`，保存本人列表列设置。 |
| GET / PATCH | `/api/preferences/requirement-detail` | 本人/项目读 | `{basicFields:[...],customFieldKeys:[...]}`；保存本人详情字段偏好。 |

个人资料 PATCH 字段：`name`、`email`、`phone`、`jobTitle`、`bio`、`avatarColor`、`locale`、`timezone`、`emailNotifications`。不要在这里传 `tenantRole`、`memberships`、`active` 等只读管理字段。头像色使用服务端接受的颜色格式，时区使用有效 IANA 时区，例如 `Asia/Shanghai`。

代访问受到额外保护：不能借代访问修改组织管理、他人资料、凭据或项目生命周期；业务写入仍按被代访用户权限执行并记录原管理员身份。

### 页面水印

`GET /api/watermark` 使用当前 Cookie 会话，不接受任意用户查询。`serverTime` 为 UTC RFC3339 时间，`ipSource` 固定为 `connection`，`refreshSeconds` 为 60。无法解析连接 IP 时 `ipAddress` 为空；经过反向代理时表示代理连接地址，不直接相信 `X-Forwarded-For` 或 `X-Real-IP`。支持 `X-DevFlow-Expected-User` 身份检查，非 GET 返回 405，数据库不可用返回脱敏 503。

页面每秒更新时间、每分钟同步元数据。水印不参与业务授权，也不保证下载文件或原生客户端自动带水印。

## 企业、部门、成员与加入流程

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET | `/api/organization/admin` | `organization.read` | 返回组织摘要、当前权限、角色、项目、统计和初始密码是否已配置；不包含密码。 |
| GET | `/api/organization/permissions` | `organization.read` | `{items:[...]}` 企业权限目录。 |
| GET | `/api/organization/directory` | `organization.read` | 企业部门与成员目录，包含授权范围内的邮箱、工号等信息；不是普通项目通讯录。 |
| GET / POST | `/api/organization/departments` | 读 `organization.read`；写 `departments.manage` | GET `{items}`；POST `name,code,parentId?,status?,sortOrder?`，创建部门。 |
| PATCH / DELETE | `/api/organization/departments/{id}` | `departments.manage` | PATCH 同创建字段；DELETE 仅允许未被成员、子部门、字段配置等引用的部门，不自动迁移人员。 |
| GET / POST | `/api/organization/groups` | `groups.manage` | GET `{items}`；POST `{name,description?,permissions?,memberIds?}`。 |
| PATCH / DELETE | `/api/organization/groups/{id}` | `groups.manage` | 更新用户组或删除用户组授权；`permissions` 仅可包含允许委派的能力。 |
| GET / POST | `/api/organization/members` | 读 `organization.read`；写 `members.manage` | GET `{items}`，排除已移除成员；POST 创建成员，字段见下表。 |
| POST | `/api/organization/members/bulk` | `members.manage`；删除限企业管理员 | `{action,userIds,webhook?}`，事务性批量激活、停用、配置机器人或删除；返回 `{affected,userIds}`，详见下文。 |
| PATCH | `/api/organization/members/{id}` | `members.manage`；高风险字段另需管理员 | 按需更新姓名、邮箱、部门、激活状态、操作状态及项目成员关系。 |
| DELETE | `/api/organization/members/{id}` | 企业管理员 | 移除企业成员，返回 `{deleted:true}`；不能删除自己或最后一个活动管理员。 |
| GET / PATCH | `/api/organization/members/{id}/wecom-webhook` | 本人或服务端认可的管理权限 | 查看/修改指定成员机器人配置；字段为 `url,enabled,clear`。 |
| GET | `/api/organization/members/export` | `members.export` | 返回成员 CSV 下载，敏感凭据不会导出。 |
| POST | `/api/organization/members/import/preview` | `members.import` | `{csv}`；逐行校验，返回 `previewId,canCommit` 和有效行/错误，尚未创建账号。 |
| POST | `/api/organization/members/import/commit` | 同一用户、同一企业及 `members.import` | `{previewId}`；消费未过期预览并事务性创建成员，默认未激活。 |
| GET / POST | `/api/organization/invitations` | `invitations.manage` | GET `{items}`；POST 创建邀请，字段见下文；链接 token 仅在创建时提供。 |
| POST | `/api/organization/invitations/{id}/revoke` | `invitations.manage` | 撤销邀请，链接立即失效。 |
| GET | `/api/public/organization-invitations/{token}` | 有效公开邀请 | 返回可展示的邀请、允许部门和申请角色；不泄露组织成员目录。 |
| POST | `/api/public/organization-invitations/{token}` | 有效公开邀请 | `{name,departmentId,requestedRole}`，提交待审核申请，不能直接获取账号或项目权限。 |
| GET | `/api/organization/applications` | `applications.review` | 返回加入申请列表；查询 `status` 可筛选审核状态。 |
| POST | `/api/organization/applications/{id}/approve` | `applications.review` + 分配权限校验 | `{email,initialPassword?,employeeNo?,projectMemberships?}`；审核通过并创建账号。 |
| POST | `/api/organization/applications/{id}/reject` | `applications.review` | `{reason}`；拒绝申请，不创建账号。 |
| GET / PATCH | `/api/organization/ai-settings` | 企业 AI 管理权限 | GET 脱敏配置；PATCH `{apiKey?,model?,enabled?,clear?}`，加密保存密钥；`clear` 与 `apiKey` 不可同时使用。 |

### 成员创建和更新字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `name` / `email` | string | 创建必需；邮箱是登录标识，必须唯一。系统创建登录账号并不等于替企业开通邮箱。 |
| `employeeNo` | string | 工号，可空。 |
| `initialPassword` | string | 可选；留空使用部署配置。文档不提供生产通用密码。启用账号前确认密码配置及首次改密要求。 |
| `active` | boolean | 账号是否激活；未激活账号不能登录。 |
| `operationDisabled` | boolean | 与 `active` 不同：可保留登录，但禁止业务操作。 |
| `tenantRole` | string | 企业角色；不得将项目角色填在这里。管理员角色的授予/撤销执行额外检查。 |
| `departmentIds` | string[] | 企业内部门 ID；传入数组会替换对应成员归属。 |
| `primaryDepartmentId` | string | 主部门，应与部门归属保持一致。 |
| `projectMemberships` | object[] | `[{projectId,role}]`；只允许有效项目和角色，不填写不会自动授权所有项目。 |

安全的新成员示例（不会自动激活或授予项目权限）：

```http
POST /api/organization/members
Content-Type: application/json
X-DevFlow-Expected-User: CURRENT_USER_ID
```

```json
{
  "name": "示例成员",
  "email": "shilichengyuan@example.com",
  "employeeNo": "DEMO001",
  "active": false,
  "tenantRole": "member",
  "departmentIds": ["DEPARTMENT_ID"],
  "primaryDepartmentId": "DEPARTMENT_ID",
  "projectMemberships": []
}
```

成员 CSV 表头接受 `name,email,employeeNo,departmentCode,projectCode,projectRole,active`；预览不会相信 CSV 中的 `active` 来自动激活账号。`departmentCode` 和 `projectCode` 是业务代号，不是数据库 ID。预览与确认分两步，预览绑定当前操作者、企业和有效期；确认前再次校验，已消费或过期预览不能重复导入。

移除成员是保留历史的“退出企业”，不是清空 `users`：撤销组织/部门/用户组/项目关系、停用业务能力、终止登录与代访问，保留旧需求、缺陷、评论和审计中的身份。旧邮箱仍可能被历史用户记录占用，不应直接删库来复用。执行批量移除前应先用一致性备份保存数据库。

### 批量成员操作

- `action` 必须为 `activate`、`deactivate`、`delete` 或 `wecom-config`。`userIds` 为非空 ID 数组，最多 200 项，按首次出现顺序去重；未知、已移除或跨企业目标会拒绝整批。
- 激活同时设置 `active=true,operationDisabled=false`，不重置已有密码或扩大角色；停用关闭登录并使现有会话失效。停用和删除始终拒绝操作者本人及任何企业管理员。
- 委派成员管理员还受高权限用户和权限组成员保护，不能通过批量入口扩大原授权范围。
- 机器人动作支持 `webhook:{enabled?:boolean,url?:string}`。只传 `enabled` 修改启停，传非空 `url` 统一替换地址；仅接受服务端认可的企业微信官方 HTTPS 地址，启用时每个目标必须具有可用配置。不返回地址或密钥，不发送测试消息。
- 全部目标先校验，再在同一事务中写入关系及审计；失败整体回滚。`affected` 为成功处理的去重目标数，包含状态已符合要求的目标，不代表实际发生变化的行数。

邀请创建字段：`name`、`expiresInHours`、`maxUses`、`departmentIds`、`allowedRoles`。批准申请不能通过任意字段注入企业管理员，项目角色仍单独验证。

## 项目、首页、个人工作与搜索

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET / POST | `/api/projects` | 登录；创建须企业管理员 | GET 查询 `q,status`，返回可访问项目及计数，顶层 `canManageMembers` 表示是否有企业成员管理能力；POST `{name,code,description?,ownerUserId?,icon?,color?}`。 |
| GET / PATCH | `/api/projects/{id}` | 目标项目读/项目管理 | GET 项目详情及 `canManage,canRestore,canDelete`；PATCH 更新名称、描述、负责人、图标和颜色等允许字段。 |
| GET | `/api/projects/{id}/summary` | 目标项目读 | 成员、需求、缺陷、迭代、用例计数。 |
| GET | `/api/projects/{id}/members` | 活动目标项目读 | `{items:[{id,name,email,role,active}]}`，当前项目人员选择来源。 |
| PATCH | `/api/projects/{id}/members` | 企业管理员或 `members.manage` | `{addUserIds,removeUserIds,role:"viewer"}`，活动项目成员增量变更；默认只读，保留既有角色，不自动激活。返回完整候选目录，见下文。 |
| POST | `/api/projects/{id}/visit` | 目标项目读 | 记录本人最近访问，返回 `{visited:true}`。 |
| POST | `/api/projects/{id}/archive` | 项目管理 | 活动项目变为归档；保留数据，阻止正常业务访问。 |
| POST | `/api/projects/{id}/restore` | 企业管理员 | 归档项目恢复为活动。 |
| DELETE | `/api/projects/{id}` | 企业管理员 | 必须先归档，JSON `{confirmCode:"完整项目代号"}`；写删除墓碑、保留业务数据，不能再通过 restore 恢复。 |
| GET | `/api/home/recent-content` | 本人/项目读 | 查询 `limit`，返回本人可访问项目的最近内容。 |
| GET | `/api/meta` | 项目读 | 需求状态、优先级、迭代、分类元数据；优先用此处返回值渲染选项。 |
| GET | `/api/my-work` | 本人/项目读 | 查询 `project,q,type,status,category,sprintId`；`view=favorites` 切换为收藏视图；仅汇总本人和可见工作项。 |
| GET | `/api/search` | 项目读 | 查询 `q,type,project,status,limit,offset`；搜索范围仍受项目可见性约束。 |
| GET | `/api/dashboard` | 项目读 | 项目工作项、进度、质量等仪表盘数据。 |
| GET | `/api/project-health` | 项目读 | 项目健康指标和待关注事项。 |
| GET | `/api/roadmap` | 项目读 | 查询 `scope=current/all,page,pageSize`；返回排期与依赖关系视图，不用于直接保存日期。 |
| GET / POST / PATCH | `/api/members` | 读项目；写需项目/企业管理 | 旧项目成员兼容入口。PATCH 必须带 `id`；没有企业成员管理权限的项目管理员仅允许修改已有项目成员的 `projectRole`，不能借此新建企业账号。新代码优先使用企业成员及项目成员接口。 |

项目成员管理先请求 `GET /api/projects/{id}/members?candidates=1`。该模式仅允许企业管理员或具有 `members.manage` 的操作者，返回 `{items:[{id,name,email,active,tenantRole,role}],canManage:true,currentUserId,isTenantAdmin}`；`role:null` 表示尚未加入项目。目录包含未激活但未移除的企业成员。无参数 GET 保持原有项目读权限与响应结构，不会扩大普通成员可见的目录范围。

项目成员 PATCH 的新增与移除集合不能重叠、总变更人数最多 200，角色仅支持 `viewer`；已有成员的角色不被覆盖。不能移除本人或企业管理员，委派操作者还不能移除项目管理员和权限组成员。项目身份、企业范围、成员有效性与权限均在服务端复核，关系变更和审计事务性提交。成功返回与候选 GET 相同的完整目录供界面重新建立选择基线。
| GET | `/api/departments` | 项目读 | 返回字段人员控件可用的部门配置，与企业部门管理接口不同。 |

## 需求与关联资源

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET / POST | `/api/requirements` | 项目读/写 | GET 列表参数见下表；POST 创建需求，返回完整需求对象并记录动态、分配/提及通知。 |
| GET / PATCH | `/api/requirements/{id}` | 项目读/写 | 读取/部分更新需求；传入集合替换对应集合，省略字段保留。状态流转及当前项目人员在事务中再次检查。 |
| GET | `/api/requirements/{id}/export` | 项目读；审计另需项目管理 | 必须查询 `format=json` 或 `format=markdown`；单个只读快照导出已保存需求树及其关联资料，完整范围、限额和错误见“单条需求完整导出”。 |
| GET | `/api/requirements/{id}/transitions` | 项目读 | 返回该需求当前允许执行的工作流流转；更改状态使用需求 PATCH。 |
| GET / PUT / DELETE | `/api/requirements/{id}/favorite` | 本人/项目读 | 查询、收藏、取消收藏；个人行为不修改需求正文。 |
| GET / POST | `/api/requirements/{id}/comments` | 项目读/写 | POST `{body,mentionUserIds?,contentDoc?,replyToId?}`；创建评论、回复及提及通知。 |
| GET / POST / PATCH | `/api/requirements/{id}/checklist` | 项目读/写 | POST `{text}`；PATCH `{id,done}`，ID 在请求体中，不是路径子资源；返回检查项。 |
| GET | `/api/requirements/{id}/activities` | 项目读 | `{items:[{id,actor,event,detail,createdAt}]}`。 |
| GET / POST | `/api/requirements/{id}/attachments` | 项目读/写 | GET 附件元数据；POST 上传 multipart 单文件，详见下文。 |
| GET / PATCH / DELETE | `/api/requirements/{id}/attachments/{attachmentId}` | 项目读/写 | 下载附件/删除指定附件，父需求及附件作用域均校验。 |
| GET / POST | `/api/requirements/{id}/design-links` | 项目读/写 | POST `{title,url}`；保存规范化 Figma 链接，不是任意 URL 抓取。 |
| DELETE | `/api/requirements/{id}/design-links/{linkId}` | 项目写 | 删除设计链接，不删除外部设计文件。 |
| GET / POST | `/api/requirements/{id}/links` | 项目读/写 | POST `{requirementId}`；建立同项目需求关联，返回关联状态。 |
| DELETE | `/api/requirements/{id}/links/{requirementId}` | 项目写 | 解除关联，不删除需求。 |
| GET / POST | `/api/requirements/{id}/dependencies` | 两端可见/可写校验 | GET 依赖列表；POST `{targetTenantId,targetProjectId,targetRequirementId,relationType}`。 |
| PATCH / DELETE | `/api/requirements/{id}/dependencies/{dependencyId}` | 依赖两端权限校验 | PATCH 修改 `relationType`；DELETE 删除依赖边，不删除工作项。 |
| GET | `/api/requirement-dependency-candidates` | 项目读 | 查询 `requirementId,q,scope=current/all,page,pageSize`，返回有权关联的候选需求。 |
| GET | `/api/requirements/{id}/test-cases` | 项目读 | 查询 `limit,cursor`；返回关联用例轻量摘要、统计和下一页游标；用例步骤从用例详情读取。 |
| GET / POST | `/api/requirements/{id}/ai-test-cases` | 读项目；生成需 AI 生成 | GET 生成能力；POST 生成候选草稿，不直接入库，见 AI 章节。 |
| POST | `/api/requirements/{id}/ai-test-cases/import` | 项目写及生成草稿归属校验 | `{draftId,indexes,libraryId?,folderId?}`；选择候选入库，自动绑定来源需求。 |

### 需求列表参数

| 参数 | 说明 |
| --- | --- |
| `q` | 标题、编号与可搜索自定义字段关键字。 |
| `status` / `statuses` | 单状态或 URL 编码 JSON 字符串数组，例如 `statuses=["草稿","规划中"]`；二者不能同时提供非空选择，最多 100 个已知状态 key。 |
| `statusCategory` | `todo`、`doing`、`done`、`cancelled`。 |
| `priority,category,sprint` | 优先级、分类、迭代筛选。迭代名称/代号会经过当前项目规范化。 |
| `assigneeUserId` | 稳定处理人 ID，匹配多人分配；`assignee` 仅为旧姓名链接兼容。 |
| `cf.<key>` | 自定义字段简单匹配；复杂条件使用 `filters`。 |
| `filters` | URL 编码后的 JSON 数组，每项 `{field,operator,value?}`；按字段类型校验。 |
| `sort,order` | 合法字段名；`order=asc/desc`。支持基础字段、角色权重字段及已启用自定义字段，不接受 SQL 片段。 |
| `page,pageSize` | 任一出现则启用分页；默认 `1,30`，每页上限 200，页码上限 100000。返回 `items,total,page,pageSize`。 |
| `projection` | 空值为完整对象；`list` 不带富文本；`reference` 返回 `id,code,title,parentId,status,category,sprint`。 |

支持的筛选运算：文本 `eq,neq,contains,not_contains`；数字/日期 `eq,neq,gt,gte,lt,lte`；人员/多选 `includes,not_includes`；所有支持字段可用 `is_empty,not_empty`（此时不传 value）。例如 `filters=[{"field":"priority","operator":"eq","value":"P1"}]`。日期筛选按服务端用户时区处理，不能把空值等同于数值 0。

### 需求主要写入字段

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| `title,type,description,acceptance` | string | 标题必填；默认类型“产品需求”；验收标准应描述可验证结果。 |
| `descriptionDoc` | object/null | 站点富文本文档树；提交时纯文本与提及从文档提取，不要拼接未经验证的 HTML。 |
| `parentId` | number/null | 同项目父需求；不能形成循环，null 解除父子关系。 |
| `category,sprint,priority,status` | string | 默认“未分类”“待规划”“P2”；省略状态采用当前项目配置的初始状态。 |
| `assigneeUserIds,ownerUserIds` | string[] | 当前项目有效成员，多人数组第一位为主成员；优先稳定 ID，不用同名猜人。 |
| `roleWeights` | object | key 为 `frontend,backend,algorithm,ui,product`；每项 `{userIds:[],value:number/null}`，null 是未估算，0 是明确零值。 |
| `startDate,endDate` | string | `YYYY-MM-DD` 或允许的空字符串；结束不得早于开始。 |
| `tags,remarks` | string | 标签、备注；`tagColors` 是标签到 `#RRGGBB` 的映射。 |
| `descriptionMentionUserIds,remarksMentionUserIds` | string[] | 提及的稳定成员 ID；服务端校验关联和范围。仅写 `@姓名` 文本不能保证准确通知。 |
| `discipline,progress,estimatedHours,actualHours` | string/number | 角色方向、进度及工时，执行类型/范围校验。 |
| `sensitive,authImpact` | boolean | 敏感数据/权限影响标识。 |
| `customFields` | object | 已启用字段 key 到值的映射；类型、成员范围和必填规则由字段配置决定。 |

```http
POST /api/requirements
X-DevFlow-Project: PROJECT_ID
X-DevFlow-Expected-User: CURRENT_USER_ID
Content-Type: application/json
```

```json
{
  "title": "导出任务支持展示处理进度",
  "type": "产品需求",
  "description": "用户发起导出后，可查看排队、处理及完成状态。",
  "acceptance": "重复点击不会创建重复任务；完成后可下载文件。",
  "priority": "P1",
  "assigneeUserIds": ["PROJECT_MEMBER_ID"],
  "ownerUserIds": [],
  "startDate": "2026-09-07",
  "endDate": "2026-09-11"
}
```

局部更新只传真实变更，例如 `PATCH /api/requirements/{id}` 的 `{"priority":"P2"}`。不要把 GET 得到的整个对象无差别回写，避免覆盖别人刚调整的集合字段。

依赖类型为 `blocks`（本需求阻塞目标）、`blocked_by`（本需求被目标阻塞）、`relates_to`（相关）。跨项目依赖不代表获得目标项目权限；服务端检查两端权限与循环，不可借候选查询读取无权项目。

### 附件上传协议

使用 `POST /api/requirements/{id}/attachments`，请求为 `multipart/form-data`，必须且只能包含一个名为 `file` 的文件字段。浏览器用 `FormData` 时让浏览器生成 Content-Type 与 boundary，不要手动写 JSON Content-Type。服务端检查原始文件名不得含路径、嗅探内容类型、计算 SHA-256，并限制单文件 10 MiB。返回附件对象包含 `id,name,size,contentType,category,language,sha256,downloadUrl` 等。上传 URL 可带 `?category=bug|api|design|code|other|auto`，默认 auto；multipart 仍只包含 file。分类分别表示缺陷、接口、设计稿、代码、其他，auto 优先按文件名推断，名称不明确时检查有限前缀识别明确代码。语言独立于业务分类。

使用 `PATCH /api/requirements/{id}/attachments/{attachmentId}` 和 `{"category":"api"}` 修改分类（项目写权限、审计记录）；`GET` 同一路径带 `?metadata=1` 仅返回元数据。普通 GET 仍下载原文件。正文与需求评论的待保存 image/attachment 节点支持可选 `category` 属性；已保存附件的分类以附件接口为准，不通过伪造正文覆盖。导出 JSON/Markdown 的附件索引包含分类和语言。

下载使用 GET 返回的 `downloadUrl`，下载地址仍要求会话和项目权限；不能用公开静态地址绕过鉴权。正文或评论仍引用附件时，删除返回 `409 attachment_in_use`；解除引用后删除会删除该附件内容及记录，操作前确认准确附件 ID。

## 分类、标签、状态、工作流与字段

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET / POST | `/api/requirement-categories` | 项目读/写 | GET 返回分类及排序版本；POST `{name}`。 |
| PATCH | `/api/requirement-categories/order` | 项目写 | `{orderedIds,orderVersion}`；完整排序和版本检查，避免覆盖其他标签页排序。 |
| PATCH / DELETE | `/api/requirement-categories/{id}` | 项目写 | PATCH `{name}`；重命名/删除同步处理该分类的需求归属，不删除需求。 |
| GET | `/api/requirement-tags` | 项目读 | 聚合当前项目已有标签及颜色。 |
| GET / POST | `/api/requirement-statuses` | 项目读/项目管理 | GET 状态定义；POST `key,name,color,category,enabled,sortOrder`。 |
| PATCH | `/api/requirement-statuses/{id}` | 项目管理 | 修改显示名、颜色、分类、启用与排序；系统状态和引用状态受保护。 |
| POST | `/api/requirement-statuses/defaults` | 项目管理 | 补齐缺失默认状态，不重置现有配置。 |
| GET / PUT | `/api/requirement-workflow` | 项目读/项目管理 | GET 流转图；PUT `{initialStatus,endStatuses,transitions,version}`，每条 transitions 为 `{from,to,roles}`。 |
| GET / POST | `/api/field-definitions` | 项目读/项目管理 | 查询 `objectType`；POST 字段定义，字段见下文。 |
| PATCH / DELETE | `/api/field-definitions/{id}` | 项目管理 | 编辑/删除字段定义；引用、历史值及系统字段保护按处理器执行，不应直接操作字段值表。 |
| GET | `/api/field-presets` | 项目读 | 查询 `objectType`，返回预设及是否已安装。 |
| POST | `/api/field-presets/apply` | 项目管理 | `{objectType,keys}`；仅安装选择的预设。 |
| GET / POST | `/api/requirement-views` | 项目读；写按个人/共享权限 | GET 视图列表；POST `{name,scope,config}`，`scope=personal/shared`。 |
| PATCH / DELETE | `/api/requirement-views/{id}` | 视图所有者/共享视图管理权限 | PATCH `{name,config,version}`；DELETE 请求体 `{version}`；不能修改 scope，冲突需重新读取。 |

字段定义使用 `objectType,key,name,type,description,departmentId,memberRoles,required,searchable,filterable,listVisible,enabled,sortOrder,defaultValue,options`。`objectType` 为 `requirement,defect,test_case,sprint`。人员字段的部门和角色交集在后端验证；不能通过手写用户 ID 越过选择范围。项目使用者应先获取定义，再提交对应 `customFields`，不要将字段名称当成 key。

保存视图的 `config.schema=1`，其余字段为 `q,statuses,statusCategory,assigneeMode,assigneeUserId,sprint,sprintId,category,categoryId,priority,filters,columns,sort,order,view`。`assigneeMode=any/me/member`，`view=list/board`。动态需求分类/迭代可附稳定 ID，过期引用会校验。

## 自动化规则

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET / POST | `/api/automation-rules` | 项目管理 | GET 返回规则及选项；POST 创建规则。 |
| POST | `/api/automation-rules/preview` | 项目管理 | 提交候选规则预览匹配样本，`dryRun=true`，不会发送通知。 |
| PATCH / DELETE | `/api/automation-rules/{id}` | 项目管理 | 更新 JSON 携带当前 `version`；删除使用查询参数 `?version=当前版本`，不是 JSON 请求体。 |
| POST | `/api/automation-rules/{id}/preview` | 项目管理 | 预览已保存规则，不执行真实业务动作。 |

当前自动化模型只支持 `trigger="requirement.status_changed"` 和 `action="notify"`，用于站内通知，不是任意脚本/Webhook/邮件执行器。字段为 `name,enabled,trigger,fromStatus,toStatus,action,recipientModes,recipientUserIds`，更新时附 `version`。空 `fromStatus`/`toStatus` 的含义及合法状态由规则校验器确定。

收件人模式：`assignee,owner,frontend,backend,algorithm,ui,product,frontend_lead,backend_lead,tester`。这些模式解析工作项上绑定的具体人，不向“所有有前端角色的人”广播；显式收件人为当前项目有效成员 ID。前端/后端负责人等模式与角色字段映射以服务端返回选项为准。多个渠道命中同一用户会去重。预览无法还原历史变更前状态，返回 `fromStatusEvaluatedAtRuntime` 表示真实变更发生时才校验来源状态。

## 迭代

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET / POST | `/api/sprints` | 项目读/迭代管理 | GET `{items}` 含需求/缺陷及工时统计；POST 字段见下文。 |
| GET / PATCH | `/api/sprints/{id}` | 项目读/迭代管理 | GET `{sprint,items,weightSummary,summary}`；PATCH 更新迭代信息或合法状态。 |
| GET | `/api/sprints/{id}/activities` | 项目读 | 迭代活动记录。 |
| POST | `/api/sprints/{id}/complete` | 迭代管理 | `{targetSprint}`；仅进行中的迭代可完成，未完成工作项迁移到目标迭代，默认“待规划”；同事务记录通知与统计。 |
| GET | `/api/sprints/backlog/items` | 项目读 | 待规划需求和缺陷统一列表。 |
| GET | `/api/sprints/backlog/weights` | 项目读 | 待规划需求角色权重统计。 |

迭代主要字段：`name,goal,startDate,endDate,status,capacity`，日期 `YYYY-MM-DD` 且开始不晚于结束。迭代不设独立负责人，状态通知使用项目负责人。创建/开始/取消与完成不是同一个操作；完成必须使用 `/complete`，不能用普通 PATCH 绕过未完成项迁移。需求/缺陷所属迭代通过其各自的 PATCH `sprint` 更新。

```json
{
  "name": "九月第二周",
  "goal": "完成导出进度与异常回归",
  "startDate": "2026-09-07",
  "endDate": "2026-09-11",
  "status": "规划中",
  "ownerUserIds": ["PROJECT_MEMBER_ID"],
  "capacity": 40
}
```

## 缺陷

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET / POST | `/api/defects` | 项目读/写 | GET 当前项目缺陷，`{items,total}`；POST 创建缺陷并记录活动/通知。 |
| GET / PATCH | `/api/defects/{id}` | 项目读/写 | GET 详情及 `allowedTransitions`；PATCH 允许字段，校验状态和负责人。 |
| GET / POST | `/api/defects/{id}/comments` | 项目读/写 | POST `{body,mentionUserIds?,replyToId?}`；评论、回复和提及通知。 |
| GET | `/api/defects/{id}/activities` | 项目读 | 缺陷活动记录。 |

缺陷列表查询：`q,status,statuses,severity,priority,assignee,sprint,assigneeUserId,verifierUserId,mine`。`mine=1/true` 表示当前会话本人是负责人或验证人，`0/false` 关闭；不是由客户端指定一个“自己”ID。此内部列表不支持统一 page/pageSize，若需有界第三方同步使用开放 API。

主要写入字段：`title,description,steps,actual,expected,environment,foundVersion,fixVersion,severity,priority,status,assigneeUserId,verifierUserId,sprint,requirementId,tags,discipline,progress,estimatedHours,actualHours,customFields`。`requirementId` 为当前项目需求 ID 或 null；`severity=致命/严重/一般/轻微`，`priority=P0/P1/P2/P3`。合法状态为“新建、已确认、修复中、已解决、待验证、已关闭、重新打开、已拒绝”，但不能任意跳转，应使用 GET 返回的 `allowedTransitions`。

```json
{
  "title": "空结果导出任务一直显示处理中",
  "description": "在测试环境对零行结果发起导出，任务没有结束。",
  "steps": "1. 查询无数据条件\n2. 点击导出\n3. 查看任务状态",
  "expected": "任务结束并提示无可导出数据",
  "actual": "超过一分钟仍显示处理中",
  "severity": "一般",
  "priority": "P2",
  "status": "新建",
  "assigneeUserId": "PROJECT_MEMBER_ID",
  "verifierUserId": "QA_MEMBER_ID",
  "requirementId": 123
}
```

## 测试设计、用例库、评审、计划与执行

### 用例及测试工作台入口

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET / POST | `/api/test-cases` | 项目读/写 | GET 用例列表；POST 创建完整用例，字段见下文，自动绑定同项目需求。 |
| GET / PATCH | `/api/test-cases/{id}` | 项目读/写 | 用例详情/部分更新；服务端应用测试模板必填校验。 |
| POST | `/api/test-cases/{id}/copy` | 项目写 | 克隆为草稿副本，返回新用例；不会覆盖原用例。 |
| GET / POST | `/api/test-cases/{id}/comments` | 项目读/写 | 用例评论及 `mentionUserIds` 提及。 |
| GET | `/api/test-cases/{id}/activities` | 项目读 | 旧用例活动入口；完整工作台变更另见 history。 |
| GET / POST | `/api/test-cases/{id}/ai-review` | 项目读/AI 生成 | 查询能力/请求规范或逻辑审查；不会自动批准用例。 |
| GET | `/api/testing` | 项目读 | 工作台快照，包括 libraries、folders、locations、settings、designs、canManage、canEdit。 |
| GET | `/api/testing/workspace` | 项目读 | 与 `/api/testing` 相同的工作台快照别名。 |
| GET / POST | `/api/testing/libraries` | 项目读/工作台写校验 | 库列表；POST `{name}`。 |
| PATCH / DELETE | `/api/testing/libraries/{id}` | 工作台写校验 | PATCH `{name}`；DELETE 删除空库，默认库和在用库受保护。 |
| GET / POST | `/api/testing/folders` | 项目读/工作台写校验 | 目录列表；POST `{libraryId,parentId?,name,sortOrder?}`。 |
| PATCH / DELETE | `/api/testing/folders/{id}` | 工作台写校验 | 更新目录；父目录与库、循环引用、在用目录删除均校验。 |
| POST | `/api/testing/cases/move` | 项目写 | `{caseIds,libraryId?,folderId?}`；移动选定用例，保留内容及历史。 |
| GET / PATCH | `/api/testing/cases/{id}/metadata` | 项目读/写 | `description,testData,estimatedMinutes,libraryId,folderId,preconditions,requirementId` 扩展信息。 |
| GET | `/api/testing/cases/{id}/history` | 项目读 | 用例完整工作台变更历史。 |
| GET / POST | `/api/testing/cases/{id}/reviews` | 项目读/提交或评审权限 | GET 评审历史；POST `{decision,comment}`；decision 为 `submit,approve,reject`。批准/驳回需测试人员或管理员。 |
| GET / PATCH | `/api/testing/settings` | 项目读/项目管理 | 测试模板、结果选项、AI 审查规则；PATCH 带当前 `version`。 |
| GET / POST | `/api/testing/designs` | 项目读/写 | GET 设计列表；POST 测试设计及测试点。 |
| GET / PATCH / DELETE | `/api/testing/designs/{id}` | 项目读/写 | 读取、编辑、删除设计；关联用例是独立资产，不等于删除用例。 |
| POST | `/api/testing/designs/{id}/points/{pointId}/case` | 项目写 | `{caseId}`；把当前项目既有用例关联到指定测试点。 |

用例列表只要携带 `q,status,priority,caseType,ownerUserId,requirementId,libraryId,folderId,page,pageSize` 中任一参数就启用分页，默认 20 条，上限 100，返回 `items,total,page,pageSize`；旧无参数请求仅返回 `{items}`。`folderId` 包含其子目录；`q` 当前服务端匹配标题和编号，不能假设会搜索步骤、描述或标签全文。

### 用例字段及创建示例

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `title` | string | 用例标题，必须表达可验证场景。 |
| `category,priority,status,caseType` | string | 分类、等级、生命周期状态、类型；`caseType` 不叫 `type`。 |
| `preconditions` | string | 前置条件；是否必填以项目模板为准。 |
| `stepsDetail` | object[] | `{order,action,expected}` 结构化步骤，推荐使用。 |
| `steps,expected` | string | 兼容旧文本步骤与预期；不要把两种格式写成互相冲突的内容。 |
| `ownerUserId` | string | 当前项目成员，承担用例负责人和相关通知角色。 |
| `requirementId` | number/null | 关联一个同项目需求；需求详情自动列出相应用例。 |
| `tags` | string | 用例标签。 |
| `enabled` | boolean | 仅 PATCH 生效；创建固定启用，即使正文传 false 也不会创建停用用例。启用不是评审通过。 |
| `metadata` | object | `{description,testData,estimatedMinutes,libraryId,folderId}`；目录/库必须同项目。 |
| `customFields` | object | 当前测试用例字段定义认可的扩展值。 |

```http
POST /api/test-cases
Content-Type: application/json
X-DevFlow-Project: PROJECT_ID
```

```json
{
  "title": "导出零行结果时任务正常结束",
  "category": "导出管理",
  "caseType": "功能测试",
  "priority": "P1",
  "status": "草稿",
  "preconditions": "已登录具有导出权限的测试账号；筛选结果为零行。",
  "requirementId": 123,
  "stepsDetail": [
    {"order": 1, "action": "点击导出按钮", "expected": "只创建一个导出任务"},
    {"order": 2, "action": "查看导出任务状态", "expected": "任务结束并明确提示无可导出数据"}
  ],
  "metadata": {"description": "验证空结果边界", "testData": "零行查询结果", "estimatedMinutes": 3}
}
```

测试设计字段：`name,requirementId,description,ownerUserId,tags,points`；每个测试点为 `{id?,title,category,priority,caseIds}`。编辑时应先读取完整设计，避免丢失现有测试点/关联。

测试设置字段：`version,fields,blockedEnabled,aiReviewRules,aiLogicRules,businessContext`。`fields` 中每项为 `key,name,description,required,listVisible,defaultValue,enabled`，仅接受系统注册的模板字段。关闭 `blockedEnabled` 会让新的“阻塞”执行结果被拒绝，不能仅靠前端隐藏选项代替后端校验。

### 计划与执行

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET / POST | `/api/test-plans` | 项目读/写 | GET 计划及 total/executed/passed/failed；POST 创建计划并关联用例/生成执行。 |
| GET / PATCH | `/api/test-plans/{id}` | 项目读/写 | GET 详情含 caseIds；PATCH 更新计划与允许的用例集合，已执行记录受保护。 |
| GET / POST | `/api/test-plans/{id}/comments` | 项目读/写 | 测试计划评论与提及。 |
| GET | `/api/test-executions` | 项目读 | 查询 `planId`；返回 `{items,summary}`，summary 含 total/executed/passed/failed/blocked。 |
| PATCH | `/api/test-executions/{id}` | 项目写 | `{status,actualResult,note}`；`status` 必填，省略 `note` 或 `actualResult` 会清空对应旧值。执行人取当前会话身份，不接受通过正文指派其他执行人；保存执行结果、历史及失败通知。 |
| GET | `/api/test-executions/{id}/history` | 项目读 | 执行历史、实际结果和生成的缺陷链接。 |
| GET / POST | `/api/test-executions/{id}/comments` | 项目读/写 | 执行记录评论与提及。 |
| POST | `/api/test-executions/{id}/create-defect` | 项目写 | 仅失败执行可生成缺陷；返回 `{defectId,code}`；已有缺陷返回 `409 defect_already_created`。 |

计划写入字段：`name,sprint,version,scope,ownerUserId,startDate,endDate,status,environment,executorUserId,caseIds`。这里 `version` 是产品版本字符串，不是乐观锁整数。用例 ID 必须属于当前项目，并符合加入计划的状态/模板校验。执行状态为 `未执行,通过,失败,阻塞,跳过`；执行记录来自测试计划，不支持凭空 `POST /api/test-executions`。内部接口无独立 `GET /api/test-executions/{id}` 详情契约，应从列表、history、comments 读取。

```json
{
  "name": "导出模块回归",
  "sprint": "待规划",
  "version": "1.2.0",
  "scope": "正常导出、空结果、重试、权限",
  "status": "规划中",
  "environment": "测试环境",
  "caseIds": [456]
}
```

上述 JSON 用于 POST 测试计划；用例需先经过项目要求的评审。失败执行的 PATCH 示例为 `{"status":"失败","actualResult":"任务未结束","note":"测试环境复现"}`。随后调用 create-defect，系统自动回链需求、用例、计划及执行，避免重复人工录入。质量报告页面组合测试计划/执行统计，并非存在未实现的 `/api/test-reports` 写入接口。

## AI 生成与人工确认

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET / POST | `/api/ai/requirement-title` | 项目读/AI 生成 | GET 能力与模型状态；POST `{description,confirmed:true,requirementId?}`，返回建议标题，用户决定是否应用。 |
| GET / POST | `/api/requirements/{id}/ai-test-cases` | 项目读/AI 生成 | GET `configured,enabled,model,canGenerate,inputFields,maxCases,focusOptions`；POST 下方生成请求。 |
| POST | `/api/requirements/{id}/ai-test-cases/import` | 项目写、本人草稿 | 选择候选入库；不能导入别人的草稿，需求/配置版本变化须重新生成或按错误提示重新确认。 |
| GET / POST | `/api/test-cases/{id}/ai-review` | 项目读/AI 生成 | POST `{confirmed:true,caseUpdatedAt,mode}`；`mode=standard/logic`，返回 `summary,issues,model` 等；不自动修改原用例、评审状态或权限。 |

AI 用例生成前必须保存需求。只有明确确认才会把需求标题、描述、验收标准及选定补充说明发送给配置的 AI 服务。图片和附件不会因为关联需求就默认全部作为模型输入。模型输出是待审阅建议，不是“保证完整正确”的测试结论。

```json
{
  "confirmed": true,
  "requirementUpdatedAt": "GET 需求返回的 updatedAt 原值",
  "focus": [],
  "extraInstructions": "重点补充边界、异常重试和权限校验",
  "count": 5
}
```

生成最多 10 条，`focus` 值从 GET 的 `focusOptions` 选择。收到候选草稿后，使用返回的 `draftId` 与零基候选 `indexes` 入库，例如 `{"draftId":"RETURNED_DRAFT_ID","indexes":[0,2]}`。生成不等于入库；入库不等于评审通过；评审通过不等于执行通过。AI 服务故障不得伪装成成功或偷偷替换为固定模板。

## 个人需求模板

模板按企业及当前用户隔离，可在有产品角色权限的项目编辑器使用。产品角色 `product` 和管理员可以管理自己的模板；管理员不能查看其他用户的模板，代访问禁止访问。服务端在事务内重新校验账号、项目权限和角色。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/requirement-templates` | 返回本人模板摘要及 `canManage`，不返回正文。 |
| GET | `/api/requirement-templates/{uuid}` | 返回本人完整 `template`。 |
| PUT | `/api/requirement-templates/{uuid}` | `{name,description,acceptance,remarks,baseVersion}`；新建版本 0，更新须传当前版本。 |
| DELETE | `/api/requirement-templates/{uuid}` | `{baseVersion}`；删除前验证版本。 |

每人最多 100 个模板；名称 1–80 字，描述最多 120 KB，验收标准和备注各最多 30 KB，请求最多 256 KB。Markdown 正文以纯文本存储，填入编辑器时经现有解析器转换，人员 ID 通过独立配置字段保存，不携带附件或工作流状态。未知字段拒绝；版本冲突返回 409。保存模板不创建需求、不发送通知，应用模板仅修改编辑器草稿。

## 私有草稿

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET | `/api/drafts` | 本人/项目读 | 本人当前作用域草稿列表。 |
| GET | `/api/drafts/{id}` | 本人/项目读 | 读取完整草稿，包含 payload 和 context。 |
| PUT | `/api/drafts/{id}` | 本人/项目写 | `{kind,targetId,context,payload,baseVersion}`；kind 为 `requirement/defect`，新建 baseVersion=0，更新携带当前版本。 |
| DELETE | `/api/drafts/{id}` | 本人/项目写 | `{version}`；按版本删除，冲突不覆盖新草稿。 |

草稿 ID 遵循处理器的 ID 格式，建议使用站点现有 UUID 生成逻辑。草稿不是正式需求/缺陷，不应产生分配通知；正式提交仍需经过业务接口验证。不能通过管理员身份枚举其他成员私有草稿。

## 人员与部门搜索

搜索补充（2026-09-08）：`/api/search`、`/api/requirements`、`/api/defects` 的 `q` 也匹配相关负责人、处理人、职能开发人员及人员字段对应的姓名和有效直属部门名；范围与权限仍由原接口约束。详见 [人员与部门搜索](people-search.md)。

## 通知、投递队列与实时数字

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET | `/api/notifications` | 本人/项目读 | 查询 `read,eventType,project,limit,offset`；返回 `items,unread,limit,offset`。 |
| GET | `/api/notifications/unread-count` | 本人/项目读 | `{unread:number}`；站点轻量更新角标使用。不是 SSE 或 WebSocket。 |
| POST | `/api/notifications/read-all` | 本人/项目读 | 将本人当前可见通知全部标为已读，返回 `{updated:number}`；不是仅把当前筛选页标为已读。 |
| PATCH | `/api/notifications/{id}` | 本人/项目读 | `{read:true/false}`，只更改本人可见通知的已读状态。 |
| GET | `/api/notifications/outbox` | 项目管理权限校验 | 当前项目最近 100 条外发队列，返回 `mode,items`，不等于站内通知箱。 |
| POST | `/api/notifications/outbox/{id}/retry` | 项目管理权限校验 | 仅失败记录可重试，返回 `id,status`；成功入队不代表企业微信已送达。 |

通知 `read=unread/read`，不传为全部。limit 默认 40，最大 100，offset 从 0 开始。列表条目含 `id,projectId,projectName,actorUserId,actor,eventType,subjectType,subjectId,title,body,readAt,createdAt,url`。`unread` 是本人当前可见范围未读总数，不是当前分页条数或当前筛选条数。

事件筛选使用服务端 eventType，不使用中文显示文案。评论和正文提及、分配、状态交接、迭代及测试失败等通知都必须绑定稳定接收人 ID，并验证接收人在项目中的当前权限。没有有效接收人时不能回退发给某个演示用户。前端数字刷新依赖轻量读取与页面同步，当前接口不承诺推送流；外发队列的 mock 模式也不意味着已经向真实企业微信发送。

### 个人机器人投递状态（2026-09-08）

`GET /api/profile/wecom-webhook`（以及有权限的成员机器人 GET 接口）增加 `publicUrlConfigured:boolean`；`deliveries` 每条增加 `group:string`（中文分类名）、`parts:number`、`sentParts:number`。`attempts` 是当前分段的尝试次数，不是整条通知的累积次数。配置和投递状态不回传完整 Webhook 或密钥。

所有新站内通知，包括 `automation.*`，会为当时已启用机器人的接收人原子入队。不回放历史消息。首次发送冻结完整正文及发送时事项概况，长文本分段；只有全部分段得到企业微信成功响应才标记 `sent`。`mock_sent` 不代表真实发送。每段发送前复核接收人权限和机器人配置版本；失去权限或配置改变则停止。网络不确定结果可能导致当前分段重发，不承诺端到端恰好一次。

通知中心“机器人投递”读取上述个人接口，不使用旧的 `/notifications/outbox` 项目事件队列。详情及启用条件见 [机器人通知说明](robot-notifications.md)。

## 报表、审计与导出

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET | `/api/reports/workload` | `reports.view` | 查询 `month=YYYY-MM`；企业月度工作量，返回 totals、people、departments、roles、未分配/未估算及统计范围。 |
| GET | `/api/reports/workload/trends` | `reports.view` | 查询 `month,months,department,user,role`；months 仅 6 或 12，返回 monthly/iterations/options 等趋势。 |
| GET | `/api/audit-logs` | 项目管理 | 查询 `objectType,action,actorId,from,to,limit,cursor`；返回脱敏变更列表及下一页游标。 |
| GET | `/api/exports/requirement/{id}.pdf` | 项目读 | 导出授权需求 PDF。 |
| GET | `/api/exports/defect/{id}.pdf` | 项目读 | 导出授权缺陷 PDF。 |

审计接口分页为游标分页，不使用 offset；`from,to` 接受代码认可的日期/时间边界，结束时间须晚于开始时间。不要自行解码或拼接游标。报表是对应筛选范围的只读快照，未分配与未估算是独立指标，不能在客户端把它们静默归零。列表页面的部分 CSV 导出由前端使用已授权列表生成，因此不能虚构 `/api/requirements/export` 或 `/api/test-cases/export` 服务端路径。

### 单条需求完整导出

`GET /api/requirements/{id}/export?format=json|markdown` 使用当前站点会话及 `X-DevFlow-Project`，不是开放平台 Bearer 接口。`format` 必填且只能出现一次，不接受其它查询参数或额外子路径。该只读请求不需要写入幂等键或 `If-Match`，不会创建工作项、写导出审计或调用外部模型。原有需求 PDF 接口保持不变；PDF、列表 CSV 与本接口的用途和内容范围不同。

```http
GET /api/requirements/123/export?format=json HTTP/1.1
X-DevFlow-Project: prj_example
```

上述 ID 仅为示例；浏览器应通过同源会话发送请求，不要把 Cookie 写入导出文件或分享给模型。

| 内容集合 | 范围与边界 |
| --- | --- |
| 需求树 | 根需求及同项目全部递归子孙，不限定为一层；包含业务字段、富文本、验收条件、备注、标签、人员/职能权重、可见自定义字段及定义、状态信息、检查项、活动、设计链接和需求附件元数据。 |
| 评论与回复 | 全部纳入需求的评论、富文本、提及和回复关系；以及纳入用例、计划、执行、缺陷、迭代的公开讨论和活动。不复用列表分页或 AI 上下文摘要。历史评论作者快照保留，人员表按稳定 ID 补充当前可解析姓名，包括历史移除人员。 |
| 测试设计与用例 | 用例集合取“直接绑定树内需求”与“树内测试设计的测试点挂接用例”的并集；包含完整步骤、元数据、评审和历史、设计/测试点关系，以及用例库和目录祖先。不会因用例另有关联需求就展开另一棵需求树。 |
| 测试计划与执行 | 纳入所选用例的执行及执行历史、关联计划本体与公开讨论；`testPlanCases` 和 `testExecutions` 仅覆盖所选用例，不能理解成共享计划的全部用例或全部执行。 |
| 缺陷、迭代与关系 | 包含直接绑定树内需求或关联所选执行的同项目缺陷；迭代按树内需求的迭代归属纳入。普通关系和树外需求仅附可见关联/摘要，不扩展其正文；跨项目依赖必须双方项目活动且当前身份都有读取权限。 |
| 审计 | `auditHistory` 仅含纳入工作项级别的审计事件，不含评论、附件、设计等子资源的独立审计事件；仅真实企业管理员或当前项目 `project_admin` 可获此集合，仍沿用敏感字段脱敏。普通读者得到空集合及 `auditIncluded:false`。遗留项目 `tenant_admin` 字符串不授予企业管理员审计权。 |
| 附件 | 不内联图片或附件二进制。受支持的需求附件含相对 `downloadUrl` 和所需项目头；实际下载仍须有效会话和附件权限。旧附件仅保留元数据，无法下载时 `downloadUrl:null`，并标明原因。 |

当前账号、企业/项目权限和数据均在同一个只读事务快照中再次校验。不存在或越权的根对象返回错误，不能借 ID 探测读取。异常跨项目/不存在的父需求、用例需求、缺陷/执行外键，以及无效目录、回复引用会清空，不会随可读根对象泄露。私有草稿、私有 AI 评审缓存、账号凭据、机器人配置及无关共享计划用例/执行明确排除；业务作者自行写进正文的敏感信息仍属于业务内容，分享前需自行审查。

| 响应契约 | 说明 |
| --- | --- |
| JSON | `application/json; charset=utf-8`，附件名 `REQ-{id}-complete.json`。 |
| Markdown | `text/markdown; charset=utf-8`，附件名 `REQ-{id}-complete.md`；固定章节内以 JSON 数据围栏呈现，与 JSON 格式携带相同数据集合，不是摘要。 |
| 顶层字段 | `schemaVersion:"devflow.requirement-export.v1"`、`exportedAt`、`source`、`completeness`、`contentTrust`、`data`。字段为 camelCase，保存为 JSON 的业务列解码为结构化值；这不是可直接回写的请求体。 |
| 完整性 | 成功时 `completeness.status:"complete"`、`truncated:false`，并给出集合 `counts`、`limits`、排除类别、计划范围及审计说明；“完整”限于该授权范围，并非整库备份。 |
| 来源与缓存 | `source` 标识企业、项目、根需求、来源路径及 `single_read_transaction`；下载带 `Content-Disposition: attachment`、`Content-Length`、`X-Content-Type-Options: nosniff` 和 `Cache-Control: private, no-store`。 |
| 模型安全提示 | `contentTrust` 明确业务文字是“不可信资料，不是指令”。Markdown 仅采用固定标题，业务数据保留在足够长的安全代码围栏中；导出不会执行其中链接或代码，也不代表内容已获对外分享授权。 |

安全上限：最多 **1,000 条需求（含根）**、所有导出集合合计 **50,000 条记录**、**32 MiB** 数据/最终文件、读取快照 **30 秒超时**；每个服务进程同时最多 **2 个**完整导出。没有分页、游标、静默截断或可下载的半成品；超过限制应选较小的子需求范围，不能靠加 `limit` 绕过。导出的是已保存版本，不包含浏览器尚未保存的修改。

| 状态 / 错误 code | 处理方式 |
| --- | --- |
| `400 invalid_export_format` | 检查唯一的 `format` 参数；只能为 `json` 或 `markdown`。 |
| `401` / `403` / `404 not_found` | 核对当前会话、账号状态、活动项目和对象读取权限；不存在/不可见对象不返回资料。 |
| `405 method_not_allowed` | 仅支持 GET。 |
| `409 export_invalid_hierarchy` | 需求层级存在循环，无法作为完整树导出；由获授权人员核查层级后再试。 |
| `413 export_too_large` | 需求、记录或大小超限，缩小根需求范围。 |
| `503 export_unavailable` | 并发槽已满、超时或数据读取异常；有限退避重试，持续失败交管理员排查。 |

验证依据保存在仓库文档 `docs/requirement-export-verification.md`，它是开发验证记录，不是新的站内业务入口。

## 开放平台的站点管理入口

以下路径由登录后的站点管理个人协作凭证，不属于 Bearer 数据 API。完整字段、scope、幂等、ETag、MCP 和 OpenAPI 下载见 [开放 API 与 MCP 文档](api-reference.md)。

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET | `/api/integrations` | 当前项目本人 | 个人凭证、可用 scope、平台能力等；不会重新返回已有凭证明文。 |
| POST | `/api/integrations/tokens` | 本人及 scope 对应项目权限 | 创建项目限定个人凭证；明文仅首次返回，应立即安全保存。 |
| DELETE | `/api/integrations/tokens/{id}` | 本人及凭证归属检查 | 撤销协作凭证。 |
| GET | `/api/integrations/logs` | 本人及项目范围 | 查看个人协作调用审计。 |
| GET | `/api/integrations/context` | 本人及项目范围 | 导出当前授权项目的结构化协作上下文。 |
| GET | `/api/integrations/openapi` | 本人及项目范围 | 获取开放 API 的机器可读描述。 |

与上述 Cookie 管理入口不同，下面是独立 Bearer 边界入口；完整业务子路径请直接查阅开放 API 文档，不在内部文档复制一套 schema。

| 方法 | 路径 | 权限 | 请求与结果 / 副作用 |
| --- | --- | --- | --- |
| GET | `/api/open/v1/me` | 有效个人协作 Bearer 凭证 | 读取当前凭证对应的身份、项目和 scope，用于集成连通性验证。 |
| POST | `/api/open/mcp` | 有效个人协作 Bearer 凭证 | MCP JSON-RPC 入口；工具调用再次受 scope、项目、幂等与条件写入约束。 |

## 代码定位与覆盖检查

内部注册入口位于 `cmd/server/v3.go` 的 `apiMux`，认证、企业及开放平台旁路位于同文件 `scopedAPI`；企业子路由在 `organization_admin.go`、需求子路由在 `main.go`、测试旧接口与迭代/缺陷在 `v2.go`，测试工作台在 `testing_workspace.go`。业务校验拆分在同目录的 `requirement_*.go`、`organization_*.go`、`quality_*.go`、`testing_*.go` 等文件。

运行 `node scripts/check-api-doc-coverage.mjs` 检查全部 Go 生产文件中的 mux 注册入口和显式 URL 路径是否在 API 文档中有对应索引。该检查保证入口不遗漏，**不替代**动态子路由、请求 schema、权限及业务行为的代码审查或接口测试。新增路由时同时补充本页的“方法、路径、权限、请求、返回与副作用”，修改开放接口时另更新机器可读 OpenAPI。


### 个人模板：难度和人员配置（2026-09-08）

个人模板现支持保存前端、后端、算法、UI、产品难度及各职能工程师，以及负责人（产品角色）和测试负责人（测试角色，对应需求的测试人员字段）。旧模板保持兼容。模板不保存迭代、附件或工作流状态，也不会触发通知。

接口新增可选字段 `roleWeights`、`ownerUserIds`、`testerUserIds`。难度范围为 0–1000000，留空表示未评估；每组人员最多 50 人。保存时校验当前项目有效成员、职能和测试部门限制。

“填入空白”保留已填写字段；“替换内容”在确认后替换模板中已配置的文本、难度及人员，仍不改变标题、迭代。跨项目或人员变更导致候选不可用时，确认后跳过，不自动替换成同名人员。填写结果仍需保存需求才生效。创建页与详情页选择迭代时，进行中的迭代优先展示并标注“当前进行中”。
