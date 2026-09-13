/** A closed details panel has no rendered rectangle, so it must not fetch its files. */
export function observeVisibleAsset(element: Element, load: () => void): () => void {
  if (typeof IntersectionObserver === 'undefined') {
    // Older browsers keep the explicit preview/download action as their fallback.
    return () => {}
  }
  let active = true
  const observer = new IntersectionObserver(entries => {
    if (active && entries.some(entry => entry.target === element && entry.isIntersecting && entry.boundingClientRect.width > 0)) {
      active = false
      observer.disconnect()
      load()
    }
  }, { rootMargin: '160px 0px', threshold: 0 })
  observer.observe(element)
  return () => { active = false; observer.disconnect() }
}
