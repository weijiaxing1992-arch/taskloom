<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useRoute, useRouter } from 'vue-router'
import { APIError, api } from './api'
import Icon from './components/Icon.vue'
import Login from './views/Login.vue'
import InitialPasswordChange from './components/InitialPasswordChange.vue'
import LocaleSwitcher from './components/LocaleSwitcher.vue'
import { applyLanguagePreferences, t } from './i18n'
import { applyDisplayPreferences } from './displayPreferences'
import { applyThemePreferences, startThemeClock } from './theme'
import { applyLayoutScope, clearLayoutScope, useLayoutBoolean } from './layoutScope'
import { useWorkspaceStore } from './stores/workspace'
import { Button } from './components/ui/button'
import TopSearch from './components/TopSearch.vue'
import ProjectNavigation from './components/ProjectNavigation.vue'
import PageWatermark from './components/PageWatermark.vue'
import MobileWorkNavigation from './components/MobileWorkNavigation.vue'
import DesktopNotifications from './components/DesktopNotifications.vue'

const router = useRouter()
const route = useRoute()
const workspace = useWorkspaceStore()
const { session, authChecking, authRefreshing, authError, projects, unread, project, switchingProject, projectError, returning, identityConflict, impersonationRecovery } = storeToRefs(workspace)
const isPublicRoute = computed(() => route.meta?.public === true)
const loginNotice = ref('')
let loadVersion = 0
// 项目切换是一个异步边界：旧项目的未读请求即使晚到，也不能覆盖已选择新项目后的角标。
// 不复用 loadVersion，避免取消切换时意外中止仍在读取的会话加载。
let projectContextVersion = 0
let linkProjectInitialized = false
let linkProjectResolved = false
let linkProject = ''
let unreadRequest = false
let unreadTimer: ReturnType<typeof setInterval> | undefined
// 通知由其他成员操作后只能通过服务端读取获知。页面可见时以较短节奏同步，
// 同时在当前页面产生通知的业务动作完成后立即刷新，避免角标停留在旧数量。
const unreadRefreshInterval = 3_000
let stopThemeClock: (() => void) | undefined
const topSearch = ref<InstanceType<typeof TopSearch> | null>(null)
const mobileMenu = ref(false)
const mobileViewport = ref(false)
// 仅缓存桌面偏好；手机抽屉始终完整展开，切账号时由 layoutScope 同步恢复各自选择。
const sidebarCollapsed = useLayoutBoolean('sidebar-collapsed', false)
const railCollapsed = computed(() => !mobileViewport.value && sidebarCollapsed.value)
const mobileRail = ref<HTMLElement|null>(null)
const mobileTrigger = ref<HTMLButtonElement|null>(null)
let mobileQuery:MediaQueryList|undefined
function syncMobile(){mobileViewport.value=!!mobileQuery?.matches;if(!mobileViewport.value)mobileMenu.value=false}
function mobileNavigationKeys(event:KeyboardEvent){
 if(!mobileViewport.value||!mobileMenu.value)return
 if(event.key==='Escape'){event.preventDefault();mobileMenu.value=false;return}
 if(event.key!=='Tab')return
 const elements=[...mobileRail.value?.querySelectorAll<HTMLElement>('a[href],button:not([disabled]),select:not([disabled])')||[]].filter(element=>element.getClientRects().length)
 const first=elements[0],last=elements.at(-1)
 if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus()}
 else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus()}
}
watch(mobileMenu,async opened=>{await nextTick();if(opened)mobileRail.value?.querySelector<HTMLElement>('button')?.focus();else if(mobileViewport.value)mobileTrigger.value?.focus()})
onMounted(()=>{mobileQuery=window.matchMedia('(max-width:1024px)');syncMobile();mobileQuery.addEventListener('change',syncMobile)})
onBeforeUnmount(()=>mobileQuery?.removeEventListener('change',syncMobile))
const roleNames: Record<string, string> = { tenant_admin: '企业管理员', project_admin: '项目管理员', product: '产品', frontend: '前端', backend: '后端', algorithm: '算法', ui: 'UI 设计', frontend_lead: '前端组长', backend_lead: '后端组长', qa: '测试', viewer: '只读' }
const roleLabel = computed(() => t(roleNames[session.value?.user.role || ''] || '团队成员'))
const avatarStyle = computed(() => ({ background: session.value?.user.avatarColor || '#665FE8' }))

