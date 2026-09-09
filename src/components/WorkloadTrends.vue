<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import { t, locale, formatDate, formatNumber } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import { workloadRoleNames, validWorkloadMonth } from '../workload'
import { trendGeometry, trendMetricValue, validTrendReport, type WorkloadTrendReport, type TrendMode, type TrendMetric, type TrendPoint } from '../workloadTrends'
import { Button } from './ui/button'
import MemberMultiSelect from './MemberMultiSelect.vue'
import AppSelect from './AppSelect.vue'
import type { MentionMember } from '../mentions'
const props=defineProps<{month:string;refreshKey?:number;disabled?:boolean}>()
const workspace=useWorkspaceStore(),report=ref<WorkloadTrendReport|null>(null),loading=ref(false),error=ref(''),months=ref(6),department=ref('*'),member=ref(''),role=ref(''),mode=ref<TrendMode>('monthly'),metric=ref<TrendMetric>('weight'),page=ref(1),active=ref<string|null>(null)
const options=ref<WorkloadTrendReport['options']>({people:[],departments:[]})
// Historical members remain selectable for report filters only, never assignments.
const filterMembers=computed<MentionMember[]>(()=>options.value.people.map(person=>({id:person.userId,name:person.name,department:person.departmentName,departmentIds:person.departmentId?[person.departmentId]:[],departmentNames:person.departmentName?[person.departmentName]:[],active:true})))
function selectMember(ids:string[]){if(!blocked.value)member.value=ids[0]||''}
const metrics:Record<TrendMetric,string>={weight:'已填工作权重',shippedRequirementCount:'已上线需求',defectCount:'迭代缺陷'}
const metricOptions=computed(()=>Object.entries(metrics).map(([value,label])=>({value,label:t(label)})))
const monthOptions=computed(()=>[{value:6,label:t('最近 6 个月')},{value:12,label:t('最近 12 个月')}])
const departmentOptions=computed(()=>[{value:'*',label:t('所有部门')},...options.value.departments.map(item=>({value:item.id,label:item.name||t('未分配部门')}))])
const roleOptions=computed(()=>[{value:'',label:t('所有职能')},...Object.entries(workloadRoleNames).map(([value,label])=>({value,label:t(label)}))])
const points=computed(()=>report.value?.[mode.value]||[]),pages=computed(()=>Math.max(1,Math.ceil(points.value.length/12))),visiblePoints=computed(()=>points.value.slice((page.value-1)*12,page.value*12)),graph=computed(()=>trendGeometry(visiblePoints.value,metric.value)),hasValues=computed(()=>points.value.some(point=>trendMetricValue(point,metric.value)!==null))
const activePoint=computed(()=>points.value.find(point=>point.key===active.value))
const blocked=computed(()=>!!props.disabled||workspace.identityConflict||!workspace.currentUser||workspace.operationDisabled)
let sequence=0,disposed=false,controller:AbortController|undefined,timer:ReturnType<typeof setTimeout>|undefined
const currentFilters=()=>({department:department.value,user:member.value,role:role.value})
function invalidate(clearOptions=false){sequence++;controller?.abort();clearTimeout(timer);report.value=null;loading.value=false;error.value='';active.value=null;if(clearOptions)options.value={people:[],departments:[]}}
async function load(){
 if(disposed||blocked.value||!validWorkloadMonth(props.month))return
 const request=++sequence,tenant=workspace.session!.tenant.id,user=workspace.currentUser!.id,month=props.month,windowSize=months.value,filters=currentFilters()
 controller?.abort();controller=new AbortController();loading.value=true;error.value='';report.value=null;active.value=null
 const current=()=>!disposed&&!blocked.value&&sequence===request&&workspace.session?.tenant.id===tenant&&workspace.currentUser?.id===user&&props.month===month&&months.value===windowSize&&JSON.stringify(currentFilters())===JSON.stringify(filters)
 try{const params=new URLSearchParams({month,months:String(windowSize),...filters}),data=await api<WorkloadTrendReport>('/reports/workload/trends?'+params,{signal:controller.signal});if(!current())return;if(!validTrendReport(data,{tenant,month,months:windowSize,filters}))throw new Error(t('趋势上下文校验失败，请重试'));report.value=data;options.value=data.options}
 catch(cause){if(current()){error.value=cause instanceof Error?cause.message:t('趋势暂时无法读取，请重试');options.value={people:[],departments:[]}}}
 finally{if(current())loading.value=false}
}
function schedule(){invalidate();page.value=1;if(!blocked.value&&!disposed)timer=setTimeout(()=>void load(),120)}
function identityChanged(){invalidate(true)}
function reset(){department.value='*';member.value='';role.value=''}
function selectMetric(value:string|number){const next=String(value);if(next in metrics)metric.value=next as TrendMetric}
function selectMonths(value:string|number){const next=Number(value);if(next===6||next===12)months.value=next}
function selectDepartment(value:string|number){department.value=String(value)}
function selectRole(value:string|number){role.value=String(value)}
function label(point:TrendPoint){return mode.value==='monthly'?point.month:`${point.projectName} · ${point.name}`}
function number(value:number){return formatNumber(value,{maximumFractionDigits:6})}
function valueLabel(point:TrendPoint){const value=trendMetricValue(point,metric.value);return value!==null?number(value):t(!point.hasData?'无数据':'尚未估算')}
function pointDescription(point:TrendPoint){return `${label(point)} · ${t(metrics[metric.value])}: ${valueLabel(point)}`}
watch([()=>props.month,()=>props.refreshKey,months,department,member,role],schedule,{flush:'sync'})
watch([mode,metric],()=>{page.value=1;active.value=null})
watch(page,()=>{active.value=null})
watch(()=>[workspace.currentUser?.id,workspace.session?.tenant.id,blocked.value],()=>{invalidate(true);reset();schedule()},{flush:'sync'})
onMounted(()=>{void load();window.addEventListener('devflow-identity-changed',identityChanged);window.addEventListener('devflow-auth-expired',identityChanged)})
onBeforeUnmount(()=>{disposed=true;invalidate(true);window.removeEventListener('devflow-identity-changed',identityChanged);window.removeEventListener('devflow-auth-expired',identityChanged)})
</script>
<template>
 <section class="workload-trends" :aria-label="t('工作量趋势')">
  <header class="trend-heading"><div><h2>{{t('工作量趋势')}}</h2><p>{{t('按迭代结束月份归属的当前快照，不代表历史当月实际完成或上线。')}}</p></div><Button variant="outline" size="sm" :disabled="loading||blocked" @click="load">{{t('刷新趋势')}}</Button></header>
  <div class="trend-controls"><div class="trend-modes" role="tablist" :aria-label="t('趋势分组')"><button v-for="item in [{key:'monthly',name:'按月份'},{key:'iterations',name:'按迭代版本'}]" :key="item.key" type="button" role="tab" :aria-selected="mode===item.key" :class="{active:mode===item.key}" @click="mode=item.key as TrendMode">{{t(item.name)}}</button></div><label><span>{{t('统计指标')}}</span><AppSelect class="trend-select" :model-value="metric" :options="metricOptions" :label="t('统计指标')" :disabled="blocked" @update:model-value="selectMetric"/></label><label><span>{{t('时间范围')}}</span><AppSelect class="trend-select" :model-value="months" :options="monthOptions" :label="t('时间范围')" :disabled="blocked" @update:model-value="selectMonths"/></label></div>
  <div class="trend-filters"><label><span>{{t('所属部门')}}</span><AppSelect class="trend-select" :model-value="department" :options="departmentOptions" :label="t('所属部门')" :disabled="blocked" @update:model-value="selectDepartment"/></label><div class="trend-member-filter"><MemberMultiSelect single :show-lead="false" :model-value="member?[member]:[]" :members="filterMembers" :all-departments="options.departments" :disabled="blocked" :label="t('筛选成员（含历史成员）')" :hint="t('仅筛选统计记录，不变更工作项人员绑定。')" @update:model-value="selectMember"/></div><label><span>{{t('职能')}}</span><AppSelect class="trend-select" :model-value="role" :options="roleOptions" :label="t('职能')" :disabled="blocked" @update:model-value="selectRole"/></label><Button v-if="department!=='*'||member||role" variant="ghost" size="sm" :disabled="blocked" @click="reset">{{t('清除筛选')}}</Button></div>
  <p class="trend-filter-note">{{t('趋势筛选仅影响本图和下方数据表；部门、成员与职能采用月度明细相同的归属和分摊口径。')}}</p>
  <p v-if="error" class="trend-error" role="alert">{{t(error)}} <button type="button" :disabled="blocked" @click="load">{{t('重试')}}</button></p>
  <div v-else-if="loading" class="trend-empty" role="status"><span class="spinner"></span>{{t('正在汇总趋势…')}}</div>
  <template v-else-if="report">
   <div class="trend-meta"><span>{{report.fromMonth}} — {{report.month}} · {{report.scope.tenantName}}</span><span>{{t('当前快照')}} · {{formatDate(report.generatedAt)}}</span></div>
   <p v-if="report.ambiguousSprintItemCount" class="trend-warning">{{t('{count} 个历史工作项的迭代简称有歧义，未计入',{count:report.ambiguousSprintItemCount})}}</p>
   <p v-if="!points.length||!hasValues" class="trend-empty">{{t('当前范围没有可绘制的数据；未估算权重不会伪装成已填的零。')}}</p>
   <template v-if="points.length">
    <div class="trend-chart-scroll" tabindex="0" :aria-label="t('横向滚动查看趋势图')"><svg :viewBox="`0 0 ${graph.width} ${graph.height}`" :style="{minWidth:graph.width+'px'}" role="img" :aria-label="t('{metric}趋势，共 {count} 个分组',{metric:t(metrics[metric]),count:visiblePoints.length})"><title>{{t('工作量趋势')}} · {{t(metrics[metric])}}</title><desc>{{t('无数据或未估算位置断开连线，真实零值显示在基线上；下方表格提供精确数值。')}}</desc><g v-for="tick in graph.ticks" :key="tick.y"><line :x1="graph.left" :x2="graph.width-graph.right" :y1="tick.y" :y2="tick.y" class="trend-grid"/><text :x="graph.left-8" :y="tick.y+4" text-anchor="end" class="trend-axis">{{number(tick.value)}}</text></g><polyline v-for="(path,index) in graph.paths" :key="index" :points="path" class="trend-line"/><g v-for="point in graph.dots" :key="point.key"><text :x="point.x" :y="graph.height-12" text-anchor="middle" class="trend-axis">{{mode==='monthly'?point.month:(point.name||'').slice(0,9)}}<title>{{label(point)}}</title></text><circle v-if="point.y!==null" :cx="point.x" :cy="point.y" r="5" class="trend-dot" tabindex="0" :aria-label="pointDescription(point)" @mouseenter="active=point.key" @focus="active=point.key" @mouseleave="active=null" @blur="active=null"><title>{{pointDescription(point)}}</title></circle><text v-else :x="point.x" :y="graph.height-graph.bottom-8" text-anchor="middle" class="trend-axis">—</text></g></svg></div>
    <p class="trend-tooltip" aria-live="polite">{{activePoint?pointDescription(activePoint):t('悬停或聚焦数据点查看精确数值。')}}</p>
    <div v-if="pages>1" class="trend-pagination"><Button size="sm" variant="outline" :disabled="page<=1" @click="page--">{{t('上一页')}}</Button><span>{{t('第 {page}/{pages} 组，每组最多 12 个迭代',{page,pages})}}</span><Button size="sm" variant="outline" :disabled="page>=pages" @click="page++">{{t('下一页')}}</Button></div>
    <details class="trend-exact"><summary>{{t('查看精确趋势数据')}} · {{points.length}}</summary><div class="trend-data-scroll" tabindex="0" :aria-label="t('精确趋势数据表')"><table><thead><tr><th>{{t(mode==='monthly'?'月份':'迭代版本')}}</th><th v-if="mode==='iterations'">{{t('项目 / 结束日期')}}</th><th>{{t('已填工作权重')}}</th><th>{{t('已上线需求')}}</th><th>{{t('迭代缺陷')}}</th><th>{{t('数据状态')}}</th></tr></thead><tbody><tr v-for="point in points" :key="point.key"><td>{{mode==='monthly'?point.month:point.name}}</td><td v-if="mode==='iterations'">{{point.projectName}} · {{point.endDate}} · #{{point.sprintId}}</td><td>{{!point.hasData?t('无数据'):point.metrics.estimatedRoleCount?number(point.metrics.weight):t('尚未估算')}}</td><td>{{number(point.metrics.shippedRequirementCount)}}</td><td>{{number(point.metrics.defectCount)}}</td><td>{{!point.hasData?t('无匹配工作项'):t('{count} 条需求 · {gaps} 项职能未估算',{count:point.metrics.requirementCount,gaps:point.metrics.unestimatedRoleCount})}}</td></tr></tbody></table></div></details>
   </template>
   <p class="trend-footnote">{{t('按迭代日期排序；跨项目同名迭代按项目与迭代 ID 分开。没有匹配工作项显示无数据，有已填权重且总和为零才显示 0。')}}</p>
  </template>
 </section>
