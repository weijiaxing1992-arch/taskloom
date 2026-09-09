export type ClipboardPerson = { id?: string; name: string }
export type ClipboardMember = { id: string; name: string }
export type ClipboardRequirement = {
  id: number | string; code?: string; title?: string; projectId?: string; objectType?: string; type?: string; createdAt?: string
  assignee?: string; assigneeUserId?: string; assigneeUserIds?: string[]; assignees?: ClipboardPerson[]
  owner?: string; ownerUserId?: string; ownerUserIds?: string[]; owners?: ClipboardPerson[]
  roleWeights?: Record<string, { userId?: string; userIds?: string[]; userName?: string; users?: ClipboardPerson[]; value?: number | null }>
}
type Person = ClipboardPerson & { unresolved?: boolean }
export const clipboardRoles = ['frontend', 'backend', 'algorithm', 'ui', 'product'] as const
const labels = {frontend:'前端工程师',backend:'后端工程师',algorithm:'算法工程师',ui:'UI 工程师',product:'产品经理'}
const ids = (many: unknown, one?: string): string[] => Array.isArray(many) ? [...new Set(many.filter((value):value is string=>typeof value==='string'&&!!value))] : one ? [one] : []
function people(selected: string[], snapshots: ClipboardPerson[] = [], legacy = '', members: ClipboardMember[] = []): Person[] {
  if (!selected.length) return snapshots.length ? snapshots.map(person=>({...person})) : legacy ? [{name:legacy}] : []
  return selected.map((id,index)=>{
    const name = snapshots.find(person=>person.id===id)?.name || members.find(person=>person.id===id)?.name || (index===0 ? legacy : '')
    return {id,name:name||id,unresolved:!name}
  })
}
function unique(people: Person[]): Person[] {
  const seen = new Set<string>()
  return people.filter(person=>{const key=person.id?'id:'+person.id:'name:'+person.name;if(seen.has(key))return false;seen.add(key);return true})
}
export function clipboardPeople(item: ClipboardRequirement, members: ClipboardMember[] = []): Record<string, Person[]> {
  const result:Record<string,Person[]> = {assignee:people(ids(item.assigneeUserIds,item.assigneeUserId),item.assignees,item.assignee,members)}
  for (const key of clipboardRoles) {
    const role=item.roleWeights?.[key]
    result[key]=people(ids(role?.userIds,role?.userId),role?.users,role?.userName,members)
  }
  result.product=unique([...people(ids(item.ownerUserIds,item.ownerUserId),item.owners,item.owner,members),...result.product!])
  return result
}
export function clipboardNeedsMembers(item: ClipboardRequirement, members: ClipboardMember[] = []): boolean {
  return Object.values(clipboardPeople(item,members)).some(people=>people.some(person=>person.unresolved))
}
export function completeClipboardRequirement(item: ClipboardRequirement): boolean {
  return !!item && Number.isSafeInteger(Number(item.id)) && Number(item.id)>0 && typeof item.code==='string' && !!item.code && typeof item.title==='string'
    && typeof item.createdAt==='string' && /T\d{2}:\d{2}/.test(item.createdAt) && Number.isFinite(Date.parse(item.createdAt))
    && (Array.isArray(item.assignees)||Array.isArray(item.assigneeUserIds)) && (Array.isArray(item.owners)||Array.isArray(item.ownerUserIds))
    && !!item.roleWeights && clipboardRoles.every(role=>Object.hasOwn(item.roleWeights!,role))
}
export function requirementClipboard(item: ClipboardRequirement, members: ClipboardMember[] = [], options: {translate?:(value:string,params?:Record<string,string|number>)=>string;formatDate?:(value:string)=>string} = {}): string {
  const t=options.translate||((value:string,params:Record<string,string|number>={})=>value.replace(/\{(\w+)\}/g,(all,key)=>String(params[key]??all)))
  const participants=clipboardPeople(item,members)
  const names=(people:Person[])=>people.length?people.map(person=>person.unresolved?t('姓名暂不可用（{id}）',{id:person.id||person.name}):people.filter(other=>other.name===person.name).length>1&&person.id?`${person.name}（${person.id}）`:person.name).join('、'):t('未分配')
  const date=item.createdAt||''
  const created=options.formatDate&&date?options.formatDate(date):date&&Number.isFinite(Date.parse(date))?new Date(date).toISOString():date||t('创建时间暂不可用')
  const rows:[string,string][]=[['需求编号',item.code||String(item.id)],['标题',item.title||''],['处理人',names(participants.assignee!)],...clipboardRoles.map(key=>[labels[key],names(participants[key]!)] as [string,string]),['创建时间',created]]
  // Never translate business names/titles, substitute a current time, or include
  // unrelated custom fields, descriptions, member-directory entries or secrets.
  return rows.map(([label,value])=>`${t(label)}：${value.replace(/\r\n?/g,'\n').replace(/\n/g,'\n  ')}`).join('\n')
}
