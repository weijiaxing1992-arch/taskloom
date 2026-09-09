// 列、过滤器和排序共用此白名单。key/value 参与业务比较，label 可本地化，二者不可混用。
// 混合迭代列表的自定义字段带对象类型前缀，防止需求和缺陷同 key 相互覆盖。
export type WorkFieldKind = 'text' | 'number' | 'date' | 'boolean' | 'person' | 'multi'
export type WorkFilter = { field: string; operator: string; value?: string | number | boolean }
export type WorkField = { key: string; label: string; kind: WorkFieldKind; group?: string; options?: { value: string; label: string }[]; systemOptions?: boolean; custom?: boolean; width?: number; default?: boolean; fixed?: boolean }
export const workRoleFields = [
  ['frontend','前端开发难度','前端工程师'], ['backend','后端难度','后端工程师'], ['ui','UI 难度','UI 工程师'], ['algorithm','算法难度','算法工程师'], ['product','产品难度','产品负责人'],
] as const
const core: WorkField[] = [
  { key:'code',label:'编号',kind:'text',fixed:true,width:115 }, { key:'title',label:'标题',kind:'text',fixed:true,width:300 },
  { key:'objectType',label:'工作项类型',kind:'text',default:true,options:[{value:'requirement',label:'需求'},{value:'defect',label:'缺陷'}],systemOptions:true },
  { key:'type',label:'需求类型',kind:'text' }, { key:'category',label:'分类',kind:'text' },
  { key:'status',label:'状态',kind:'text',default:true }, { key:'priority',label:'优先级',kind:'text',default:true,options:['P0','P1','P2','P3'].map(value=>({value,label:value})) },
  { key:'sprint',label:'迭代',kind:'text' }, { key:'owner',label:'产品负责人',kind:'person' }, { key:'assignee',label:'处理人',kind:'person',default:true,width:190 },
  { key:'discipline',label:'职能',kind:'text',options:Object.entries({product:'产品',frontend:'前端',backend:'后端',algorithm:'算法',ui:'UI',qa:'测试'}).map(([value,label])=>({value,label})),systemOptions:true },
  ...workRoleFields.flatMap(([key,label,person]):WorkField[]=>[{key:`role.${key}.userId`,label:person,kind:'person',group:'角色权重',width:170},{key:`role.${key}.value`,label,kind:'number',group:'角色权重',default:true,width:130}]),
  { key:'weightTotal',label:'总权重',kind:'number',default:true,group:'角色权重' },
  { key:'tags',label:'标签',kind:'multi',width:180 }, { key:'remarks',label:'备注',kind:'text',width:230 },
  { key:'description',label:'需求描述',kind:'text',width:260 }, { key:'acceptance',label:'验收标准',kind:'text',width:230 },
  { key:'parentId',label:'父需求',kind:'number' }, { key:'progress',label:'进度',kind:'number' },
  { key:'estimatedHours',label:'预估工时',kind:'number' }, { key:'actualHours',label:'实际工时',kind:'number' },
  { key:'sensitive',label:'涉及敏感数据',kind:'boolean' }, { key:'authImpact',label:'涉及权限认证',kind:'boolean' },
  { key:'startDate',label:'计划开始',kind:'date' }, { key:'endDate',label:'计划结束',kind:'date' },
  { key:'createdAt',label:'创建时间',kind:'date',width:180 }, { key:'updatedAt',label:'更新时间',kind:'date',width:180 },
]
const defectFields: WorkField[] = [
  {key:'severity',label:'严重程度',kind:'text'}, {key:'verifier',label:'验证人',kind:'text'}, {key:'requirementId',label:'关联需求',kind:'number'},
  {key:'steps',label:'复现步骤',kind:'text'}, {key:'actual',label:'实际结果',kind:'text'}, {key:'expected',label:'预期结果',kind:'text'},
  {key:'environment',label:'环境',kind:'text'}, {key:'foundVersion',label:'发现版本',kind:'text'}, {key:'fixVersion',label:'修复版本',kind:'text'},
]
export function workItemFields(definitions:any[]=[], members:any[]=[], mixed=false):WorkField[] {
  // 人员候选以稳定用户 ID 为值，部门约束来自真实 departmentIds；姓名只显示。
  // 可选候选不等于授权：保存时服务端仍应校验活动成员、项目和字段部门约束。
  const people=members.map(member=>({value:member.id,label:member.name+' · '+(member.department||member.email||member.id)}))
  // 迭代默认顺序：编号、标题、创建时间；不覆盖用户已经保存的列顺序。
  // 恢复默认与首次加载共用此字段表，创建时间仍可隐藏、移动。
  const base=mixed?[...core.slice(0,2),{...core.find(field=>field.key==='createdAt')!,default:true},...core.slice(2).filter(field=>field.key!=='createdAt')]:core.filter(field=>field.key!=='objectType')
  return [...base.map(field=>({...field,group:field.group||'基础信息',options:field.kind==='person'?people:field.options})),...(mixed?defectFields.map(field=>({...field,group:'缺陷字段'})):[]),
    ...definitions.filter(definition=>definition.enabled!==false).map(definition=>({
      key:'cf.'+(mixed?definition.objectType+'.':'')+definition.key,label:definition.name,kind:({number:'number',date:'date',boolean:'boolean',multi_select:'multi',users:'multi'} as Record<string,WorkFieldKind>)[definition.type]||'text',
      options:['user','users'].includes(definition.type)?members.filter(member=>member.active!==false&&(!definition.departmentId||member.departmentIds?.includes(definition.departmentId))).map(member=>({value:member.id,label:member.name+' · '+(member.departmentNames?.join('、')||member.email||member.id)})):(definition.options||[]).map((value:string)=>({value,label:value})),
      group:mixed?(definition.objectType==='defect'?'缺陷自定义字段':'需求自定义字段'):'自定义字段',custom:true,default:definition.listVisible,width:160,
    }))]
}
export const operatorLabels:Record<string,string>={eq:'等于',neq:'不等于',contains:'包含文本',not_contains:'不包含文本',gt:'大于',gte:'大于或等于',lt:'小于',lte:'小于或等于',includes:'包含成员或选项',not_includes:'不包含成员或选项',is_empty:'为空',not_empty:'不为空'}
export function workOperators(field:WorkField):string[]{return [...(field.kind==='number'||field.kind==='date'?['eq','neq','gt','gte','lt','lte']:field.kind==='boolean'?['eq','neq']:['person','multi'].includes(field.kind)?['includes','not_includes']:field.options?.length?['eq','neq']:['contains','not_contains','eq','neq']),'is_empty','not_empty']}
export function workFilterValue(field:WorkField,operator:string,value:unknown):WorkFilter['value']{
  // 空值判断操作不携带 value；数值 0、布尔 false 都是有效条件，不能用 truthy 判断。
  if(['is_empty','not_empty'].includes(operator))return undefined
  if(field.kind==='number'){if((typeof value==='string'&&!value.trim())||value==null||!Number.isFinite(Number(value)))throw new Error('请输入有效数值');return Number(value)}
  if(field.kind==='boolean'){if(value!==true&&value!==false&&value!=='true'&&value!=='false')throw new Error('请选择是或否');return value===true||value==='true'}
  if(typeof value!=='string'||!value.trim())throw new Error('请输入筛选值')
  if(field.kind==='date'&&(!/^\d{4}-\d{2}-\d{2}$/.test(value)||!Number.isFinite(Date.parse(value))||new Date(value).toISOString().slice(0,10)!==value))throw new Error('请选择有效日期')
  return value
}
function personIDs(item:any,kind:string):string[]{const values=item[kind+'UserIds'];return Array.isArray(values)&&values.length?[...new Set(values)]:(item[kind+'UserId']?[item[kind+'UserId']]:item[kind]?['legacy:'+item[kind]]:[])}
// 兼容多人数组、旧单 ID 和历史姓名；legacy: 仅供显示/比较，不能当成真实用户 ID 提交。
export function workItemValue(item:any,key:string):any {
  if(key.startsWith('cf.')){const parts=key.split('.');return parts.length===3?(parts[1]===item.objectType?item.customFields?.[parts[2]!]:null):item.customFields?.[key.slice(3)]}
  if(key.startsWith('role.')){const [,role,property]=key.split('.'),weight=item.roleWeights?.[role!];return property==='userId'?(weight?.userIds?.length?weight.userIds:weight?.userId?[weight.userId]:[]):weight?.value}
  if(key==='assignee'||key==='owner')return personIDs(item,key)
  if(key==='tags')return String(item.tags||'').split(/[,，\n]/).map(value=>value.trim()).filter(Boolean)
  return item[key]
}
export function workItemPersonNames(item:any,key:string,members:any[]=[]):string[]{
  const ids=workItemValue(item,key),snapshots=key==='assignee'?item.assignees:key==='owner'?item.owners:[]
  return Array.isArray(ids)&&ids.length?ids.map(id=>members.find(member=>member.id===id)?.name||snapshots?.find((person:any)=>person.id===id)?.name||id.replace(/^legacy:/,'')):(key==='assignee'||key==='owner')&&item[key]?[item[key]]:[]
}
export function emptyWorkValue(value:any):boolean{return value==null||value===''||(Array.isArray(value)&&!value.length)}
function scalar(value:any,kind:WorkFieldKind){return kind==='date'?String(value).slice(0,10):kind==='text'?String(value).toLowerCase():Array.isArray(value)?value.join('\u0000'):value}
export function workCalendarDate(value:any,timezone='UTC'):string{
  // 计划日期 YYYY-MM-DD 不做时区平移；创建时间等时间戳按账号时区归属自然日。
  const text=String(value);if(/^\d{4}-\d{2}-\d{2}$/.test(text))return text
  const date=new Date(text);if(!Number.isFinite(date.getTime()))return text.slice(0,10)
  const parts=new Intl.DateTimeFormat('en-US',{timeZone:timezone,year:'numeric',month:'2-digit',day:'2-digit'}).formatToParts(date)
  return ['year','month','day'].map(type=>parts.find(part=>part.type===type)?.value).join('-')
}
export function matchesWorkFilter(item:any,rule:WorkFilter,field:WorkField,timezone='UTC'):boolean{
  const value=workItemValue(item,rule.field),empty=emptyWorkValue(value)
  if(rule.operator==='is_empty')return empty
  if(rule.operator==='not_empty')return !empty
  if(empty)return false
  if(rule.operator==='includes'||rule.operator==='not_includes'){const has=Array.isArray(value)&&value.includes(rule.value);return rule.operator==='includes'?has:!has}
  const actual=field.kind==='date'?workCalendarDate(value,timezone):scalar(value,field.kind),expected=scalar(rule.value,field.kind)
  switch(rule.operator){case'eq':return actual===expected;case'neq':return actual!==expected;case'contains':return String(actual).includes(String(expected));case'not_contains':return !String(actual).includes(String(expected));case'gt':return actual>expected;case'gte':return actual>=expected;case'lt':return actual<expected;case'lte':return actual<=expected;default:return false}
}
export function queryWorkItems(items:any[],fields:WorkField[],rules:WorkFilter[],sortKey:string,order:string,members:any[]=[],timezone='UTC'):any[]{
  // 多个字段条件为 AND；未知字段直接不匹配，避免字段删除后静默扩大结果。
  // 这是当前已授权数据的本地查询；服务端分页列表需服务端完成同口径过滤后再分页。
  const registry=new Map(fields.map(field=>[field.key,field])),sortField=registry.get(sortKey)
  const result=items.filter(item=>rules.every(rule=>{const field=registry.get(rule.field);return !!field&&matchesWorkFilter(item,rule,field,timezone)}))
  return result.sort((left,right)=>{
    const a=sortKey==='code'?left.id:sortField?.kind==='person'?workItemPersonNames(left,sortKey,members):workItemValue(left,sortKey),b=sortKey==='code'?right.id:sortField?.kind==='person'?workItemPersonNames(right,sortKey,members):workItemValue(right,sortKey)
    if(emptyWorkValue(a)!==emptyWorkValue(b))return emptyWorkValue(a)?1:-1
    let compared=0
    if(!emptyWorkValue(a)){const kind=sortKey==='code'?'number':sortField?.kind||'text',x=kind==='date'?String(a):scalar(a,kind),y=kind==='date'?String(b):scalar(b,kind);compared=x<y?-1:x>y?1:0}
    return compared*(order==='asc'?1:-1)||String(left.objectType||'').localeCompare(String(right.objectType||''))||left.id-right.id
  })
}
