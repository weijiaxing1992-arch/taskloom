<script setup lang="ts">
import CodeTextView from './CodeTextView.vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { t, formatDate } from '../i18n'
import type { MentionMember } from '../mentions'
import MentionComment from './MentionComment.vue'
import CommentReplyContext from './CommentReplyContext.vue'
import type { CommentReplyTarget } from '../commentReplies'
import { useSettingsScope } from './settingsScope'

type Comment = { id:number;author:string;authorUserId:string;body:string;mentionUserIds:string[];createdAt:string;replyToId?:number|null;replyToAuthor?:string;replyToAuthorUserId?:string }
const props=defineProps<{ resource:'test-cases'|'test-plans'|'test-executions';objectId:number;members:MentionMember[];disabled?:boolean }>()
const scope=useSettingsScope(),comments=ref<Comment[]>([]),body=ref(''),mentionUserIds=ref<string[]>([]),loading=ref(false),saving=ref(false),error=ref(''),role=ref('')
const replyTarget=ref<CommentReplyTarget|null>(null),composer=ref<InstanceType<typeof MentionComment>|null>(null)
const commentMap=computed(()=>new Map(comments.value.map(comment=>[comment.id,comment])))
let sequence=0,readVersion=0,disposed=false,projectLeaveApproved=false
const endpoint=computed(()=>`/${props.resource}/${props.objectId}/comments`)
const mutable=computed(()=>!!role.value&&role.value!=='viewer'&&!scope.locked.value)
const blocked=computed(()=>saving.value||!!props.disabled||scope.locked.value)
const dirty=computed(()=>!!body.value.trim()||!!replyTarget.value)
const message=(cause:unknown)=>cause instanceof Error?cause.message:'操作失败，请稍后重试'
const current=(version:number,path:string)=>!disposed&&version===sequence&&path===endpoint.value&&scope.current()
async function load(){if(saving.value||!scope.current()||disposed)return;const version=sequence,read=++readVersion,path=endpoint.value;loading.value=true;error.value='';try{const [data,session]=await Promise.all([scope.request<{items:Comment[]}>(path),scope.request<{user:{role:string}}>('/session')]);if(!current(version,path)||read!==readVersion)return;if(!Array.isArray(data.items))throw Error('评论数据格式不正确，请重试');comments.value=data.items;role.value=session.user.role}catch(cause){if(current(version,path)&&read===readVersion)error.value=message(cause)}finally{if(current(version,path)&&read===readVersion)loading.value=false}}
async function reply(id:number){if(blocked.value||loading.value||!mutable.value||!scope.current())return;const item=commentMap.value.get(id);if(!item||!Number.isSafeInteger(id)||id<1)return;replyTarget.value={id,author:item.author,body:item.body};const path=endpoint.value;await nextTick();if(!disposed&&!blocked.value&&path===endpoint.value&&replyTarget.value?.id===id)composer.value?.focus()}
function cancelReply(){if(!blocked.value)replyTarget.value=null}
async function submit(value:{body:string;mentionUserIds:string[]}){
 if(blocked.value||loading.value||!value.body.trim()||!mutable.value||!scope.current()||disposed)return
 const version=sequence,path=endpoint.value,draft=JSON.stringify({body:body.value,ids:mentionUserIds.value,reply:replyTarget.value}),payload={...value,...(replyTarget.value?{replyToId:replyTarget.value.id}:{})}
 saving.value=true;error.value='';++readVersion
 try{const saved=await scope.request<Comment>(path,{method:'POST',body:JSON.stringify(payload)});if(!current(version,path))return;if(!saved||!Number.isSafeInteger(saved.id)||saved.id<1||('replyToId' in payload&&saved.replyToId!==payload.replyToId))throw Error('评论返回数据不完整，请刷新讨论后确认，勿重复提交。');comments.value=[saved,...comments.value.filter(comment=>comment.id!==saved.id)];if(draft===JSON.stringify({body:body.value,ids:mentionUserIds.value,reply:replyTarget.value})){body.value='';mentionUserIds.value=[];replyTarget.value=null};window.dispatchEvent(new Event('devflow-notifications-changed'))}
 catch(cause){if(current(version,path))error.value=message(cause)}finally{if(current(version,path))saving.value=false}
}
function canLeave(){if(saving.value||props.disabled)return false;return projectLeaveApproved||!dirty.value||window.confirm(t('评论尚未发布，确定离开并放弃内容？'))}
function beforeProjectChange(event:Event){projectLeaveApproved=false;if(!canLeave())event.preventDefault();else projectLeaveApproved=true}
function cancelProjectLeave(){projectLeaveApproved=false}
function beforeUnload(event:BeforeUnloadEvent){const switching=projectLeaveApproved&&(localStorage.getItem('devflow-project')||'prj_orbit')!==scope.project;if(!switching&&(dirty.value||saving.value||props.disabled)){event.preventDefault();event.returnValue=''}}
function reset(){sequence++;readVersion++;body.value='';mentionUserIds.value=[];replyTarget.value=null;comments.value=[];role.value='';loading.value=false;saving.value=false;error.value='';projectLeaveApproved=false}
watch(()=>[props.resource,props.objectId],()=>{reset();void load()},{flush:'sync'})
watch(scope.locked,value=>{if(value)reset()},{flush:'sync'})
onBeforeRouteLeave(canLeave);onBeforeRouteUpdate(canLeave)
onMounted(()=>{void load();window.addEventListener('devflow-before-project-change',beforeProjectChange);window.addEventListener('devflow-project-change-cancelled',cancelProjectLeave);window.addEventListener('beforeunload',beforeUnload)})
onBeforeUnmount(()=>{disposed=true;sequence++;readVersion++;window.removeEventListener('devflow-before-project-change',beforeProjectChange);window.removeEventListener('devflow-project-change-cancelled',cancelProjectLeave);window.removeEventListener('beforeunload',beforeUnload)})
defineExpose({canLeave})
</script>
<template>
  <section class="quality-comments" :aria-label="t('测试讨论')" :aria-busy="loading||saving||props.disabled">
    <header><h3>{{t('测试讨论')}}</h3><span>{{comments.length}}</span><button type="button" class="link comment-reply-button" :disabled="blocked||loading" @click="load">{{t('刷新评论')}}</button></header>
    <p v-if="error" class="field-error" role="alert">{{t(error)}} <button type="button" class="link" :disabled="blocked||loading" @click="load">{{t('重试')}}</button></p>
    <p v-if="scope.locked.value" class="field-error" role="alert">{{t('项目或账号已变化，请刷新页面后继续')}}</p>
    <p v-if="loading" role="status">{{t('正在加载…')}}</p>
    <CommentReplyContext v-if="mutable&&replyTarget" :target="replyTarget" composing :disabled="blocked" @cancel="cancelReply"/>
    <MentionComment v-if="mutable" ref="composer" v-model="body" v-model:mention-user-ids="mentionUserIds" :members="members" :disabled="blocked||loading" :label="t('测试评论')" :maxlength="20000" @submit="submit"/>
    <p v-else-if="!loading&&!error" class="quality-comments-note">{{t('当前角色仅可查看')}}</p>
    <div class="quality-comment-list"><article v-for="comment in comments" :key="comment.id"><header><b>{{comment.author}}</b><time>{{formatDate(comment.createdAt)}}</time></header><CommentReplyContext v-if="comment.replyToId" :target="commentMap.get(comment.replyToId)||{id:comment.replyToId,author:comment.replyToAuthor,unavailable:true}"/><CodeTextView :text="comment.body"/><button v-if="mutable" type="button" class="link comment-reply-button" :disabled="blocked||loading" :aria-label="t('回复 {name} 的评论',{name:comment.author||t('未知用户')})" @click="reply(comment.id)">{{t('回复')}}</button></article><p v-if="!loading&&!comments.length" class="quality-comments-note">{{t('暂无讨论，发布评论开始协作。')}}</p></div>
  </section>