</template>
<style scoped>
.trend-member-filter{min-width:220px;max-width:360px;flex:1}.trend-member-filter:deep(.member-hint){max-width:100%}
@media(max-width:760px){.trend-member-filter{min-width:0;max-width:100%;width:100%}}
.workload-trends{margin-top:22px;border:1px solid var(--line);border-radius:12px;background:var(--surface);padding:22px;min-width:0}.trend-heading,.trend-controls,.trend-filters,.trend-meta,.trend-pagination{display:flex;align-items:center;flex-wrap:wrap;gap:12px}.trend-heading{justify-content:space-between;margin-bottom:18px}.trend-heading h2{font-size:16px;margin:0 0 7px}.trend-heading p,.trend-filter-note,.trend-footnote{font-size:11px;color:var(--muted);line-height:1.8;margin:0}.trend-controls{justify-content:space-between}.trend-controls label,.trend-filters label{display:flex;gap:8px;align-items:center;min-width:0;font-size:12px;color:var(--muted)}.trend-controls :deep(.trend-select),.trend-filters :deep(.trend-select){max-width:270px;min-width:132px}.trend-filters{margin-top:12px}.trend-modes{display:flex;background:var(--surface-subtle);padding:3px;border-radius:8px}.trend-modes button{background:transparent;border:0;border-radius:6px;padding:8px 13px;color:var(--muted);font-size:12px}.trend-modes button.active{background:var(--surface);color:var(--primary);box-shadow:0 1px 4px #00000012}.trend-filter-note{margin:10px 0 15px}.trend-meta{justify-content:space-between;font-size:11px;color:var(--muted);padding:12px 0}.trend-chart-scroll{overflow-x:auto;max-width:100%;border-bottom:1px solid var(--line);overscroll-behavior-x:contain}.trend-chart-scroll svg{display:block;width:100%;height:230px}.trend-grid{stroke:var(--line);stroke-dasharray:3 5}.trend-axis{fill:var(--muted);font-size:10px}.trend-line{fill:none;stroke:var(--primary);stroke-width:2.5;stroke-linejoin:round}.trend-dot{fill:var(--surface);stroke:var(--primary);stroke-width:2.5;cursor:crosshair}.trend-dot:hover,.trend-dot:focus{r:7;fill:var(--primary);outline:none;stroke:var(--ink)}.trend-tooltip{min-height:20px;font-size:11px;color:var(--muted);margin:12px 0;overflow-wrap:anywhere}.trend-pagination{justify-content:flex-end;font-size:11px;color:var(--muted)}.trend-exact{border:1px solid var(--line);border-radius:8px;margin:14px 0}.trend-exact summary{padding:12px;font-size:12px;cursor:pointer}.trend-data-scroll{overflow:auto;max-height:360px}.trend-data-scroll table{border-collapse:collapse;width:100%;min-width:680px;font-size:12px;text-align:left}.trend-data-scroll th{position:sticky;top:0;background:var(--surface-subtle);color:var(--muted);font-weight:500}.trend-data-scroll td,.trend-data-scroll th{padding:11px 13px;border-bottom:1px solid var(--line);white-space:nowrap}.trend-data-scroll tbody tr:hover{background:var(--surface-subtle)}.trend-empty{padding:28px;text-align:center;color:var(--muted);font-size:12px;line-height:1.8}.trend-error,.trend-warning{background:var(--surface-subtle);padding:12px;border:1px solid var(--line);border-left:3px solid var(--danger,#c54f58);border-radius:7px;font-size:12px;line-height:1.7}.trend-error button{color:var(--primary);border:0;background:none}.trend-warning{border-left-color:#bd8525}.trend-controls button:focus-visible,.trend-exact summary:focus-visible,.trend-chart-scroll:focus-visible,.trend-data-scroll:focus-visible{outline:2px solid var(--primary);outline-offset:2px}
@media(max-width:760px){.workload-trends{padding:16px}.trend-heading{align-items:flex-start}.trend-controls{gap:10px}.trend-modes{width:100%}.trend-modes button{flex:1;min-height:40px}.trend-controls label{flex:1}.trend-controls :deep(.trend-select){min-width:0;max-width:100%;flex:1;min-height:40px}.trend-filters{display:grid;grid-template-columns:minmax(0,1fr);gap:10px}.trend-filters label{display:grid;grid-template-columns:64px minmax(0,1fr)}.trend-filters :deep(.trend-select){width:100%;max-width:100%;min-width:0;min-height:40px}.trend-heading p{max-width:100%;overflow-wrap:anywhere}.trend-pagination{justify-content:space-between}.trend-pagination span{flex-basis:100%;order:-1}}
</style>
