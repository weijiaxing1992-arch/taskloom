import type {WorkloadMetrics} from './workload'
export interface IterationPoint extends WorkloadMetrics {sprintId:number;hasData:boolean;deliveredWeight:number}
export interface IterationPerson {userId:string;name:string;active:boolean;departmentId:string;departmentName:string;roles:{role:string;points:IterationPoint[]}[]}
export interface IterationReport {scope:'personal'|'organization';tenantId:string;userId:string;month:string;count:number;project:string;asOf:string;snapshot:true;generatedAt:string;iterations:{id:number;name:string;endDate:string;projectId:string;projectName:string}[];people:IterationPerson[];ambiguousSprintItemCount:number;unassignedRequirementCount:number}
export interface IterationSummary {person:IterationPerson;role:string;points:IterationPoint[];weight:number;delivered:number;coverage:number;completion:number|null;missing:number;samples:number;change:number|null;alerts:string[];rank:number|null}
const validNumber=(v:unknown)=>typeof v==='number'&&Number.isFinite(v)&&v>=0
export function validIterationReport(value:unknown,expected:Pick<IterationReport,'tenantId'|'userId'|'month'|'count'|'scope'|'project'>):value is IterationReport {
 const r=value as IterationReport
 if(!r||!Object.entries(expected).every(([k,v])=>(r as unknown as Record<string,unknown>)[k]===v)||r.snapshot!==true||!Array.isArray(r.iterations)||r.iterations.length>expected.count||!Array.isArray(r.people))return false
 const ids=r.iterations.map(s=>s?.id)
 if(new Set(ids).size!==ids.length||!r.iterations.every(s=>Number.isSafeInteger(s.id)&&s.id>0&&typeof s.name==='string'&&typeof s.projectId==='string'&&typeof s.projectName==='string'&&/^\d{4}-\d{2}-\d{2}$/.test(s.endDate)))return false
 if(new Set(r.people.map(p=>p.userId)).size!==r.people.length)return false
 return r.people.every(p=>typeof p.userId==='string'&&typeof p.name==='string'&&typeof p.active==='boolean'&&typeof p.departmentId==='string'&&typeof p.departmentName==='string'&&(expected.scope!=='personal'||p.userId===expected.userId)&&Array.isArray(p.roles)&&new Set(p.roles.map(x=>x.role)).size===p.roles.length&&p.roles.every(role=>['frontend','backend','algorithm','ui','product'].includes(role.role)&&Array.isArray(role.points)&&role.points.length===ids.length&&role.points.every((point,i)=>point.sprintId===ids[i]&&typeof point.hasData==='boolean'&&['weight','deliveredWeight','requirementCount','shippedRequirementCount','defectCount','estimatedRoleCount','unestimatedRoleCount'].every(k=>validNumber(point[k as keyof IterationPoint]))&&point.deliveredWeight<=point.weight+0.000001)))
}
export function iterationSummaries(report:IterationReport,role:string,threshold=50):IterationSummary[] {
 const rows:IterationSummary[]=report.people.flatMap(person=>person.roles.filter(item=>item.role===role).map(item=>{
  const weight=item.points.reduce((a,p)=>a+p.weight,0),delivered=item.points.reduce((a,p)=>a+p.deliveredWeight,0)
  const missing=item.points.reduce((a,p)=>a+p.unestimatedRoleCount,0),estimated=item.points.reduce((a,p)=>a+p.estimatedRoleCount,0)
  const measured=item.points.filter(p=>p.hasData&&p.estimatedRoleCount>0&&p.unestimatedRoleCount===0)
  const latest=item.points.at(-1),baseline=item.points.slice(0,-1).filter(p=>p.hasData&&p.estimatedRoleCount>0&&p.unestimatedRoleCount===0)
  const average=baseline.length?baseline.reduce((a,p)=>a+p.weight,0)/baseline.length:0
  const change=latest?.hasData&&latest.estimatedRoleCount>0&&latest.unestimatedRoleCount===0&&baseline.length>=2&&average>0?(latest.weight-average)/average*100:null
  const alerts:string[]=[]
  if(missing)alerts.push('存在未估算权重，请先补齐')
  if(measured.length<3)alerts.push('有效样本不足 3 次，暂不判断趋势')
  if(change!==null&&change>=threshold)alerts.push('近期权重明显上升，建议核对负荷')
  if(change!==null&&change<=-threshold)alerts.push('近期权重明显下降，建议核对分配变化')
  if(latest?.hasData&&latest.unestimatedRoleCount===0&&latest.weight>0&&baseline.length>=2&&average===0)alerts.push('此前平均权重为零，请核对新增分配')
  if(latest?.hasData&&latest.unestimatedRoleCount===0&&latest.weight>latest.deliveredWeight+0.000001)alerts.push('已结束迭代仍有未完成权重，请核对状态')
  return {person,role:item.role,points:item.points,weight,delivered,missing,coverage:estimated+missing?estimated/(estimated+missing):0,completion:weight>0?delivered/weight*100:null,samples:measured.length,change,alerts,rank:null}
 }))
 // Dense ties by the displayed six-decimal measure; no-data, historical or incomplete
 // samples remain visible but never become a fabricated lowest-performing employee.
 const eligible=rows.filter(r=>r.person.active&&r.samples>=2&&!r.missing)
 eligible.sort((a,b)=>Number(b.delivered.toFixed(6))-Number(a.delivered.toFixed(6))||a.person.userId.localeCompare(b.person.userId))
 let rank=0,last:number|undefined
 for(const row of eligible){const value=Number(row.delivered.toFixed(6));if(value!==last){rank++;last=value}row.rank=rank}
 return rows.sort((a,b)=>(a.rank??Infinity)-(b.rank??Infinity)||a.person.name.localeCompare(b.person.name))
}
export function iterationSparkline(points:IterationPoint[]):{path:string;x:number;y:number;value:number}[] {
 const max=Math.max(1,...points.map(p=>p.weight)),out:{path:string;x:number;y:number;value:number}[]=[]
 let joined=false
 points.forEach((p,i)=>{if(!p.hasData||p.estimatedRoleCount===0||p.unestimatedRoleCount>0){joined=false;return}const x=12+(points.length>1?i/(points.length-1)*216:108),y=65-p.weight/max*50;out.push({path:`${joined?'L':'M'}${x},${y}`,x,y,value:p.weight});joined=true})
 return out
}
