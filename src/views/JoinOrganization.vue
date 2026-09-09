<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { t, locale } from '../i18n'
import { Button } from '../components/ui/button'
import LocaleSwitcher from '../components/LocaleSwitcher.vue'
const route=useRoute(),loading=ref(true),saving=ref(false),error=ref(''),submitted=ref(false)
const data=ref<{organizationName:string;departments:{id:string;name:string;parentId?:string}[];roles:{key:string;name:string}[]}|null>(null)
const form=reactive({name:'',departmentId:'',requestedRole:''});let version=0,controller:AbortController|undefined
async function request(method='GET'){
  const token=typeof route.params.token==='string'?route.params.token:''
  if(!/^[A-Za-z0-9_-]{20,200}$/.test(token))throw Error('邀请链接无效或已失效')
  const res=await fetch('/api/public/organization-invitations/'+encodeURIComponent(token),{method,credentials:'omit',referrerPolicy:'no-referrer',signal:controller?.signal,headers:{'Content-Type':'application/json','Accept-Language':locale.value},...(method==='POST'?{body:JSON.stringify({name:form.name.trim(),departmentId:form.departmentId,requestedRole:form.requestedRole})}:{})})
  const result=await res.json();if(!res.ok)throw Error(result.error?.message||'邀请链接无效或已失效');return result
}
async function load(){const current=++version;controller?.abort();controller=new AbortController();loading.value=true;error.value='';data.value=null;submitted.value=false;Object.assign(form,{name:'',departmentId:'',requestedRole:''});try{const result=await request();if(current===version)data.value=result}catch(cause){if(current===version)error.value=cause instanceof Error?cause.message:'无法加载邀请'}finally{if(current===version)loading.value=false}}
async function submit(){if(saving.value||!data.value)return;const current=version;saving.value=true;error.value='';try{await request('POST');if(current===version){submitted.value=true;form.name=''}}catch(cause){if(current===version)error.value=cause instanceof Error?cause.message:'申请提交失败，请稍后重试'}finally{if(current===version)saving.value=false}}
watch(()=>route.params.token,load)
onMounted(load);onBeforeUnmount(()=>{version++;controller?.abort()})
</script>
<template>
  <div class="join-page"><header><div class="join-brand"><span>D</span><b>TaskLoom</b></div><LocaleSwitcher /></header><main><section class="join-card"><div class="join-symbol" aria-hidden="true">{{submitted?'✓':'↗'}}</div><template v-if="submitted"><h1>{{t('申请已提交')}}</h1><p>{{t('管理员审核通过并激活后，才会为你开放组织与项目访问。')}}</p><div class="join-info">{{t('请等待管理员联系并提供登录信息，无需重复提交。')}}</div><RouterLink to="/requirements">{{t('返回登录')}}</RouterLink></template><template v-else><span class="join-kicker">{{t('加入团队')}}</span><h1>{{data?.organizationName||t('组织邀请')}}</h1><p>{{t('填写基本信息，向管理员申请加入组织。')}}</p><p v-if="loading" role="status">{{t('正在加载…')}}</p><p v-if="error" role="alert" class="join-error">{{t(error)}}</p><form v-if="data" @submit.prevent="submit"><fieldset :disabled="saving"><label>{{t('姓名')}} *<input v-model="form.name" autocomplete="name" required maxlength="80" :placeholder="t('填写真实姓名，方便管理员核验')"></label><label>{{t('部门')}} *<select v-model="form.departmentId" required><option value="">{{t('选择部门')}}</option><option v-for="item in data.departments" :key="item.id" :value="item.id">{{item.name}}</option></select></label><label>{{t('申请角色')}} *<select v-model="form.requestedRole" required><option value="">{{t('选择角色')}}</option><option v-for="role in data.roles" :key="role.key" :value="role.key">{{t(role.name)}}</option></select></label><div class="join-info">{{t('此表单不创建可登录账号，也不会直接授予申请角色。实际部门与权限由管理员核定。')}}</div><Button type="submit" :disabled="saving||!form.name.trim()||!form.departmentId||!form.requestedRole">{{t(saving?'正在提交…':'提交加入申请')}}</Button></fieldset></form><Button v-else-if="!loading" variant="outline" @click="load">{{t('重新加载')}}</Button></template></section><small class="join-footer">{{t('申请信息仅用于当前组织的成员审核。')}}</small></main></div>
</template>
<style scoped>
.join-page{min-height:100dvh;background:var(--surface-soft,#f5f7fc);color:var(--ink);font-family:inherit}.join-page>header{padding:25px 38px;display:flex;align-items:center;justify-content:space-between}.join-brand{display:flex;gap:10px;align-items:center;font-size:19px}.join-brand>span{display:grid;place-items:center;width:34px;height:34px;border-radius:10px;color:#fff;background:#6366f1}.join-page>main{max-width:520px;padding:24px;margin:20px auto}.join-card{padding:36px;border:1px solid var(--line);border-radius:18px;box-shadow:0 15px 55px #3c42630a;background:var(--surface,#fff)}.join-symbol{width:48px;height:48px;display:grid;place-items:center;background:var(--primary-soft);color:var(--primary);border-radius:14px;font-size:26px;margin-bottom:25px}.join-kicker{font-size:12px;color:var(--primary)}.join-card h1{font-size:25px;letter-spacing:-.6px;margin:8px 0}.join-card p{font-size:13px;color:var(--muted);line-height:1.9}.join-card fieldset{border:0;padding:0;display:grid;gap:20px;margin-top:25px}.join-card label{display:grid;gap:8px;font-size:12px}.join-card input,.join-card select{width:100%;border:1px solid var(--line);padding:11px 12px;border-radius:7px;background:var(--surface,#fff);color:var(--ink);font:inherit;font-size:13px}.join-card input:focus,.join-card select:focus{outline:2px solid var(--primary);outline-offset:1px}.join-info{padding:14px;border-radius:8px;background:var(--surface-soft,#f5f7fb);color:var(--muted);font-size:11px;line-height:1.9}.join-error{color:#dc5353!important}.join-footer{display:block;text-align:center;margin:24px auto;font-size:11px;color:var(--muted)}.join-card>a{display:inline-block;margin-top:22px;color:var(--primary);font-size:13px}
</style>

<style scoped>
@media(max-width:820px){
 .join-page>header{padding:20px 16px;gap:12px;flex-wrap:wrap}.join-page>main{padding:12px;margin:10px auto;min-width:0;width:100%}.join-card{padding:24px 18px;min-width:0}.join-card h1,.join-card p{overflow-wrap:anywhere}.join-card fieldset{min-width:0}.join-card input,.join-card select{min-width:0;min-height:42px}.join-brand{font-size:17px;min-width:0}.join-footer{margin:18px auto;line-height:1.7;padding:0 14px}
}
</style>
