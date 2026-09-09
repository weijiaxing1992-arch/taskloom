import { ref } from 'vue'
import './display.css'
export const fontSizes = { small: 0.92, standard: 1, large: 1.12, extraLarge: 1.25 } as const
export type FontSize = keyof typeof fontSizes
export const fontSize = ref<FontSize>('standard')
export const displayRevision = ref(0)
// 只接受预设字号，统一写入根变量；版本号供浮层重新测量，避免放大字号后仍使用旧位置。
export function applyDisplayPreferences(value: { fontSize?: string }) {
  fontSize.value = value.fontSize && Object.hasOwn(fontSizes, value.fontSize) ? value.fontSize as FontSize : 'standard'
  document.documentElement.style.setProperty('--devflow-font-scale', String(fontSizes[fontSize.value]))
  return ++displayRevision.value
}
