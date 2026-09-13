export const recentSearchLimit = 5

function storageKey(scope: string) {
  return scope ? `devflow:recent-searches:v1:${scope}` : ''
}

function normalizedQuery(value: unknown) {
  return typeof value === 'string' ? value.replace(/\s+/g, ' ').trim().slice(0, 200) : ''
}

/**
 * Search history is a local convenience only. It is explicitly scoped by the
 * signed-in account (and, for the top search, current project), so a later
 * account never sees another user's query text on a shared browser.
 */
export function readRecentSearches(storage: Storage | undefined, scope: string): string[] {
  const key = storageKey(scope)
  if (!key) return []
  try {
    const raw = JSON.parse(storage?.getItem(key) || '[]')
    if (!Array.isArray(raw)) return []
    const seen = new Set<string>()
    return raw.map(normalizedQuery).filter(query => {
      const dedupe = query.toLocaleLowerCase()
      if (!query || seen.has(dedupe)) return false
      seen.add(dedupe)
      return true
    }).slice(0, recentSearchLimit)
  } catch { return [] }
}

export function rememberRecentSearch(storage: Storage | undefined, scope: string, value: unknown): string[] {
  const query = normalizedQuery(value)
  if (!query) return readRecentSearches(storage, scope)
  const key = storageKey(scope)
  if (!key) return []
  const next = [query, ...readRecentSearches(storage, scope).filter(item => item.toLocaleLowerCase() !== query.toLocaleLowerCase())].slice(0, recentSearchLimit)
  try { storage?.setItem(key, JSON.stringify(next)) } catch { /* Local history may be unavailable in private mode. */ }
  return next
}
