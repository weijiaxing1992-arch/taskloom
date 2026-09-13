// 审计动作是稳定的服务端代码，不能直接作为中文页面上的操作名称展示。
// 这里仅做显示层转换：筛选接口仍使用原 action 值，方便管理员精确定位事件。
const exactActionLabels: Record<string, string> = {
  'requirement.description_saved': '保存需求正文',
  'requirement.status_changed': '变更需求状态',
  'requirement.assigned': '分配需求处理人',
  'requirement.owner_assigned': '分配产品负责人',
  'requirement.role_assigned': '分配工程师角色',
  'requirement.mentioned': '在需求中提及成员',
  'requirement.replied': '回复需求评论',
  'requirement.updated': '更新需求',
  'requirement.backend_completed': '完成后端开发',
  'requirement.frontend_completed': '完成前端开发',
  'defect.assigned': '分配缺陷负责人',
  'defect.assignee_changed': '变更缺陷负责人',
  'defect.verifier_assigned': '分配缺陷验证人',
  'defect.verifier_changed': '变更缺陷验证人',
  'defect.status_changed': '变更缺陷状态',
  'defect.created_from_execution': '由测试执行创建缺陷',
  'sprint.status_changed': '变更迭代状态',
  'sprint.completed': '完成迭代',
  'test_case.owner_assigned': '分配测试用例负责人',
  'test_case.owner_changed': '变更测试用例负责人',
  'test_case.mentioned': '在测试用例中提及成员',
  'test_case.replied': '回复测试用例评论',
  'test_plan.owner_assigned': '分配测试计划负责人',
  'test_plan.executor_assigned': '分配测试计划执行人',
  'test_execution.mentioned': '在测试执行中提及成员',
  'test_execution.replied': '回复测试执行评论',
  'organization_member_saved': '保存成员资料',
  'organization_member_removed': '移除成员',
  'project_member_role_changed': '变更项目成员角色',
  'project_members_updated': '更新项目成员',
  'department_saved': '保存部门',
  'group_saved': '保存用户组',
  'profile_updated': '更新个人资料',
  'password_changed': '修改密码',
  'user.initial_password_changed': '修改初始密码',
  'impersonation_started': '开始代访问',
  'impersonation_stopped': '结束代访问',
  'tapd_import': '导入 TAPD 需求',
  'attachment.uploaded': '上传附件',
  'attachment.deleted': '删除附件',
  'attachment.classified': '更新附件分类',
  'design_link.added': '关联设计稿',
  'design_link.deleted': '取消关联设计稿',
  'comment_created': '创建评论',
  'comment.rich_created': '创建评论',
  'status_changed': '变更需求状态',
  'members_imported': '导入成员',
  'members_exported': '导出成员',
  'settings_updated': '更新配置',
  'admin.credentials_changed': '更新管理员登录信息',
  'application_approved': '通过入企申请',
  'application_rejected': '拒绝入企申请',
  'application_pending': '提交入企申请',
  'ai_test_cases_generated': '生成测试用例草稿',
  'ai_test_cases_imported': '导入 AI 测试用例',
  'ai_reviewed': '完成 AI 测试评审',
  'release_notes_queued': '排队生成升级日志',
  'release_notes_generate': '生成升级日志',
  'release_notes_edit': '编辑升级日志',
  'release_notes_drafted': '完成升级日志草稿',
  'application_submitted': '提交加入申请',
  'category_presets_migration': '应用需求分类预设',
}

// 对象类型与字段名来自服务端稳定代码。它们可用于 API 精确筛选，但不应该直接
// 出现在管理员日常阅读的审计卡片中；这里统一转换为业务中文。
const objectLabels: Record<string, string> = {
  requirement: '需求', defect: '缺陷', sprint: '迭代', test_case: '测试用例', test_plan: '测试计划', test_execution: '测试执行',
  project: '项目', user: '成员', department: '部门', organization_group: '用户组', automation_rule: '自动化规则',
  field_definition: '自定义字段', requirement_category: '需求分类', requirement_status: '需求状态', requirement_workflow: '需求工作流',
  testing_library: '测试库', testing_folder: '测试目录', testing_design: '测试设计', testing_settings: '测试设置',
  requirement_attachment: '需求附件', requirement_design_link: '需求设计稿', requirement_dependency: '需求关联',
  comment: '评论', user_wecom: '成员企业微信', wecom_custom_app: '企业微信自建应用', ai_settings: 'AI 服务配置', ai_title_request: 'AI 标题生成',
  organization: '企业组织', organization_import: '成员导入', organization_invitation: '成员邀请', organization_application: '入企申请',
}

