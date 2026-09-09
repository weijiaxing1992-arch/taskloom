import { formatDate, t } from './i18n'
import { workItemFields, workItemPersonNames, workItemValue } from './workItemQuery'

export function csvCell(value:unknown):string {
  let text=value==null?'':String(value)
  // Spreadsheets interpret quoted formula cells too; neutralize before CSV quoting.
  if(/^[\s\uFEFF]*[=+@-]/.test(text)||/^[\t\r\n]/.test(text))text="'"+text
  return '"'+text.replace(/"/g,'""')+'"'
}
export function requirementCSV(items:any[],definitions:any[],members:any[],columns?:string[],contexts?:Map<string,{definitions:any[];members:any[]}>):string {
  const fields=workItemFields(definitions,members)
  const keys=[...new Set(columns?.length?['code','title',...columns.map(key=>key.replace(/^cf\.requirement\./,'cf.'))]:fields.map(field=>field.key))]
  const selected=keys.map(key=>fields.find(field=>field.key===key)).filter(field=>!!field)
  const rows=[selected.map(field=>field.custom?[...new Set(fields.filter(candidate=>candidate.key===field.key).map(candidate=>candidate.label))].join(' / '):t(field.label)),...items.map(item=>selected.map(field=>{
    const context=contexts?.get(String(item.projectId)),rowMembers=context?.members||members,rowDefinitions=context?.definitions||definitions
    if(field.kind==='person')return workItemPersonNames(item,field.key,rowMembers).join('、')
    const value=workItemValue(item,field.key)
    if(field.key==='status')return item.statusName||value
    const definition=field.custom?rowDefinitions.find(d=>'cf.'+d.key===field.key):null
    if(field.custom&&!definition)return ''
    if(definition&&['user','users'].includes(definition.type))return (Array.isArray(value)?value:value?[value]:[]).map(id=>rowMembers.find(member=>member.id===id)?.name||id).join('、')
    if(field.custom)return value==null?'':Array.isArray(value)?value.join('、'):typeof value==='boolean'?t(value?'是':'否'):value
    if(field.kind==='date')return value?formatDate(value,{year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit'}):''
    if(field.kind==='boolean')return value==null?'':t(value?'是':'否')
    return Array.isArray(value)?value.join('、'):value
  }))]
  return '\uFEFF'+rows.map(row=>row.map(csvCell).join(',')).join('\r\n')+'\r\n'
}
export function downloadFile(blob:Blob,name:string){
  const url=URL.createObjectURL(blob),link=document.createElement('a')
  link.href=url;link.download=name;document.body.append(link);link.click();link.remove()
  setTimeout(()=>URL.revokeObjectURL(url),30000)
}
