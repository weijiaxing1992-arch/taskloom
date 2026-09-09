import { ref, watch } from 'vue'
import { layoutScope } from './layoutScope'

export const defaultSprintTabs = ['工作项列表', '概览', '看板', '权重统计', '团队跟踪', '进度图', '仪表盘'] as const
export function normalizeSprintTabs(value: unknown): string[] {
  const allowed = new Set<string>(defaultSprintTabs)
  const configured = Array.isArray(value) ? [...new Set(value.filter((item):item is string=>typeof item==='string'&&allowed.has(item)))] : []
  return [...configured, ...defaultSprintTabs.filter(item=>!configured.includes(item))]
}
export function moveSprintTab(items: readonly string[], item: string, target: number): string[] {
  const next=normalizeSprintTabs(items),from=next.indexOf(item)
  if(from<0||!Number.isInteger(target)||target<0||target>=next.length)return next
  next.splice(from,1);next.splice(target,0,item);return next
}
export function useSprintTabs() {
  const key=()=>layoutScope.value?'devflow-layout:v1:'+layoutScope.value+':sprints.tab-order':''
  const read=()=>{try{const name=key();return normalizeSprintTabs(name?JSON.parse(localStorage.getItem(name)||'null'):null)}catch{return normalizeSprintTabs(null)}}
  const order=ref(read());let restoring=false
  watch(layoutScope,()=>{restoring=true;order.value=read();restoring=false},{flush:'sync'})
  watch(order,value=>{if(restoring||!key())return;try{localStorage.setItem(key(),JSON.stringify(normalizeSprintTabs(value)))}catch{/* Storage restrictions must not block navigation. */}},{flush:'sync'})
  return order
}
