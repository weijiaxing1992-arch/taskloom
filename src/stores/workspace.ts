import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

export interface WorkspaceSession {
  tenant: { id: string; name: string }
  project: { id: string; name: string; code: string }
  user: { id: string; name: string; role: string; projectRoles?: string[]; avatarColor?: string; locale?: string; timezone?: string; operationDisabled?: boolean; mustChangePassword?: boolean }
  impersonation?: { adminName: string; readOnly?: boolean; [key: string]: unknown } | null
  canImpersonate?: boolean
  organizationPermissions?: string[]
}
export interface WorkspaceProject { id: string; name: string; code: string; status: string; canManage?: boolean; [key: string]: unknown }

/** 工作区会话只存于内存；仅缓存项目选择，不把身份、权限或凭据当作本地缓存的可信数据。 */
export const useWorkspaceStore = defineStore('workspace', () => {
  const session = ref<WorkspaceSession | null>(null), projects = ref<WorkspaceProject[]>([]), unread = ref(0)
  const project = ref(typeof localStorage === 'undefined' ? 'prj_orbit' : localStorage.getItem('devflow-project') || 'prj_orbit')
  const authChecking = ref(true), authRefreshing = ref(false), authError = ref('')
  const switchingProject = ref(false), projectError = ref(''), returning = ref(false), identityConflict = ref(false), impersonationRecovery = ref(false)
  const currentUser = computed(() => session.value?.user || null)
  const operationDisabled = computed(() => session.value?.user.operationDisabled === true)
  const mustChangePassword = computed(() => session.value?.user.mustChangePassword === true)
  // 切换期间 project 是尚待后端确认的选择草稿；业务作用域应读取已验证的 currentProject。
  const currentProject = computed(() => session.value?.project || null)
  // 这些值控制按钮和菜单展示，不代替 API 的服务端鉴权；禁用或身份冲突时优先收紧操作。
  const canManageOrganization = computed(() => !!session.value && !operationDisabled.value && !mustChangePassword.value && !identityConflict.value && (session.value.canImpersonate === true || session.value.user.role === 'tenant_admin'))
  const canManageProject = computed(() => canManageOrganization.value || (!!session.value && !operationDisabled.value && !mustChangePassword.value && !identityConflict.value && session.value.user.role === 'project_admin'))
  const canOpenOrganization = computed(() => canManageOrganization.value || (!!session.value && !operationDisabled.value && !mustChangePassword.value && !identityConflict.value && session.value.organizationPermissions?.includes('organization.read') === true))
  const canViewReports = computed(() => canManageOrganization.value || (!!session.value && !operationDisabled.value && !mustChangePassword.value && !identityConflict.value && session.value.organizationPermissions?.includes('reports.view') === true))
  // 个人工作量是已登录成员自己的只读数据，不等同于 reports.view 企业统计授权。
  // 服务端仍按当前账号、项目成员关系和组长角色分别收紧个人/组员范围。
  const canAccessWorkload = computed(() => !!session.value && !operationDisabled.value && !mustChangePassword.value && !identityConflict.value)
  function setUnread(value: unknown) { const count = Number(value); unread.value = Number.isFinite(count) ? Math.max(0, Math.floor(count)) : 0 }
  function acceptContext(context: { session: WorkspaceSession; projects: WorkspaceProject[]; unread: unknown }) {
    // 只有完整工作区上下文读取成功后才调用，避免项目/权限/未读数分别更新形成混合身份。
    session.value = context.session; projects.value = context.projects; setUnread(context.unread)
    if (!switchingProject.value && context.session.project.id) project.value = context.session.project.id
    identityConflict.value = false; impersonationRecovery.value = false; authError.value = ''
  }
  function clearSession() {
    // 清理内存中的敏感上下文，但保留非敏感项目选择，供下一次登录后重新校验。
    session.value = null; projects.value = []; unread.value = 0
    authChecking.value = false; authRefreshing.value = false; authError.value = ''
    identityConflict.value = false; impersonationRecovery.value = false; projectError.value = ''
  }
  function markIdentityConflict(expiredImpersonation: boolean, message: string) {
    identityConflict.value = true; impersonationRecovery.value = expiredImpersonation; authError.value = message
  }
  return { session, projects, unread, project, authChecking, authRefreshing, authError, switchingProject, projectError, returning, identityConflict, impersonationRecovery, currentUser, currentProject, operationDisabled, mustChangePassword, canManageOrganization, canManageProject, canOpenOrganization, canViewReports, canAccessWorkload, setUnread, acceptContext, clearSession, markIdentityConflict }
})
