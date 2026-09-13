<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute } from 'vue-router'
import WechatBinding from '../components/WechatBinding.vue'
import WecomAppBinding from '../components/WecomAppBinding.vue'
import { api } from '../api'
import Icon from '../components/Icon.vue'
import SidebarCollapseButton from '../components/SidebarCollapseButton.vue'
import FontSizePreference from '../components/FontSizePreference.vue'
import ThemePreference from '../components/ThemePreference.vue'
import UserWecomWebhook from '../components/UserWecomWebhook.vue'
import { applyLanguagePreferences, formatDate, locale, t } from '../i18n'
import { useLayoutBoolean } from '../layoutScope'

const tabs = [
  { id: 'basic', label: '基本资料', icon: 'user' },
  { id: 'security', label: '账号安全', icon: 'shield' },
  { id: 'preferences', label: '偏好设置', icon: 'preferences' },
  { id: 'wecom', label: '企微通知', icon: 'bell' },
  { id: 'wecom-app', label: '企业微信绑定', icon: 'shield' },
  { id: 'wechat', label: '微信绑定', icon: 'shield' },
]
const colors = ['#665FE8', '#3478F6', '#12A594', '#E17B2D', '#D84C6F', '#7357B8']
const roleNames: Record<string, string> = { tenant_admin: '企业管理员', project_admin: '项目管理员', product: '产品', frontend: '前端', backend: '后端', algorithm: '算法', ui: 'UI 设计', frontend_lead: '前端组长', backend_lead: '后端组长', qa: '测试', viewer: '只读成员', member: '企业成员' }
function profileProjectRoleNames(membership:{projectRoles?:string[];role:string}){return (membership.projectRoles??[membership.role]).map(role=>t(roleNames[role]||role)).join(' / ')}

const route = useRoute()
const activeTab = ref(route.query.tab==='wechat'||route.query.tab==='wecom-app'?String(route.query.tab):'basic')
const wechatEditor=ref<InstanceType<typeof WechatBinding>|null>(null)
const wecomAppEditor=ref<InstanceType<typeof WecomAppBinding>|null>(null)
const profileSidebarExpanded=useLayoutBoolean('profile.sidebar',true)
const webhookEditor=ref<InstanceType<typeof UserWecomWebhook>|null>(null),webhookBusy=ref(false)
const profile = ref<any>(null)
const loading = ref(true)
const saving = ref(false)
const passwordSaving = ref(false)
const revealPassword = ref(false)
const message = ref('')
const messageType = ref<'success' | 'error'>('success')
const form = reactive({ name: '', email: '', phone: '', jobTitle: '', bio: '', avatarColor: '#665FE8', locale: 'zh-CN', timezone: 'Asia/Shanghai', emailNotifications: true })
const password = reactive({ currentPassword: '', newPassword: '', confirmPassword: '' })

const initials = computed(() => (form.name || profile.value?.name || '我').slice(0, 1))
const passwordScore = computed(() => {
  const value = password.newPassword
  if (!value) return 0
  return Math.min(4, Number(value.length >= 8) + Number(/[A-Za-z]/.test(value)) + Number(/\d/.test(value)) + Number(/[^A-Za-z0-9]/.test(value)))
})
const passwordLabel = computed(() => ['', '较弱', '一般', '良好', '较强'][passwordScore.value])

function applyProfile(value: any) {
  profile.value = value
  Object.assign(form, {
    name: value.name || '', email: value.email || '', phone: value.phone || '', jobTitle: value.jobTitle || '', bio: value.bio || '',
    avatarColor: value.avatarColor || '#665FE8', locale: value.locale || 'zh-CN', timezone: value.timezone || 'Asia/Shanghai', emailNotifications: Boolean(value.emailNotifications),
  })
}

async function load() {
  loading.value = true
  message.value = ''
  try { applyProfile(await api('/profile')) }
  catch (cause: any) { showMessage(cause.message || '个人信息加载失败', 'error') }
  finally { loading.value = false }
}

