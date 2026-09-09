<script setup lang="ts">
import {Button} from './ui/button'
// 只统一呈现，不接管权限、确认或请求；业务入口继续负责这些安全边界。
withDefaults(defineProps<{busy?:boolean;disabled?:boolean}>(),{busy:false,disabled:false})
</script>
<template>
 <Button class="ai-action" size="toolbar" :aria-busy="busy" :disabled="busy||disabled">
  <svg class="ai-wand" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m4 17 12-12 4 4L8 21H4v-4Z"/><path d="m12 9 4 4M5 2v4M3 4h4M19 1v4M17 3h4M20 17v4M18 19h4"/></svg>
  <slot />
 </Button>
</template>
