// A single counter owner prevents list responses and pre-mutation polls from restoring old counts.
export function createUnreadCounter(load: (signal: AbortSignal) => Promise<{ unread?: number }>, onChange: (value: number) => void) {
  let version = 0
  let pending: Promise<void> | undefined
  let controller: AbortController | undefined
  const invalidate = () => { version++; controller?.abort(); controller = undefined; pending = undefined }
  return {
    invalidate,
    commit(value: number) { invalidate(); onChange(Math.max(0, Number(value) || 0)) },
    refresh(): Promise<void> {
      if (pending) return pending
      const current = ++version
      controller = new AbortController()
      const signal = controller.signal
      const request = load(signal).then(data => {
        if (current === version && !signal.aborted) onChange(Math.max(0, Number(data.unread) || 0))
      }).finally(() => { if (pending === request) { pending = undefined; controller = undefined } })
      pending = request
      return request
    },
  }
}
