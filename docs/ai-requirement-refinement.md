# AI 需求完善与数字徽标

## 使用

在创建／编辑需求页的描述下点击「AI 完善需求」，勾选外发同意，再生成预览。返回背景与目标、功能规则、异常与边界、验收标准、待确认问题五部分。默认全部不选，按需勾选后确认追加；正文图片、原文、负责人、难度与排期不替换。正式保存仍走原需求保存流程。

修改描述、验收标准或切换需求后，旧预览失效。生成失败保留输入。预览只在当前页面内存中，不跨页面恢复。生成结果需人工校验；本机规则检查仍明确标注为非 AI。

## 接口

`GET /api/ai/requirement-refine`：返回 configured、enabled、model、canGenerate、maxDescriptionLength 等能力信息（与标题能力共享实现，maxTitleLength 对完善接口无意义）。

`POST /api/ai/requirement-refine`：登录并具备当前项目生成权限，提交 `{description: string, confirmed: true, requirementId?: number}`。前端将当前描述和已有验收标准组合为 description；总计上限 100000 UTF-8 字节，超限拒绝，不截断。响应为 `{model, preview: {background, rules, exceptions, acceptance, questions}}`，各段均为纯文本。不创建需求、不自动修改字段。

复用管理员模型配置、加密密钥、项目鉴权、请求前后权限及版本校验。与标题生成共享每用户每小时 10 次、每企业每小时 80 次、用户同时 1 次、企业同时 4 次限流。审计事件 `ai_requirement_refined` 不记录生成正文。外部模型请求设置 `store:false`，不等同于服务商完全不保留任何日志。

依据 [OpenAI Structured Outputs 文档](https://developers.openai.com/api/docs/guides/structured-outputs) 使用严格结构化输出；服务端另行校验字段、长度、重复键及返回状态。

## 数字显示

通知数、草稿数、用例数、标题计数与迭代筛选数统一使用自适应胶囊、等宽数字及独立字号。移除数字继承按钮字号导致的固定高度裁切。通知数继续显示 99+，完整数量保持原业务数据。

## 验证边界

接口使用模拟模型验证权限、同意、结构校验与不自动写入；没有调用真实付费模型。真实生成取决于管理员配置的密钥、模型权限及服务可用性。子需求自动拆解、变更影响分析、持久化 AI 预览和撤销历史不在本批实现范围。
