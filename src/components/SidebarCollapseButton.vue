<script setup lang="ts">
const props = withDefaults(defineProps<{ expanded:boolean; label?:string }>(), { label:'侧边栏' })
const emit = defineEmits<{toggle:[]}>()
</script>

<template>
  <button
    type="button"
    class="sidebar-collapse-toggle"
    :aria-label="expanded ? `收起${label}` : `展开${label}`"
    :title="expanded ? `收起${label}` : `展开${label}`"
    :aria-expanded="expanded"
    @click="emit('toggle')"
  ><span aria-hidden="true">{{ expanded ? '‹' : '›' }}</span></button>
</template>

<style scoped>
.sidebar-collapse-toggle{display:grid;place-items:center;flex:none;width:26px;height:26px;padding:0;border:1px solid var(--line,#e4e7ec);border-radius:7px;background:var(--surface,#fff);color:var(--muted,#667085);font-size:21px;line-height:1;cursor:pointer;box-shadow:none;transition:color .16s ease,border-color .16s ease,background .16s ease}
.sidebar-collapse-toggle:hover{border-color:var(--primary,#3370ff);color:var(--primary,#3370ff);background:var(--primary-soft,#eff4ff)}
.sidebar-collapse-toggle:focus-visible{outline:2px solid var(--primary,#3370ff);outline-offset:2px}
</style>