async function load() {
  // 会话先验证，其他工作区数据再并行读取；仅同一版本的完整结果可以替换当前上下文。
  if (isPublicRoute.value) return false
  if (!linkProjectInitialized) { linkProjectInitialized=true; linkProject=typeof route.query.project==='string' ? route.query.project : '' }
  const version = ++loadVersion
  authRefreshing.value = true
  if (!session.value) authChecking.value = true
  async function fetchContext(explicitProject?: string) {
    const options = explicitProject ? { headers: { 'X-DevFlow-Project': explicitProject } } : undefined
    const verifiedSession = await api<any>('/session', options)
    if (version !== loadVersion || isPublicRoute.value) throw new Error('Workspace load superseded')
    // 初始改密阶段只取最小会话，业务列表/个人偏好接口应由后端强制拒绝，不要并行预读。
    if (verifiedSession.user?.operationDisabled || verifiedSession.user?.mustChangePassword) return { verifiedSession, display: {}, projects: [], unread: 0 }
    // 企业所有项目均归档后，管理员仍可管理项目；空项目会话不能预读项目收件箱。
    if (!verifiedSession.project?.id) {
      const [projectData, display] = await Promise.all([api<any>('/projects'), api<any>('/preferences/display')])
      return { verifiedSession, display, projects: projectData.items || [], unread: 0 }
    }
    const [projectData, notificationData, display] = await Promise.all([api<any>('/projects', options), api<any>('/notifications/unread-count', options), api<any>('/preferences/display', options)])
    return { verifiedSession, display, projects: projectData.items || [], unread: Number(notificationData.unread || 0) }
  }
  try {
    let context
    try { context = await fetchContext() } catch (cause) {
      // 首次加载发现缓存项目已撤权时只恢复一次默认项目；网络/数据库故障不能冒充撤权，
      // 也不能据此清掉有效登录或静默切换用户当前工作区。
      const managementPage = route.path === '/projects' || route.path === '/organization' || route.path.startsWith('/organization/')
      if (version === loadVersion && managementPage && !linkProject && cause instanceof APIError && cause.status === 403 && cause.code === 'project_forbidden') {
        // 归档/删除当前项目后，旧选择器不应锁死企业管理入口。先读取受鉴权项目目录，
        // 仅在另一个 active 项目会话校验成功后更新缓存；不自动修复越权通知深链。
        const directory = await api<any>('/projects')
        if (version !== loadVersion) return false
        const next = directory.items?.find((item: any) => item.status === 'active')
        if (!next) throw cause
        context = await fetchContext(next.id)
        if (version !== loadVersion) return false
        if (context.verifiedSession.project.id !== next.id) throw cause
        localStorage.setItem('devflow-project', next.id)
      } else if (version === loadVersion && !session.value && cause instanceof APIError && cause.status === 403 && cause.code === 'project_forbidden' && project.value !== 'prj_orbit') {
        project.value = 'prj_orbit'
        localStorage.setItem('devflow-project', project.value)
        context = await fetchContext()
      } else throw cause
    }
    if (version !== loadVersion) return false
    if (!context.verifiedSession.user?.operationDisabled && !context.verifiedSession.user?.mustChangePassword && !linkProjectResolved && linkProject && linkProject!==context.verifiedSession.project.id) {
      if (!context.projects.some((item:any)=>item.id===linkProject && item.status==='active')) throw new Error(t('通知链接中的项目无权访问或已停用'))
      context=await fetchContext(linkProject)
      if (version!==loadVersion || isPublicRoute.value) return false
      if (context.verifiedSession.project.id!==linkProject) throw new Error(t('通知项目上下文校验失败，请重新打开链接'))
      localStorage.setItem('devflow-project',linkProject)
    }
    if (!context.verifiedSession.user?.mustChangePassword) linkProjectResolved=true
    applyLayoutScope(context.verifiedSession.tenant?.id, context.verifiedSession.user?.id)
    workspace.acceptContext({ session: context.verifiedSession, projects: context.projects, unread: context.unread })
    applyLanguagePreferences(context.verifiedSession.user)
    applyDisplayPreferences(context.display)
    applyThemePreferences(context.display)
    localStorage.removeItem('devflow-user')
    loginNotice.value = ''
    return true
  } catch (cause) {
    if (version !== loadVersion) return false
    if (cause instanceof APIError && cause.status === 401) {
      clearLayoutScope()
      workspace.clearSession()
    } else {
      authError.value = cause instanceof Error ? cause.message : t('暂时无法连接服务，请稍后重试')
      if (!session.value && cause instanceof APIError && cause.status === 403) {
        try { const state = await api<any>('/auth/impersonation'); if (version === loadVersion && state.active) impersonationRecovery.value = true } catch { /* 保留原始错误；恢复连接后仍可重试返回管理员身份。 */ }
      }
    }
    return false
  } finally {
    if (version === loadVersion) { authChecking.value = false; authRefreshing.value = false }
  }
}

