<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { t } from '../i18n'
import { useSettingsScope } from './settingsScope'
import { Button } from './ui/button'

type AlertLevel = 'warning'|'critical'
interface Alert { level:AlertLevel; title:string; detail:string; metric?:string }
interface Snapshot {
  collectedAt:string; status:'healthy'|'warning'|'critical'; alerts:Alert[]
  server:{startedAt:string;uptimeSeconds:number;address:string;operatingSystem:string;architecture:string;goVersion:string;webRoot:string}
  host:{hostname:string;cpuCores:number;load1?:number;memory:{totalBytes:number;availableBytes:number};disk:{totalBytes:number;availableBytes:number;path:string}}
  process:{pid:number;goroutines:number;heapAllocBytes:number;heapSystemBytes:number;openConnections:number;inUseConnections:number;requestCapacity:number;inFlightRequests:number}
  database:{fileName:string;fileBytes:number;walBytes:number;pingMillis:number;quickCheck:string;foreignKeys:boolean;synchronous:string}
}

const scope=useSettingsScope(), snapshot=ref<Snapshot|null>(null), loading=ref(false), error=ref('')
let timer:number|undefined
const statusText=computed(()=>({healthy:'运行正常',warning:'存在关注项',critical:'需要立即处理'})[snapshot.value?.status||'healthy'])
const statusClass=computed(()=>snapshot.value?.status||'healthy')

function formatBytes(value:number|undefined){
  if(value===undefined||!Number.isFinite(value)||value<0)return '—'
  const units=['B','KB','MB','GB','TB'];let index=0,size=value
  while(size>=1024&&index<units.length-1){size/=1024;index++}
  return `${size>=100||index===0?Math.round(size):size.toFixed(1)} ${units[index]}`
}
function resourceRate(available:number,total:number){return total>0?Math.max(0,Math.min(100,available/total*100)):null}
function resourceText(available:number,total:number){const rate=resourceRate(available,total);return rate===null?'—':`${formatBytes(available)} 可用 · ${rate.toFixed(0)}%`}
function uptime(seconds:number){
  const days=Math.floor(seconds/86400),hours=Math.floor(seconds%86400/3600),minutes=Math.floor(seconds%3600/60)
  return days?`${days} 天 ${hours} 小时`:hours?`${hours} 小时 ${minutes} 分钟`:`${Math.max(0,minutes)} 分钟`
}
function date(value:string){return value?new Intl.DateTimeFormat('zh-CN',{dateStyle:'medium',timeStyle:'medium'}).format(new Date(value)):'—'}
function checkText(value:string){return value==='ok'?'通过':value==='failed'?'失败':value==='unavailable'?'不可用':'未知'}
function alertLevel(alert:Alert){return alert.level==='critical'?'严重风险':'需要关注'}
async function load(){
  if(loading.value||!scope.current())return
  loading.value=true;error.value=''
  try{snapshot.value=await scope.request<Snapshot>('/organization/server-monitor')}
  catch(cause){if(scope.current())error.value=cause instanceof Error?cause.message:'无法加载服务器监控'}
  finally{if(scope.current())loading.value=false}
}
onMounted(()=>{void load();timer=window.setInterval(()=>void load(),30000)})
onBeforeUnmount(()=>{if(timer!==undefined)window.clearInterval(timer)})
</script>