// 仅供下拉筛选使用，避免把内部对象代码作为可见文案维护在多个页面中。
export const auditObjectTypes = Object.keys(objectLabels)

const fieldLabels: Record<string, string> = {
  id: '记录编号', name: '名称', title: '标题', code: '编号', description: '描述', document: '正文内容', content: '内容', contentDoc: '富文本内容', descriptionDoc: '需求正文',
  createdAt: '创建时间', updatedAt: '更新时间', deletedAt: '删除时间', expiresAt: '失效时间', status: '状态', active: '启用状态', enabled: '启用状态',
  member: '成员资料', members: '成员列表', memberIds: '成员', userId: '成员', userIds: '成员', actorId: '操作成员', ownerUserId: '负责人', assigneeUserId: '处理人',
  department: '部门', departmentId: '部门', departmentIds: '部门', project: '项目', projectId: '项目', requirement: '需求', requirementId: '需求编号',
  requirementIds: '需求', source: '来源需求', target: '目标需求', relationType: '关联类型', category: '分类', type: '类型', size: '文件大小',
  attachmentId: '附件编号', attachmentIds: '附件', resourceId: '资源编号', url: '链接地址', fileName: '文件名称', passwordChanged: '密码更新状态',
  membershipStatus: '成员状态', relationshipsRevoked: '关联权限状态', role: '项目角色', roles: '项目角色', projectRolesBefore: '原项目角色', projectRolesAfter: '更新后项目角色',
  from: '原状态', to: '新状态', count: '数量', revision: '版本修订号', automatic: '自动生成', published: '发布状态', requirementCount: '需求数量',
  draftId: '草稿编号', requestedCount: '期望数量', issueCount: '问题数量', focus: '关注点', model: '使用模型', mode: '执行方式',
  configured: '配置状态', corpId: '企业编号', agentId: '应用编号', callback: '回调配置', verifyFile: '验证文件', apiBase: '服务地址',
  allowedRoles: '允许角色', maxUses: '最大使用次数', targetSprint: '目标迭代', requirements: '需求数量', defects: '缺陷数量',
}

const verbLabels: Record<string, string> = {
  created: '创建', updated: '更新', deleted: '删除', saved: '保存', complete: '完成', completed: '完成',
  changed: '变更', assigned: '分配', removed: '移除', imported: '导入', import: '导入',
  queued: '排队处理', generated: '生成', generate: '生成', drafted: '生成草稿', edit: '编辑',
  enabled: '启用', disabled: '停用', restored: '恢复', restore: '恢复', executed: '执行', delete: '删除', archive: '归档', revoked: '撤销', configure: '配置', reviewed: '完成评审',
}

function normalizedAction(value: unknown): string {
  return typeof value === 'string' ? value.trim().toLowerCase() : ''
}

function normalizedText(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** 将服务端对象代码转换为稳定的业务名称；未知对象不回显内部代码。 */
export function auditObjectLabel(objectType: unknown): string {
  return objectLabels[normalizedAction(objectType)] || '其他记录'
}

/**
 * 审计字段常来自旧版本 JSON 快照。已知字段展示中文，未知字段使用中性回退，
 * 防止内部列名、协议字段名意外裸露在管理页面。
 */
export function auditFieldLabel(field: unknown): string {
  const source = normalizedText(field)
  if (!source) return '配置项'
  if (/[^\x00-\x7F]/.test(source)) return source
  if (fieldLabels[source]) return fieldLabels[source]
  const parts = source.split('.').filter(Boolean)
  if (parts.length > 1 && parts.every(part => fieldLabels[part])) return parts.map(part => fieldLabels[part]).join(' · ')
  return '配置项'
}

/** 未能关联到成员名称时，也不把 u_xxx、脚本账号等技术标识直接展示给普通管理员。 */
export function auditActorFallbackLabel(actorID: unknown): string {
  const id = normalizedAction(actorID)
  if (!id || id === 'system') return '系统自动操作'
  if (id.includes('cli') || id.includes('bootstrap')) return '系统维护任务'
  if (id === 'u_admin') return '管理员账户'
  return '未知操作成员'
}

/** 返回面向用户的简短动作名称，不在页面上泄露难理解的内部动作代码。 */
export function auditActionLabel(action: unknown, objectType: unknown): string {
  const code = normalizedAction(action)
  if (!code) return '系统操作'
  if (exactActionLabels[code]) return exactActionLabels[code]

  const objectCode = normalizedAction(objectType)
  const entity = objectLabels[objectCode] || ''
  const tokens = code.split(/[._-]+/).filter(Boolean)
  const verb = verbLabels[tokens.at(-1) || '']
  if (entity && verb) return verb === '完成' ? `完成${entity}` : `${verb}${entity}`
  return '系统操作'
}