async function logout() {
  try { await api('/auth/logout', { method: 'POST', body: '{}' })
	loadVersion++
	clearLayoutScope()
	workspace.clearSession()
	applyDisplayPreferences({fontSize:'standard'})
    applyThemePreferences({themeMode:'light'})
	  authError.value = ''; authChecking.value = false; authRefreshing.value = false
  } catch (cause) { authError.value = cause instanceof Error ? cause.message : t('退出登录失败，请重试') }
}
async function switchProject() {
  // 先让原项目下的脏草稿/抽屉守卫决定是否退出，再改请求项目。取消时须保留原上下文。
  if (switchingProject.value) return
  const previousProject = session.value?.project.id || localStorage.getItem('devflow-project') || 'prj_orbit'
  const nextProject = project.value
  switchingProject.value = true
  projectError.value = ''
  try {
    const guard = new Event('devflow-before-project-change', { cancelable: true })
    if (!window.dispatchEvent(guard)) { project.value = previousProject; window.dispatchEvent(new Event('devflow-project-change-cancelled')); return }
    // 处理路由守卫期间，请求头仍指向原项目，避免旧草稿误写入即将切换的新项目。
    if (!['/projects', '/my-work', '/search', '/notifications', '/profile'].includes(route.path)) {
      const query = { ...route.query }
      // 工作项详情和创建上下文都绑定当前项目。切换项目时一并丢弃它们，避免旧项目
      // 的需求 ID 触发新项目的测试用例创建抽屉；后端仍会做最终的项目归属校验。
      // `project` 是跨项目深链的入口，不是当前页面的持久筛选条件。用户主动切换
      // 项目时必须移除，避免刷新后又按旧链接的项目把用户切回去。
      for (const key of ['project', 'req', 'sprint', 'bug', 'case', 'plan', 'execution', 'requirement', 'create', 'createChild', 'parentId']) delete query[key]
      const path = /^\/requirements\/(?:new|\d+\/edit)$/.test(route.path) ? '/requirements' : route.path
      const target = { path, query }
      if (router.resolve(target).fullPath !== route.fullPath) {
        const failure = await router.replace(target)
        if (failure) {
          project.value = previousProject
          window.dispatchEvent(new Event('devflow-project-change-cancelled'))
          return
        }
        await nextTick()
      }
    }
    // 从这里起，旧项目上下文中的异步角标响应均已过期。项目选择只有在服务端
    // 确认当前用户仍可访问目标项目之后才写入本地缓存，不能用待确认的下拉值做 API 默认作用域。
    projectContextVersion++
    await api(`/projects/${nextProject}/visit`, { method: 'POST', headers: { 'X-DevFlow-Project': nextProject } })
    localStorage.setItem('devflow-project', nextProject)
    if (['/projects', '/my-work', '/search', '/notifications', '/profile'].includes(route.path)) {
      if (!(await load())) throw new Error(authError.value || t('暂时无法载入项目，请重试'))
      window.dispatchEvent(new CustomEvent('devflow-project-changed', { detail: project.value }))
      return
    }
    location.href = router.resolve({ path: route.path, query: route.query }).href
  } catch (cause) {
    project.value = previousProject
    localStorage.setItem('devflow-project', previousProject)
    window.dispatchEvent(new Event('devflow-project-change-cancelled'))
    projectError.value = cause instanceof Error ? cause.message : t('切换项目失败，请重试')
  } finally {
    switchingProject.value = false
  }
}
function shortcuts(event: KeyboardEvent) { if (!isPublicRoute.value && !workspace.operationDisabled && (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); void topSearch.value?.focus() } }
function unreadChanged(event: Event) { if (!isPublicRoute.value) workspace.setUnread((event as CustomEvent).detail) }
function profileChanged() { if (!isPublicRoute.value) void load() }
function authExpired() { loadVersion++; clearLayoutScope(); applyThemePreferences({themeMode:'light'}); workspace.clearSession() }
function initialPasswordRequired(event: Event) {
  if (identityConflict.value || (session.value && (event as CustomEvent).detail?.userId !== session.value.user.id)) return
  loadVersion++; clearLayoutScope(); mobileMenu.value = false
  if (session.value) workspace.acceptContext({session:{...session.value,user:{...session.value.user,mustChangePassword:true}},projects:[],unread:0})
  else void load()
}
function passwordChangeComplete() { authExpired(); loginNotice.value = '密码已修改，请使用新密码重新登录' }
function accountDisabled() {
  loadVersion++; authRefreshing.value=false; authChecking.value=false
  if (session.value) {
    workspace.acceptContext({session:{...session.value,user:{...session.value.user,operationDisabled:true}},projects:[],unread:0})
  } else void load()
}
function identityChanged(event:Event){loadVersion++;clearLayoutScope();const expired=(event as CustomEvent).detail==='impersonation_expired';workspace.markIdentityConflict(expired,t(expired?'代访问已失效，请返回管理员账号':'账号身份已在其他页面切换，请刷新后继续'))}
async function stopImpersonation(){if(returning.value)return;returning.value=true;try{const data=await api<any>('/auth/impersonation/stop',{method:'POST',body:'{}'});clearLayoutScope();localStorage.setItem('devflow-project',data.projectId||'prj_orbit');location.href='/members'}catch(cause){authError.value=cause instanceof Error?cause.message:t('操作失败，请稍后重试')}finally{returning.value=false}}
function reloadIdentity(){location.reload()}
async function refreshUnread() {
  // 切换过程的 project 是下拉框中尚未验证的候选值；此时不得向候选项目读取未读数。
  if (isPublicRoute.value || workspace.operationDisabled || workspace.mustChangePassword || identityConflict.value || switchingProject.value || document.visibilityState === 'hidden' || !session.value || !session.value.project?.id || unreadRequest || authRefreshing.value) return
  const version = loadVersion, contextVersion = projectContextVersion, userID = session.value.user.id, projectID = session.value.project.id
  unreadRequest = true
  try {
    // 显式携带已验证项目，而不是依赖 localStorage 中可能被切换器修改的默认请求头。
    const data = await api<any>('/notifications/unread-count', { headers: { 'X-DevFlow-Project': projectID } })
    if (version === loadVersion && contextVersion === projectContextVersion && session.value?.user.id === userID && session.value?.project?.id === projectID && !switchingProject.value && !isPublicRoute.value) workspace.setUnread(data.unread)
  } catch { /* Keep the last known badge on transient failures; API handles expired sessions. */ }
  finally { unreadRequest = false }
}

