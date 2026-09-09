<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../api'
import { applyDisplayPreferences, displayRevision, fontSize, type FontSize } from '../displayPreferences'
import { t } from '../i18n'
const saving=ref(false),error=ref(''),saved=ref(false)
const choices = [{key:'small',label:'较小'},{key:'standard',label:'标准'},{key:'large',label:'较大'},{key:'extraLarge',label:'特大'}]
async function change(event:Event) {
  if(saving.value)return
  const value=(event.target as HTMLSelectElement).value as FontSize,previous=fontSize.value
  saving.value=true;error.value='';saved.value=false
  const revision=applyDisplayPreferences({fontSize:value})
  try { const result=await api<{fontSize:FontSize}>('/preferences/display',{method:'PATCH',body:JSON.stringify({fontSize:value})});if(displayRevision.value===revision){applyDisplayPreferences(result);saved.value=true} }
  catch(cause) { if(displayRevision.value===revision){applyDisplayPreferences({fontSize:previous});(event.target as HTMLSelectElement).value=previous}error.value=cause instanceof Error?cause.message:t('请求失败') }
  finally { saving.value=false }
}
</script>
<template>
  <section class="font-preference">
    <label><div><b>{{t('站点字号')}}</b><p>{{t('调整整个站点的文字大小，自动保存到当前账号。')}}</p></div><select :value="fontSize" :disabled="saving" :aria-label="t('站点字号')" @change="change"><option v-for="choice in choices" :key="choice.key" :value="choice.key">{{t(choice.label)}}</option></select></label>
    <small v-if="saving||saved" role="status">{{t(saving?'保存中…':'字号已保存')}}</small><p v-if="error" class="inline-notice" role="alert">{{error}}</p>
  </section>
</template>
<style scoped>
.font-preference{padding:4px 0 20px;border-bottom:1px solid var(--line)}.font-preference label{display:flex;align-items:center;justify-content:space-between;gap:24px}.font-preference p{font-size:13px;color:#667085}.font-preference select{min-width:130px;padding:8px;border:1px solid #d0d5dd;border-radius:6px;background:white}.font-preference small{color:#16836b}
</style>

<style scoped>
/* 字号设置是个人中心中最常用的原生下拉项，显式使用令牌避免暗色下残留白底。 */
.font-preference p{color:var(--muted-foreground)}
.font-preference select{background:var(--background);color:var(--foreground);border-color:var(--input)}
.font-preference select:focus-visible{outline:2px solid var(--ring);outline-offset:2px;border-color:var(--ring)}
.font-preference small{color:var(--success)}
</style>
