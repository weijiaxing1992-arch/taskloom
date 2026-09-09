<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { t } from '../i18n'
import { useSettingsDialog } from './settingsScope'
import { Button } from './ui/button'
const props=defineProps<{title:string;busy?:boolean;wide?:boolean}>()
const emit=defineEmits<{(event:'close'):void}>()
const opened=ref(false)
const modal=useSettingsDialog(opened,close)
onMounted(()=>{opened.value=true})
function close(){if(!props.busy)emit('close')}
</script>
<template>
  <div class="org-modal-shade" @click.self="close"><section ref="modal" class="org-modal" :class="{'org-modal-wide':wide}" tabindex="-1" role="dialog" aria-modal="true" :aria-label="title"><header><h2>{{title}}</h2><Button type="button" variant="ghost" size="icon" :disabled="busy" :aria-label="t('关闭')" @click="close">×</Button></header><div class="org-modal-body"><slot /></div><footer v-if="$slots.footer"><slot name="footer" /></footer></section></div>
</template>
<style scoped>
/* 弹窗须自带桌面布局，不能依赖企业管理路由按需加载的样式。 */
.org-modal-shade{position:fixed;inset:0;z-index:270;display:flex;align-items:center;justify-content:center;padding:24px;background:#0f172a66}
.org-modal{width:min(600px,100%);min-width:0;max-height:calc(100dvh - 48px);display:flex;flex-direction:column;min-height:0;background:var(--surface,#fff);color:var(--ink);border:1px solid var(--line);border-radius:8px;box-shadow:none;text-align:left}
.org-modal-wide{width:min(960px,100%)}
.org-modal>header{padding:16px 20px;display:flex;align-items:center;justify-content:space-between;gap:12px;border-bottom:1px solid var(--line);flex:none}
.org-modal h2{font-size:17px;margin:0;min-width:0;overflow-wrap:anywhere}
.org-modal>header button{flex:none}
.org-modal-body{padding:20px;overflow:auto;min-height:0;min-width:0;scrollbar-gutter:stable;overscroll-behavior:contain}
.org-modal>footer{display:flex;justify-content:flex-end;gap:8px;padding:14px 20px;border-top:1px solid var(--line);flex:none}
@media(max-width:820px){
 .org-modal-shade{padding:10px;padding-top:max(10px,env(safe-area-inset-top));padding-bottom:max(10px,env(safe-area-inset-bottom))}
 .org-modal{min-width:0;width:100%;max-height:calc(100dvh - 20px - env(safe-area-inset-top) - env(safe-area-inset-bottom));border-radius:12px}
 .org-modal>header{padding:12px 16px;gap:12px}.org-modal h2{min-width:0;overflow-wrap:anywhere;font-size:16px}.org-modal>header button{flex:none;min-width:44px;min-height:44px}
 .org-modal-body{padding:16px;min-width:0;overscroll-behavior:contain}
 .org-modal>footer{padding:12px 16px;flex-wrap:wrap;gap:8px}.org-modal>footer :deep(button){min-height:44px;white-space:normal}
}
</style>