watch(isPublicRoute, value => { if (value) { loadVersion++; authRefreshing.value=false } else void load() })
watch(()=>route.fullPath,()=>mobileMenu.value=false)
onMounted(() => { void router.isReady().then(() => { if (!isPublicRoute.value) void load() }); stopThemeClock = startThemeClock(); unreadTimer = setInterval(refreshUnread, unreadRefreshInterval); window.addEventListener('devflow-account-disabled',accountDisabled); window.addEventListener('devflow-identity-changed',identityChanged); window.addEventListener('focus', refreshUnread); document.addEventListener('visibilitychange', refreshUnread); window.addEventListener('keydown', shortcuts); window.addEventListener('devflow-unread', unreadChanged); window.addEventListener('devflow-notifications-changed', refreshUnread); window.addEventListener('devflow-profile-changed', profileChanged); window.addEventListener('devflow-auth-expired', authExpired) })
onBeforeUnmount(() => { clearLayoutScope(); clearInterval(unreadTimer); stopThemeClock?.(); window.removeEventListener('devflow-account-disabled',accountDisabled); window.removeEventListener('devflow-identity-changed',identityChanged); window.removeEventListener('focus', refreshUnread); document.removeEventListener('visibilitychange', refreshUnread); window.removeEventListener('keydown', shortcuts); window.removeEventListener('devflow-unread', unreadChanged); window.removeEventListener('devflow-notifications-changed', refreshUnread); window.removeEventListener('devflow-profile-changed', profileChanged); window.removeEventListener('devflow-auth-expired', authExpired) })
onMounted(() => window.addEventListener('devflow-password-change-required', initialPasswordRequired))
onBeforeUnmount(() => window.removeEventListener('devflow-password-change-required', initialPasswordRequired))
onMounted(() => window.addEventListener('devflow-project-list-changed', profileChanged))
onBeforeUnmount(() => window.removeEventListener('devflow-project-list-changed', profileChanged))
</script>

