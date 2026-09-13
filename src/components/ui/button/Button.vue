<script setup lang="ts">
import type { ButtonHTMLAttributes, HTMLAttributes } from 'vue'
import { Primitive, type PrimitiveProps } from 'reka-ui'
import { cn } from '@/lib/utils'
import { usePressRipple } from '@/usePressRipple'
import { buttonVariants, type ButtonVariants } from '.'

interface Props extends PrimitiveProps {
  variant?: ButtonVariants['variant']
  size?: ButtonVariants['size']
  class?: HTMLAttributes['class']
  type?: ButtonHTMLAttributes['type']
  disabled?: boolean
}
withDefaults(defineProps<Props>(), { as: 'button', variant: 'default', size: 'default' })
const pressRipple = usePressRipple()
</script>
<template>
  <Primitive :as="as" :as-child="asChild" :type="!asChild && as === 'button' ? (type || 'button') : undefined" :disabled="disabled" data-slot="button" :data-variant="variant" :data-size="size" :class="cn(buttonVariants({ variant, size }), variant === 'link' ? undefined : 'df-motion-pressable', $props.class)" @pointerdown="pressRipple.pointerdown" @keydown="pressRipple.keydown"><slot /></Primitive>
</template>