<template>
  <section class="server-monitor" :aria-busy="loading">
    <header class="server-monitor-head">
      <div><h2>{{ t('服务器监控') }}</h2><p>{{ t('只读采集当前主机与 SQLite 运行状态；每 30 秒自动刷新，不展示密钥或完整环境变量。') }}</p></div>
      <div class="server-monitor-actions"><small v-if="snapshot">{{ t('更新于') }} {{ date(snapshot.collectedAt) }}</small><Button size="toolbar" variant="outline" :disabled="loading||scope.locked.value" @click="load">{{ t(loading?'刷新中…':'刷新') }}</Button></div>
    </header>
    <p v-if="scope.locked.value" role="alert" class="org-error">{{ t('项目或账号已变化，请刷新页面后继续') }}</p>
    <p v-else-if="error" role="alert" class="org-error">{{ t(error) }}</p>
    <p v-else-if="loading&&!snapshot" role="status" class="monitor-loading">{{ t('正在采集服务器状态…') }}</p>
    <template v-else-if="snapshot">
      <section class="monitor-overview" :class="statusClass" aria-live="polite">
        <div><span class="monitor-kicker">{{ t('当前风险等级') }}</span><strong>{{ t(statusText) }}</strong><small>{{ snapshot.alerts.length?t('已发现 {count} 项风险信号',{count:snapshot.alerts.length}):t('未发现超过阈值的服务器或数据库风险。') }}</small></div>
        <dl><div><dt>{{ t('运行时长') }}</dt><dd>{{ uptime(snapshot.server.uptimeSeconds) }}</dd></div><div><dt>{{ t('数据库响应') }}</dt><dd>{{ snapshot.database.pingMillis }} ms</dd></div><div><dt>{{ t('请求并发') }}</dt><dd>{{ snapshot.process.inFlightRequests }} / {{ snapshot.process.requestCapacity||'—' }}</dd></div></dl>
      </section>

      <section v-if="snapshot.alerts.length" class="monitor-alerts" :aria-label="t('风险提醒')">
        <article v-for="alert in snapshot.alerts" :key="`${alert.level}-${alert.metric}-${alert.title}`" :class="alert.level"><b>{{ t(alertLevel(alert)) }} · {{ t(alert.title) }}</b><p>{{ t(alert.detail) }}</p></article>
      </section>

      <div class="monitor-resource-cards">
        <article><span>{{ t('逻辑 CPU') }}</span><b>{{ snapshot.host.cpuCores }} {{ t('核') }}</b><small>{{ snapshot.host.load1===undefined?t('1 分钟负载不可用'):t('1 分钟负载 {value}',{value:snapshot.host.load1.toFixed(2)}) }}</small></article>
        <article><span>{{ t('主机可用内存') }}</span><b>{{ resourceText(snapshot.host.memory.availableBytes,snapshot.host.memory.totalBytes) }}</b><small>{{ t('总内存') }} {{ formatBytes(snapshot.host.memory.totalBytes) }} · {{ t('macOS 为可回收内存估算') }}</small></article>
        <article><span>{{ t('数据库所在磁盘') }}</span><b>{{ resourceText(snapshot.host.disk.availableBytes,snapshot.host.disk.totalBytes) }}</b><small>{{ t('数据目录') }} {{ snapshot.host.disk.path||'—' }}</small></article>
        <article><span>{{ t('服务堆内存') }}</span><b>{{ formatBytes(snapshot.process.heapAllocBytes) }}</b><small>{{ t('已申请堆空间') }} {{ formatBytes(snapshot.process.heapSystemBytes) }} · {{ snapshot.process.goroutines }} {{ t('个协程') }}</small></article>
      </div>

      <div class="monitor-grid">
        <section class="monitor-panel"><h3>{{ t('服务配置') }}</h3><dl class="monitor-details"><div><dt>{{ t('主机名称') }}</dt><dd>{{ snapshot.host.hostname }}</dd></div><div><dt>{{ t('监听地址') }}</dt><dd><code>{{ snapshot.server.address }}</code></dd></div><div><dt>{{ t('运行环境') }}</dt><dd>{{ snapshot.server.operatingSystem }} / {{ snapshot.server.architecture }}</dd></div><div><dt>{{ t('Go 版本') }}</dt><dd>{{ snapshot.server.goVersion }}</dd></div><div><dt>{{ t('启动时间') }}</dt><dd>{{ date(snapshot.server.startedAt) }}</dd></div><div><dt>{{ t('静态资源目录') }}</dt><dd>{{ snapshot.server.webRoot||'—' }}</dd></div></dl></section>
        <section class="monitor-panel"><h3>{{ t('数据库健康') }}</h3><dl class="monitor-details"><div><dt>{{ t('数据库文件') }}</dt><dd>{{ snapshot.database.fileName }}</dd></div><div><dt>{{ t('数据库大小') }}</dt><dd>{{ formatBytes(snapshot.database.fileBytes) }}</dd></div><div><dt>{{ t('WAL 日志大小') }}</dt><dd>{{ formatBytes(snapshot.database.walBytes) }}</dd></div><div><dt>{{ t('快速完整性检查') }}</dt><dd :class="snapshot.database.quickCheck==='ok'?'monitor-good':'monitor-bad'">{{ t(checkText(snapshot.database.quickCheck)) }}</dd></div><div><dt>{{ t('外键约束') }}</dt><dd :class="snapshot.database.foreignKeys?'monitor-good':'monitor-bad'">{{ t(snapshot.database.foreignKeys?'已启用':'未启用') }}</dd></div><div><dt>{{ t('同步模式') }}</dt><dd><code>{{ snapshot.database.synchronous }}</code></dd></div></dl></section>
        <section class="monitor-panel"><h3>{{ t('运行进程') }}</h3><dl class="monitor-details"><div><dt>PID</dt><dd>{{ snapshot.process.pid }}</dd></div><div><dt>{{ t('连接池') }}</dt><dd>{{ snapshot.process.inUseConnections }} {{ t('使用中') }} / {{ snapshot.process.openConnections }} {{ t('已打开') }}</dd></div><div><dt>{{ t('请求处理槽') }}</dt><dd>{{ snapshot.process.inFlightRequests }} / {{ snapshot.process.requestCapacity||'—' }}</dd></div><div><dt>{{ t('协程数量') }}</dt><dd>{{ snapshot.process.goroutines }}</dd></div></dl></section>
      </div>
      <p class="monitor-footnote">{{ t('阈值：磁盘或可用内存低于 20% 提醒、低于 10% 严重；CPU 负载按逻辑核心数判断；数据库完整性与外键异常始终视为严重风险。') }}</p>
    </template>
  </section>