function showMessage(text: string, type: 'success' | 'error' = 'success') {
  message.value = t(text)
  messageType.value = type
}

async function saveProfile(success = '个人信息已保存') {
  saving.value = true
  message.value = ''
  try {
    applyProfile(await api('/profile', { method: 'PATCH', body: JSON.stringify(form) }))
    applyLanguagePreferences(profile.value)
    window.dispatchEvent(new CustomEvent('devflow-profile-changed'))
    showMessage(success)
  } catch (cause: any) {
    showMessage(cause.message || '保存失败，请稍后重试', 'error')
  } finally { saving.value = false }
}

async function changePassword() {
  passwordSaving.value = true
  message.value = ''
  try {
    const result = await api<any>('/profile/password', { method: 'POST', body: JSON.stringify(password) })
    profile.value.passwordConfigured = result.passwordConfigured
    profile.value.passwordChangedAt = result.passwordChangedAt
    Object.assign(password, { currentPassword: '', newPassword: '', confirmPassword: '' })
    showMessage('登录密码已更新，新的安全记录已写入通知中心。')
  } catch (cause: any) {
    showMessage(cause.message || '密码修改失败', 'error')
  } finally { passwordSaving.value = false }
}

function canLeave(){return !saving.value&&!passwordSaving.value&&(wechatEditor.value?.canLeave()??true)&&(wecomAppEditor.value?.canLeave()??true)&&(webhookEditor.value?.canLeave()??true)}
function switchTab(value: string) { if(value===activeTab.value||!canLeave())return;activeTab.value = value; message.value = '' }
onBeforeRouteLeave(canLeave);onBeforeRouteUpdate(canLeave)
watch(locale, value => { form.locale = value })

onMounted(load)
</script>

