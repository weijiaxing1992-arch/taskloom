<script setup lang="ts">
import { computed } from 'vue'
import { t } from '../i18n'
import { commentExcerpt, validCommentId, type CommentReplyTarget } from '../commentReplies'
const props = defineProps<{ target: CommentReplyTarget; composing?: boolean; disabled?: boolean }>()
const emit = defineEmits<{ (event:'cancel'):void }>()
const author = computed(() => props.target.author || t('未知用户'))
const excerpt = computed(() => props.target.unavailable ? t('原评论暂不可用') : commentExcerpt(props.target.body) || t('无文字内容（可能包含图片或附件）'))
function cancel(){if(!props.disabled)emit('cancel')}
</script>
<template>
 <aside v-if="validCommentId(target.id)" class="comment-reply-context" :class="{'is-composing':composing}" :role="composing?'status':undefined" :aria-live="composing?'polite':undefined" :aria-label="t(composing?'当前回复目标':'回复引用')">
  <div><span>{{t(composing?'正在回复':'回复了')}} <b>{{author}}</b> <small>#{{target.id}}</small></span><p>{{excerpt}}</p></div>
  <button v-if="composing" type="button" :disabled="disabled" :aria-label="t('取消回复，保留评论草稿')" @click="cancel">{{t('取消回复')}}</button>
 </aside>
</template>
<style scoped>
.comment-reply-context{display:flex;align-items:flex-start;gap:12px;border-left:3px solid var(--primary,#665fe8);border-radius:0 7px 7px 0;background:var(--surface-soft,#f7f8fc);padding:10px 12px;margin:10px 0;color:var(--muted,#667085);min-width:0;font-size:11px;line-height:1.7}.comment-reply-context>div{flex:1;min-width:0}.comment-reply-context b{color:var(--ink,#344054);font-weight:600;overflow-wrap:anywhere}.comment-reply-context small{font-size:10px;margin-left:5px}.comment-reply-context p{margin:3px 0 0;overflow-wrap:anywhere;white-space:pre-wrap;font-size:12px;line-height:1.7;color:var(--muted,#667085)}.comment-reply-context button{flex:none;border:1px solid var(--line,#e4e7ec);border-radius:6px;background:var(--surface,#fff);color:var(--primary,#665fe8);padding:5px 8px;min-height:30px;font:inherit;cursor:pointer}.comment-reply-context button:disabled{opacity:.5;cursor:not-allowed}.comment-reply-context button:focus-visible{outline:2px solid var(--primary,#665fe8);outline-offset:2px}.is-composing{border:1px solid var(--line,#e4e7ec);border-left:3px solid var(--primary,#665fe8)}@media(max-width:640px){.comment-reply-context{padding:9px 10px;gap:8px;flex-wrap:wrap}.comment-reply-context button{min-height:38px;margin-left:auto}.comment-reply-context>div{flex-basis:180px}}
</style>
