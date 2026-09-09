export const DRAWER_MOBILE_BREAKPOINT = 760
export const DEFAULT_DRAWER_WIDTH = 1240
export const DEFAULT_DRAWER_MIN_WIDTH = 860
export const DEFAULT_DRAWER_VIEWPORT_RATIO = .96

export type DrawerBounds = { viewport: number; min: number; max: number; mobile: boolean; resizable: boolean }

function positive(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : fallback
}

export function drawerBounds(viewportWidth: number, minWidth = DEFAULT_DRAWER_MIN_WIDTH, maxViewportRatio = DEFAULT_DRAWER_VIEWPORT_RATIO): DrawerBounds {
  const viewport = Math.max(0, Math.floor(positive(viewportWidth, 0)))
  const mobile = viewport <= DRAWER_MOBILE_BREAKPOINT
  const max = mobile ? viewport : Math.floor(viewport * Math.min(1, positive(maxViewportRatio, DEFAULT_DRAWER_VIEWPORT_RATIO)))
  const min = mobile ? viewport : Math.min(max, Math.floor(positive(minWidth, DEFAULT_DRAWER_MIN_WIDTH)))
  return { viewport, min, max, mobile, resizable: !mobile && max > min }
}

export function preferredDrawerWidth(value: unknown, initialWidth = DEFAULT_DRAWER_WIDTH): number {
  return Math.round(positive(value, positive(initialWidth, DEFAULT_DRAWER_WIDTH)))
}

export function clampDrawerWidth(width: number | undefined, bounds: DrawerBounds, initialWidth = DEFAULT_DRAWER_WIDTH): number {
  return Math.min(bounds.max, Math.max(bounds.min, preferredDrawerWidth(width, initialWidth)))
}

// The drawer is anchored to the right, so moving its left edge left increases width.
export function drawerWidthFromPointer(startWidth: number, startX: number, clientX: number, bounds: DrawerBounds): number {
  if (!Number.isFinite(startX) || !Number.isFinite(clientX) || !bounds.resizable) return clampDrawerWidth(startWidth, bounds)
  return Math.min(bounds.max, Math.max(bounds.min, Math.round(startWidth + startX - clientX)))
}

export function drawerWidthFromKey(width: number, key: string, bounds: DrawerBounds, shiftKey = false, step = 24): number | null {
  if (!bounds.resizable) return null
  const current = clampDrawerWidth(width, bounds)
  const increment = positive(step, 24) * (shiftKey ? 4 : 1)
  if (key === 'Home') return bounds.min
  if (key === 'End') return bounds.max
  if (key === 'ArrowLeft') return Math.min(bounds.max, current + increment)
  if (key === 'ArrowRight') return Math.max(bounds.min, current - increment)
  return null
}
