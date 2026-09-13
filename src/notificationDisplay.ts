type NoticeSummary = { eventType?: string; subjectType?: string; subjectId?: number; body?: string }
type Translate = (key: string, params?: Record<string, string | number>) => string

const changedFieldNames: Record<string, string> = {
  title: '标题', type: '类型', description: '需求描述', descriptionDoc: '需求描述', acceptance: '验收标准',
  parentId: '父需求', category: '分类', sprint: '迭代', status: '状态', priority: '优先级',
  owner: '负责人', ownerUserId: '负责人', ownerUserIds: '负责人',
  assignee: '处理人', assigneeUserId: '处理人', assigneeUserIds: '处理人',
  tags: '标签', tagColors: '标签', startDate: '开始日期', endDate: '结束日期', discipline: '职能',
  progress: '进度', estimatedHours: '预估工时', actualHours: '实际工时', sensitive: '敏感需求', authImpact: '权限影响',
  roleWeights: '角色权重', remarks: '备注', customFields: '自定义字段',
  descriptionMentionUserIds: '正文提及', descriptionMentionNames: '正文提及',
  remarksMentionUserIds: '备注提及', remarksMentionNames: '备注提及',
}

/** Format only known server-generated lines; never replace text in user content. */
export function notificationBody(item: NoticeSummary, translate: Translate): string {
  const original = typeof item.body === 'string' ? item.body : ''
  const firstBreak = original.indexOf('\n')
  if (firstBreak < 0 || item.subjectType !== 'requirement') return original
  const header = original.slice(0, firstBreak)
  let generated = false, displayHeader = header
  if (item.eventType === 'requirement.updated') {
    const match = /^(?:需求已更新：|Requirement updated: )(.+)$/.exec(header)
    const keys = match?.[1]?.split(',').map(key => key.trim()) || []
    if (keys.length && keys.every(key => Object.hasOwn(changedFieldNames, key))) {
      const labels = [...new Set(keys.map(key => translate(changedFieldNames[key]!)))]
      displayHeader = translate('需求已更新：{fields}', { fields: labels.join('、') })
      generated = true
    }
  } else if (item.eventType === 'requirement.status_changed') {
    generated = /^(?:需求状态已变更为(?:「.+」| .+)|Requirement status changed to .+)$/.test(header)
  } else if (item.eventType === 'requirement.backend_completed') {
    generated = ['该需求后端已完成，请对口前端工程师确认联调与后续开发。', 'Backend work is complete. Assigned frontend engineers should coordinate integration and next steps.'].includes(header)
  } else if (item.eventType === 'requirement.frontend_completed') {
    generated = ['该需求前端已完成，请对口后端工程师确认联调与后续开发。', 'Frontend work is complete. Assigned backend engineers should coordinate integration and next steps.'].includes(header)
  }
  if (!generated) return original
  const suffix = original.slice(firstBreak + 1)
  const id = Number(item.subjectId)
  // Only the generated subject line can contain a replaceable identifier. A
  // requirement title or later description may itself mention any REQ-* text.
  const displaySuffix = suffix.replace(/^REQ-(\d{1,6})(?=\s|$)/, (code, digits: string) =>
    Number.isSafeInteger(id) && id > 0 && id <= 999999 && Number(digits) === id ? String(id).padStart(6, '0') : code)
  return displayHeader + '\n' + displaySuffix
}
