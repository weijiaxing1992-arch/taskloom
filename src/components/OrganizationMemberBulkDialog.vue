<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { t } from '../i18n'
import type { OrganizationMember } from '../organization'
import { validWecomWebhookURL } from '../wecomWebhook'
import OrganizationModal from './OrganizationModal.vue'
import { Button } from './ui/button'

type BulkAction='activate'|'deactivate'|'delete'|'wecom-config'
const props=defineProps<{action:BulkAction;members:OrganizationMember[];currentUserId:string;busy:boolean;error:string;uncertain:boolean}>()
const emit=defineEmits<{(event:'close'):void;(event:'confirm',webhook?:{enabled?:boolean;url?:string}):void}>()
const confirmed=ref(false),protectedConfirmed=ref(false),sharedConfirmed=ref(false)
const mode=ref<'enable-existing'|'disable-existing'|'replace'>('enable-existing'),replacementState=ref<'keep'|'enable'|'disable'>('keep'),url=ref(''),localError=ref('')
const discardOpen=ref(false),discardPrompt=ref<HTMLElement|null>(null)
const names:Record<BulkAction,string>={activate:'批量激活成员',deactivate:'批量停用账号',delete:'批量删除成员','wecom-config':'批量配置机器人'}
const title=computed(()=>t(names[props.action]))
const protectedMembers=computed(()=>props.members.filter(member=>member.tenantRole==='tenant_admin'||member.id===props.currentUserId))
const protectedActionBlocked=computed(()=>['deactivate','delete'].includes(props.action)&&!!protectedMembers.value.length)
const dirty=computed(()=>props.action==='wecom-config'&&(!!url.value||mode.value!=='enable-existing'||replacementState.value!=='keep'))
const maySubmit=computed(()=>!props.busy&&!props.uncertain&&!protectedActionBlocked.value&&!discardOpen.value&&confirmed.value&&!!props.members.length&&(!protectedMembers.value.length||protectedConfirmed.value)&&(props.action!=='wecom-config'||mode.value!=='replace'||sharedConfirmed.value&&validWecomWebhookURL(url.value)))

// 更改机器人方案或地址后，之前的确认不再适用；密钥只在当前弹窗内存中保留。
watch([mode,replacementState,url],()=>{confirmed.value=false;sharedConfirmed.value=false;localError.value='';discardOpen.value=false},{flush:'sync'})
function submit(){
  if(props.busy||props.uncertain)return
  localError.value=''
  if(props.action==='wecom-config'&&mode.value==='replace'&&!validWecomWebhookURL(url.value)){localError.value='请输入有效的企业微信官方机器人地址';return}
  if(!maySubmit.value)return
  if(props.action!=='wecom-config'){emit('confirm');return}
  if(mode.value==='replace')emit('confirm',{url:url.value.trim(),...(replacementState.value==='keep'?{}:{enabled:replacementState.value==='enable'})})
  else emit('confirm',{enabled:mode.value==='enable-existing'})
}
function discard(){if(props.busy)return;url.value='';confirmed.value=false;sharedConfirmed.value=false;protectedConfirmed.value=false;discardOpen.value=false;emit('close')}
function requestClose(){
  if(props.busy)return false
  if(!dirty.value){discard();return true}
  discardOpen.value=true;void nextTick(()=>discardPrompt.value?.focus());return false
}
function canLeave(){if(props.busy)return false;if(!dirty.value)return true;requestClose();return false}
onBeforeUnmount(()=>{url.value='';confirmed.value=false;protectedConfirmed.value=false;sharedConfirmed.value=false})
defineExpose({dirty,canLeave,requestClose})
</script>

