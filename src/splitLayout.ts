export type SplitBounds = { width: number; min: number; max: number; mobile: boolean; resizable: boolean; separator: number }
const positive = (value: unknown, fallback: number) => typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : fallback

/** 内部分栏按实际容器限幅，不使用窗口宽度猜测；侧栏展开、外层抽屉收窄都会重新计算。 */
export function splitBounds(width: number, minMain = 360, minAside = 300, maxAside = 640, breakpoint = 820): SplitBounds {
  const available = Math.max(0, Math.floor(positive(width, 0))), separator = 9
  const minimum = Math.ceil(positive(minAside, 300)), main = Math.ceil(positive(minMain, 360))
  const mobile = available <= positive(breakpoint, 820) || available < main + minimum + separator
  const max = mobile ? available : Math.min(Math.floor(positive(maxAside, 640)), available - main - separator)
  const min = mobile ? available : Math.min(minimum, max)
  return { width: available, min, max, mobile, resizable: !mobile && max > min, separator }
}
export function splitWidth(preferred: number, bounds: SplitBounds, fallback = 360): number {
  const value = typeof preferred === 'number' && Number.isFinite(preferred) ? preferred : positive(fallback, 360)
  return Math.round(Math.min(bounds.max, Math.max(bounds.min, value)))
}
export function splitPointerWidth(startWidth: number, startX: number, currentX: number, bounds: SplitBounds): number {
  if (!bounds.resizable || !Number.isFinite(startX) || !Number.isFinite(currentX)) return splitWidth(startWidth, bounds)
  return splitWidth(startWidth + startX - currentX, bounds)
}
export function splitKeyboardWidth(current: number, key: string, bounds: SplitBounds, shift = false): number | null {
  if (!bounds.resizable) return null
  const step = shift ? 80 : 20
  if (key === 'Home') return bounds.min
  if (key === 'End') return bounds.max
  if (key === 'ArrowLeft') return splitWidth(current + step, bounds)
  if (key === 'ArrowRight') return splitWidth(current - step, bounds)
  return null
}
/** 接收 layoutScope 的已验证租户/有效账号范围；项目来自会话，不读取旧 localStorage 项目选择。 */
export function splitStorageKey(scope: string, projectId: string, scene: string): string | null {
  if (!scope || typeof projectId !== 'string' || !projectId.trim() || projectId.length > 128 || !/^[a-z0-9][a-z0-9.-]{0,79}$/.test(scene)) return null
  return `devflow-layout:v1:${scope}:split:${encodeURIComponent(projectId)}:${scene}`
}
