// 历史页面的像素字号在构建时归入同一字阶；新组件直接引用同名令牌。
// 根字号保持 16px，账号缩放只在文字令牌上应用一次，避免 rem 再乘一次。
export function typographyValue(value, selector = '') {
  if (!/^\d+(?:\.\d+)?px$/.test(value)) return value
  if (selector.split(',').every(part => /^(?:html|:root)$/.test(part.trim()))) return value
  const size = Number.parseFloat(value)
  // 用户编写的富文本、品牌和图形符号不是界面字阶，不改变其相对层级。
  if (/rich-content|rich-document|ProseMirror|tiptap|rich-emoji|brand|avatar|symbol|art\b/.test(selector) || size > 28 || size < 9) {
    return `calc(${value} * var(--devflow-font-scale, 1))`
  }
  const token = size <= 12 ? 'caption' : size <= 14 ? 'body' : size <= 17 ? 'section' : size <= 19 ? 'subtitle' : size <= 22 ? 'title' : 'display'
  return `var(--ui-font-${token})`
}

export function uiTypographyPlugin() {
  return {
    postcssPlugin: 'devflow-ui-typography',
    Declaration(declaration) {
      if (declaration.prop !== 'font-size') return
      declaration.value = typographyValue(declaration.value, declaration.parent?.selector || '')
    },
  }
}
