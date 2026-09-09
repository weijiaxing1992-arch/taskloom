// Adapt the existing component palette through CSS variables. Original values
// remain the light-mode fallbacks; user-authored rich text, tag colors and images
// are deliberately outside this build-time transformation.
function metrics(hex) {
  const normalized = hex.length === 4 ? '#' + [...hex.slice(1)].map(x => x + x).join('') : hex
  const [r, g, b] = [1, 3, 5].map(i => parseInt(normalized.slice(i, i + 2), 16) / 255)
  const max = Math.max(r, g, b), min = Math.min(r, g, b), chroma = max - min, light = (max + min) / 2
  let hue = !chroma ? 0 : max === r ? ((g - b) / chroma + 6) % 6 : max === g ? (b - r) / chroma + 2 : (r - g) / chroma + 4
  return { light, chroma, hue: hue * 60 }
}
function hueName(hue) { return hue < 25 || hue >= 345 ? 'red' : hue < 75 ? 'amber' : hue < 175 ? 'green' : hue < 265 ? 'blue' : 'purple' }
export function themedColor(property, value) {
  const hex = value === 'white' ? '#ffffff' : value === 'black' ? '#000000' : value
  if (!/^#(?:[a-f\d]{3}|[a-f\d]{6})$/i.test(hex)) return value
  const { light, chroma, hue } = metrics(hex), neutral = chroma < 0.15 || chroma < 0.27 && hue >= 195 && hue <= 245
  let token
  if (property === 'color') {
    if (light > 0.85) return value // Preserve white text on brand/status buttons.
    token = neutral ? light < 0.35 ? 'text' : 'muted' : 'text-' + hueName(hue)
  } else if (/^border(?:-.+)?$|^outline(?:-color)?$/.test(property)) {
    if (light < 0.72 && chroma > 0.23) return value // Active/focus indicators keep their identity.
    token = 'border'
  } else if (/^background(?:-color)?$/.test(property)) {
    if (light > 0.78) token = chroma > 0.065 ? 'tint-' + hueName(hue) : light > 0.985 ? 'surface' : 'subtle'
    else if (neutral && light < 0.38) token = 'strong'
  }
  return token ? `var(--palette-${token}, ${value})` : value
}
export function themePalettePlugin() {
  const visited = new WeakSet()
  return {
    postcssPlugin: 'devflow-theme-palette',
    Declaration(declaration) {
      if (visited.has(declaration)) return
      visited.add(declaration)
      // Theme tokens and miniature previews intentionally show their own colors.
      const source = declaration.source?.input?.file || ''
      if (/theme\.css|ThemePreference\.vue/.test(source) || declaration.prop.startsWith('--') || declaration.value.includes('var(') || declaration.value.includes('url(')) return
      declaration.value = declaration.value.replace(/#[a-f\d]{3,8}\b|\bwhite\b|\bblack\b/gi, value => themedColor(declaration.prop, value))
    },
  }
}
