<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover'
import { usePressRipple } from '../usePressRipple'

/**
 * 轻量的项目内下拉选择器。
 *
 * 原生 select 的展开列表由浏览器/系统绘制，无法跟随深浅色主题，也容易在窄布局
 * 中显得突兀。这里用 Reka Popover 统一承载选项列表；它只负责有限选项的单选，
 * 保留原有 v-model 数据流，不替代人员、多选等复杂选择器。
 */
type SelectValue = string | number
type SelectOption = { value: SelectValue; label: string; disabled?: boolean }

const props = withDefaults(defineProps<{
  modelValue: SelectValue
  options: SelectOption[]
  label: string
  disabled?: boolean
  placeholder?: string
  class?: string
}>(), { disabled: false, placeholder: '请选择' })
const emit = defineEmits<{ 'update:modelValue': [value: SelectValue] }>()

const open = ref(false)
const optionRefs = ref<HTMLButtonElement[]>([])
const pressRipple = usePressRipple()
const selected = computed(() => props.options.find(option => option.value === props.modelValue))
const selectedIndex = computed(() => Math.max(0, props.options.findIndex(option => option.value === props.modelValue)))

function enabledOptions() {
  return props.options.map((option, itemIndex) => ({ option, itemIndex })).filter(({ option }) => !option.disabled)
}

function focusOption(index: number) {
  const available = enabledOptions()
  if (!available.length) return
  const target = available.find(item => item.itemIndex === index) || available[0]
  void nextTick(() => optionRefs.value[target.itemIndex]?.focus())
}

function focusEdge(last: boolean) {
  const available = enabledOptions()
  if (!available.length) return
  const target = last ? available[available.length - 1] : available[0]
  void nextTick(() => optionRefs.value[target.itemIndex]?.focus())
}

function focusAdjacent(index: number, direction: 1 | -1) {
  const available = enabledOptions()
  if (!available.length) return
  const position = available.findIndex(item => item.itemIndex === index)
  const next = available[(position < 0 ? 0 : position + direction + available.length) % available.length]
  void nextTick(() => optionRefs.value[next.itemIndex]?.focus())
}

function openMenu() {
  if (props.disabled) return
  open.value = true
  void nextTick(() => focusOption(selectedIndex.value))
}

function changeOpen(next: boolean) {
  open.value = next
  if (next) void nextTick(() => focusOption(selectedIndex.value))
}

function select(option: SelectOption) {
  if (option.disabled) return
  emit('update:modelValue', option.value)
  open.value = false
}

function triggerKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp' || event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    openMenu()
  }
}

function optionKeydown(event: KeyboardEvent, index: number) {
  if (event.key === 'Escape') {
    event.preventDefault()
    open.value = false
    return
  }
  if (event.key === 'Home') {
    event.preventDefault()
    focusEdge(false)
    return
  }
  if (event.key === 'End') {
    event.preventDefault()
    // 最后一项可能被禁用；End 应落到最后一个可用选项，而不是回退到第一项。
    focusEdge(true)
    return
  }
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
  event.preventDefault()
  focusAdjacent(index, event.key === 'ArrowDown' ? 1 : -1)
}
</script>

<template>
  <Popover :open="open" @update:open="changeOpen">
    <PopoverTrigger as-child>
      <button
        type="button"
        :class="['app-select-trigger', 'df-motion-pressable', props.class]"
        :disabled="disabled"
        :aria-label="label"
        aria-haspopup="listbox"
        :aria-expanded="open"
        @pointerdown="pressRipple.pointerdown"
        @keydown="event=>{pressRipple.keydown(event);triggerKeydown(event)}"
      >
        <span class="app-select-value">{{ selected?.label || placeholder }}</span>
        <svg aria-hidden="true" viewBox="0 0 16 16" fill="none"><path d="m4 6 4 4 4-4" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </button>
    </PopoverTrigger>
    <PopoverContent v-if="open" class="app-select-menu w-[max(172px,var(--reka-popover-trigger-width))] p-1" align="start" :side-offset="6" :collision-padding="12" @escape-key-down="open=false">
      <div role="listbox" :aria-label="label">
        <button
          v-for="(option, index) in options"
          :key="String(option.value)"
          ref="optionRefs"
          type="button"
          role="option"
          class="app-select-option df-motion-pressable df-motion-list-row"
          :class="{ selected: option.value === modelValue }"
          :disabled="option.disabled"
          :aria-selected="option.value === modelValue"
          @pointerdown="pressRipple.pointerdown"
          @click="select(option)"
          @keydown="event=>{pressRipple.keydown(event);optionKeydown(event,index)}"
        >
          <span>{{ option.label }}</span>
          <svg v-if="option.value === modelValue" aria-hidden="true" viewBox="0 0 16 16" fill="none"><path d="m3.5 8.25 2.6 2.5 6.4-6" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </button>
      </div>
    </PopoverContent>
  </Popover>
</template>

<style scoped>
/* 内容经 PopoverContent 多层组件渲染，不携带此处 scoped 标记；明确限定全局
   的菜单类与 data-slot，保证长选项列表真正内滚且不影响其他弹层。 */
.app-select-trigger{display:inline-flex;align-items:center;justify-content:space-between;gap:10px;min-width:132px;min-height:34px;padding:6px 10px;border:1px solid var(--input,var(--line));border-radius:8px;background:var(--card,var(--surface));color:var(--foreground,var(--ink));font:inherit;font-size:12px;line-height:1.35;text-align:left;box-shadow:none;transition:border-color .15s ease,background .15s ease}.app-select-trigger:hover:not(:disabled){border-color:color-mix(in srgb,var(--primary) 52%,var(--input));background:color-mix(in srgb,var(--primary) 3%,var(--card))}.app-select-trigger:focus-visible{outline:2px solid color-mix(in srgb,var(--ring,var(--primary)) 38%,transparent);outline-offset:1px;border-color:var(--ring,var(--primary));box-shadow:none}.app-select-trigger:disabled{cursor:not-allowed;opacity:.58}.app-select-value{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.app-select-trigger svg{width:15px;height:15px;flex:none;color:var(--muted-foreground,var(--muted));transition:transform .15s ease}.app-select-trigger[aria-expanded="true"] svg{transform:rotate(180deg);color:var(--primary)}:global(.app-select-menu[data-slot="popover-content"]){width:var(--reka-popover-trigger-width);min-width:172px;max-height:min(280px,var(--reka-popover-content-available-height,calc(100dvh - 24px)));padding:4px!important;overflow-y:auto;overflow-x:hidden;overscroll-behavior:contain;scroll-padding:4px;border:1px solid var(--border)!important;border-radius:8px!important;background:var(--popover,var(--card))!important;box-shadow:none!important}.app-select-option{display:flex;align-items:center;justify-content:space-between;gap:12px;width:100%;min-width:0;min-height:34px;padding:7px 8px;border:0;border-radius:6px;background:transparent;color:var(--popover-foreground,var(--foreground));font:inherit;font-size:12px;text-align:left;overflow-wrap:anywhere}.app-select-option>span{min-width:0;overflow-wrap:anywhere}.app-select-option:hover:not(:disabled),.app-select-option:focus-visible{outline:0;background:var(--accent);color:var(--accent-foreground)}.app-select-option.selected{font-weight:650;color:var(--primary)}.app-select-option:disabled{cursor:not-allowed;opacity:.5}.app-select-option svg{width:15px;height:15px;flex:none}
</style>
