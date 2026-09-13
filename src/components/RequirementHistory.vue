<script setup lang="ts">
import { t, formatDate } from '../i18n'
import { statusLabel } from '../requirementWorkflow'
import { workItemFields } from '../workItemQuery'
const props=defineProps<{items:any[];count:number;members:any[];definitions:any[];statuses:any[]}>()

// 历史表会保留稳定的服务端字段和账号标识，方便审计；详情页不应把这些
// 技术标识直接当作业务文案展示。下面只负责展示层转换，原始记录仍可按需展开。
const eventNames:Record<string,string>={
 created:'创建需求',updated:'更新了需求字段',
 'attachment.uploaded':'上传附件','attachment.deleted':'删除附件','attachment.classified':'更新附件分类',
 'design_link.added':'关联设计稿','design_link.deleted':'取消关联设计稿',
 'checklist.updated':'更新检查项',commented:'添加评论','comment.created':'添加评论','comment.rich_created':'添加评论',
 sprint_renamed:'所属迭代更名',sprint_transferred:'完成迭代后迁移需求',category_changed:'分类调整后更新需求归属',
}
const fieldNames:Record<string,string>={
 title:'需求标题',type:'需求类型',description:'需求描述',descriptionDoc:'富文本正文',acceptance:'验收标准',parentId:'父需求',
 category:'分类',sprint:'所属迭代',status:'状态',priority:'优先级',owner:'产品负责人',ownerUserIds:'产品负责人',
 assignee:'处理人',assigneeUserIds:'处理人',tags:'标签',tagColors:'标签颜色',roleWeights:'人员与开发评估',remarks:'备注',
 startDate:'计划开始',endDate:'计划结束',discipline:'研发职能',progress:'进度',estimatedHours:'预估工时',actualHours:'实际工时',
 sensitive:'敏感数据',authImpact:'权限认证',customFields:'自定义字段',descriptionMentionUserIds:'正文提及成员',
 remarksMentionUserIds:'备注中提及的成员',checklist:'检查项',attachment:'附件',designLink:'设计链接',comment:'评论',relatedRequirement:'关联需求',
}
const markNames:Record<string,string>={bold:'加粗',italic:'斜体',underline:'下划线',strike:'删除线',code:'行内代码'}
function text(value:any):string{return typeof value==='string'?value.trim():''}
function memberName(value:any):string{
 const id=text(value),member=props.members.find(item=>String(item?.id||'')===id),name=text(member?.name)
 if(name)return name
 if(id==='u_admin')return t('管理员账户')
 if(id==='system')return t('系统自动操作')
 return id&&/^(?:u|user|member|sys|bot|service|cli)[._-][a-z0-9._-]+$/i.test(id)?t('历史成员'):id
}
function isTechnicalIdentifier(value:any):boolean{
 const id=text(value)
 return id==='u_admin'||id==='system'||/^(?:u|user|member|sys|bot|service|cli)[._-][a-z0-9._-]+$/i.test(id)
}
function fieldName(key:string):string {
 if(key.startsWith('customFields.'))return props.definitions.find(field=>field.key===key.slice(13))?.name||t('自定义字段')
 if(key.startsWith('roleWeights.'))return t(({frontend:'前端工程师',backend:'后端工程师',algorithm:'算法工程师',ui:'UI 工程师',product:'产品负责人'} as Record<string,string>)[key.slice(12)]||'人员与开发评估')
 return t(fieldNames[key]||workItemFields().find(field=>field.key===key)?.label||'其他需求字段')
}
function readableUpdateDetail(value:any):string{
 const detail=text(value),match=detail.match(/^(?:更新了需求字段|updated\s+requirement\s+fields?)\s*[:：]\s*(.+)$/i)
 if(!match)return ''
 const fields=match[1].split(/[、,，;；]+/).map(item=>item.trim()).filter(Boolean)
 return fields.length?t('更新了需求字段')+'：'+fields.map(fieldName).join('、'):t('更新了需求字段')
}
function technicalActivityText(value:any):boolean{
 const detail=text(value)
 return !!detail&&(!!readableUpdateDetail(detail)||/[a-z][a-z0-9]*(?:[._-][a-z0-9]+)+/i.test(detail)||/(?:^|[、,，;；\s])(descriptionDoc|description|assigneeUserIds|ownerUserIds)(?:$|[、,，;；\s])/i.test(detail))
}
function eventLabel(entry:any):string{
 const event=text(entry?.event),rawDetail=text(entry?.detail),detail=readableUpdateDetail(rawDetail)
 if(event==='updated')return entry.snapshotAvailable?t('更新了需求字段'):detail||t('更新了需求字段')
 return t(eventNames[event]||detail||(!technicalActivityText(rawDetail)&&rawDetail)||'系统操作')
}
function actorLabel(entry:any):string{
 const preferred=text(entry?.actorName),fromMember=memberName(entry?.actorId)||memberName(entry?.actor)
 if(preferred)return preferred
 if(fromMember)return fromMember
 const actor=text(entry?.actor)||text(entry?.actorId)
 return actor||t('未知用户')
}
function richDocumentSummary(value:any):string{
 if(value==='富文本内容已更新')return t('富文本内容已更新')
 if(!value||typeof value!=='object')return value==null?'—':String(value)
 const fragments:string[]=[],marks=new Set<string>(),assets:string[]=[],languages=new Set<string>()
 const visit=(node:any)=>{
  if(!node||typeof node!=='object')return
  if(typeof node.text==='string')fragments.push(node.text)
  for(const mark of Array.isArray(node.marks)?node.marks:[]){const name=markNames[text(mark?.type)];if(name)marks.add(t(name))}
  const type=text(node.type),attrs=node.attrs||{}
  if(type==='mention'){const name=memberName(attrs.id||attrs.userId);fragments.push('@'+(name||t('历史成员')))}
  if(type==='image')assets.push(t('图片')+(text(attrs.name)?'：'+text(attrs.name):''))
  if(type==='attachment')assets.push(t('附件')+(text(attrs.name)?'：'+text(attrs.name):''))
  if(type==='codeBlock'&&text(attrs.language))languages.add(text(attrs.language))
  for(const child of Array.isArray(node.content)?node.content:[])visit(child)
 }
 visit(value)
 const pieces=[fragments.join(''),...assets,...[...marks],...[...languages].map(language=>t('代码块')+'：'+language)].filter(Boolean)
 return pieces.length?pieces.join(' · '):t('富文本内容已更新')
}
function relatedValueSummary(value:any,key:string):string|undefined{
 if(!value||typeof value!=='object'||Array.isArray(value))return undefined
 if(key==='checklist'){const title=text(value.text)||text(value.title);return title?(title+(typeof value.done==='boolean'?' · '+t(value.done?'已完成':'未完成'):'')):t('检查项已更新')}
 if(key==='attachment'){const name=text(value.name)||text(value.fileName)||text(value.title);return name?t('附件')+'：'+name:t('附件已更新')}
 if(key==='designLink'){const title=text(value.title)||text(value.name)||text(value.url);return title?t('设计链接')+'：'+title:t('设计链接已更新')}
 if(key==='comment'){const body=text(value.body)||text(value.content)||text(value.text);return body?t('评论')+'：'+body:t('评论已更新')}
 if(key==='relatedRequirement'){const title=text(value.title)||text(value.code)||text(value.name);return title?t('关联需求')+'：'+title:t('关联需求已更新')}
 if(key==='tagColors')return t('标签颜色已更新')
 return undefined
}
function isMemberField(key:string):boolean{
 if(/(?:UserIds?|owner|assignee)$/i.test(key))return true
 if(key.startsWith('customFields.'))return ['user','users'].includes(props.definitions.find(field=>field.key===key.slice(13))?.type)
 return false
}
function display(value:any,key:string):string {
 if(value===null||value===undefined||value==='')return '—'
 if(key==='descriptionDoc')return richDocumentSummary(value)
 if(key==='status')return statusLabel({status:String(value)},props.statuses,t)
 if(key==='sprint')return value==='待规划'?t('待规划'):String(value)
 if(typeof value==='boolean')return t(value?'是':'否')
 if(key.startsWith('roleWeights.')&&typeof value==='object')return [display(value.userIds||[], 'assigneeUserIds'),t('难度')+': '+(value.value??'—')].join(' · ')
 if(Array.isArray(value))return value.map(item=>typeof item==='string'&&isMemberField(key)?memberName(item):typeof item==='object'?JSON.stringify(item):String(item)).join('、')||'—'
 const related=relatedValueSummary(value,key)
 if(related)return related
 if(typeof value==='object')return JSON.stringify(value,null,2)
 if(isMemberField(key)&&isTechnicalIdentifier(value))return memberName(value)||t('历史成员')
 return String(value)
}
function technicalTrace(entry:any):{label:string;value:string}[]{
 const lines:{label:string;value:string}[]=[]
 const actorID=text(entry?.actorId),actor=text(entry?.actor),event=text(entry?.event),detail=text(entry?.detail)
 if(actorID)lines.push({label:'操作成员标识',value:actorID})
 else if(isTechnicalIdentifier(actor))lines.push({label:'操作成员标识',value:actor})
 if(event)lines.push({label:'事件标识',value:event})
 if(technicalActivityText(detail))lines.push({label:'原始描述',value:detail})
 return lines
}
</script>
<template>
 <section class="requirement-history" :aria-label="t('需求变更与迭代历史')">
  <header><h3>{{t('需求变更与迭代历史')}}</h3><span :class="['iteration-delay-badge',{'has-delay':count>0}]">{{t('迭代延误 ×{count}',{count})}}</span></header>
  <p class="history-explanation">{{t('从一个已分配迭代转入另一个迭代记 1 次延误；首次规划和迭代改名不计入。')}}</p>
  <p class="history-explanation">{{t('历史记录仅按已有证据恢复；未保存的字段值、所属迭代和状态显示为未记录。')}}</p>
  <p v-if="!items.length" class="history-explanation">{{t('暂无变更记录')}}</p>
  <ol v-else class="history-timeline">
   <li v-for="entry in items" :key="entry.id">
    <article><header><b>{{actorLabel(entry)}}</b><time :datetime="entry.createdAt">{{formatDate(entry.createdAt)}}</time></header>
     <p>{{eventLabel(entry)}} <span v-if="entry.iterationDelay" class="iteration-delay-badge has-delay">{{t('迭代延误 ×{count}',{count:entry.iterationDelayCount})}}</span><small v-if="entry.recovered">{{t('由审计记录恢复')}}</small></p>
     <dl class="history-context"><dt>{{t('所属迭代')}}</dt><dd>{{entry.sprint==null?t('未记录'):display(entry.sprint,'sprint')}}</dd><dt>{{t('状态')}}</dt><dd>{{entry.status==null?t('未记录'):display(entry.status,'status')}}</dd></dl>
     <details v-if="entry.changes?.length" :open="entry.event!=='created'"><summary>{{t('{count} 项字段变更',{count:entry.changes.length})}}</summary><div class="history-changes"><div v-for="change in entry.changes" :key="change.field" class="history-change"><b>{{fieldName(change.field)}}</b><div><span class="history-value-label">{{t('变更前')}}</span><pre>{{display(change.before,change.field)}}</pre></div><div><span class="history-value-label">{{t('变更后')}}</span><pre>{{display(change.after,change.field)}}</pre></div></div></div></details>
     <p v-else-if="!entry.snapshotAvailable" class="history-explanation">{{t('此历史记录未保存字段前后值。')}}</p>
     <details v-if="technicalTrace(entry).length" class="history-technical-trace"><summary>{{t('查看原始记录')}}</summary><dl><template v-for="line in technicalTrace(entry)" :key="line.label"><dt>{{t(line.label)}}</dt><dd>{{line.value}}</dd></template></dl></details>
    </article>
   </li>
  </ol>
 </section>