</template>
<style scoped>
.quality-comments{margin-top:26px;border-top:1px solid var(--line,#e4e7ec);padding-top:20px;min-width:0}.quality-comments>header{display:flex;gap:8px;align-items:center;margin-bottom:14px}.quality-comments h3{margin:0;font-size:14px}.quality-comments>header>span{color:var(--muted,#98a2b3);font-size:12px}.quality-comments-note{font-size:12px;color:var(--muted,#98a2b3);padding:12px 0}.quality-comment-list article{padding:16px 0;border-bottom:1px solid var(--line,#eef0f4)}.quality-comment-list header{display:flex;justify-content:space-between;gap:12px;font-size:12px}.quality-comment-list time{font-size:11px;color:var(--muted,#98a2b3)}.quality-comment-list p{white-space:pre-wrap;overflow-wrap:anywhere;font-size:13px;line-height:1.8;margin:10px 0 0}
</style>
<style scoped>.quality-comments>header>.comment-reply-button{margin-left:auto}.comment-reply-button{font:inherit;font-size:11px;min-height:30px;border:0;background:transparent;color:var(--primary,#665fe8);padding:5px 0;cursor:pointer}.comment-reply-button:disabled{opacity:.5;cursor:not-allowed}.comment-reply-button:focus-visible{outline:2px solid var(--primary,#665fe8);outline-offset:2px}.quality-comment-list :deep(.comment-reply-context p){font-size:12px;margin:3px 0 0}@media(max-width:640px){.quality-comments>header{flex-wrap:wrap}.comment-reply-button{min-height:40px}.quality-comment-list header{flex-wrap:wrap}}</style>
