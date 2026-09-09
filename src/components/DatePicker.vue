<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useAttrs, useId, watch } from 'vue'
import { t, locale } from '../i18n'
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from './ui/popover'
import { calendarGrid, calendarKeyboardDate, calendarMonthDays, calendarToday, calendarWeekday, currentWeekFriday, isCalendarDate, isCalendarMonth, shiftCalendarMonth, validCalendarValue, type CalendarMode } from '../calendarDates'
import { calendarHoliday, hasHolidayCalendar, holidaySources } from '../calendarHolidays'

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{ modelValue?: string | null; mode?: CalendarMode; id?: string; inputId?: string; label?: string; disabled?: boolean; min?: string; max?: string; required?: boolean; placeholder?: string; error?: string }>(), { modelValue: '', mode: 'date', disabled: false, min: '', max: '', required: false, error: '' })
const emit = defineEmits<{ (event: 'update:modelValue', value: string): void; (event: 'change', value: Event): void; (event: 'validity-change', valid: boolean): void; (event: 'input', value: Event): void }>()
const attrs = useAttrs(), uid = useId(), input = ref<HTMLInputElement | null>(null), grid = ref<HTMLElement | null>(null)
const open = ref(false), raw = ref(props.modelValue || ''), touched = ref(false)
const today = ref(calendarToday()), viewMonth = ref(today.value.slice(0, 7)), focused = ref(today.value)
const identifier = computed(() => props.inputId || props.id || 'date-picker-' + uid)
const inputLabel = computed(() => props.label || String(attrs['aria-label'] || t(props.mode === 'month' ? '选择月份' : '选择日期')))
const inputAttrs = computed(() => Object.fromEntries(Object.entries(attrs).filter(([key]) => !['class', 'style', 'aria-label', 'aria-invalid', 'aria-describedby'].includes(key))))
const validFormat = (value: string) => props.mode === 'month' ? isCalendarMonth(value) : isCalendarDate(value)
const allowed = (value: string) => validCalendarValue(value, props.mode, props.min, props.max)
const issue = computed(() => {
  if (props.disabled) return ''
  if (props.error) return props.error
  const value = raw.value
  if (!value) return props.required ? t(props.mode === 'month' ? '请选择月份' : '请选择日期') : ''
  if (!validFormat(value)) return t(props.mode === 'month' ? '请输入 YYYY-MM 格式的有效月份' : '请输入 YYYY-MM-DD 格式的有效日期')
  if (validFormat(props.min) && value < props.min) return t(props.mode === 'month' ? '月份不能早于 {date}' : '日期不能早于 {date}', { date: props.min })
  if (validFormat(props.max) && value > props.max) return t(props.mode === 'month' ? '月份不能晚于 {date}' : '日期不能晚于 {date}', { date: props.max })
  return ''
})
const errorID = computed(() => identifier.value + '-error')
const describedBy = computed(() => [attrs['aria-describedby'], (touched.value || props.error) && issue.value ? errorID.value : ''].filter(Boolean).join(' ') || undefined)
const heading = computed(() => props.mode === 'month' ? viewMonth.value.slice(0, 4) : new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'long', timeZone: 'UTC' }).format(new Date(viewMonth.value + '-01T12:00:00Z')))
const days = computed(() => calendarGrid(viewMonth.value))
const weeks = computed(() => Array.from({ length: 6 }, (_, index) => days.value.slice(index * 7, index * 7 + 7)))
const monthRows = computed(() => Array.from({ length: 4 }, (_, index) => months.value.slice(index * 3, index * 3 + 3)))
const weekdays = ['周一', '周二', '周三', '周四', '周五', '周六', '周日']
const months = computed(() => Array.from({ length: 12 }, (_, index) => viewMonth.value.slice(0, 4) + '-' + String(index + 1).padStart(2, '0')))
const friday = computed(() => currentWeekFriday(today.value))
const year = computed(() => Number(viewMonth.value.slice(0, 4)))
function monthLabel(value: string) { return new Intl.DateTimeFormat(locale.value, { month: 'short', timeZone: 'UTC' }).format(new Date(value + '-01T12:00:00Z')) }
function dayLabel(value: string) {
  if (!value) return ''
  const info = calendarHoliday(value)
  return value + ' ' + t(weekdays[calendarWeekday(value)]!) + (info ? ' · ' + t(info.name) + ' · ' + t(info.kind === 'holiday' ? '放假' : '调休上班') : '')
}
function blocked() { return props.disabled || !!input.value?.closest('[inert]') }
function clamp(value: string) {
  if (validFormat(props.min) && value < props.min) return props.min
  if (validFormat(props.max) && value > props.max) return props.max
  return value
}
function nativeEvent(kind: string) {
  const event = new Event(kind, { bubbles: true })
  Object.defineProperties(event, { target: { value: input.value }, currentTarget: { value: input.value } })
  return event
}
function syncValidity() { input.value?.setCustomValidity(issue.value); emit('validity-change', !issue.value) }
function commit(value: string, close = true) {
  if (blocked() || props.error || (value === '' ? props.required : !allowed(value))) return
  raw.value = value; touched.value = true
  if (input.value) input.value.value = value
  syncValidity()
  if (value !== (props.modelValue || '')) { emit('update:modelValue', value); emit('change', nativeEvent('change')) }
  if (close) open.value = false
}
function typed(event: Event) {
  if (blocked()) return
  raw.value = (event.target as HTMLInputElement).value; touched.value = true
  syncValidity(); emit('input', event)
  if (!issue.value) commit(raw.value, false)
}
function validate() { touched.value = true; syncValidity(); return !issue.value }
function restoreFocus(event: Event) { event.preventDefault(); if (!blocked()) input.value?.focus() }
async function focusCell() { await nextTick(); grid.value?.querySelector<HTMLElement>(`[data-calendar-value="${focused.value}"]`)?.focus() }
function prepareOpen(value: boolean) {
  if (!value) { open.value = false; return }
  if (blocked()) return
  today.value = calendarToday()
  const fallback = props.mode === 'month' ? today.value.slice(0, 7) : today.value
  focused.value = clamp(allowed(raw.value) ? raw.value : fallback)
  viewMonth.value = focused.value.slice(0, 7)
  open.value = true
  void focusCell()
}
function moveView(direction: number) {
  const next = shiftCalendarMonth(viewMonth.value, direction * (props.mode === 'month' ? 12 : 1))
  if (!next || blocked()) return
  viewMonth.value = next
  const candidate = props.mode === 'month' ? next : next + '-01'
  focused.value = clamp(candidate)
}
function canMove(direction: number) {
  const next = shiftCalendarMonth(viewMonth.value, direction * (props.mode === 'month' ? 12 : 1))
  if (!next) return false
  const start = props.mode === 'month' ? next.slice(0, 4) + '-01' : next + '-01'
  const end = props.mode === 'month' ? next.slice(0, 4) + '-12' : next + '-' + calendarMonthDays(Number(next.slice(0, 4)), Number(next.slice(5, 7)))
  return (!validFormat(props.min) || end >= props.min) && (!validFormat(props.max) || start <= props.max)
}
function gridKey(event: KeyboardEvent) {
  if (blocked()) return
  if (event.key === 'Escape') { event.preventDefault(); event.stopImmediatePropagation(); open.value = false; return }
  if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); commit(focused.value); return }
  const next = calendarKeyboardDate(focused.value, event.key, props.mode, event.shiftKey)
  if (next === null) return
  event.preventDefault(); event.stopPropagation()
  if (!next) return
  focused.value = clamp(next); viewMonth.value = focused.value.slice(0, 7)
  void focusCell()
}
function inputKey(event: KeyboardEvent) {
  if (event.key === 'ArrowDown') { event.preventDefault(); prepareOpen(true) }
  else if (event.key === 'Escape' && open.value) { event.preventDefault(); event.stopImmediatePropagation(); open.value = false }
  else if (event.key === 'Enter' && !validate()) event.preventDefault()
}
watch(() => props.modelValue, value => { raw.value = value || ''; touched.value = false; syncValidity() })
watch([issue, input], syncValidity, { immediate: true, flush: 'post' })
watch(() => props.disabled, value => { if (value) open.value = false })
watch(() => props.mode, () => { open.value = false; raw.value = props.modelValue || '' })
onBeforeUnmount(() => { emit('validity-change', true) })
defineExpose({ validate, reportValidity: () => { validate(); return input.value?.reportValidity() ?? !issue.value }, focus: () => input.value?.focus() })
</script>

