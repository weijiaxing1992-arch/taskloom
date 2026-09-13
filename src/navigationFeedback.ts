import { reactive } from 'vue'
import type { Router } from 'vue-router'

// The old view stays mounted while a route chunk is loading. Never reload the
// browser automatically: a failed chunk must not discard an unsaved form.
export function createNavigationFeedback(router: Router) {
  const state = reactive({ busy: false, failed: false, reloadSuggested: false })
  let target = '', failedTarget = '', version = 0, disposed = false, retrying = false
  let timer: ReturnType<typeof setTimeout> | undefined
  function stop() { version++; if (timer !== undefined) clearTimeout(timer); timer = undefined; state.busy = false }
  const removers = [
    router.beforeEach((to, from) => {
      stop(); state.failed = false; state.reloadSuggested = false; failedTarget = ''; target = to.fullPath
      if (to.path === from.path) return
      const current = version
      timer = setTimeout(() => { if (!disposed && current === version) state.busy = true }, 160)
    }),
    router.afterEach(to => { if (to.fullPath === target) { stop(); target = '' } }),
    router.onError((_cause, to) => {
      if (disposed || to.fullPath !== target) return
      stop(); target = ''; failedTarget = to.fullPath; state.failed = true; state.reloadSuggested = retrying
    }),
  ]
  async function retry() {
    if (disposed || !state.failed || !failedTarget.startsWith('/') || failedTarget.startsWith('//')) return
    const next = failedTarget
    state.failed = false; retrying = true
    try { await router.push(next) }
    catch { if (!disposed && !state.failed && !target) { failedTarget = next; state.failed = true; state.reloadSuggested = true } }
    finally { retrying = false }
  }
  // Browsers can memoize a failed dynamic import. A full navigation is only an
  // explicit, confirmed fallback after retry fails; never silently discard work.
  function reopen(confirmLeave: () => boolean, navigate: (path: string) => void) {
    if (disposed || !state.failed || !state.reloadSuggested || !failedTarget.startsWith('/') || failedTarget.startsWith('//')) return
    const next = failedTarget
    if (confirmLeave()) navigate(next)
  }
  function dismiss() { state.failed = false; state.reloadSuggested = false; failedTarget = '' }
  function dispose() { disposed = true; stop(); dismiss(); removers.forEach(remove => remove()) }
  return { state, retry, reopen, dismiss, dispose }
}
