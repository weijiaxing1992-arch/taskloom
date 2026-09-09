export interface RecentMemberStorage { getItem(key: string): string | null; setItem(key: string, value: string): void; removeItem(key: string): void }
export const recentMemberLimit = 20
// 最近人员按“已验证租户 + 有效账号 + 项目”隔离；身份尚未一致时返回空范围并停止缓存。
export function recentMemberScope(verifiedScope: string, session: { tenant?: { id?: string }; user?: { id?: string }; project?: { id?: string } } | null): string {
  const tenant=session?.tenant?.id,user=session?.user?.id,project=session?.project?.id
  return tenant&&user&&project&&verifiedScope===`${encodeURIComponent(tenant)}:${encodeURIComponent(user)}` ? `${verifiedScope}:${encodeURIComponent(project)}` : ''
}
export function recentMemberKey(scope: string): string | null { return scope && scope.length<=1000 ? 'devflow-recent-members:v1:'+scope : null }
export function normalizeRecentMembers(value: unknown): string[] { return Array.isArray(value)?[...new Set(value.filter((id):id is string=>typeof id==='string'&&id.length>0&&id.length<=160&&!/[\s\x00-\x1f]/.test(id)))].slice(0,recentMemberLimit):[] }
export function readRecentMembers(storage: RecentMemberStorage | undefined, scope: string): string[] {
  const key=recentMemberKey(scope);if(!key||!storage)return[]
  try{return normalizeRecentMembers(JSON.parse(storage.getItem(key)||'[]'))}catch{return[]}
}
export function rememberMembers(storage: RecentMemberStorage | undefined, scope: string, added: string[], eligible: readonly {id:string}[]): string[] {
  // 只记显式选择且在当前候选集中的 ID，名称与部门每次从当前目录解析，避免缓存过时资料。
  const key=recentMemberKey(scope);if(!key||!storage)return[]
  const allowed=new Set(eligible.map(member=>member.id)),safe=normalizeRecentMembers(added).filter(id=>allowed.has(id))
  if(!safe.length)return readRecentMembers(storage,scope)
  const next=normalizeRecentMembers([...safe,...readRecentMembers(storage,scope)])
  try{storage.setItem(key,JSON.stringify(next))}catch{/* 缓存不可写时仍允许本次人员选择成功。 */}
  return next
}
export function clearRecentMembers(storage: RecentMemberStorage | undefined, scope: string) { const key=recentMemberKey(scope);if(key)try{storage?.removeItem(key)}catch{/* 最近选择只是可选的本地便利功能。 */} }
// 浮层基于可视窗口计算可用高度，不受抽屉 overflow 裁剪；下方不足 180px 时优先向上展开。
export function memberPanelGeometry(rect: {left:number;top:number;bottom:number;width:number}, viewport: {width:number;height:number;left?:number;top?:number}) {
  const edge=10,gap=5,left=viewport.left||0,top=viewport.top||0,width=Math.max(0,viewport.width),height=Math.max(0,viewport.height)
  const panelWidth=Math.min(Math.max(rect.width,Math.min(320,width-2*edge)),Math.max(0,width-2*edge))
  const below=Math.max(0,top+height-edge-rect.bottom-gap),above=Math.max(0,rect.top-top-edge-gap),up=below<180&&above>below
  return {left:Math.max(left+edge,Math.min(rect.left,left+width-panelWidth-edge)),width:panelWidth,maxHeight:up?above:below,top:up?Math.max(top+edge,rect.top-gap-above):rect.bottom+gap,bottom:up?rect.top-gap:null,up}
}
