import type { WorkloadMetrics, WorkloadPerson, WorkloadDepartment, WorkloadReport } from './workload'
export type TrendMetric = 'weight'|'shippedRequirementCount'|'defectCount'
export type TrendMode = 'monthly'|'iterations'
export interface TrendPoint { key:string;month:string;sprintCount:number;sprintId?:number;name?:string;projectId?:string;projectName?:string;endDate?:string;hasData:boolean;metrics:WorkloadMetrics }
export interface TrendFilters { department:string;user:string;role:string }
export interface WorkloadTrendReport { month:string;months:number;fromMonth:string;generatedAt:string;snapshot:boolean;precision:number;scope:WorkloadReport['scope'];filters:TrendFilters;monthly:TrendPoint[];iterations:TrendPoint[];options:{people:WorkloadPerson[];departments:WorkloadDepartment[]};ambiguousSprintItemCount:number }
export function validTrendReport(data:WorkloadTrendReport,expected:{tenant:string;month:string;months:number;filters:TrendFilters}):boolean {
 if(!data||data.month!==expected.month||data.months!==expected.months||data.snapshot!==true||data.scope?.type!=='organization'||data.scope.tenantId!==expected.tenant||!data.filters||!['department','user','role'].every(key=>data.filters[key as keyof TrendFilters]===expected.filters[key as keyof TrendFilters]))return false
 if(!Array.isArray(data.monthly)||!Array.isArray(data.iterations)||!Array.isArray(data.options?.people)||!Array.isArray(data.options?.departments))return false
 for(const points of [data.monthly,data.iterations]){
  const seen=new Set<string>()
  for(const point of points){if(!point||typeof point.key!=='string'||seen.has(point.key)||typeof point.hasData!=='boolean'||!point.metrics||!['weight','requirementCount','shippedRequirementCount','defectCount','estimatedRoleCount','unestimatedRoleCount'].every(key=>typeof point.metrics[key as keyof WorkloadMetrics]==='number'&&Number.isFinite(point.metrics[key as keyof WorkloadMetrics])&&point.metrics[key as keyof WorkloadMetrics]>=0))return false;seen.add(point.key)}
 }
 return true
}
export function trendMetricValue(point:TrendPoint,metric:TrendMetric):number|null {
 if(!point.hasData||(metric==='weight'&&point.metrics.estimatedRoleCount===0))return null
 return point.metrics[metric]
}
export function trendGeometry(points:TrendPoint[],metric:TrendMetric){
 const width=Math.max(620,points.length*78+64),height=230,left=48,right=24,top=20,bottom=38
 const values=points.map(point=>trendMetricValue(point,metric)),peak=Math.max(1,...values.map(value=>value??0)),max=metric==='weight'?peak:Math.max(4,Math.ceil(peak/4)*4)
 const dots=points.map((point,index)=>({...point,value:values[index],x:points.length===1?(width+left-right)/2:left+index*(width-left-right)/Math.max(1,points.length-1),y:values[index]===null?null:height-bottom-(values[index]||0)/max*(height-top-bottom)}))
 const paths:string[]=[];let segment:string[]=[]
 for(const point of dots){if(point.y===null){if(segment.length)paths.push(segment.join(' '));segment=[]}else segment.push(`${point.x},${point.y}`)}
 if(segment.length)paths.push(segment.join(' '))
 return {width,height,left,right,top,bottom,max,dots,paths,ticks:[0,.25,.5,.75,1].map(ratio=>({value:ratio*max,y:height-bottom-ratio*(height-top-bottom)}))}
}
