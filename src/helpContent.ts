import handbook from '../docs/product-handbook.md?raw'
import apiReference from '../docs/api-reference.md?raw'
import internalReference from '../docs/internal-api-reference.md?raw'
import aiCollaboration from '../docs/ai-collaboration.md?raw'
import aiReleaseNotes from '../docs/ai-release-notes.md?raw'
import operations from '../docs/maintenance-zh.md?raw'
import openAPIURL from '../docs/openapi.json?url'
import { parseHelpDocument } from './helpDocumentation'

// 页面与代码包使用同一份 Markdown，避免另写一份帮助文案后与实际文档脱节。
export const helpDocuments = [
  { id: 'guide', title: '产品帮助手册', description: '从首次使用到需求、迭代、缺陷与测试协作。', filename: 'product-handbook.md', markdown: handbook },
  { id: 'api', title: '开放 API 文档', description: '面向 Codex 和第三方集成的 REST / MCP 接口。', filename: 'api-reference.md', markdown: apiReference },
  { id: 'internal-api', title: '内部 API 参考', description: '面向维护者的站点接口、会话与权限说明。', filename: 'internal-api-reference.md', markdown: internalReference },
  { id: 'ai', title: 'Codex 与 AI 接入', description: '安全连接项目上下文，按授权读写研发工作项。', filename: 'ai-collaboration.md', markdown: aiCollaboration },
  { id: 'release-notes', title: 'AI 迭代升级日志', description: '按固定七类汇总完成需求，复核配图并导出发布图文包。', filename: 'ai-release-notes.md', markdown: aiReleaseNotes },
  { id: 'operations', title: '部署与维护', description: '服务运行、数据库备份、升级与故障排查。', filename: 'maintenance-zh.md', markdown: operations },
].map(parseHelpDocument)

export { openAPIURL }
