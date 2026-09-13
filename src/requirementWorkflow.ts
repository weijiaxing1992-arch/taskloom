import { tagStyle } from './requirementFields'

// key 是状态持久化/筛选标识，name 是可修改的展示名，category 决定统计类别。
// 业务保存不能提交翻译后的 label，也不能仅按颜色或“看起来已完成”的名称推断权限。
export type RequirementState = { id: number; key: string; name: string; color: string; category: 'todo'|'doing'|'done'|'cancelled'; enabled: boolean; sortOrder: number; system?: boolean }
export type StatusOption = { value: string; label: string; color?: string; custom?: boolean }
const tones: Record<string,string> = {
  '草稿':'#64748B','规划中':'#6366F1','评审中':'#D97706','待开发':'#64748B','进行中':'#2563EB',
  '开发中':'#2563EB','实现中':'#2563EB','修复中':'#2563EB','已确认':'#2563EB',
  '前端已完成':'#0891B2','后端已完成':'#0891B2','开发完成':'#0891B2',
  '后端完成 | 前端开发中':'#2563EB','前端完成 | 后端开发中':'#2563EB',
  '冒烟测试完成':'#7C3AED','测试中':'#7C3AED','待验证':'#7C3AED','已解决':'#0891B2',
  '待上线':'#D97706','流程挂起':'#D97706','已上线':'#059669','已完成':'#059669','已关闭':'#059669',
  '已拒绝':'#DC2626','流程终止':'#DC2626','已取消':'#64748B','重新打开':'#EA580C','新建':'#64748B',
}
export const defectStatusOptions: StatusOption[] = ['新建','已确认','修复中','已解决','待验证','已关闭','重新打开','已拒绝'].map(value=>({value,label:value,color:tones[value]}))
export function stateInfo(value: any, definitions: RequirementState[] = []) {
  // 优先工作项服务端快照，其次当前项目配置；硬编码 tones/category 仅兼容旧数据展示。
  // 状态能否迁移必须询问服务端工作流，不能把这里的类别推断当成迁移许可。
  const key = typeof value === 'string' ? value : String(value?.status || '')
  const definition = definitions.find(item=>item.key===key)
  return { key, name: value?.statusName || definition?.name || key, system: value?.statusSystem ?? definition?.system ?? !!tones[key], color: value?.statusColor || definition?.color || tones[key] || '#64748B', category: value?.statusCategory || definition?.category || (['已完成','已上线','已关闭'].includes(key)?'done':['已拒绝','已取消','流程终止'].includes(key)?'cancelled':['草稿','规划中','评审中','待开发','新建'].includes(key)?'todo':'doing') }
}
export function statusLabel(value: any, definitions: RequirementState[], translate: (text:string)=>string): string { const info=stateInfo(value,definitions);return info.system&&info.name===info.key?translate(info.name):info.name }
// 自定义状态名即使恰好等于某个中文系统词，也保持原文，不进行二次翻译。
export function statusOptionLabel(option: StatusOption, translate: (text:string)=>string) { return option.custom?option.label:translate(option.label) }
// 内置状态采用一致的展示语义，修正历史“规划中=完成绿色”的混淆。
// 不改服务端快照、配置、分类或权限；显式自定义状态（即使同名）仍尊重配置。
export function workflowColor(value: any, definitions: RequirementState[] = []): string {
  const info = stateInfo(value, definitions)
  return info.system && tones[info.key] ? tones[info.key]! : info.color
}
export function workflowStyle(value: any, definitions: RequirementState[] = []) { return tagStyle(workflowColor(value,definitions)) }
export function workflowOptions(definitions: RequirementState[], observed: string[] = []): StatusOption[] {
  // 历史状态仍可显示/筛选，不因目录停用而抹去；可选过滤项不等于可迁移目标。
  const values:StatusOption[] = [...definitions].sort((a,b)=>a.sortOrder-b.sortOrder||a.id-b.id).map(item=>({value:item.key,label:item.name,color:workflowColor(item.key,definitions),custom:!item.system||item.key!==item.name}))
  for(const key of observed) if(key&&!values.some(item=>item.value===key)) values.push({value:key,label:key,color:tones[key]||'#64748B',custom:!tones[key]})
  return values
}
export function normalizeStatusSelection(value: unknown): string[] { return Array.isArray(value)?[...new Set(value.filter((item):item is string=>typeof item==='string'&&!!item))].slice(0,100):[] }
export function matchesSelectedStatuses(status: string, selected: string[]) { return !selected.length || selected.includes(status) }
// 同一状态筛选内为 OR，未选状态表示不限制；与其它字段条件在列表层组合为 AND。
