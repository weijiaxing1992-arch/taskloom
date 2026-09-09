import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { layoutScope } from './layoutScope'
import { useWorkspaceStore } from './stores/workspace'
import { t } from './i18n'
import type { MentionMember } from './mentions'
import { recentMemberScope } from './recentMembers'
import { rankMentionMembers, readRecentMentions, recentMentionKey, rememberMention } from './recentMentions'

// 用共享版本号同步同一页面的备注、描述和评论选择；浏览器只持久化稳定 ID，不缓存姓名。
const revision = ref(0)
function storage() { try { return typeof localStorage === 'undefined' ? undefined : localStorage } catch { return undefined } }
export function useRecentMentions(members: () => MentionMember[], query: () => string) {
  const workspace = useWorkspaceStore()
  const scope = computed(() => recentMemberScope(layoutScope.value, workspace.session))
  const directoryScope = ref(scope.value), recentIDs = ref<string[]>([])
  const usable = computed(() => !!scope.value && scope.value === directoryScope.value && !workspace.identityConflict && !workspace.operationDisabled && !workspace.switchingProject)
  const selfId = computed(() => usable.value ? workspace.session?.user.id || '' : '')
  const matches = computed(() => usable.value ? rankMentionMembers(members(), query(), selfId.value, recentIDs.value, t) : [])
  function refresh() { recentIDs.value = usable.value ? readRecentMentions(storage(), scope.value) : [] }
  function accepts(id: string) { return usable.value && members().some(member => member.id === id && member.active !== false) }
  function remember(id: string) {
    if (!accepts(id)) return
    recentIDs.value = rememberMention(storage(), scope.value, id, members(), selfId.value)
    revision.value++
  }
  function badge(id: string) { return id === selfId.value ? t('我') : recentIDs.value.includes(id) ? t('最近提及') : '' }
  // 切项目/账号后先使旧目录失效，等父组件提供新目录数组才恢复；防止迟到数据跨项目选人。
  watch(scope, (next, previous) => { if (next !== previous) directoryScope.value = '' }, { flush: 'sync' })
  watch(members, (next, previous) => { if (next !== previous && scope.value) directoryScope.value = scope.value }, { flush: 'sync' })
  watch([scope, usable, revision], refresh, { immediate: true, flush: 'sync' })
  function onStorage(event: StorageEvent) { if (event.key === recentMentionKey(scope.value) || event.key === null) refresh() }
  // storage 事件负责其他标签页，本页面组件通过 revision 同步；卸载必须移除监听。
  onMounted(() => window.addEventListener('storage', onStorage))
  onBeforeUnmount(() => window.removeEventListener('storage', onStorage))
  return { matches, usable, accepts, remember, badge }
}