<template>
  <RouterView v-if="isPublicRoute" />
  <div v-else-if="authChecking" class="auth-loading"><span class="spinner"></span><p>{{ t('正在验证登录状态…') }}</p></div>
  <div v-else-if="!session && authError" class="auth-loading auth-retry-state" role="alert"><div class="auth-retry-card"><span class="auth-retry-icon">!</span><h1>{{ t('暂时无法打开工作空间') }}</h1><p>{{ authError }}</p><small>{{ t('暂未确认登录已失效，无需反复登录。请在服务恢复后重试。') }}</small><button v-if="impersonationRecovery" class="btn primary" :disabled="returning" @click="stopImpersonation">{{t('返回管理员')}}</button><button v-else class="btn primary" :disabled="authRefreshing" @click="load">{{ t(authRefreshing ? '正在重试…' : '重新连接') }}</button></div></div>
  <Login v-else-if="!session" :notice="loginNotice" @authenticated="load" />
  <section v-else-if="workspace.operationDisabled" class="account-disabled" role="alert"><div><span class="disabled-symbol">⊘</span><h1>{{t('账户已禁用')}}</h1><p>{{session.user.name}} · {{session.tenant.name}}</p><p>{{t('您可以登录，但当前无法访问或操作业务数据。请联系管理员重新启用账户。')}}</p><p v-if="authError">{{authError}}</p><div class="disabled-actions"><Button variant="outline" :disabled="authRefreshing" @click="load">{{t('检查启用状态')}}</Button><Button @click="logout">{{t('退出登录')}}</Button></div></div></section>
  <InitialPasswordChange v-else-if="workspace.mustChangePassword" :key="session.tenant.id+':'+session.user.id" :user="session.user" :blocked="identityConflict" :block-reason="authError" :impersonated="!!session.impersonation" :returning="returning" @return-administrator="stopImpersonation" @refresh="reloadIdentity" @completed="passwordChangeComplete" @logout="logout" />
  <div v-else :class="['app-shell', { viewer: session.user.role === 'viewer', impersonating: !!session.impersonation }]">
    <button v-if="mobileMenu" class="mobile-nav-backdrop" :aria-label="t('关闭导航')" @click="mobileMenu=false"></button>
    <aside id="primary-navigation" ref="mobileRail" class="rail" :class="{'mobile-open':mobileMenu,'rail-collapsed':railCollapsed}" :inert="mobileViewport&&!mobileMenu" :role="mobileMenu?'dialog':undefined" :aria-modal="mobileMenu||undefined" :aria-label="t('主导航')" @keydown="mobileNavigationKeys">
      <button class="mobile-nav-close" :aria-label="t('关闭导航')" @click="mobileMenu=false">×</button>
      <router-link class="brand brand-home" to="/" :aria-label="t('返回首页')" :title="t('返回首页')" @click="mobileMenu=false"><span class="brand-mark">D</span><div><strong>TaskLoom</strong><small>{{ t('星河示例企业研发协作') }}</small></div></router-link>
      <div class="tenant"><span class="avatar">{{ t('蝠') }}</span><div><b>{{ session?.tenant.name || '星河示例企业' }}</b><small>{{ t('企业工作台') }}</small></div></div>
      <button type="button" class="sidebar-collapse-toggle" aria-controls="primary-navigation" :aria-expanded="!railCollapsed" :aria-label="t(railCollapsed?'展开侧边栏':'收起侧边栏')" :title="t(railCollapsed?'展开侧边栏':'收起侧边栏')" @click="sidebarCollapsed=!sidebarCollapsed"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 4h18v16H3zM8 4v16"/><path :d="railCollapsed?'m12 9 3 3-3 3':'m16 9-3 3 3 3'"/></svg><span class="sidebar-toggle-label">{{t(railCollapsed?'展开侧边栏':'收起侧边栏')}}</span></button>
      <nav class="global-nav" :aria-label="t('工作台导航')">
        <p class="nav-section-label">{{ t('工作台') }}</p>
        <router-link to="/projects" :title="t('项目空间')" :aria-label="t('项目空间')"><Icon name="projects"/><span class="rail-link-label">{{ t('项目空间') }}</span></router-link>
        <router-link to="/my-work" :title="t('我的工作')" :aria-label="t('我的工作')"><Icon name="work"/><span class="rail-link-label">{{ t('我的工作') }}</span></router-link>
        <router-link to="/search" :title="t('全局搜索')" :aria-label="t('全局搜索')"><Icon name="search"/><span class="rail-link-label">{{ t('全局搜索') }}</span><kbd>⌘K</kbd></router-link>
        <router-link to="/notifications" :title="t('通知中心')" :aria-label="t('通知中心')+(unread?' · '+unread:'')"><Icon name="bell"/><span class="rail-link-label">{{ t('通知中心') }}</span><em v-if="unread" class="number-badge">{{ unread > 99 ? '99+' : unread }}</em></router-link>
        <p v-if="workspace.canViewReports" class="nav-section-label nav-section-spaced">{{ t('团队洞察') }}</p>
        <router-link v-if="workspace.canViewReports" to="/reports/workload" :title="t('工作量统计')" :aria-label="t('工作量统计')"><Icon name="chart"/><span class="rail-link-label">{{t('工作量统计')}}</span></router-link>
      </nav>
      <div class="rail-bottom">
        <p class="brand-slogan">{{t('努力只能及格，拼命Vibe才能优秀！')}}</p>
        <p v-if="workspace.canManageProject || workspace.canOpenOrganization || (session.project?.id&&!session.impersonation)" class="nav-section-label">{{ t('管理与配置') }}</p>
        <router-link v-if="workspace.canManageProject" to="/settings/fields" :title="t('项目设置')" :aria-label="t('项目设置')"><Icon name="fields"/><span class="rail-link-label">{{ t('项目设置') }}</span></router-link>
        <router-link v-if="workspace.canManageProject" to="/settings/fields?tab=automation" :title="t('自动化规则')" :aria-label="t('自动化规则')"><Icon name="settings"/><span class="rail-link-label">{{ t('自动化规则') }}</span></router-link>
        <router-link v-if="workspace.canManageProject" to="/audit" :title="t('变更历史')" :aria-label="t('变更历史')"><Icon name="review"/><span class="rail-link-label">{{ t('变更历史') }}</span></router-link>
        <router-link v-if="workspace.canOpenOrganization" to="/organization" :title="t('企业管理')" :aria-label="t('企业管理')"><Icon name="settings"/><span class="rail-link-label">{{ t('企业管理') }}</span></router-link>
        <router-link v-if="workspace.canManageOrganization&&!session.impersonation" to="/settings/ai" :title="t('AI 服务配置')" :aria-label="t('AI 服务配置')"><Icon name="settings"/><span class="rail-link-label">{{t('AI 服务配置')}}</span></router-link>
        <router-link v-if="session.project?.id&&!session.impersonation" to="/settings/integrations" :title="t('API 与 AI 集成')" :aria-label="t('API 与 AI 集成')"><Icon name="settings"/><span class="rail-link-label">{{ t('API 与 AI 集成') }}</span></router-link>
        <router-link class="current-user" to="/profile" :aria-label="t('打开个人信息')" :title="(session?.user.name||'')+' · '+roleLabel"><span class="avatar small" :style="avatarStyle">{{ session?.user.name?.slice(0, 1) || '林' }}</span><div><b>{{ session?.user.name || '林夏' }}</b><small>{{ roleLabel }}</small></div><span class="profile-chevron">›</span></router-link>
		<Button variant="ghost" size="sm" class="rail-logout mt-2 w-full justify-start" :title="t('退出登录')" :aria-label="t('退出登录')" @click="logout"><svg class="rail-logout-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 4H4v16h5M10 12h11m-4-4 4 4-4 4"/></svg><span class="rail-link-label">{{ t('退出登录') }}</span></Button>
      </div>
    </aside>
    <section class="workspace" :class="{'mobile-work-hub':['/my-work','/search','/notifications','/projects'].includes(route.path)}" :inert="mobileViewport&&mobileMenu">
      <DesktopNotifications :key="session.tenant.id+':'+session.user.id" runtime-only />
      <div v-if="session.impersonation" class="impersonation-banner" role="status"><div><b>{{t('正在代访问：{name}',{name:session.user.name})}}</b><span>{{t('管理员 {name} · 操作全程审计 · 最长 30 分钟',{name:session.impersonation.adminName})}}</span></div><button class="btn compact" :disabled="returning" @click="stopImpersonation">{{t('返回管理员')}}</button></div>
      <header class="topbar">
        <div class="topbar-leading">
          <button ref="mobileTrigger" class="mobile-nav-trigger" :aria-label="t('打开导航')" :aria-expanded="mobileMenu" @click="mobileMenu=!mobileMenu"><Icon name="menu"/></button>
          <div class="project-switcher"><span class="project-icon">{{ session?.project.name?.slice(0, 1) || '项' }}</span><select v-model="project" :aria-label="t('切换当前项目')" :disabled="switchingProject" @change="switchProject"><option v-for="x in projects.filter(x => x.status === 'active')" :key="x.id" :value="x.id">{{ x.name }} · {{ x.code }}</option></select></div>
        </div>
        <TopSearch ref="topSearch" :disabled="identityConflict||switchingProject"/>
        <div class="top-actions"><LocaleSwitcher authenticated /><router-link class="plain-icon help-entry" to="/help" :aria-label="t('帮助')" :title="t('帮助')"><Icon name="help"/></router-link><router-link class="icon-link" to="/notifications" :aria-label="t('通知中心')+(unread?' · '+(unread>99?'99+':unread):'')"><Icon name="bell" :size="18"/><em v-if="unread" class="notification-badge">{{ unread > 99 ? '99+' : unread }}</em></router-link><router-link class="avatar small avatar-link" :style="avatarStyle" to="/profile" :aria-label="t('打开个人信息')">{{ session?.user.name?.slice(0, 1) || '林' }}</router-link></div>
      </header>
      <ProjectNavigation />
      <div v-if="projectError" class="readonly-banner" role="alert">{{ projectError }}</div>
      <div v-if="authError" class="service-retry-banner" role="alert"><div><b>{{ t('服务暂时不可用，已保留当前登录和工作空间') }}</b><span>{{ authError }}</span></div><button class="btn compact" :disabled="authRefreshing" @click="load">{{ t(authRefreshing ? '正在重试…' : '重试连接') }}</button></div>
      <div v-if="session?.user.role === 'viewer'" class="readonly-banner">{{ t('只读身份：可以浏览、搜索和处理自己的通知，业务写操作由后端强制拦截。') }}</div>
      <div v-if="identityConflict" class="readonly-banner" role="alert">{{authError}} <button class="btn compact" @click="impersonationRecovery?stopImpersonation():reloadIdentity()">{{t(impersonationRecovery?'返回管理员':'刷新')}}</button></div>
      <main :inert="identityConflict"><router-view /></main>
      <MobileWorkNavigation v-if="['/my-work','/search','/notifications','/projects'].includes(route.path)&&!identityConflict" :unread="unread" />
    </section>
    <PageWatermark />
  </div>