<template>
  <OrganizationModal :title="title" :busy="busy" :presentation="action==='wecom-config'?'drawer':'confirmation'" wide @close="requestClose">
    <form id="org-member-bulk-form" class="org-form member-bulk-form" @submit.prevent="submit">
      <p class="member-bulk-summary">{{t('本次将处理 {count} 位成员，请逐一核对名单',{count:members.length})}}</p>
      <p v-if="action==='activate'" class="org-note">{{t('激活账号并恢复业务操作。已设置的密码保留；缺少凭据时按系统初始密码规则处理，无法激活的任一成员会使整批失败。')}}</p>
      <p v-else-if="action==='deactivate'" class="org-note">{{t('停用后所选成员不能登录，已有会话会失效；需求、缺陷、评论等历史保留。这与仍可登录的“业务禁用”不同。')}}</p>
      <p v-else-if="action==='delete'" class="org-note">{{t('将撤销所选成员的组织、部门、项目和权限组访问并终止会话；业务与审计历史保留。成员移除没有普通恢复入口。')}}</p>
      <div class="member-bulk-targets" tabindex="0" :aria-label="t('本次批量操作成员名单')">
        <table class="org-table"><thead><tr><th>{{t('成员')}}</th><th>{{t('登录邮箱')}}</th><th>{{t('身份')}}</th></tr></thead><tbody><tr v-for="member in members" :key="member.id"><td>{{member.name}}</td><td>{{member.email}}</td><td>{{t(member.tenantRole==='tenant_admin'?'企业管理员':'普通成员')}}<span v-if="member.id===currentUserId"> · {{t('当前账号')}}</span></td></tr></tbody></table>
      </div>
      <fieldset v-if="action==='wecom-config'" class="member-bulk-webhook" :disabled="busy||uncertain">
        <div class="org-form-two"><label>{{t('批量机器人操作')}}<select v-model="mode"><option value="enable-existing">{{t('启用各自已有机器人')}}</option><option value="disable-existing">{{t('停用各自已有机器人')}}</option><option value="replace">{{t('为所有所选成员设置同一地址')}}</option></select></label><label v-if="mode==='replace'">{{t('替换地址后的通知开关')}}<select v-model="replacementState"><option value="keep">{{t('保留各自当前开关')}}</option><option value="enable">{{t('统一启用')}}</option><option value="disable">{{t('统一停用')}}</option></select></label></div>
        <p v-if="mode==='enable-existing'" class="org-note">{{t('保留各自地址并启用；任一成员尚未配置地址时整批失败，不会自动跳过。')}}</p>
        <p v-else-if="mode==='disable-existing'" class="org-note">{{t('保留各自地址并停用；未配置地址的成员也可停用，站内通知不受影响。')}}</p>
        <template v-else><label>{{t('机器人 Webhook 地址')}}<input v-model="url" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="1024" :placeholder="t('粘贴企业微信官方机器人 Webhook 地址')" :aria-label="t('为所选成员设置的机器人地址')"></label><p class="org-note">{{t('同一群内所有成员可能看到所选账号的相关通知。地址会替换已有配置，请确认群成员均有权访问这些内容。')}}</p><label class="org-check"><input v-model="sharedConfirmed" type="checkbox">{{t('我确认同一机器人地址用于名单中的全部成员')}}</label></template>
        <p class="org-note">{{t('保存只修改设置，不主动发送测试消息或补发历史；之后的新通知按服务端投递模式处理。')}}</p>
      </fieldset>
      <p v-if="protectedActionBlocked" class="org-error" role="alert">{{t('批量停用和删除不能包含当前账号或企业管理员，请取消勾选这些成员')}}</p>
      <label v-else-if="protectedMembers.length" class="org-check member-bulk-protected"><input v-model="protectedConfirmed" type="checkbox" :disabled="busy||uncertain">{{t('所选包含企业管理员或当前账号，我确认他们也在本次操作范围内')}}</label>
      <label class="org-check"><input v-model="confirmed" type="checkbox" :disabled="busy||uncertain">{{t('我已核对 {count} 位成员及操作内容',{count:members.length})}}</label>
      <p class="org-note">{{t('任意成员校验失败时整批不生效，不会只处理其中一部分。')}}</p>
      <p v-if="error||localError" class="org-error" role="alert">{{t(localError||error)}}</p>
      <p v-if="uncertain" class="org-error" role="alert">{{t('操作结果尚未确认。请关闭后刷新名单或逐个核对机器人配置，不要直接再次提交。')}}</p>
    </form>
    <template #footer>
      <div v-if="discardOpen" ref="discardPrompt" class="member-bulk-discard" tabindex="-1" role="alert"><p>{{t('放弃尚未保存的机器人批量设置？')}}</p><div class="org-actions"><Button type="button" variant="outline" :disabled="busy" @click="discardOpen=false">{{t('继续编辑')}}</Button><Button type="button" variant="destructive" :disabled="busy" @click="discard">{{t('放弃修改')}}</Button></div></div>
      <template v-else><Button type="button" variant="outline" :disabled="busy" @click="requestClose">{{t('取消')}}</Button><Button form="org-member-bulk-form" type="submit" :variant="action==='delete'||action==='deactivate'?'destructive':'default'" :disabled="!maySubmit">{{t(busy?'处理中…':'确认批量操作')}}</Button></template>
    </template>
  </OrganizationModal>
</template>

<style scoped>
.member-bulk-form{gap:12px}.member-bulk-summary{margin:0;font-weight:600;color:var(--foreground)}.member-bulk-form>.org-note{margin:0}.member-bulk-targets{max-height:220px;overflow:auto;border:1px solid var(--border);border-radius:6px}.member-bulk-targets table{min-width:520px}.member-bulk-targets th{position:sticky;top:0;background:var(--secondary)}.member-bulk-targets td{overflow-wrap:anywhere}.member-bulk-webhook{display:grid;gap:10px;border:0;border-top:1px solid var(--border);padding:12px 0 0;margin:0;min-width:0}.member-bulk-webhook>.org-note{margin:0}.member-bulk-webhook>label:not(.org-check){display:grid;gap:6px}.member-bulk-protected{padding:10px;border:1px solid var(--warning-border);border-radius:6px;background:var(--warning-background);color:var(--warning)}.member-bulk-discard{display:flex;align-items:center;justify-content:space-between;gap:12px;width:100%}.member-bulk-discard p{margin:0;line-height:1.7}.member-bulk-discard .org-actions{flex:none}@media(max-width:640px){.member-bulk-discard{flex-wrap:wrap}.member-bulk-discard .org-actions{margin-left:auto}.member-bulk-targets{max-height:180px}}
</style>
