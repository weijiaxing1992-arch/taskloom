import { cloneReleaseNotes, releaseEntriesError, releaseImagePath, releaseNoteCategories, type ReleaseNoteEntry, type ReleaseNoteImage, type ReleaseNoteSource } from './releaseNotes'

export type ReleaseNotesCenterImage = ReleaseNoteImage & { sha256:string; contentType:'image/png'|'image/jpeg'|'image/gif' }
export type ReleaseNotesCenterRequirement = {
  id:number; code:string; title:string; type:string; category:string; status:string; description:string; acceptance:string; updatedAt:string; images:ReleaseNotesCenterImage[]
}
export type ReleaseNotesCenterSummary = {
  sprintId:number; sprintCode:string; versionName:string; releaseDate:string; revision:number; state:string; createdAt:string; updatedAt:string; requirementCount:number; entryCount:number; selectedImageCount:number
}
export type ReleaseNotesCenterList = { projectId:string; items:ReleaseNotesCenterSummary[]; total:number; page:number; pageSize:number }
export type ReleaseNotesCenterDetail = {
  projectId:string; sprintId:number; sprintCode:string; versionName:string; releaseDate:string; revision:number; state:string; createdAt:string; updatedAt:string; snapshot:true; categories:string[]; entries:ReleaseNoteEntry[]; requirements:ReleaseNotesCenterRequirement[]; markdown:string
}

const id=(value:unknown):value is number=>Number.isSafeInteger(value)&&Number(value)>0
const count=(value:unknown):value is number=>Number.isSafeInteger(value)&&Number(value)>=0
const text=(value:unknown):value is string=>typeof value==='string'
const date=(value:unknown):value is string=>text(value)&&Number.isFinite(Date.parse(value))
const defect=(value:string)=>/缺陷|(?:^|[^a-z])(?:bugs?|defects?|bugfix(?:es)?|defectfix(?:es)?)(?:$|[^a-z])/iu.test(value)
const invalid=()=>{throw Error('升级日志返回格式不正确，请刷新重试')}
const exactCategories=(value:unknown)=>Array.isArray(value)&&JSON.stringify(value)===JSON.stringify(releaseNoteCategories)

function source(requirements:ReleaseNotesCenterRequirement[]):ReleaseNoteSource[]{return requirements.map(item=>({id:item.id,code:item.code,title:item.title}))}
function image(image:unknown, requirementId:number):image is ReleaseNotesCenterImage{
  const value=image as ReleaseNotesCenterImage
  if(!value||!id(value.id)||value.requirementId!==requirementId||!text(value.name)||!text(value.sha256)||!validMime(value.contentType)||!text(value.url))return false
  try{return releaseImagePath(value)===`/requirements/${requirementId}/attachments/${value.id}`}catch{return false}
}
function validMime(value:unknown):value is ReleaseNotesCenterImage['contentType']{return value==='image/png'||value==='image/jpeg'||value==='image/gif'}
function requirement(value:unknown):value is ReleaseNotesCenterRequirement{
  const item=value as ReleaseNotesCenterRequirement
  return !!item&&id(item.id)&&['code','title','type','category','status','description','acceptance','updatedAt'].every(key=>text(item[key as keyof ReleaseNotesCenterRequirement]))&&date(item.updatedAt)&&!defect(item.type)&&!defect(item.category)&&Array.isArray(item.images)&&item.images.every(candidate=>image(candidate,item.id))&&new Set(item.images.map(candidate=>candidate.id)).size===item.images.length
}
function summary(value:unknown):value is ReleaseNotesCenterSummary{
  const item=value as ReleaseNotesCenterSummary
  return !!item&&id(item.sprintId)&&['sprintCode','versionName','state','createdAt','updatedAt'].every(key=>text(item[key as keyof ReleaseNotesCenterSummary]))&&date(item.releaseDate)&&date(item.createdAt)&&date(item.updatedAt)&&Number.isSafeInteger(item.revision)&&item.revision>0&&['requirementCount','entryCount','selectedImageCount'].every(key=>count(item[key as keyof ReleaseNotesCenterSummary]))
}

export function parseReleaseNotesCenterList(value:unknown):ReleaseNotesCenterList{
  const data=value as ReleaseNotesCenterList
  if(!data||!text(data.projectId)||!Array.isArray(data.items)||!data.items.every(summary)||!count(data.total)||!Number.isSafeInteger(data.page)||data.page<1||!Number.isSafeInteger(data.pageSize)||data.pageSize<1||data.pageSize>100||data.items.length>data.pageSize||data.total<data.items.length)invalid()
  return cloneReleaseNotes(data)
}

export function parseReleaseNotesCenterDetail(value:unknown):ReleaseNotesCenterDetail{
  const data=value as ReleaseNotesCenterDetail
  if(!data||!text(data.projectId)||!id(data.sprintId)||!['sprintCode','versionName','state','createdAt','updatedAt','markdown'].every(key=>text(data[key as keyof ReleaseNotesCenterDetail]))||!date(data.releaseDate)||!date(data.createdAt)||!date(data.updatedAt)||!Number.isSafeInteger(data.revision)||data.revision<1||data.snapshot!==true||!exactCategories(data.categories)||!Array.isArray(data.entries)||!Array.isArray(data.requirements)||!data.requirements.every(requirement)||new Set(data.requirements.map(item=>item.id)).size!==data.requirements.length)invalid()
  const problem=releaseEntriesError(data.entries,data.requirements.flatMap(item=>item.images),source(data.requirements))
  if(problem)throw Error(problem)
  const selected=data.entries.reduce((sum,entry)=>sum+entry.imageIds.length,0)
  if(selected>data.requirements.length*8)invalid()
  return cloneReleaseNotes(data)
}

export function releaseNotesCenterGroups(detail:ReleaseNotesCenterDetail){
  return releaseNoteCategories.map(category=>({category,entries:detail.entries.filter(entry=>entry.category===category).sort((left,right)=>(left.requirementIds[0]||0)-(right.requirementIds[0]||0))}))
}
export function releaseNotesCenterRequirement(detail:ReleaseNotesCenterDetail,id:number){return detail.requirements.find(item=>item.id===id)}
export function releaseNotesCenterImages(detail:ReleaseNotesCenterDetail,entry:ReleaseNoteEntry){const requirement=releaseNotesCenterRequirement(detail,entry.requirementIds[0]||0);return requirement?.images.filter(image=>entry.imageIds.includes(image.id))||[]}
