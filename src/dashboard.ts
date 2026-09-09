export interface DashboardStatus { key:string; name:string; system:boolean; color:string; count:number }
export interface DashboardSprint { id:number; name:string; status:string; startDate:string; endDate:string; total:number; done:number; weightTotal:number }
export interface DashboardDay { date:string; requirementsCreated:number; defectsCreated:number }
export interface DashboardMember { id:string; name:string; active:boolean; requirementCount:number; defectCount:number; executionCount:number }
export type ProjectHealthStatus='healthy'|'at_risk'|'blocked'
export type ProjectHealthReasonKey='open_fatal_defects'|'overdue_sprints'|'overdue_requirements'|'open_major_defects'
export interface ProjectHealthReason { key:ProjectHealthReasonKey;count:number }
export interface ProjectHealth {
  project:{id:string;name:string};generatedAt:string;status:ProjectHealthStatus
  counts:{overdueRequirements:number;overdueSprints:number;openFatalDefects:number;openMajorDefects:number}
  reasons:ProjectHealthReason[]
}
export interface ProjectDashboard {
  project:{id:string;name:string}; generatedAt:string; days:number; timezone:string
  totals:{requirements:{total:number;done:number;cancelled:number};defects:{total:number;closed:number;open:number;critical:number};sprints:{total:number;ongoing:number;planned:number;completed:number;cancelled:number};testCases:{total:number;enabled:number};testPlans:{total:number};executions:{total:number;passed:number;failed:number;blocked:number;notRun:number;skipped:number}}
  statuses:{requirements:DashboardStatus[];defects:DashboardStatus[]};currentSprints:DashboardSprint[];trend:DashboardDay[];trendAvailable:boolean;missingCreationDates:number;members:DashboardMember[];health:ProjectHealth
}
export type DashboardMemberMetric='requirementCount'|'defectCount'|'executionCount'
export function dashboardPercent(value:number,total:number):number { return total>0?Math.round(Math.min(1,Math.max(0,value/total))*100):0 }
export function dashboardMembers(members:DashboardMember[],search:string,sort:DashboardMemberMetric|'name',descending:boolean,locale='zh-CN'):DashboardMember[] {
  const query=search.trim().toLocaleLowerCase(locale),sign=descending?-1:1
  return members.filter(person=>person.name.toLocaleLowerCase(locale).includes(query)).sort((a,b)=>{const value=sort==='name'?a.name.localeCompare(b.name,locale):a[sort]-b[sort];return value*sign||a.id.localeCompare(b.id)})
}
export function dashboardTrend(days:DashboardDay[],series:'both'|'requirements'|'defects'='both') {
  const maximum=Math.max(1,...days.flatMap(day=>[series==='defects'?0:day.requirementsCreated,series==='requirements'?0:day.defectsCreated]))
  const point=(value:number,index:number)=>`${Math.round((days.length<=1?320:index/(days.length-1)*640)*100)/100},${Math.round((140-value/maximum*124)*100)/100}`
  return {maximum,requirements:days.map((day,index)=>point(day.requirementsCreated,index)).join(' '),defects:days.map((day,index)=>point(day.defectsCreated,index)).join(' '),requirementTotal:days.reduce((sum,day)=>sum+day.requirementsCreated,0),defectTotal:days.reduce((sum,day)=>sum+day.defectsCreated,0)}
}
/** 拒绝范围不符、不完整或格式异常的响应，避免界面把失败伪装成零数据。 */
export function validDashboard(value:unknown,project:string,days:number):value is ProjectDashboard {
  if(!value||typeof value!=='object')return false
  const report=value as ProjectDashboard,count=(n:unknown)=>typeof n==='number'&&Number.isSafeInteger(n)&&n>=0
  if(report.project?.id!==project||typeof report.project?.name!=='string'||report.days!==days||!Number.isFinite(Date.parse(report.generatedAt))||report.timezone!=='Asia/Shanghai'||typeof report.trendAvailable!=='boolean'||!count(report.missingCreationDates))return false
  for(const [name,keys] of Object.entries({requirements:['total','done','cancelled'],defects:['total','closed','open','critical'],sprints:['total','ongoing','planned','completed','cancelled'],testCases:['total','enabled'],testPlans:['total'],executions:['total','passed','failed','blocked','notRun','skipped']})) {
    const values=report.totals?.[name as keyof ProjectDashboard['totals']] as Record<string,number>|undefined
    if(!values||keys.some(key=>!count(values[key])))return false
  }
  if(!Array.isArray(report.trend)||report.trend.length!==days||report.trend.some(day=>!/^\d{4}-\d{2}-\d{2}$/.test(day.date)||!count(day.requirementsCreated)||!count(day.defectsCreated)))return false
  for(const entries of [report.statuses?.requirements,report.statuses?.defects])if(!Array.isArray(entries)||entries.some(item=>typeof item.key!=='string'||typeof item.name!=='string'||typeof item.system!=='boolean'||typeof item.color!=='string'||!count(item.count)))return false
  if(!Array.isArray(report.currentSprints)||report.currentSprints.some(item=>!count(item.id)||item.id===0||typeof item.name!=='string'||typeof item.status!=='string'||typeof item.startDate!=='string'||typeof item.endDate!=='string'||!count(item.total)||!count(item.done)||item.done>item.total||!Number.isFinite(item.weightTotal)||item.weightTotal<0))return false
  if(!Array.isArray(report.members)||report.members.some(person=>typeof person.id!=='string'||typeof person.name!=='string'||typeof person.active!=='boolean'||![person.requirementCount,person.defectCount,person.executionCount].every(count)))return false
  const health=report.health,validStatus=['healthy','at_risk','blocked'].includes(health?.status),validReason=['open_fatal_defects','overdue_sprints','overdue_requirements','open_major_defects']
  if(!health||health.project?.id!==report.project.id||health.project?.name!==report.project.name||!Number.isFinite(Date.parse(health.generatedAt))||!validStatus||!health.counts||![health.counts.overdueRequirements,health.counts.overdueSprints,health.counts.openFatalDefects,health.counts.openMajorDefects].every(count)||!Array.isArray(health.reasons)||health.reasons.some(reason=>!validReason.includes(reason.key)||!count(reason.count)||reason.count===0))return false
  return true
}
