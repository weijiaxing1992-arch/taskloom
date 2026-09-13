<script setup lang="ts">
import { t, locale, formatDate } from '../i18n'
import{computed,onMounted,reactive,ref}from'vue';import{api}from'../api'
const roles=[['project_admin','项目管理员'],['product','产品'],['frontend','前端'],['backend','后端'],['algorithm','算法'],['ui','UI 设计'],['frontend_lead','前端组长'],['backend_lead','后端组长'],['qa','测试'],['viewer','只读']]
const items=ref<any[]>([]),role=ref(''),status=ref(''),show=ref(false),error=ref(''),loading=ref(false),saving=ref(false)
const form=reactive<any>({name:'',email:'',employeeNo:'',department:'',tenantRole:'member',projectRole:'backend',initialPassword:''})
// 保持旧成员路由与组织中心一致：代访问默认记录常规工作查看原因。
const defaultImpersonationReason='查看工作'
const context=ref<any>(null),impersonating=ref<any>(null),reason=ref('')
const filtered=computed(()=>items.value.filter(x=>(!role.value||x.projectRole===role.value)&&(!status.value||String(x.active)===status.value)))
const message=(cause:unknown)=>cause instanceof Error?cause.message:'操作失败，请稍后重试'
async function load(){loading.value=true;try{const [members,session]=await Promise.all([api<any>('/members'),api('/session')]);items.value=members.items||[];context.value=session}catch(cause){error.value=message(cause)}finally{loading.value=false}}
onMounted(load)
async function save(){if(saving.value)return;error.value='';saving.value=true;try{await api('/members',{method:'POST',body:JSON.stringify(form)});show.value=false;Object.assign(form,{name:'',email:'',employeeNo:'',initialPassword:''});await load()}catch(cause){error.value=message(cause)}finally{saving.value=false}}
async function patch(x:any,p:any){if(saving.value)return;error.value='';saving.value=true;try{await api('/members',{method:'PATCH',body:JSON.stringify({id:x.id,...p})});await load()}catch(cause){error.value=message(cause)}finally{saving.value=false}}
function canImpersonateMember(member:any){return !!member&&context.value?.canImpersonate===true&&!context.value?.impersonation&&member.active&&member.id!==context.value?.user?.id&&!member.isCurrent}
function validImpersonationReason(value:string){const length=Array.from(value.trim()).length;return length>=4&&length<=500}
function prepareImpersonation(member:any){if(!canImpersonateMember(member)||saving.value)return;impersonating.value=member;reason.value=defaultImpersonationReason;error.value=''}
function closeImpersonation(){if(saving.value)return;impersonating.value=null;reason.value='';error.value=''}
async function startImpersonation(){
 if(saving.value||!canImpersonateMember(impersonating.value)||!validImpersonationReason(reason.value))return
 error.value='';saving.value=true
 try{const result=await api<any>('/auth/impersonation',{method:'POST',body:JSON.stringify({userId:impersonating.value.id,reason:reason.value.trim()})});localStorage.setItem('devflow-project',result.projectId);location.href='/my-work'}catch(cause){error.value=message(cause)}finally{saving.value=false}
}
</script>
<template>
<div class="module-page">
<div class="page-heading">
<div>
<span class="eyebrow">{{ t("企业管理") }}</span>
<h1>{{ t("母账户与子账户") }}</h1>
<p>{{ t("母账户负责企业治理，子账户通过租户成员关系和项目角色获得权限。") }}</p>
</div>
<button class="btn primary" @click="show=true">{{ t("＋ 创建子账户") }}</button>
</div>
<p v-if="error" class="field-error" role="alert">{{ t(error) }}</p>
<p v-if="loading" role="status">{{ t("正在加载…") }}</p>
<div class="account-summary">
<div>
<b>{{items.length}}</b>
<span>{{ t("企业成员") }}</span>
</div>
<div>
<b>{{items.filter(x=>x.tenantRole==='tenant_admin').length}}</b>
<span>{{ t("母账户 / 管理员") }}</span>
</div>
<div>
<b>{{items.filter(x=>x.active).length}}</b>
<span>{{ t("已启用") }}</span>
</div>
<div>
<b>{{new Set(items.map(x=>x.department)).size}}</b>
<span>{{ t("部门 / 团队") }}</span>
</div>
</div>
<div class="toolbar module-toolbar">
<select :aria-label="t('全部项目角色')" v-model="role">
<option value="">{{ t("全部项目角色") }}</option>
<option v-for="x in roles" :value="x[0]">{{t(x[1])}}</option>
</select>
<select :aria-label="t('全部状态')" v-model="status">
<option value="">{{ t("全部状态") }}</option>
<option value="true">{{ t("已启用") }}</option>
<option value="false">{{ t("已停用") }}</option>
</select>
</div>
<div class="card table-card">
<table>
<thead>
<tr>
<th>{{ t("成员") }}</th>
<th>{{ t("部门 / 工号") }}</th>
<th>{{ t("企业角色") }}</th>
<th>{{ t("项目角色") }}</th>
<th>{{ t("状态") }}</th>
<th>{{ t("最近活动") }}</th>
<th>
</th>
</tr>
</thead>
<tbody>
<tr v-if="!loading && !filtered.length">
<td colspan="7" class="empty-mini">{{ t("暂无符合条件的记录") }}</td>
</tr>
<tr v-for="x in filtered" :key="x.id">
<td>
<div class="member-cell">
<span class="avatar small">{{x.name.slice(0,1)}}</span>
<div>
<b>{{x.name}} <small v-if="x.isCurrent">{{ t("当前") }}</small>
</b>
<span>{{x.email}}</span>
</div>
</div>
</td>
<td>{{x.department}}<small>{{x.employeeNo||t("未设置工号")}}</small>
</td>
<td>
<select :aria-label="t('企业角色')" :disabled="saving" :value="x.tenantRole" @change="patch(x,{tenantRole:($event.target as HTMLSelectElement).value})">
<option value="tenant_admin">{{ t("企业管理员") }}</option>
<option value="member">{{ t("普通成员") }}</option>
</select>
</td>
<td>
<select :aria-label="t('项目角色')" :disabled="saving" :value="x.projectRole" @change="patch(x,{projectRole:($event.target as HTMLSelectElement).value})">
<option v-for="r in roles" :value="r[0]">{{t(r[1])}}</option>
</select>
</td>
<td>
<span :class="['dot',x.active?'green':'gray']">
</span>{{x.active?t("已启用"):t("已停用")}}</td>
<td>{{formatDate(x.lastActive)}}</td>
<td>
<button class="link" :disabled="x.isCurrent || saving" @click="patch(x,{active:!x.active})">{{x.active?t("停用"):t("启用")}}</button>
<button v-if="context?.canImpersonate && !x.isCurrent" class="link member-login" :disabled="!canImpersonateMember(x) || saving" :title="t(x.mustChangePassword?'受限代看（待首次改密账号）：仅查看，完成首次改密后可常规代访问':'按成员实际权限代访问')" @click="prepareImpersonation(x)">{{t(x.mustChangePassword?'受限代看':'代访问账号')}}</button>
</td>
</tr>
</tbody>
</table>
</div>
<div v-if="show" class="modal-shade slide-panel-shade" @click.self="show=false">
<div class="modal">
<header>
<h2>{{ t("创建子账户") }}</h2>
<button @click="show=false" :aria-label="t('关闭')">×</button>
</header>
<div class="modal-body form-grid">
<label>{{ t("姓名 *") }}</label>
<input :aria-label="t('姓名 *')" v-model="form.name">
<label>{{ t("企业邮箱 *") }}</label>
<input :aria-label="t('企业邮箱 *')" v-model="form.email" type="email">
<label>{{ t("初始密码") }}</label>
<input :aria-label="t('初始密码')" v-model="form.initialPassword" type="password" minlength="6" maxlength="72" autocomplete="new-password">
<label>{{ t("工号") }}</label>
<input :aria-label="t('工号')" v-model="form.employeeNo">
<label>{{ t("部门 / 团队") }}</label>
<input :aria-label="t('部门 / 团队')" v-model="form.department">
<label>{{ t("企业角色") }}</label>
<select :aria-label="t('企业角色')" v-model="form.tenantRole">
<option value="member">{{ t("普通成员") }}</option>
<option value="tenant_admin">{{ t("企业管理员") }}</option>
</select>
<label>{{ t("项目角色") }}</label>
<select :aria-label="t('项目角色')" v-model="form.projectRole">
<option v-for="x in roles" :value="x[0]">{{t(x[1])}}</option>
</select>
<p class="hint">{{ t("初始密码须包含字母和数字；留空时成员仅进入组织目录，暂不能密码登录。") }}</p>
<p v-if="error" class="field-error">{{ t(error) }}</p>
</div>
<footer>
<button class="btn" @click="show=false">{{ t("取消") }}</button>
<button class="btn primary" :disabled="saving" @click="save">{{ t("创建子账户") }}</button>
</footer>
</div>
</div>
<div v-if="impersonating" class="modal-shade" @click.self="closeImpersonation">
 <section class="modal impersonation-modal" role="dialog" aria-modal="true" aria-labelledby="impersonation-title">
  <header><h2 id="impersonation-title">{{t(impersonating.mustChangePassword?'受限代看（待首次改密账号）':'代访问账号')}}</h2><button :disabled="saving" :aria-label="t('关闭')" @click="closeImpersonation">×</button></header>
  <div class="modal-body">
   <div class="impersonation-member"><span class="avatar">{{impersonating.name.slice(0,1)}}</span><div><b>{{impersonating.name}}</b><small>{{impersonating.email}}</small></div></div>
   <p>{{t('将按该成员的实际权限进入工作台，不需要成员密码。代访问最长 30 分钟，所有操作记录管理员与成员双重身份。')}}</p>
   <p v-if="impersonating.mustChangePassword" class="readonly-review">{{t('受限代看（待首次改密账号）：只能查看工作与通知，不能修改业务、通知已读状态、显示偏好、密码或权限。成员完成首次改密后才能使用常规代访问。')}}</p>
   <p v-else class="hint">{{t('不能修改密码、账号资料或成员权限；可随时返回管理员。其他已打开页面需刷新后继续操作。')}}</p>
   <label for="impersonation-reason">{{t('代访问理由')}}</label>
   <textarea id="impersonation-reason" v-model="reason" :disabled="saving" minlength="4" maxlength="500" :placeholder="t('例如：协助排查任务或核对通知，至少 4 个字')"></textarea>
   <p v-if="error" class="field-error" role="alert">{{t(error)}}</p>
  </div>
  <footer><button class="btn" :disabled="saving" @click="closeImpersonation">{{t('取消')}}</button><button class="btn primary" :disabled="saving||!validImpersonationReason(reason)" @click="startImpersonation">{{saving?t('正在进入…'):t(impersonating.mustChangePassword?'确认受限代看':'确认进入成员账号')}}</button></footer>
 </section>
</div>
</div>
</template>
<style scoped>
.member-login{margin-left:12px;white-space:nowrap}.impersonation-modal{max-width:520px}.impersonation-modal p{font-size:13px;line-height:1.8;color:#667085}.impersonation-modal .readonly-review{margin:14px 0;padding:10px 12px;border:1px solid #f2d28b;border-radius:8px;background:#fff8e8;color:#80580f}.impersonation-modal label{display:block;margin:20px 0 8px;font-size:13px;font-weight:600}.impersonation-modal textarea{width:100%;min-height:90px;border:1px solid #d0d5dd;border-radius:8px;padding:10px;font:inherit;font-size:13px;resize:vertical}.impersonation-member{display:flex;gap:12px;align-items:center}.impersonation-member b,.impersonation-member small{display:block}.impersonation-member small{margin-top:5px;color:#98a2b3}.impersonation-modal footer{gap:8px}
</style>
