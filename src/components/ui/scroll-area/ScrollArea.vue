<script setup lang="ts">
import { computed, type HTMLAttributes } from 'vue'
import { ScrollAreaCorner, ScrollAreaRoot, ScrollAreaViewport, useForwardExpose, useForwardProps, type ScrollAreaRootProps } from 'reka-ui'
import { cn } from '@/lib/utils'
import ScrollBar from './ScrollBar.vue'

const props = defineProps<ScrollAreaRootProps & { class?: HTMLAttributes['class']; viewportClass?: HTMLAttributes['class']; horizontal?: boolean }>()
const delegated = computed(() => { const { class: _class, viewportClass: _viewportClass, horizontal: _horizontal, ...rest } = props; return rest })
const forwarded = useForwardProps(delegated)
const { forwardRef } = useForwardExpose()
</script>
<template>
  <ScrollAreaRoot :ref="forwardRef" v-bind="forwarded" data-slot="scroll-area" :class="cn('relative min-h-0 overflow-hidden', props.class)"><ScrollAreaViewport data-slot="scroll-area-viewport" :class="cn('size-full rounded-[inherit] outline-none focus-visible:ring-2 focus-visible:ring-ring/40', props.viewportClass)" tabindex="0"><slot /></ScrollAreaViewport><ScrollBar /><ScrollBar v-if="horizontal" orientation="horizontal" /><ScrollAreaCorner /></ScrollAreaRoot>
</template>