</template>

<style scoped>
.mobile-nav-trigger,.mobile-nav-close,.mobile-nav-backdrop{display:none}
@media(max-width:1024px){.mobile-nav-trigger{display:grid;place-items:center;flex:none;width:44px;height:44px;border:1px solid var(--line);border-radius:8px;background:var(--surface);color:var(--ink)}.mobile-nav-close{display:block;position:absolute;right:10px;top:10px;width:40px;height:40px;border:0;border-radius:8px;background:#ffffff15;color:white;font-size:22px}.mobile-nav-backdrop{display:block;position:fixed;inset:0;z-index:399;border:0;background:#09122499}.rail{display:flex!important;position:fixed;inset:0 auto 0 0;width:260px!important;z-index:400;transform:translateX(-100%);transition:transform .2s ease}.rail.mobile-open{transform:translateX(0)}.brand{margin-top:20px}}
.brand-home{text-decoration:none}.brand-home:focus-visible{outline:2px solid var(--ring);outline-offset:3px;border-radius:var(--radius)}.brand-slogan{margin:12px 10px 18px;padding-left:10px;border-left:2px solid #9f9bff;color:#cbd5e1;font-size:11px;line-height:1.9;letter-spacing:.25px}.brand-slogan span{margin:0 2px;color:#c4b5fd;font-weight:700}
.account-disabled{min-height:100dvh;display:grid;place-items:center;background:var(--page-bg,#f5f6fa);color:var(--text-primary,#243247);padding:24px}.account-disabled>div{max-width:520px;text-align:center;border:1px solid var(--border-color,#dce2ec);border-radius:18px;padding:36px;background:var(--surface,#fff)}.account-disabled p{color:var(--text-secondary,#667085);line-height:1.8}.disabled-symbol{font-size:36px;color:#dc6858}.disabled-actions{display:flex;gap:12px;justify-content:center;margin-top:24px}
.impersonation-banner{position:relative;z-index:100;flex:none;height:64px;display:flex;align-items:center;justify-content:space-between;gap:16px;padding:12px 24px;background:#fff5dc;border-bottom:1px solid #f6da93;color:#89520d}.impersonation-banner>div{display:grid;gap:4px}.impersonation-banner b{font-size:13px}.impersonation-banner span{font-size:11px}.impersonation-banner button{flex:none;border-color:#e4c788;color:#89520d}
:global(.app-shell.impersonating .drawer-shade),:global(.app-shell.impersonating .modal-shade){top:64px}
.help-entry{display:inline-flex;align-items:center;justify-content:center;text-decoration:none;color:var(--muted-foreground)}.help-entry:hover{color:var(--primary)}
.auth-retry-card{width:min(440px,calc(100vw - 40px));padding:32px;border:1px solid #e4e7ec;border-radius:12px;background:#fff;text-align:center}.auth-retry-icon{display:grid;place-items:center;width:38px;height:38px;margin:0 auto 16px;border-radius:11px;background:#fff3df;color:#b7791f;font-size:22px;font-weight:650}.auth-retry-card h1{margin:0 0 12px;font-size:20px;color:#344054}.auth-retry-card p{margin:0 0 10px;color:#667085;font-size:12px;line-height:1.7}.auth-retry-card small{display:block;color:#98a2b3;font-size:11px;line-height:1.7}.auth-retry-card>.btn{margin-top:22px}.service-retry-banner{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:10px 24px;background:#fffaeb;border-bottom:1px solid #f5dfad;color:#986214}.service-retry-banner>div{display:grid;gap:3px}.service-retry-banner b{font-size:11px;font-weight:550}.service-retry-banner span{font-size:10px;color:#a87530}.service-retry-banner .btn{flex:none}
</style>
