![TaskLoom 研织：开源研发协作，自主部署，让需求、团队与 AI 协作成网](docs/images/taskloom-banner.svg)

# TaskLoom · 研织

**开源、可自部署的 AI 研发协作与项目管理工具，面向寻找 TAPD / Teambition 替代方案的团队。**

[English](README.en.md) · [使用手册](docs/product-handbook.md) · [API 文档](docs/internal-api-reference.md) · [路线图](docs/OPEN_SOURCE_PLAN.md) · [安全说明](SECURITY.md)

让需求可理解、协作可追溯、AI 可参与。用一个工作空间连接需求管理、敏捷迭代、缺陷跟踪、测试用例、讨论和交付。

> 当前为 **0.1.0-rc1 预发布版**，适合评估与反馈；不承诺与 TAPD / Teambition 功能完全对等，也不关联或获得它们的官方认可。生产使用前请验证备份、权限、通知与部署环境。

## 为什么使用 TaskLoom？

- **研发协作闭环**：项目空间、需求与子需求、迭代、缺陷、测试用例与执行记录。
- **AI 辅助需求管理**：生成标题、完善需求、生成用例、审查用例；生成内容先预览再确认，不静默覆盖原文。
- **面向 AI 编程协作**：导出需求 JSON / Markdown，整合关联信息供模型理解；提供 API 读写集成能力。导出范围以使用手册为准。
- **研发资料集中管理**：Markdown、代码片段、评论、附件分类和关联内容，减少在多个工具间搬运资料。
- **个人工作与通知**：查看与自己相关的工作，按项目、人员或部门搜索；搜索命中文字黄色高亮。
- **自部署与可控数据**：Vue 3 + TypeScript 前端、Go API、SQLite。AI 与外部通知服务由部署方自行配置，默认不调用付费模型。
- **迁移起点**：支持 TAPD PDF 需求导入流程。导入前核对字段与人员匹配，不保证所有导出模板无损兼容。

## 产品一览

![TaskLoom 功能总览：需求、迭代、缺陷与测试、AI 辅助、研发资料和个人协作](docs/images/product-overview.svg)

![AI 协作流程：描述需求、生成建议、人工确认、通过导出或 API 连接外部 AI 工具](docs/images/ai-workflow.svg)

以上为功能与流程示意图，非实际界面截图。AI 与外部通知服务需独立配置；能力及限制以本文和使用手册为准。

## TAPD / Teambition 开源替代场景

如果你在寻找 TAPD 平替、Teambition 平替、开源需求管理系统、敏捷项目管理工具或可私有化部署的研发协同平台，可以从以下流程试用：

| 需求场景 | TaskLoom 对应能力 |
| --- | --- |
| 管理需求池、拆分工作 | 需求、子需求、个人模板、工程师字段 |
| 管理研发计划与缺陷 | 迭代、工作项、缺陷跟踪 |
| 关联需求与测试 | 测试用例、执行、AI 生成与审查 |
| 与 AI 编程助手协作 | JSON/Markdown 导出、API 集成 |
| 从既有工具迁移 | TAPD PDF 导入与字段核对 |

这是使用场景说明，不是竞品功能或价格比较。自动子需求拆解、自动代码提交和变更影响分析尚未实现。

## 快速启动（源码）

需要 Node.js >=22.12、pnpm 11.19.0，以及 go.mod 指定的 Go 1.27.1。只支持你有权限管理的独立数据库。

```sh
git clone https://github.com/weijiaxing1992-arch/taskloom.git
cd taskloom
corepack pnpm install --frozen-lockfile
corepack pnpm build
go build -o server ./cmd/server
cp -R dist web
node start.mjs
```

访问 http://127.0.0.1:8080。启动器生成本机随机会话密钥，将数据写入当前目录 data/，默认只监听回环地址。再次构建时应使用新的发布目录，不混入历史网页资源。

**首次账号：Admin；首次密码：123456。登录后必须修改密码。** 未改密时业务接口受限，服务端拒绝非回环监听。初始化期间也不要配置公网反向代理；远程主机请通过 SSH 隧道完成首次改密。其他示例成员的密码随机生成且不公开，由管理员重设。

预置的是星河示例企业、8 个部门和 11 位虚构成员，不包含原企业用户或数据库。不要将历史商业版本数据库直接导入社区版。

## 架构与目录

```text
浏览器 / H5 → Go HTTP API → SQLite
                    └→ 可选 AI / 微信 / 机器人服务
src/       Vue 页面与组件
cmd/       Go 服务端和管理工具
docs/      使用、API 与运维文档
scripts/   构建及测试工具
```

首版发行 Web + Go，暂不提供社区 DMG 或历史 Flutter 客户端。环境变量及 API 请求头保留 DEVFLOW 兼容前缀。

## 测试与已知限制

```sh
go test ./cmd/server
corepack pnpm typecheck
corepack pnpm test
```

本候选版已通过 Go 服务端测试、前端类型检查与构建、初始化登录和强制改密测试。Linux 二进制仅做过交叉编译，不代表完成 Linux 生产验收。AI 使用模拟接口测试，没有真实付费模型质量评测。仍需完善全站人工验收、依赖漏洞审计、性能基线与异地备份演练。

## 参与与反馈

欢迎提交脱敏复现、部署体验、文档改进及 PR。先阅读 [贡献指南](CONTRIBUTING.md)。安全问题请使用 GitHub 私密漏洞报告，不要公开密钥、数据库或真实客户内容。

本项目使用 [Apache-2.0](LICENSE)。第三方依赖保留各自许可，见 [第三方说明](THIRD_PARTY_NOTICES.md)。