<template>
  <div :class="['date-picker', attrs.class, { 'date-picker-invalid': (touched || error) && issue }]" :style="attrs.style as any">
    <Popover :open="open" @update:open="prepareOpen">
      <!-- 整个日期输入区都是触发点，避免用户只能点到很小的日历图标。 -->
      <PopoverAnchor as-child><div class="date-picker-field" @click="prepareOpen(true)">
        <input ref="input" v-bind="inputAttrs" :id="identifier" type="text" :value="raw" :placeholder="placeholder || (mode === 'month' ? 'YYYY-MM' : 'YYYY-MM-DD')" :required="required" :disabled="disabled" :aria-label="inputLabel" :aria-invalid="issue ? 'true' : undefined" :aria-describedby="describedBy" :aria-required="required" autocomplete="off" inputmode="text" @input="typed" @blur="validate" @keydown="inputKey">
        <PopoverTrigger as-child><button type="button" class="date-picker-toggle" :disabled="disabled" :aria-label="t('打开日历') + '：' + inputLabel"><svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><rect x="3" y="5" width="18" height="16" rx="3"/><path d="M7 3v4m10-4v4M3 11h18M7 15h3m4 0h3"/></svg></button></PopoverTrigger>
      </div></PopoverAnchor>
      <PopoverContent v-if="open && !disabled" class="calendar-popover" align="start" :side-offset="6" :collision-padding="12" :aria-label="inputLabel" @open-auto-focus.prevent="focusCell" @close-auto-focus="restoreFocus" @escape-key-down="$event.stopImmediatePropagation()">
        <header class="calendar-header"><button type="button" :disabled="!canMove(-1)" :aria-label="t(mode === 'month' ? '上一年' : '上个月')" @click="moveView(-1)">‹</button><b aria-live="polite">{{ heading }}</b><button type="button" :disabled="!canMove(1)" :aria-label="t(mode === 'month' ? '下一年' : '下个月')" @click="moveView(1)">›</button></header>
        <div v-if="mode === 'date'" class="calendar-weekdays" aria-hidden="true"><span v-for="day in weekdays" :key="day">{{ t(day) }}</span></div>
        <div ref="grid" role="grid" :aria-label="heading" :class="['calendar-grid', { 'calendar-month-grid': mode === 'month' }]" @keydown="gridKey">
          <template v-if="mode === 'date'"><div v-for="(week,row) in weeks" :key="row" role="row" class="calendar-grid-row"><button v-for="(day,index) in week" :key="day || index" type="button" role="gridcell" :data-calendar-value="day" :disabled="!allowed(day)" :tabindex="day === focused ? 0 : -1" :aria-label="dayLabel(day)" :aria-selected="day === modelValue" :aria-current="day === today ? 'date' : undefined" :title="dayLabel(day)" :class="{ selected: day === modelValue, today: day === today, outside: day.slice(0,7) !== viewMonth, weekend: calendarWeekday(day) >= 5, holiday: calendarHoliday(day)?.kind === 'holiday', workday: calendarHoliday(day)?.kind === 'workday' }" @focus="focused = day" @click="commit(day)"><span>{{ day ? Number(day.slice(8,10)) : '' }}</span><em v-if="calendarHoliday(day)">{{ t(calendarHoliday(day)?.kind === 'holiday' ? '休' : '班') }}</em></button></div></template>
          <template v-else><div v-for="(row,index) in monthRows" :key="index" role="row" class="calendar-grid-row"><button v-for="month in row" :key="month" type="button" role="gridcell" :data-calendar-value="month" :disabled="!allowed(month)" :tabindex="month === focused ? 0 : -1" :aria-label="month" :aria-selected="month === modelValue" :class="{ selected: month === modelValue, today: month === today.slice(0,7) }" @focus="focused = month" @click="commit(month)">{{ monthLabel(month) }}</button></div></template>
        </div>
        <div v-if="mode === 'date'" class="calendar-legend"><span><i class="holiday">{{ t('休') }}</i>{{ t('放假') }}</span><span><i class="workday">{{ t('班') }}</i>{{ t('调休上班') }}</span><a v-if="hasHolidayCalendar(year)" :href="holidaySources[year]" target="_blank" rel="noopener noreferrer" :title="t('中国大陆官方放假与调休')">{{ t('官方节假日来源') }}</a></div>
        <p v-if="mode === 'date' && !hasHolidayCalendar(year)" class="calendar-unverified" role="note">{{ t('该年份尚未配置官方调休数据，日期仍可选择') }}</p>
        <footer class="calendar-shortcuts"><button type="button" :disabled="!allowed(mode === 'month' ? today.slice(0,7) : today)" @click="commit(mode === 'month' ? today.slice(0,7) : today)">{{ t(mode === 'month' ? '本月' : '今天') }}</button><button v-if="mode === 'date'" type="button" :disabled="!allowed(friday)" @click="commit(friday)">{{ t('本周五') }}</button><button v-if="!required" type="button" class="calendar-clear" @click="commit('')">{{ t(mode === 'month' ? '清空月份' : '清空日期') }}</button></footer>
      </PopoverContent>
    </Popover>
    <small v-if="(touched || error) && issue" :id="errorID" class="date-picker-error" role="alert">{{ issue }}</small>
  </div>
