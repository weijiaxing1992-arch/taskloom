export const releaseNoteCategories = ['大模型类型', '智能体类型', 'AI呼叫类型', 'CRM/短信/账单', '管理端/代理端', 'API接口', '其他'] as const
export type ReleaseNoteEntry = { category:string; title:string; description:string; requirementIds:number[]; imageIds:number[]; imageCaptions?:Record<string,string> }
export type ReleaseNoteImage = { id:number; requirementId:number; name:string; url:string }
export type ReleaseNoteSource = { id:number; code:string; title:string }
export type ReleaseNotes = {
  state:'none'|'queued'|'generating'|'draft'|'failed'; revision:number; entries:ReleaseNoteEntry[]; categories:string[]; markdown:string;
  canGenerate:boolean; canEdit:boolean; sourceChanged:boolean; configured:boolean; enabled:boolean; autoEnabled:boolean; model:string; baseUrl:string; settingsVersion:number;
  sourceCount:number; error:string; updatedAt:string; sprintName:string; completedAt:string; images:ReleaseNoteImage[]; sources:ReleaseNoteSource[];
}
const id = (value:unknown):value is number => Number.isSafeInteger(value) && Number(value)>0
const ids = (value:unknown):value is number[] => Array.isArray(value)&&value.length<=200&&value.every(id)&&new Set(value).size===value.length
const plainText = (value:unknown,limit:number):value is string => typeof value==='string'&&value.trim()===value&&!!value&&[...value].length<=limit&&!/[\p{Cc}\p{Cf}\u2028\u2029<>]|(?:https?|data|javascript):|\]\(/iu.test(value)
export const releaseNotesPending = (state:string) => state==='queued'||state==='generating'
export function releaseImagePath(image:ReleaseNoteImage):string {
  if(!id(image.id)||!id(image.requirementId)||image.url!==`/api/requirements/${image.requirementId}/attachments/${image.id}`)throw Error('升级日志图片引用无效')
  return image.url.slice(4)
}
export function releaseEntriesError(entries:ReleaseNoteEntry[],images:ReleaseNoteImage[],sources:ReleaseNoteSource[]):string {
  if(!Array.isArray(entries)||entries.length>200)return '升级日志条目格式不正确'
  const seen=new Set<number>()
  for(const entry of entries){
    if(!entry||!releaseNoteCategories.includes(entry.category as typeof releaseNoteCategories[number])||!plainText(entry.title,120)||!plainText(entry.description,2000))return '请填写有效分类、功能标题和说明；标题最多 120 字，说明最多 2000 字，仅支持不含链接的单段纯文本'
    if(!ids(entry.requirementIds)||entry.requirementIds.length!==1||seen.has(entry.requirementIds[0]!)||entry.requirementIds.some(value=>!sources.some(source=>source.id===value))||!ids(entry.imageIds)||entry.imageIds.length>8||entry.imageIds.some(value=>!images.some(image=>image.id===value&&entry.requirementIds.includes(image.requirementId))))return '升级日志来源或截图引用不匹配'
    seen.add(entry.requirementIds[0]!)
    if(entry.imageCaptions!==undefined){
      if(!entry.imageCaptions||typeof entry.imageCaptions!=='object'||Array.isArray(entry.imageCaptions))return '升级日志图注格式不正确'
      for(const [key,caption] of Object.entries(entry.imageCaptions))if(!/^[1-9]\d*$/.test(key)||!entry.imageIds.includes(Number(key))||!plainText(caption,500))return '图注需填写不含链接的单段纯文本，最多 500 字，且必须属于当前条目的截图'
    }
  }
  if(entries.length&&entries.length!==sources.length)return '升级日志必须保留每项来源需求，不可重复或遗漏'
  return ''
}
export function parseReleaseNotes(value:unknown):ReleaseNotes {
  const v=value as ReleaseNotes
  if(!v||!['none','queued','generating','draft','failed'].includes(v.state)||!Number.isSafeInteger(v.revision)||v.revision<0||!Number.isSafeInteger(v.settingsVersion)||v.settingsVersion<0||!Number.isSafeInteger(v.sourceCount)||v.sourceCount<0||['canGenerate','canEdit','sourceChanged','configured','enabled','autoEnabled'].some(key=>typeof v[key as keyof ReleaseNotes]!=='boolean'))throw Error('升级日志返回格式不正确，请刷新重试')
  if(!Array.isArray(v.categories)||JSON.stringify(v.categories)!==JSON.stringify(releaseNoteCategories)||!Array.isArray(v.entries)||!Array.isArray(v.sources)||!Array.isArray(v.images)||v.sources.some(source=>!source||!id(source.id)||typeof source.code!=='string'||typeof source.title!=='string')||v.images.some(image=>!image||!id(image.id)||!id(image.requirementId)||typeof image.name!=='string')||new Set(v.images.map(image=>image.id)).size!==v.images.length)throw Error('升级日志返回格式不正确，请刷新重试')
  for(const image of v.images)releaseImagePath(image)
  if(['markdown','model','baseUrl','error','updatedAt','sprintName','completedAt'].some(key=>typeof v[key as keyof ReleaseNotes]!=='string'))throw Error('升级日志返回格式不正确，请刷新重试')
  const problem=releaseEntriesError(v.entries,v.images,v.sources);if(problem)throw Error(problem)
  return cloneReleaseNotes(v)
}
export function cloneReleaseNotes<T>(value:T):T { return JSON.parse(JSON.stringify(value)) as T }
export function releaseNoteGroups(entries:ReleaseNoteEntry[]) { return releaseNoteCategories.map(category=>({category,entries:entries.map((entry,index)=>({entry,index})).filter(item=>item.entry.category===category).sort((a,b)=>(a.entry.requirementIds[0]||0)-(b.entry.requirementIds[0]||0))})) }
export function orderedReleaseNoteEntries(entries:ReleaseNoteEntry[]):ReleaseNoteEntry[] { const copy=cloneReleaseNotes(entries);return copy.sort((a,b)=>releaseNoteCategories.indexOf(a.category as typeof releaseNoteCategories[number])-releaseNoteCategories.indexOf(b.category as typeof releaseNoteCategories[number])||(a.requirementIds[0]||0)-(b.requirementIds[0]||0)) }
export function releaseNoteFilename(name:string,format:'md'|'json'|'zip') { return (name.replace(/[\\/:*?"<>|\u0000-\u001f]/g,'_').trim().slice(0,100)||'release-notes')+'.'+format }
export function releaseNoteJSON(note:ReleaseNotes):string { return JSON.stringify({sprintName:note.sprintName,completedAt:note.completedAt,updatedAt:note.updatedAt,revision:note.revision,categories:releaseNoteCategories,entries:orderedReleaseNoteEntries(note.entries),sources:note.sources,images:note.images},null,2)+'\n' }
// Never render an arbitrary download as SVG/HTML. Attachment responses intentionally
// use octet-stream; identify a supported bitmap before constructing an object URL.
export function releaseImageMime(bytes:Uint8Array):string {
  if(bytes.length>=8&&[137,80,78,71,13,10,26,10].every((b,i)=>bytes[i]===b))return 'image/png'
  if(bytes.length>=3&&bytes[0]===255&&bytes[1]===216&&bytes[2]===255)return 'image/jpeg'
  const signature=String.fromCharCode(...bytes.slice(0,12))
  if(signature.startsWith('GIF87a')||signature.startsWith('GIF89a'))return 'image/gif'
  if(signature.startsWith('RIFF')&&signature.slice(8,12)==='WEBP')return 'image/webp'
  throw Error('此截图格式暂不能安全预览，请查看原需求附件')
}