</template>
<style scoped>
.requirement-history{padding:20px;min-width:0}.requirement-history>header,.history-timeline article>header{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.requirement-history h3{margin:0}.history-explanation{font-size:12px;color:var(--muted);line-height:1.8}.history-timeline{list-style:none;padding:0 0 0 16px;margin:20px 0;border-left:2px solid var(--line)}.history-timeline>li{position:relative;padding:0 0 24px 10px}.history-timeline>li:before{content:'';position:absolute;left:-22px;top:5px;width:10px;height:10px;border-radius:50%;background:var(--accent,#2563eb)}.history-timeline article{min-width:0}.history-timeline time,.history-timeline small{font-size:12px;color:var(--muted)}.history-timeline small{margin-left:8px}.history-timeline p{line-height:1.8;overflow-wrap:anywhere}.history-context{display:flex;gap:6px 12px;flex-wrap:wrap;font-size:12px}.history-context dt{color:var(--muted)}.history-context dd{margin:0;overflow-wrap:anywhere}.history-timeline summary{cursor:pointer;font-size:12px;padding:8px 0}.history-changes{border:1px solid var(--line);border-radius:6px;overflow:hidden}.history-change{display:grid;grid-template-columns:minmax(90px,.65fr) minmax(0,1fr) minmax(0,1fr);gap:12px;padding:12px;font-size:12px}.history-change+.history-change{border-top:1px solid var(--line)}.history-change>b{overflow-wrap:anywhere}.history-value-label{color:var(--muted);font-size:11px}.history-change pre{margin:6px 0 0;white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;line-height:1.7}.history-technical-trace{margin-top:6px;border-top:1px dashed var(--line)}.history-technical-trace summary{color:var(--muted)}.history-technical-trace dl{display:grid;grid-template-columns:max-content minmax(0,1fr);gap:5px 12px;margin:0;padding:0 0 8px;font-size:11px;color:var(--muted)}.history-technical-trace dt{font-weight:600}.history-technical-trace dd{margin:0;overflow-wrap:anywhere}.iteration-delay-badge{display:inline-flex;align-items:center;padding:3px 7px;border-radius:5px;font-size:12px;color:var(--muted);background:var(--surface-soft);white-space:nowrap}.iteration-delay-badge.has-delay{color:#9a3412;background:#ffedd5;border:1px solid #fdba74;font-weight:600}@media(max-width:640px){.requirement-history{padding:14px}.history-change{grid-template-columns:minmax(0,1fr);gap:8px}.history-context{gap:6px}.history-timeline{padding-left:12px}.history-timeline>li:before{left:-18px}.history-technical-trace dl{grid-template-columns:1fr;gap:2px}}
</style>