</template>

<style scoped>
.date-picker{min-width:0;width:100%;color:var(--ink,#25304a)}.date-picker-field{display:flex;align-items:center;position:relative;min-width:0}.date-picker-field>input{width:100%;min-width:0;box-sizing:border-box;padding-right:38px;font:inherit;font-size:13px;border:1px solid var(--line,#d6dce7);border-radius:7px;background:var(--surface,#fff);color:inherit;min-height:36px}.date-picker-field>input:focus{outline:2px solid var(--primary,#6374ea);outline-offset:1px}.date-picker-invalid .date-picker-field>input{border-color:#d74b4b}.date-picker-toggle{position:absolute;right:3px;top:3px;bottom:3px;display:grid;place-items:center;width:32px;border:0;border-radius:5px;background:transparent;color:var(--muted,#778298);cursor:pointer}.date-picker-toggle:hover{background:var(--surface-subtle,#eef1f7)}.date-picker-toggle:disabled{opacity:.45;cursor:not-allowed}.date-picker-error{display:block;color:var(--ui-destructive,#c33838);font-size:11px;line-height:1.4;margin-top:4px}.calendar-popover{width:306px;max-width:calc(100vw - 24px);padding:12px;border-radius:12px;border:1px solid var(--line,#dfe4ec);background:var(--surface-raised,#fff);color:var(--ink,#26314a);box-shadow:0 12px 35px #101b352b}.calendar-header{display:flex;align-items:center;justify-content:space-between;margin-bottom:10px}.calendar-header>b{font-size:14px;font-weight:650}.calendar-header >button{border:0;background:transparent;color:inherit;width:32px;height:32px;border-radius:6px;font-size:24px;line-height:1;cursor:pointer}.calendar-header>button:hover,.calendar-shortcuts>button:hover{background:var(--surface-subtle,#edf0f8)}.calendar-grid{display:flex;flex-direction:column;gap:3px}.calendar-weekdays,.calendar-grid-row{display:grid;grid-template-columns:repeat(7,minmax(0,1fr));gap:3px}.calendar-weekdays{margin-bottom:5px;text-align:center;font-size:11px;color:var(--muted,#8690a4)}.calendar-weekdays>span{padding:5px 0}.calendar-grid-row>button{position:relative;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:0;border:1px solid transparent;background:transparent;color:inherit;height:38px;min-width:0;border-radius:7px;font:inherit;font-size:12px;cursor:pointer}.calendar-grid-row>button:hover:not(:disabled){background:var(--surface-subtle,#edf0f8)}.calendar-grid-row>button:focus-visible,.calendar-shortcuts>button:focus-visible,.calendar-header>button:focus-visible,.date-picker-toggle:focus-visible{outline:2px solid var(--primary,#6374ea);outline-offset:1px}.calendar-grid-row>button.outside{opacity:.42}.calendar-grid-row>button.weekend{color:var(--muted,#8690a4)}.calendar-grid-row>button.today{border-color:var(--primary,#6776e9)}.calendar-grid-row>button.holiday{color:var(--ui-destructive,#d64049)}.calendar-grid-row>button.workday{color:var(--primary,#246dc3)}.calendar-grid-row>button.selected{background:var(--control-selected,#5968dc)!important;color:var(--control-selected-text,#fff)!important}.calendar-grid-row>button:disabled,.calendar-header>button:disabled,.calendar-shortcuts>button:disabled{opacity:.3;cursor:not-allowed}.calendar-grid-row>button>em{font-style:normal;font-size:8px;line-height:10px}.calendar-month-grid .calendar-grid-row{grid-template-columns:repeat(3,minmax(0,1fr));gap:6px}.calendar-month-grid .calendar-grid-row>button{height:45px}.calendar-legend{display:flex;align-items:center;flex-wrap:wrap;gap:8px;font-size:10px;color:var(--muted,#7d889d);margin-top:10px}.calendar-legend>span{display:flex;align-items:center;gap:4px}.calendar-legend i{font-style:normal;font-size:9px;padding:1px 3px;border-radius:3px}.calendar-legend i.holiday{color:var(--ui-destructive,#d64049);background:#d6404912}.calendar-legend i.workday{color:var(--primary,#246dc3);background:#246dc312}.calendar-legend>a{margin-left:auto;color:inherit;text-decoration:none;font-size:9px}.calendar-legend>a:hover{text-decoration:underline}.calendar-unverified{font-size:10px;color:var(--muted,#7d889d);line-height:1.5;margin:8px 0}.calendar-shortcuts{display:flex;align-items:center;gap:5px;border-top:1px solid var(--line,#e9edf3);margin-top:10px;padding-top:10px}.calendar-shortcuts>button{border:0;background:transparent;color:var(--primary,#5263d3);padding:6px 8px;font:inherit;font-size:11px;border-radius:5px;cursor:pointer}.calendar-shortcuts .calendar-clear{margin-left:auto;color:var(--muted,#7d889d)}@media(max-width:640px){.date-picker-field>input{font-size:16px;min-height:44px}.date-picker-toggle{width:38px}.calendar-popover{width:326px}.calendar-grid-row>button{height:40px}.calendar-header>button{width:40px;height:40px}.calendar-shortcuts>button{min-height:40px}.calendar-month-grid .calendar-grid-row>button{height:48px}}
</style>
