export interface OrganizationRole { key:string; name:string }
export interface OrganizationProject { id:string; name:string; code:string; status:string }
export interface OrganizationContext {
  organization:{id:string;name:string}; permissions:string[]; isTenantAdmin:boolean; initialPasswordConfigured?:boolean
  counts:{members:number;activeMembers:number;departments:number;groups:number;pendingApplications:number}
  roles:OrganizationRole[]; projects:OrganizationProject[]
}
export interface Department { id:string; name:string; code:string; parentId:string|null; status:string; sortOrder:number; memberCount:number }
export interface ProjectMembership { projectId:string; role:string }
export interface OrganizationMember {
  id:string; name:string; email:string; employeeNo:string; active:boolean; tenantRole:string; operationDisabled?:boolean
  departmentIds:string[]; primaryDepartmentId:string; projectMemberships:ProjectMembership[]; groupIds:string[]
}
export function permits(context:OrganizationContext|undefined|null, permission:string) {
  return !!context && (context.isTenantAdmin || context.permissions.includes(permission))
}
export function departmentPath(department:Department, departments:Department[]):string {
  const names=[department.name], seen=new Set([department.id]);let parent=department.parentId
  while(parent&&!seen.has(parent)){seen.add(parent);const item=departments.find(item=>item.id===parent);if(!item)break;names.unshift(item.name);parent=item.parentId}
  return names.join(' / ')
}
/** Stable pre-order traversal; orphaned or cyclic historic records remain visible once. */
export function orderedDepartments(departments:Department[], locale='zh-CN'):Department[] {
  const collator=new Intl.Collator(locale==='zh-CN'?'zh-Hans-CN-u-co-pinyin':locale,{numeric:true,sensitivity:'base'})
  const byID=new Map<string,Department>();for(const item of departments)if(!byID.has(item.id))byID.set(item.id,item)
  const compare=(a:Department,b:Department)=>(Number.isFinite(a.sortOrder)?a.sortOrder:0)-(Number.isFinite(b.sortOrder)?b.sortOrder:0)||collator.compare(a.name,b.name)||(a.id<b.id?-1:a.id>b.id?1:0)
  const children=new Map<string,Department[]>(),roots:Department[]=[]
  for(const item of byID.values()){if(!item.parentId||!byID.has(item.parentId)||item.parentId===item.id)roots.push(item);else{const group=children.get(item.parentId)||[];group.push(item);children.set(item.parentId,group)}}
  roots.sort(compare);for(const group of children.values())group.sort(compare)
  const result:Department[]=[],seen=new Set<string>()
  const append=(start:Department)=>{const stack=[start];while(stack.length){const item=stack.pop()!;if(seen.has(item.id))continue;seen.add(item.id);result.push(item);const group=children.get(item.id)||[];for(let index=group.length-1;index>=0;index--)stack.push(group[index]!)}}
  for(const root of roots)append(root);for(const item of [...byID.values()].sort(compare))append(item)
  return result
}
export function orderedDepartmentIds(ids:string[], departments:Department[], locale='zh-CN'):string[] {
  const ranks=new Map(orderedDepartments(departments,locale).map((item,index)=>[item.id,index]))
  return [...new Set(ids)].sort((a,b)=>(ranks.get(a)??Number.MAX_SAFE_INTEGER)-(ranks.get(b)??Number.MAX_SAFE_INTEGER)||(a<b?-1:a>b?1:0))
}
export function memberPrimaryDepartment(member:OrganizationMember, ordered:Department[]):Department|undefined {
  const membership=new Set(member.departmentIds)
  return ordered.find(item=>item.id===member.primaryDepartmentId&&membership.has(item.id))||ordered.find(item=>membership.has(item.id))
}
export function sortOrganizationMembers(members:OrganizationMember[], departments:Department[], locale='zh-CN'):OrganizationMember[] {
  const ordered=orderedDepartments(departments,locale),ranks=new Map(ordered.map((item,index)=>[item.id,index])),collator=new Intl.Collator(locale==='zh-CN'?'zh-Hans-CN-u-co-pinyin':locale,{numeric:true,sensitivity:'base'})
  const ranked=members.map(member=>({member,rank:ranks.get(memberPrimaryDepartment(member,ordered)?.id||'')??Number.MAX_SAFE_INTEGER}))
  return ranked.sort((a,b)=>a.rank-b.rank||collator.compare(a.member.name,b.member.name)||(a.member.id<b.member.id?-1:a.member.id>b.member.id?1:0)).map(item=>item.member)
}
export const organizationSections=[
  {key:'overview',name:'企业概览',icon:'◈'}, {key:'members',name:'成员管理',icon:'♙'},
  {key:'departments',name:'部门管理',icon:'⌘'}, {key:'groups',name:'用户组权限',icon:'◇'},
  {key:'invitations',name:'邀请链接',icon:'↗'}, {key:'applications',name:'加入申请',icon:'✓'},
  {key:'server-monitor',name:'服务器监控',icon:'◌'},
  {key:'delivery',name:'交付与验收',icon:'▤'},
  {key:'wechat-login',name:'微信登录配置',icon:'◉'},
] as const
