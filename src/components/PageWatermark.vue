<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { api } from '../api'
import { useWorkspaceStore } from '../stores/workspace'
import { validWatermark, watermarkName, watermarkTime, type WatermarkMetadata } from '../watermark'

const workspace = useWorkspaceStore(), patternId = `watermark-${useId()}`
const metadata = ref<WatermarkMetadata | null>(null), clock = ref(Date.now()), failed = ref(false)
const scope = computed(() => `${workspace.session?.tenant.id || ''}:${workspace.currentUser?.id || ''}:${workspace.session?.impersonation?.adminName || ''}`)
const visible = computed(() => !!workspace.currentUser && !workspace.identityConflict && !workspace.operationDisabled && !workspace.mustChangePassword)
const account = computed(() => watermarkName(metadata.value?.accountName || workspace.currentUser?.name || ''))
const timestamp = computed(() => watermarkTime(clock.value, workspace.currentUser?.timezone || 'Asia/Shanghai'))
const ip = computed(() => metadata.value ? metadata.value.ipAddress || '未知' : (failed.value ? '暂不可用' : '获取中'))
let timer: ReturnType<typeof setInterval> | undefined, controller: AbortController | undefined
let generation = 0, serverBase = Date.now(), tickBase = performance.now(), lastRefresh = 0

async function refresh() {
  if (!visible.value || document.hidden || controller) return
  const current = generation, userId = workspace.currentUser!.id
  const request = new AbortController()
  controller = request
  lastRefresh = performance.now()
  const timeout = setTimeout(() => request.abort(), 10_000)
  try {
    const data = await api<unknown>('/watermark', { signal: request.signal })
    if (current !== generation || !visible.value) return
    if (!validWatermark(data, userId)) throw new Error('水印身份或时间校验失败')
    metadata.value = data; failed.value = false
    serverBase = Date.parse(data.serverTime); tickBase = performance.now(); clock.value = serverBase
  } catch {
    // 网络异常不阻断工作区，也不伪造 IP；再次联网或下个周期自动重试。
    if (current === generation) { failed.value = true; metadata.value = null }
  } finally {
    clearTimeout(timeout)
    if (controller === request) controller = undefined
  }
}
function resume() { if (!document.hidden) { clock.value = serverBase + performance.now() - tickBase; void refresh() } }
watch([scope, visible], () => {
  generation++; controller?.abort(); controller = undefined
  metadata.value = null; failed.value = false; lastRefresh = 0
  serverBase = Date.now(); tickBase = performance.now(); clock.value = serverBase
  void refresh()
}, { immediate: true, flush: 'sync' })
onMounted(() => {
  timer = setInterval(() => {
    if (document.hidden || !visible.value) return
    clock.value = serverBase + performance.now() - tickBase
    if (performance.now() - lastRefresh >= 60_000) void refresh()
  }, 1_000)
  document.addEventListener('visibilitychange', resume)
  window.addEventListener('online', resume)
})
onBeforeUnmount(() => {
  generation++; controller?.abort(); clearInterval(timer)
  document.removeEventListener('visibilitychange', resume); window.removeEventListener('online', resume)
})
</script>

<template>
  <!-- 放到 body 顶层覆盖抽屉、弹窗；鼠标和键盘完全穿透，不改变业务布局。 -->
  <Teleport to="body">
    <svg v-if="visible" class="page-watermark" aria-hidden="true" focusable="false" xmlns="http://www.w3.org/2000/svg">
      <defs><pattern :id="patternId" width="380" height="220" patternUnits="userSpaceOnUse">
        <g transform="translate(190 110) rotate(-20)" text-anchor="middle">
          <text y="-20">{{ account }}</text><text y="0">{{ timestamp }}</text><text y="20">IP {{ ip }}</text>
        </g>
      </pattern></defs>
      <rect width="100%" height="100%" :fill="`url(#${patternId})`"/>
    </svg>
  </Teleport>
</template>

<style scoped>
.page-watermark{position:fixed;inset:0;width:100%;height:100%;z-index:2147483000;pointer-events:none;user-select:none;overflow:hidden;fill:var(--foreground,#172b4d);opacity:.10}
.page-watermark text{font:12px/1.5 -apple-system,BlinkMacSystemFont,"PingFang SC",sans-serif}
@media print{.page-watermark{position:fixed;opacity:.16;print-color-adjust:exact;-webkit-print-color-adjust:exact}}
</style>
