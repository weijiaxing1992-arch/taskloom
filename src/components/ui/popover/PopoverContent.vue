<script setup lang="ts">
import { computed, type HTMLAttributes } from 'vue'
import { PopoverContent, useForwardPropsEmits, type PopoverContentEmits, type PopoverContentProps } from 'reka-ui'
import { cn } from '@/lib/utils'

const props = withDefaults(defineProps<PopoverContentProps & { class?: HTMLAttributes['class'] }>(), { align: 'center', sideOffset: 6, collisionPadding: 12 })
const emits = defineEmits<PopoverContentEmits>()
const delegated = computed(() => { const { class: _class, ...rest } = props; return rest })
const forwarded = useForwardPropsEmits(delegated, emits)
function escape(event: KeyboardEvent) { event.stopImmediatePropagation() }
</script>
<template>
  <!-- Deliberately local: inheriting App's inert identity guard is mandatory. -->
  <PopoverContent v-bind="forwarded" data-slot="popover-content" :class="cn('df-motion-overlay z-[300] w-72 rounded-md border border-border bg-popover p-4 text-popover-foreground shadow-none origin-[var(--reka-popover-content-transform-origin)] motion-safe:data-[state=open]:animate-in motion-safe:data-[state=closed]:animate-out motion-safe:data-[state=open]:fade-in-0 motion-safe:data-[state=closed]:fade-out-0', props.class)" @escape-key-down="escape"><slot /></PopoverContent>
</template>
