<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { createNavigationFeedback } from '../navigationFeedback'
import { t } from '../i18n'
const feedback = createNavigationFeedback(useRouter())
const confirming = ref(false)
watch(() => feedback.state.failed, () => { confirming.value = false })
function reopen() { feedback.reopen(() => confirming.value, path => window.location.assign(path)) }
onBeforeUnmount(feedback.dispose)
</script>
<template>
  <div v-if="feedback.state.busy" class="route-loading" role="status" aria-live="polite"><span>{{t('正在打开页面…')}}</span></div>
  <section v-else-if="feedback.state.failed" class="route-recovery" role="alert">
    <template v-if="confirming"><span>{{t('重新打开会离开当前页，未保存的内容可能丢失。请先保存；确认现在重新打开吗？')}}</span><div><button type="button" class="btn compact" @click="reopen">{{t('确认重新打开')}}</button><button type="button" class="btn compact" @click="confirming=false">{{t('取消')}}</button></div></template>
    <template v-else><span>{{t(feedback.state.reloadSuggested?'重试仍未成功。请先保存当前内容，再重新打开页面。':'页面暂时无法打开，可重试或留在当前页面。')}}</span>
    <div><button v-if="feedback.state.reloadSuggested" type="button" class="btn compact" @click="confirming=true">{{t('重新打开页面')}}</button><button v-else type="button" class="btn compact" @click="feedback.retry">{{t('重试打开')}}</button><button type="button" class="btn compact" @click="feedback.dismiss">{{t('留在当前页')}}</button></div></template>
  </section>
</template>
<style scoped>
.route-loading{position:fixed;top:0;left:0;right:0;z-index:2100;height:3px;overflow:hidden;pointer-events:none;background:color-mix(in srgb,var(--primary) 12%,transparent)}
.route-loading::after{content:'';display:block;height:100%;width:35%;background:var(--primary);animation:route-load 1.2s ease-in-out infinite}
.route-loading span{position:absolute;width:1px;height:1px;overflow:hidden;clip-path:inset(50%)}
.route-recovery{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:12px 18px;background:var(--surface);border-bottom:1px solid var(--line);color:var(--ink);font-size:var(--ui-font-body);line-height:1.6}
.route-recovery>div{display:flex;gap:8px;flex:none}.route-recovery>span{min-width:0;overflow-wrap:anywhere}
@keyframes route-load{from{transform:translateX(-110%)}to{transform:translateX(390%)}}
@media(prefers-reduced-motion:reduce){.route-loading::after{animation:none;width:100%}}
@media(max-width:600px){.route-recovery{align-items:stretch;flex-direction:column}.route-recovery>div{flex-wrap:wrap}}
</style>
