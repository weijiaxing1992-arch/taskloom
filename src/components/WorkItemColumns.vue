<script setup lang="ts">
import { ref } from 'vue'
import { t } from '../i18n'
import type { WorkField } from '../workItemQuery'
import FieldSelectionPanel from './FieldSelectionPanel.vue'
import { useSettingsDialog } from './settingsScope'
const props=defineProps<{fields:WorkField[];modelValue:string[];saving?:boolean;error?:string;disabled?:boolean}>()
const emit=defineEmits<{(event:'save',value:string[]):void}>()
const open=ref(false),draft=ref<string[]>([])
function begin(){if(props.saving||props.disabled)return;draft.value=[...props.modelValue];open.value=true}
function close(){if(!props.saving)open.value=false}
function reset(){if(!props.saving)draft.value=props.fields.filter(field=>field.fixed||field.default).map(field=>field.key)}
function save(){if(props.saving||props.disabled)return;emit('save',[...new Set([...props.fields.filter(field=>field.fixed).map(field=>field.key),...draft.value.filter(key=>props.fields.some(field=>field.key===key))])])}
const dialog=useSettingsDialog(open,close)
defineExpose({close:()=>open.value=false,begin})
</script>
<template><div><button type="button" class="btn compact" :disabled="saving||disabled" @click="begin">{{t('列设置')}} <small>{{modelValue.length}}</small></button><div v-if="open" class="modal-shade field-column-shade" @click.self="close"><section ref="dialog" tabindex="-1" class="modal work-columns" role="dialog" aria-modal="true" :aria-label="t('配置列表字段')"><header><div><h2>{{t('配置列表字段')}}</h2><p>{{t('显示设置仅影响当前账号和当前项目。')}}</p></div><button type="button" :disabled="saving" :aria-label="t('关闭')" @click="close">×</button></header><FieldSelectionPanel v-model="draft" :fields="fields" :disabled="saving" sortable/><p v-if="error" class="field-error" role="alert">{{t(error)}}</p><footer><button type="button" class="link" :disabled="saving" @click="reset">{{t('恢复默认')}}</button><button type="button" class="btn" :disabled="saving" @click="close">{{t('取消')}}</button><button type="button" class="btn primary" :disabled="saving" @click="save">{{saving?t('保存中…'):t('保存列设置')}}</button></footer></section></div></div></template>
<style scoped>
.field-column-shade{z-index:160}.work-columns{width:min(860px,95vw);max-width:860px;max-height:90dvh;display:flex;flex-direction:column;background:var(--surface);color:var(--ink);overflow:hidden}.work-columns header{align-items:flex-start;flex:none;padding:20px}.work-columns h2{margin:0;font-size:18px}.work-columns header p{font-size:12px;color:var(--muted);line-height:1.7;margin:7px 0 0}.work-columns header>button{background:transparent;border:0;color:var(--muted);font-size:22px;min-width:34px;min-height:34px}.work-columns .field-error{margin:8px 20px}.work-columns footer{display:flex;gap:9px;padding:16px 20px;flex:none}.work-columns footer .link{margin-right:auto}@media(max-width:700px){.work-columns{width:100%;max-height:calc(100dvh - 24px)}.work-columns header,.work-columns footer{padding:14px;flex-wrap:wrap}.work-columns footer button{min-height:40px}.work-columns header>div{min-width:0}.work-columns header>button{flex:none}}
</style>
