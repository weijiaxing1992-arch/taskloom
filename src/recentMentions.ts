import { filterMentionMembers, type MentionMember } from './mentions'
import { normalizeRecentMembers, type RecentMemberStorage } from './recentMembers'

export const recentMentionLimit = 5
// 与普通“最近处理人”使用不同键空间：提及只保留最近 5 位他人，避免两种习惯互相污染。
export function recentMentionKey(scope: string): string | null {
  return scope && scope.length <= 1000 ? 'devflow-recent-mentions:v1:' + scope : null
}
export function readRecentMentions(storage: RecentMemberStorage | undefined, scope: string): string[] {
  const key = recentMentionKey(scope)
  if (!key || !storage) return []
  try { return normalizeRecentMembers(JSON.parse(storage.getItem(key) || '[]')).slice(0, recentMentionLimit) }
  catch { return [] }
}
export function rememberMention(storage: RecentMemberStorage | undefined, scope: string, id: string, members: MentionMember[], selfId: string): string[] {
  const previous = readRecentMentions(storage, scope), key = recentMentionKey(scope)
  // 自己单独固定在首位，不占用最近 5 人名额；停用/移出当前目录的人不能新加入缓存。
  if (!key || !id || id === selfId || !members.some(member => member.id === id && member.active !== false)) return previous
  const eligible = new Set(members.filter(member => member.active !== false && member.id !== selfId).map(member => member.id))
  const next = normalizeRecentMembers([id, ...previous]).filter(value => eligible.has(value)).slice(0, recentMentionLimit)
  try { storage?.setItem(key, JSON.stringify(next)) } catch { /* 缓存失败不影响提及本身与后续通知。 */ }
  return next
}
export function rankMentionMembers(members: MentionMember[], query: string, selfId: string, recentIDs: string[], translate: (source: string) => string = source => source): MentionMember[] {
  // 先按当前目录和搜索词过滤，再置顶“我/最近”；缓存 ID 不能凭空产生候选人或绕过搜索。
  const seen = new Set<string>()
  const directory = members.filter(member => member.active !== false && !seen.has(member.id) && !!seen.add(member.id))
  const selfQuery = ['我', 'me', translate('我').toLocaleLowerCase()].includes(query.trim().toLocaleLowerCase())
  const matches = selfQuery ? directory.filter(member => member.id === selfId) : filterMentionMembers(directory, query, translate)
  const byID = new Map(matches.map(member => [member.id, member]))
  const preferred = [selfId, ...normalizeRecentMembers(recentIDs).filter(id => id !== selfId).slice(0, recentMentionLimit)]
  const result: MentionMember[] = []
  for (const id of preferred) { const member = byID.get(id); if (member) { result.push(member); byID.delete(id) } }
  return [...result, ...byID.values()]
}
