// 日期/月是业务自然日字符串（YYYY-MM-DD / YYYY-MM），不是 UTC 时间戳。
// 使用固定宽度格式可安全比较先后；无效日期必须拒绝，不能让 Date 自动滚到下个月。
export type CalendarMode = 'date' | 'month'
const pad = (value: number) => String(value).padStart(2, '0')
export function isCalendarMonth(value: unknown): value is string {
  return typeof value === 'string' && /^[0-9]{4}-(0[1-9]|1[0-2])$/.test(value) && Number(value.slice(0, 4)) >= 1
}
export function calendarMonthDays(year: number, month: number): number {
  return month === 2 ? ((year % 4 === 0 && year % 100 !== 0) || year % 400 === 0 ? 29 : 28) : [4, 6, 9, 11].includes(month) ? 30 : 31
}
export function isCalendarDate(value: unknown): value is string {
  if (typeof value !== 'string' || !/^[0-9]{4}-[0-9]{2}-[0-9]{2}$/.test(value) || !isCalendarMonth(value.slice(0, 7))) return false
  const [year, month, day] = value.split('-').map(Number) as [number, number, number]
  return day >= 1 && day <= calendarMonthDays(year, month)
}
export function validCalendarValue(value: string, mode: CalendarMode = 'date', min = '', max = ''): boolean {
  const valid = mode === 'month' ? isCalendarMonth : isCalendarDate
  return valid(value) && (!valid(min) || value >= min) && (!valid(max) || value <= max)
}
function utcDate(value: string): Date {
  // 仅借 UTC 进行日历运算，避免夏令时/设备时区影响加减天数。
  // setUTCFullYear 同时避免 JS Date 构造器将 1–99 年自动解释成 1901–1999 年。
  const date = new Date(0)
  date.setUTCFullYear(Number(value.slice(0, 4)), Number(value.slice(5, 7)) - 1, Number(value.slice(8, 10)))
  date.setUTCHours(12, 0, 0, 0)
  return date
}
function dateString(date: Date): string {
  const year = date.getUTCFullYear()
  return year >= 1 && year <= 9999 ? `${String(year).padStart(4, '0')}-${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())}` : ''
}
export function addCalendarDays(value: string, days: number): string {
  if (!isCalendarDate(value) || !Number.isInteger(days)) return ''
  const date = utcDate(value); date.setUTCDate(date.getUTCDate() + days)
  return dateString(date)
}
export function shiftCalendarMonth(value: string, offset: number): string {
  // 跨月保留日期但限幅到目标月末（如 1 月 31 日到 2 月），越出 1–9999 年返回空。
  if ((!isCalendarDate(value) && !isCalendarMonth(value)) || !Number.isInteger(offset)) return ''
  const n = Number(value.slice(0, 4)) * 12 + Number(value.slice(5, 7)) - 1 + offset
  const year = Math.floor(n / 12), month = (n % 12 + 12) % 12 + 1
  if (year < 1 || year > 9999) return ''
  const result = `${String(year).padStart(4, '0')}-${pad(month)}`
  return value.length === 7 ? result : `${result}-${pad(Math.min(Number(value.slice(8, 10)), calendarMonthDays(year, month)))}`
}
export function calendarWeekday(value: string): number { return isCalendarDate(value) ? (utcDate(value).getUTCDay() + 6) % 7 : 0 }
export function calendarToday(now = new Date(), timeZone = 'Asia/Shanghai'): string {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(now)
  const part = (key: string) => parts.find(item => item.type === key)?.value || ''
  return `${part('year').padStart(4, '0')}-${part('month')}-${part('day')}`
}
/** 当前周按周一至周日划分：周六/日仍返回本周周五，不擅自跳到下一周。 */
export function currentWeekFriday(today = calendarToday()): string { return addCalendarDays(today, 4 - calendarWeekday(today)) }
export function calendarGrid(month: string): string[] {
  if (!isCalendarMonth(month)) return []
  const first = month + '-01', offset = calendarWeekday(first)
  return Array.from({ length: 42 }, (_, index) => addCalendarDays(first, index - offset))
}
export function calendarKeyboardDate(value: string, key: string, mode: CalendarMode = 'date', shift = false): string | null {
  // 纯计算键盘目标；是否符合组件 min/max 由 DatePicker 再检查，不在此提交表单值。
  if (mode === 'month') {
    if (!isCalendarMonth(value)) return null
    const delta: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -3, ArrowDown: 3, PageUp: -12, PageDown: 12 }
    if (key === 'Home') return value.slice(0, 4) + '-01'
    if (key === 'End') return value.slice(0, 4) + '-12'
    return delta[key] === undefined ? null : shiftCalendarMonth(value, delta[key])
  }
  if (!isCalendarDate(value)) return null
  const delta: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }
  if (delta[key] !== undefined) return addCalendarDays(value, delta[key])
  if (key === 'Home') return addCalendarDays(value, -calendarWeekday(value))
  if (key === 'End') return addCalendarDays(value, 6 - calendarWeekday(value))
  if (key === 'PageUp' || key === 'PageDown') return shiftCalendarMonth(value, (key === 'PageUp' ? -1 : 1) * (shift ? 12 : 1))
  return null
}
export function defaultSprintDates(today = calendarToday()) {
  const startDate = currentWeekFriday(today)
  return { startDate, endDate: addCalendarDays(startDate, 6), name: startDate.slice(5, 7) + startDate.slice(8, 10) + '迭代' }
}
export function linkedSprintDates(startDate: string, previous: { startDate: string; endDate: string; name: string }, customized: { name: boolean; endDate: boolean }) {
  // 自动联动仅更新未手动改过的名称/结束日期；用户自定义值优先，不能因改开始日丢失。
  if (!isCalendarDate(startDate)) return { ...previous, startDate }
  return { startDate, name: customized.name ? previous.name : startDate.slice(5, 7) + startDate.slice(8, 10) + '迭代', endDate: customized.endDate ? previous.endDate : addCalendarDays(startDate, 6) }
}