</template>

<style scoped>
.server-monitor{display:grid;gap:16px}.server-monitor-head{display:flex;justify-content:space-between;align-items:flex-start;gap:16px;flex-wrap:wrap}.server-monitor-head h2{margin:0 0 7px;font-size:18px}.server-monitor-head p,.monitor-footnote,.monitor-loading{margin:0;color:var(--muted-foreground);font-size:12px;line-height:1.7}.server-monitor-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.server-monitor-actions small{color:var(--muted-foreground);font-size:11px}.monitor-overview{display:flex;align-items:center;justify-content:space-between;gap:20px;padding:18px 20px;border:1px solid var(--success-border);border-radius:10px;background:var(--success-background)}.monitor-overview.warning{border-color:var(--warning-border);background:var(--warning-background)}.monitor-overview.critical{border-color:var(--danger-border);background:var(--danger-background)}.monitor-overview>div{display:grid;gap:4px}.monitor-kicker{font-size:11px;color:var(--muted-foreground)}.monitor-overview strong{font-size:20px;color:var(--success)}.monitor-overview.warning strong{color:var(--warning)}.monitor-overview.critical strong{color:var(--danger)}.monitor-overview small{color:var(--muted-foreground);font-size:12px}.monitor-overview dl{display:flex;gap:28px;margin:0}.monitor-overview dl div{display:grid;gap:5px}.monitor-overview dt,.monitor-details dt{color:var(--muted-foreground);font-size:11px}.monitor-overview dd,.monitor-details dd{margin:0;color:var(--foreground);font-size:13px;font-weight:600}.monitor-alerts{display:grid;gap:8px}.monitor-alerts article{padding:11px 13px;border:1px solid var(--warning-border);border-radius:8px;background:var(--warning-background);color:var(--warning)}.monitor-alerts article.critical{border-color:var(--danger-border);background:var(--danger-background);color:var(--danger)}.monitor-alerts b{font-size:12px}.monitor-alerts p{margin:4px 0 0;font-size:12px;line-height:1.6;color:var(--foreground)}.monitor-resource-cards{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px}.monitor-resource-cards article,.monitor-panel{border:1px solid var(--border);border-radius:9px;background:var(--card);padding:15px}.monitor-resource-cards article{display:grid;gap:8px;min-width:0}.monitor-resource-cards span{font-size:11px;color:var(--muted-foreground)}.monitor-resource-cards b{font-size:16px;overflow-wrap:anywhere}.monitor-resource-cards small{font-size:11px;line-height:1.6;color:var(--muted-foreground)}.monitor-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px}.monitor-panel h3{margin:0 0 13px;font-size:14px}.monitor-details{margin:0;display:grid;gap:10px}.monitor-details>div{display:grid;gap:3px;min-width:0}.monitor-details dd{overflow-wrap:anywhere;font-weight:500}.monitor-details code{font-size:11px;padding:2px 5px;border-radius:4px;background:var(--secondary);color:var(--foreground)}.monitor-good{color:var(--success)!important}.monitor-bad{color:var(--danger)!important}.monitor-footnote{padding:0 2px}@media(max-width:1080px){.monitor-resource-cards{grid-template-columns:repeat(2,minmax(0,1fr))}.monitor-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.monitor-overview{align-items:flex-start;flex-direction:column}.monitor-overview dl{width:100%;justify-content:space-between}}@media(max-width:640px){.server-monitor-head,.server-monitor-actions{width:100%}.server-monitor-actions{justify-content:space-between}.monitor-resource-cards,.monitor-grid{grid-template-columns:minmax(0,1fr)}.monitor-overview dl{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.monitor-overview dd{font-size:12px}}
</style>
