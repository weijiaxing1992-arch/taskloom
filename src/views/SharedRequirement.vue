<script setup lang="ts">
import {ref,watch} from 'vue'
import {useRoute} from 'vue-router'
import {t,formatDate} from '../i18n'
const route=useRoute(),data=ref<Record<string,string>|null>(null),error=ref(''),loading=ref(true)
let sequence=0
watch(()=>route.params.token,async token=>{const version=++sequence;loading.value=true;data.value=null;error.value='';try{if(typeof token!=='string'||!/^[a-f0-9]{64}$/.test(token))throw Error('分享链接无效');const response=await fetch('/api/public/requirement-shares/'+token,{credentials:'omit',cache:'no-store',referrerPolicy:'no-referrer'});if(!response.ok)throw Error(response.status===404?'分享不存在、已撤销或已过期':'分享暂时无法读取，请稍后重试');const result=await response.json();if(version===sequence)data.value=result}catch(e){if(version===sequence)error.value=e instanceof Error?e.message:'读取失败'}finally{if(version===sequence)loading.value=false}},{immediate:true})
</script>
<template><main class="public-requirement"><header>{{t('TaskLoom · 需求分享')}}</header><p v-if="loading" role="status">{{t('正在读取分享…')}}</p><p v-else-if="error" role="alert">{{t(error)}}</p><article v-else-if="data"><p class="share-note">{{t('公开只读快照')}} · {{formatDate(data.createdAt!)}}</p><h1>{{data.title}}</h1><section v-if="data.description"><h2>{{t('需求描述')}}</h2><pre>{{data.description}}</pre></section><section v-if="data.acceptance"><h2>{{t('验收标准')}}</h2><pre>{{data.acceptance}}</pre></section><footer>{{t('有效至')}} {{formatDate(data.expiresAt!)}} · {{t('不含内部评论与附件文件')}}</footer></article></main></template>
<style scoped>
.public-requirement{min-height:100dvh;background:#f5f7fb;padding:24px;color:#172033}.public-requirement>header{max-width:900px;margin:0 auto 24px;font-weight:700;color:#326aed}article{max-width:900px;margin:auto;background:white;border:1px solid #e6eaf2;border-radius:16px;padding:clamp(20px,5vw,48px)}h1{font-size:clamp(24px,4vw,34px);overflow-wrap:anywhere;line-height:1.4}h2{font-size:18px;margin-top:32px}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;line-height:1.9}.share-note,footer{font-size:13px;color:#728097}footer{margin-top:40px;border-top:1px solid #e6eaf2;padding-top:20px}
</style>