<template>
  <div class="module-page profile-page">
    <div class="page-heading">
      <div><span class="eyebrow">{{ t('账号与偏好') }}</span><h1>{{ t('个人设置') }}</h1><p>{{ t('管理个人资料、登录安全和工作偏好，这些设置会同步到你的 TaskLoom 账号。') }}</p></div>
    </div>

    <div v-if="loading" class="state"><span class="spinner"></span>{{ t('正在载入个人信息…') }}</div>
    <div v-else-if="profile" class="profile-shell">
      <aside class="profile-sidebar" :class="{'is-collapsed':!profileSidebarExpanded}">
        <SidebarCollapseButton :expanded="profileSidebarExpanded" :label="t('个人设置侧边栏')" @toggle="profileSidebarExpanded=!profileSidebarExpanded"/>
        <div v-show="profileSidebarExpanded" class="profile-sidebar-content">
        <section class="profile-identity">
          <span class="profile-avatar" :style="{ background: form.avatarColor }">{{ initials }}</span>
          <h2>{{ profile.name }}</h2>
          <p>{{ profile.email }}</p>
          <span class="account-state"><i></i>{{ t(profile.active ? '账号正常' : '账号停用') }}</span>
        </section>
        <nav :aria-label="t('个人设置导航')">
          <button v-for="item in tabs" :key="item.id" :disabled="saving||passwordSaving||webhookBusy" :class="{ active: activeTab === item.id }" @click="switchTab(item.id)"><Icon :name="item.icon" /><span>{{ t(item.label) }}</span><b>›</b></button>
        </nav>
        <section class="profile-org">
          <span>{{ t('所属组织') }}</span><b>{{ t('星河示例企业') }}</b>
          <small>{{ t(roleNames[profile.tenantRole] || profile.tenantRole) }}</small>
        </section>
        </div>
      </aside>

      <section class="profile-content">
        <p v-if="message" :class="['profile-message', messageType]" :role="messageType === 'error' ? 'alert' : 'status'">{{ message }}</p>

        <article v-if="activeTab === 'basic'" class="profile-panel">
          <header><div><h2>{{ t('基本资料') }}</h2><p>{{ t('完善真实资料，便于团队成员在协作中识别和联系你。') }}</p></div><span class="profile-section-mark"><Icon name="user" :size="20" /></span></header>
          <form class="profile-form" @submit.prevent="saveProfile()">
            <div class="avatar-editor">
              <span class="profile-avatar large" :style="{ background: form.avatarColor }">{{ initials }}</span>
              <div><b>{{ t('头像主题') }}</b><p>{{ t('当前使用姓名首字作为头像，可选择更容易辨认的主题色。') }}</p><div class="color-picker" :aria-label="t('选择头像主题色')"><button v-for="color in colors" :key="color" type="button" :class="{ active: form.avatarColor === color }" :style="{ background: color }" :aria-label="t('选择颜色 {color}', { color })" @click="form.avatarColor = color"><span>✓</span></button></div></div>
            </div>
            <div class="profile-form-grid">
              <label><span>{{ t('姓名') }} <b>*</b></span><input v-model="form.name" maxlength="40" autocomplete="name" required></label>
              <label><span>{{ t('工作邮箱') }} <b>*</b></span><input v-model="form.email" type="email" maxlength="120" autocomplete="email" required></label>
              <label><span>{{ t('手机号码') }}</span><input v-model="form.phone" maxlength="30" autocomplete="tel" :placeholder="t('例如 +86 138 0000 0000')"></label>
              <label><span>{{ t('职位') }}</span><input v-model="form.jobTitle" maxlength="60" autocomplete="organization-title" :placeholder="t('例如 产品研发负责人')"></label>
              <label class="readonly-field"><span>{{ t('部门') }}</span><input :value="profile.department || t('未设置')" readonly><small>{{ t('组织信息由企业管理员维护') }}</small></label>
              <label class="readonly-field"><span>{{ t('工号') }}</span><input :value="profile.employeeNo || t('未设置')" readonly><small>{{ t('组织信息由企业管理员维护') }}</small></label>
              <label class="wide"><span>{{ t('个人简介') }}</span><textarea v-model="form.bio" maxlength="200" :placeholder="t('介绍你的职责、专长或当前关注方向')"></textarea><small>{{ form.bio.length }}/200</small></label>
            </div>
            <footer><span>{{ t('最近活跃：') }}{{ formatDate(profile.lastActive) }}</span><button class="btn primary" :disabled="saving">{{ t(saving ? '保存中…' : '保存基本资料') }}</button></footer>
          </form>
        </article>

        <article v-else-if="activeTab === 'security'" class="profile-panel security-panel">
          <header><div><h2>{{ t('账号安全') }}</h2><p>{{ t('定期更新密码，避免在多个系统重复使用相同凭据。') }}</p></div><span class="profile-section-mark secure"><Icon name="shield" :size="20" /></span></header>
          <div class="security-summary"><span><Icon name="key" :size="20" /></span><div><b>{{ t(profile.passwordConfigured ? '登录密码已设置' : '尚未设置登录密码') }}</b><p>{{ profile.passwordConfigured ? t('最近修改：{date}', { date: formatDate(profile.passwordChangedAt) }) : t('首次设置密码时无需填写当前密码。') }}</p></div><em>{{ t(profile.passwordConfigured ? '受保护' : '待完善') }}</em></div>
          <form class="password-form" @submit.prevent="changePassword">
            <label v-if="profile.passwordConfigured"><span>{{ t('当前密码') }}</span><div class="password-input"><input v-model="password.currentPassword" :type="revealPassword ? 'text' : 'password'" autocomplete="current-password" required><button type="button" @click="revealPassword = !revealPassword">{{ t(revealPassword ? '隐藏' : '显示') }}</button></div></label>
            <label><span>{{ t('新密码') }}</span><div class="password-input"><input v-model="password.newPassword" :type="revealPassword ? 'text' : 'password'" autocomplete="new-password" minlength="8" maxlength="72" required><button type="button" @click="revealPassword = !revealPassword">{{ t(revealPassword ? '隐藏' : '显示') }}</button></div></label>
            <div v-if="password.newPassword" class="password-strength"><i v-for="index in 4" :key="index" :class="{ on: index <= passwordScore }"></i><span>{{ t(passwordLabel) }}</span></div>
            <label><span>{{ t('确认新密码') }}</span><div class="password-input"><input v-model="password.confirmPassword" :type="revealPassword ? 'text' : 'password'" autocomplete="new-password" minlength="8" maxlength="72" required><button type="button" @click="revealPassword = !revealPassword">{{ t(revealPassword ? '隐藏' : '显示') }}</button></div></label>
            <div class="password-rules"><b>{{ t('密码要求') }}</b><span :class="{ met: password.newPassword.length >= 8 }">{{ t('至少 8 个字符') }}</span><span :class="{ met: /[A-Za-z]/.test(password.newPassword) }">{{ t('包含字母') }}</span><span :class="{ met: /\d/.test(password.newPassword) }">{{ t('包含数字') }}</span></div>
            <button class="btn primary" :disabled="passwordSaving">{{ t(passwordSaving ? '更新中…' : profile.passwordConfigured ? '更新密码' : '设置登录密码') }}</button>
          </form>
        </article>

        <article v-else-if="activeTab==='wechat'" class="profile-panel profile-wechat-panel"><WechatBinding ref="wechatEditor"/></article>
        <article v-else-if="activeTab==='wecom'" class="profile-panel profile-wecom-panel"><UserWecomWebhook ref="webhookEditor" @busy="webhookBusy=$event"/></article>
        <article v-else-if="activeTab==='wecom-app'" class="profile-panel profile-wecom-app-panel"><WecomAppBinding ref="wecomAppEditor"/></article>
        <article v-else class="profile-panel preferences-panel">
          <header><div><h2>{{ t('偏好设置') }}</h2><p>{{ t('调整界面语言、时间显示和协作消息接收方式。') }}</p></div><span class="profile-section-mark"><Icon name="preferences" :size="20" /></span></header>
          <form class="preferences-form" @submit.prevent="saveProfile('偏好设置已保存')">
            <FontSizePreference />
            <ThemePreference />
            <label><div><b>{{ t('界面语言') }}</b><p>{{ t('用于菜单、操作提示和系统消息。') }}</p></div><select v-model="form.locale"><option value="zh-CN">{{ t('简体中文') }}</option><option value="en-US">English</option></select></label>
            <label><div><b>{{ t('时区') }}</b><p>{{ t('影响活动记录、通知和计划时间的显示。') }}</p></div><select v-model="form.timezone"><option value="Asia/Shanghai">{{ t('中国标准时间（上海）') }}</option><option value="Asia/Hong_Kong">{{ t('香港时间') }}</option><option value="Asia/Singapore">{{ t('新加坡时间') }}</option><option value="UTC">{{ t('协调世界时（UTC）') }}</option></select></label>
            <label class="preference-toggle"><div><b>{{ t('邮件通知') }}</b><p>{{ t('邮件投递尚未接入；此处仅保存偏好。站内通知不受影响。') }}</p></div><input v-model="form.emailNotifications" type="checkbox" role="switch" :aria-label="t('邮件通知')"><span></span></label>
            <section class="membership-section"><div><b>{{ t('参与的项目') }}</b><p>{{ t('项目角色由企业或项目管理员配置。') }}</p></div><article v-for="membership in profile.memberships" :key="membership.projectId"><span>{{ membership.projectName.slice(0, 1) }}</span><div><b>{{ membership.projectName }}</b><small>{{ membership.projectCode }}</small></div><em>{{ profileProjectRoleNames(membership) }}</em></article></section>
            <footer><span>{{ t('偏好会跟随账号同步') }}</span><button class="btn primary" :disabled="saving">{{ t(saving ? '保存中…' : '保存偏好设置') }}</button></footer>
          </form>
        </article>
      </section>
    </div>
    <div v-else class="inline-notice" role="alert"><p>{{ message || t('个人信息加载失败') }}</p><button class="btn" @click="load">{{ t('重试') }}</button></div>
  </div>
