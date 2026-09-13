export type TopSearchType = '项目' | '需求' | '迭代' | '缺陷' | '测试用例' | '测试计划'
export interface TopSearchItem { id:string|number; type:TopSearchType; title:string; code:string; projectId:string; projectName:string; status:string; statusName?:string; statusSystem?:boolean; statusColor?:string; snippet:string; updatedAt:string }
const types = new Set<TopSearchType>(['项目','需求','迭代','缺陷','测试用例','测试计划'])
export function searchItemsInProject(value:unknown, projectId:string):TopSearchItem[] {
  if (!Array.isArray(value)) throw new Error('搜索结果格式不正确，请重试')
  return value.slice(0,12).map(item => {
    if (!item || !types.has(item.type) || String(item.projectId)!==projectId || typeof item.title!=='string' || (item.type==='项目' ? String(item.id)!==projectId : !Number.isSafeInteger(Number(item.id))||Number(item.id)<1)) throw new Error('搜索结果格式不正确，请重试')
    return { id:item.type==='项目'?String(item.id):Number(item.id),type:item.type,title:item.title,code:typeof item.code==='string'?item.code:'',projectId,projectName:typeof item.projectName==='string'?item.projectName:'',status:typeof item.status==='string'?item.status:'',statusName:typeof item.statusName==='string'?item.statusName:undefined,statusSystem:typeof item.statusSystem==='boolean'?item.statusSystem:undefined,statusColor:typeof item.statusColor==='string'&&/^#[0-9a-f]{6}$/i.test(item.statusColor)?item.statusColor:undefined,snippet:typeof item.snippet==='string'?item.snippet:'',updatedAt:typeof item.updatedAt==='string'?item.updatedAt:'' }
  })
}
export function searchDisplayCode(item: {type?:string;id?:string|number;code?:string}): string {
  const id=Number(item.id)
  return item.type==='需求'&&Number.isSafeInteger(id)&&id>0&&id<=999999?String(id).padStart(6,'0'):item.code||''
}
export function searchStatusLabel(item: Pick<TopSearchItem,'status'|'statusName'|'statusSystem'>,translate:(key:string)=>string):string {
  if(item.statusName&&item.statusSystem===false)return item.statusName
  const names:Record<string,string>={active:'进行中',archived:'已归档'}
  return translate(item.statusName||names[item.status]||item.status)
}
/** Never navigate an arbitrary URL returned in search data. */
export function topSearchTarget(item:TopSearchItem, currentPath:string, currentQuery:Record<string,unknown> = {}):{path:string;query:Record<string,any>} {
  if(item.type==='需求') {
    if(currentPath==='/requirements'||currentPath==='/iterations') {
      const query={...currentQuery,req:Number(item.id)}
      for(const key of ['bug','case','plan','execution','createChild','parentId'])delete (query as Record<string,unknown>)[key]
      return{path:currentPath,query}
    }
    return{path:'/requirements',query:{req:Number(item.id)}}
  }
  if(item.type==='缺陷')return{path:'/defects',query:{bug:Number(item.id)}}
  if(item.type==='迭代')return{path:'/iterations',query:{sprint:Number(item.id)}}
  if(item.type==='测试用例')return{path:'/tests',query:{tab:'cases',case:Number(item.id)}}
  if(item.type==='测试计划')return{path:'/tests',query:{tab:'plans',plan:Number(item.id)}}
  return{path:'/projects',query:{project:item.projectId}}
}
