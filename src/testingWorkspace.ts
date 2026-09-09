/** 测试协作的前端数据契约。目录使用稳定 ID；名称只用于展示，不承担关联身份。 */
export type TestLibrary = { id: number; name: string; isDefault: boolean; count: number }
export type TestFolder = { id: number; libraryId: number; parentId: number | null; name: string; sortOrder: number; count: number }
export type TestMetadata = { description: string; testData: string; estimatedMinutes: number; libraryId: number | null; folderId: number | null }
export type TestTemplateField = { key: string; name: string; description: string; required: boolean; listVisible: boolean; defaultValue: unknown; enabled: boolean }
export type TestSettings = { version: number; fields: TestTemplateField[]; blockedEnabled: boolean; aiReviewRules: string[]; aiLogicRules: string[]; businessContext: string; canManage?: boolean }
export type TestWorkspace = { libraries: TestLibrary[]; folders: TestFolder[]; locations: {caseId:number;libraryId:number;folderId:number|null}[]; settings: TestSettings; canEdit: boolean; canManage: boolean; designs: TestDesign[] }
export type TestPoint = { id?: number|string; title: string; category: string; priority: string; caseIds: number[] }
export type TestDesign = { id: number; name: string; requirementId: number|null; description: string; ownerUserId: string; tags: string; points: TestPoint[]; updatedAt?: string }
export type CaseStep = { order: number; action: string; expected: string }
export type TestCaseRecord = { id: number; code: string; title: string; category: string; preconditions: string; steps: string; expected: string; status: string; caseType: string; priority: string; owner: string; ownerUserId?: string; enabled: boolean; tags: string; requirementId: number|null; requirement?: {id:number;code:string;title:string;updatedAt?:string}|null; stepsDetail: CaseStep[]; metadata?: TestMetadata; customFields?:Record<string,unknown>; updatedAt:string }
export const testCaseTypes = ['功能测试','接口测试','兼容性测试','安全测试','性能测试','自动化测试']
export const testCaseStates = ['草稿','待评审','已通过','已废弃']
/**
 * 表单仅接收其声明的字段。详情响应中的 ID、统计和展开关联不能残留到下一份
 * 新建草稿，也不能被整包回传；null 保留其“清空关联”的业务含义。
 */
export function testingEditable<T extends object>(defaults:T, source:Partial<T>|null):T {
  return Object.fromEntries(Object.entries(defaults).map(([key,fallback])=>[
    key, source && Object.prototype.hasOwnProperty.call(source,key) && (source as Record<string,unknown>)[key]!==undefined
      ? (source as Record<string,unknown>)[key] : fallback,
  ])) as T
}
export function testTone(value: string) { return ({'功能测试':'blue','接口测试':'teal','兼容性测试':'purple','安全测试':'red','性能测试':'orange','自动化测试':'indigo','已通过':'green','通过':'green','待评审':'orange','草稿':'neutral','已废弃':'neutral','失败':'red','阻塞':'orange','未执行':'neutral','P0':'red','P1':'orange','P2':'blue','P3':'neutral'} as Record<string,string>)[value] || 'neutral' }
export function testPositiveID(value: unknown): number|null { const id = typeof value === 'string' && /^[1-9]\d*$/.test(value) ? Number(value) : value; return typeof id==='number' && Number.isSafeInteger(id) && id>0 ? id : null }
/** 限制遍历到当前库，且记录已访问 ID，历史错误父节点不会导致递归死循环。 */
export function folderRows(folders: TestFolder[], libraryId: number, expanded: Set<number>, query='') {
  const scoped=folders.filter(x=>x.libraryId===libraryId), byId=new Map(scoped.map(x=>[x.id,x]));
  const visible=new Set<number>(), needle=query.trim().toLocaleLowerCase();
  if(needle) for(const item of scoped) if(item.name.toLocaleLowerCase().includes(needle)) { let cursor:TestFolder|undefined=item;const seen=new Set<number>();while(cursor&&!seen.has(cursor.id)){seen.add(cursor.id);visible.add(cursor.id);cursor=byId.get(cursor.parentId||-1)} }
  const out:(TestFolder&{depth:number;hasChildren:boolean})[]=[],visited=new Set<number>();
  function walk(parentId:number|null,depth:number){for(const item of scoped.filter(x=>(x.parentId||null)===parentId).sort((a,b)=>a.sortOrder-b.sortOrder||a.id-b.id)){if(visited.has(item.id))continue;visited.add(item.id);if(needle&&!visible.has(item.id))continue;out.push({...item,depth,hasChildren:scoped.some(x=>x.parentId===item.id)});if(needle||expanded.has(item.id))walk(item.id,depth+1)}}
  walk(null,0);return out;
}
export function caseStepMove(steps:CaseStep[], index:number, delta:number){const target=index+delta;if(index<0||target<0||target>=steps.length)return steps;const next=steps.map(s=>({...s}));const item=next.splice(index,1)[0]!;next.splice(target,0,item);return next.map((s,i)=>({...s,order:i+1}))}
