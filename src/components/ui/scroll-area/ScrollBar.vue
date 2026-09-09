<script setup lang="ts">
import { computed, type HTMLAttributes } from 'vue'
import { ScrollAreaScrollbar, ScrollAreaThumb, useForwardProps, type ScrollAreaScrollbarProps } from 'reka-ui'
import { cn } from '@/lib/utils'

const props = withDefaults(defineProps<ScrollAreaScrollbarProps & { class?: HTMLAttributes['class'] }>(), { orientation: 'vertical' })
const delegated = computed(() => { const { class: _class, ...rest } = props; return rest })
const forwarded = useForwardProps(delegated)
</script>
<template>
  <ScrollAreaScrollbar v-bind="forwarded" data-slot="scroll-area-scrollbar" :class="cn('flex touch-none select-none p-px transition-colors', orientation === 'vertical' ? 'h-full w-2.5 border-l border-l-transparent' : 'h-2.5 flex-col border-t border-t-transparent', props.class)"><ScrollAreaThumb data-slot="scroll-area-thumb" class="relative flex-1 rounded-full bg-border" /></ScrollAreaScrollbar>
</template>
