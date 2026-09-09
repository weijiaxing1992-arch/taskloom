import { ref } from 'vue'
import core from './locales/core.en'
import modules from './locales/modules.en'
import requirements from './locales/requirements.en'
import settings from './locales/settings.en'
import organization from './locales/organization.en'
import workload from './locales/workload.en'
import wecom from './locales/wecom.en'
import wechat from './locales/wechat.en'
import topSearch from './locales/topSearch.en'
import ai from './locales/ai.en'
import enhancements from './locales/enhancements.en'
import collaboration from './locales/collaboration.en'
import savedViews from './locales/savedViews.en'
import calendar from './locales/calendar.en'
import workloadTrends from './locales/workloadTrends.en'
import membersPicker from './locales/membersPicker.en'
import fieldDeletion from './locales/fieldDeletion.en'
import audit from './locales/audit.en'
import navigation from './locales/navigation.en'
import automation from './locales/automation.en'
import testingAI from './locales/testingAI.en'
import testing from './locales/testing.en'
import notifications from './locales/notifications.en'
import integrations from './locales/integrations.en'
import help from './locales/help.en'
import memberBulk from './locales/memberBulk.en'
import projectMembers from './locales/projectMembers.en'
import requirementExport from './locales/requirementExport.en'
import delivery from './locales/delivery.en'
import tapd from './locales/tapd.en'
import usability from './locales/usability.en'

export type Locale = 'zh-CN' | 'en-US'
export const supportedLocales = ['zh-CN', 'en-US'] as const
export function normalizeLocale(value: unknown): Locale {
  return typeof value === 'string' && /^en(?:-|$)/i.test(value) ? 'en-US' : 'zh-CN'
}
function initialLocale(): Locale {
  try { return normalizeLocale(localStorage.getItem('devflow-locale')) } catch { return 'zh-CN' }
}
export const locale = ref<Locale>(initialLocale())
export const timezone = ref('Asia/Shanghai')
export const englishMessages: Record<string, string> = { ...core, ...modules, ...requirements, ...settings, ...organization, ...workload, ...wecom, ...wechat, ...topSearch, ...ai, ...enhancements, ...collaboration, ...savedViews, ...calendar, ...workloadTrends, ...membersPicker, ...fieldDeletion, ...audit, ...navigation, ...automation, ...testingAI, ...testing, ...notifications, ...integrations, ...help, ...memberBulk, ...projectMembers, ...requirementExport, ...delivery, ...tapd, ...usability }

/** Translate system copy only. Business content must never pass through this function. */
export function t(source: string, params: Record<string, string | number> = {}): string {
  const message = locale.value === 'en-US' ? englishMessages[source] ?? source : source
  return message.replace(/\{(\w+)\}/g, (token, key: string) => Object.hasOwn(params, key) ? String(params[key]) : token)
}

export function setLocale(value: unknown): void {
  locale.value = normalizeLocale(value)
  try { localStorage.setItem('devflow-locale', locale.value) } catch { /* Storage may be disabled; the current session still works. */ }
  if (typeof document !== 'undefined') document.documentElement.lang = locale.value
}

export function applyLanguagePreferences(user: { locale?: string; timezone?: string }): void {
  if (user.locale) setLocale(user.locale)
  if (user.timezone) {
    try { new Intl.DateTimeFormat(locale.value, { timeZone: user.timezone }); timezone.value = user.timezone } catch { /* Keep the last valid zone. */ }
  }
}

export function formatDate(value: unknown, options?: Intl.DateTimeFormatOptions): string {
  if (!value) return '—'
  // Calendar dates represent a day, not a UTC instant that shifts when the zone changes.
  const dateOnly = typeof value === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(value)
  const date = value instanceof Date ? value : new Date(typeof value === 'number' ? value : dateOnly ? value + 'T12:00:00Z' : String(value))
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat(locale.value, {
    ...(options?.dateStyle || options?.timeStyle ? {} : {
      year: 'numeric', month: '2-digit', day: '2-digit',
      ...(dateOnly ? {} : { hour: '2-digit', minute: '2-digit' }),
    }),
    timeZone: dateOnly ? 'UTC' : timezone.value, ...options,
  }).format(date)
}

export function formatNumber(value: number, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(locale.value, options).format(value)
}

// This category is reserved by the server; all user-created names stay verbatim.
export function categoryLabel(name: string): string {
  return name === '未分类' ? t('未分类') : name
}

setLocale(locale.value)
