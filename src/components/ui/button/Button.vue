<script setup lang="ts">
import type { ButtonHTMLAttributes, HTMLAttributes } from 'vue'
import { Primitive, type PrimitiveProps } from 'reka-ui'
import { cn } from '@/lib/utils'
import { buttonVariants, type ButtonVariants } from '.'

interface Props extends PrimitiveProps {
  variant?: ButtonVariants['variant']
  size?: ButtonVariants['size']
  class?: HTMLAttributes['class']
  type?: ButtonHTMLAttributes['type']
  disabled?: boolean
}
withDefaults(defineProps<Props>(), { as: 'button', variant: 'default', size: 'default' })
</script>
<template>
  <Primitive :as="as" :as-child="asChild" :type="!asChild && as === 'button' ? (type || 'button') : undefined" :disabled="disabled" data-slot="button" :data-variant="variant" :data-size="size" :class="cn(buttonVariants({ variant, size }), $props.class)"><slot /></Primitive>
</template>
