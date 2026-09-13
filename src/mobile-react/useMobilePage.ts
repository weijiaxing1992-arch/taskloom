import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { mobileAPI } from './mobileApi'

// Keep filters in mounted tabs, but cancel their reads as soon as they leave the foreground.
export function useMobileQuery<T>(path: string, active: boolean, projectId?: string, delay = 0, refreshKey?: unknown) {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const reload = useCallback(() => setAttempt(value => value + 1), [])
  useEffect(() => {
    if (!active) return
    const controller = new AbortController()
    setLoading(true)
    setError('')
    const timer = window.setTimeout(() => {
      void mobileAPI<T>(path, { signal: controller.signal, ...(projectId ? { headers: { 'X-TaskLoom-Project': projectId } } : {}) })
        .then(value => { if (!controller.signal.aborted) setData(value) })
        .catch(cause => { if (!controller.signal.aborted) { setData(null); setError(cause instanceof Error ? cause.message : '暂时无法加载，请稍后重试') } })
        .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    }, delay)
    return () => { controller.abort(); window.clearTimeout(timer) }
  }, [path, active, projectId, delay, attempt, refreshKey])
  return { data, loading, error, reload }
}

export function useMobilePageScroll(active: boolean, loading: boolean, pageKey: unknown = 'page') {
  const positions = useRef(new Map<unknown, number>())
  useLayoutEffect(() => {
    if (!active || loading) return
    window.scrollTo(0, positions.current.get(pageKey) || 0)
    const remember = () => { positions.current.set(pageKey, window.scrollY) }
    window.addEventListener('scroll', remember, { passive: true })
    return () => window.removeEventListener('scroll', remember)
  }, [active, loading, pageKey])
}
