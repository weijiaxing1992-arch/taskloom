<script setup lang="ts">
import { t } from '../i18n'
import Icon from './Icon.vue'
defineProps<{ unread: number }>()
const links = [
  { to: '/my-work', label: '我的', ariaLabel: '我的工作', icon: 'work' },
  { to: '/requirements', label: '需求', ariaLabel: '需求列表', icon: 'list' },
  { to: '/iterations', label: '迭代', ariaLabel: '迭代列表', icon: 'review' },
  { to: '/notifications', label: '通知', ariaLabel: '通知中心', icon: 'bell' },
]
</script>
<template>
  <nav class="mobile-work-navigation" :aria-label="t('常用工作入口')">
    <RouterLink v-for="link in links" :key="link.to" :to="link.to" :aria-label="t(link.ariaLabel)+(link.to==='/notifications'&&unread?' · '+unread:'')">
      <span class="mobile-work-icon"><Icon :name="link.icon" :size="20"/><small v-if="link.to==='/notifications'&&unread">{{unread>99?'99+':unread}}</small></span>
      <span>{{t(link.label)}}</span>
    </RouterLink>
  </nav>
</template>
<style scoped>
.mobile-work-navigation{display:none}
/* 位于工作区的正常文档流中，不盖住列表末尾、弹层或软键盘。 */
@media(max-width:760px){
 .mobile-work-navigation{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));flex:none;background:var(--surface);border-top:1px solid var(--line);padding:4px max(4px,env(safe-area-inset-right)) max(4px,env(safe-area-inset-bottom)) max(4px,env(safe-area-inset-left))}
 .mobile-work-navigation a{display:flex;flex-direction:column;align-items:center;justify-content:center;gap:3px;min-height:52px;min-width:0;font-size:11px;color:var(--muted);text-decoration:none;border-radius:6px}
 .mobile-work-navigation a.router-link-active{color:var(--primary);background:var(--primary-soft);font-weight:700}
 .mobile-work-navigation a:focus-visible{outline:2px solid var(--primary);outline-offset:-2px}
 .mobile-work-icon{position:relative;display:flex}.mobile-work-icon small{position:absolute;left:15px;top:-5px;min-width:16px;padding:1px 4px;border-radius:10px;background:var(--danger,#d92d20);color:white;font-size:10px;line-height:14px;text-align:center}
}
</style>