</template>

<style scoped>
.profile-sidebar{position:relative;transition:width .16s ease,min-width .16s ease,padding .16s ease}.profile-sidebar>:deep(.sidebar-collapse-toggle){position:absolute;right:10px;top:10px;z-index:2}.profile-sidebar.is-collapsed{width:50px;min-width:50px;padding:12px}.profile-sidebar.is-collapsed>:deep(.sidebar-collapse-toggle){position:static;margin:auto}.profile-sidebar-content{display:contents}
/* 企微组件同时嵌入成员弹层和个人中心；外层卡片负责留白，组件内部负责表格横向滚动。 */
.profile-wecom-panel,.profile-wechat-panel,.profile-wecom-app-panel{padding:24px;min-width:0;max-width:100%;box-sizing:border-box}
@media(max-width:820px){
 .profile-wecom-panel,.profile-wechat-panel,.profile-wecom-app-panel{padding:16px}
 .profile-shell{display:flex;flex-direction:column;gap:16px;align-items:stretch}
 .profile-sidebar{position:static;width:100%;min-width:0}.profile-identity{display:grid;grid-template-columns:52px minmax(0,1fr);gap:3px 12px;padding:16px;text-align:left}
 .profile-sidebar.is-collapsed{width:100%;min-width:0;padding:9px}.profile-sidebar.is-collapsed>:deep(.sidebar-collapse-toggle){margin-left:auto}
 .profile-identity>.profile-avatar{grid-row:1/4;align-self:center;width:50px;height:50px;margin:0;border-radius:14px;font-size:22px}
 .profile-identity h2,.profile-identity p{margin:0;overflow-wrap:anywhere}.profile-identity .account-state{justify-self:start}
 .profile-sidebar nav{display:flex;max-width:100%;overflow:auto;gap:5px}.profile-sidebar nav button{flex:none;width:auto;min-height:40px;white-space:nowrap;grid-template-columns:18px 1fr}.profile-sidebar nav button>b{display:none}
 .profile-org{display:flex;flex-wrap:wrap;gap:6px 10px;padding:10px 16px;align-items:center}.profile-org b{margin:0}
 .profile-content,.profile-panel{width:100%;min-width:0}.profile-panel>header{padding:16px;gap:12px}.profile-panel>header p{line-height:1.7}.profile-section-mark{flex:none}
 .profile-form,.preferences-form,.password-form{padding:16px;width:100%;min-width:0}.profile-form-grid{grid-template-columns:minmax(0,1fr)}
 .avatar-editor{flex-wrap:wrap}.avatar-editor>div{min-width:0;flex:1 1 180px}.avatar-editor p{line-height:1.7}.color-picker{flex-wrap:wrap;gap:10px}
 .security-summary{margin:16px;grid-template-columns:38px minmax(0,1fr);gap:10px}.security-summary em{grid-column:2;justify-self:start}.security-summary p{overflow-wrap:anywhere;line-height:1.7}
 .preferences-form>label{grid-template-columns:minmax(0,1fr);gap:10px}.preferences-form>footer,.profile-form>footer{flex-wrap:wrap;gap:12px}.preferences-form footer>span,.profile-form footer>span{flex:1 1 100%}
 .membership-section article{grid-template-columns:32px minmax(0,1fr);gap:9px}.membership-section article>div{min-width:0}.membership-section article b{overflow-wrap:anywhere}.membership-section article>em{grid-column:2;justify-self:start}
 .password-input{min-width:0}.password-input input{width:0;min-width:0;flex:1}.password-input button{flex:none;min-height:40px}
}
</style>
