import { addCalendarDays, isCalendarDate } from './calendarDates'
export type CalendarHoliday = { kind: 'holiday' | 'workday'; name: string }
// 已维护年份与核验来源一起列出；扩展年份时应先核对当年官方放假及调休通知，
// 同步补日期测试。不能把普通周末推断成法定假期，也不能自动沿用去年的调休日期。
export const holidaySources: Record<number, string> = {
  2025: 'https://www.gov.cn/zhengce/zhengceku/202411/content_6986383.htm',
  2026: 'https://www.beijing.gov.cn/cs/gncs/zcwj/202603/t20260327_4568275.html',
}
// 国办发明电〔2024〕12号、〔2025〕7号；这里只标记目录里明确维护的年份。
const holidays: Record<string, CalendarHoliday> = {}
for (const [start, end, name] of [
  ['2025-01-01', '2025-01-01', '元旦'], ['2025-01-28', '2025-02-04', '春节'],
  ['2025-04-04', '2025-04-06', '清明节'], ['2025-05-01', '2025-05-05', '劳动节'],
  ['2025-05-31', '2025-06-02', '端午节'], ['2025-10-01', '2025-10-08', '国庆节 / 中秋节'],
  ['2026-01-01', '2026-01-03', '元旦'], ['2026-02-15', '2026-02-23', '春节'],
  ['2026-04-04', '2026-04-06', '清明节'], ['2026-05-01', '2026-05-05', '劳动节'],
  ['2026-06-19', '2026-06-21', '端午节'], ['2026-09-25', '2026-09-27', '中秋节'],
  ['2026-10-01', '2026-10-07', '国庆节'],
] as [string, string, string][]) {
  for (let day = start; day && day <= end; day = addCalendarDays(day, 1)) holidays[day] = { kind: 'holiday', name }
}
for (const [day, name] of [['2026-01-04', '元旦'], ['2026-02-14', '春节'], ['2026-02-28', '春节'], ['2026-05-09', '劳动节'], ['2026-09-20', '国庆节'], ['2026-10-10', '国庆节']] as [string, string][]) holidays[day] = { kind: 'workday', name }
for (const [day, name] of [['2025-01-26', '春节'], ['2025-02-08', '春节'], ['2025-04-27', '劳动节'], ['2025-09-28', '国庆节 / 中秋节'], ['2025-10-11', '国庆节 / 中秋节']] as [string, string][]) holidays[day] = { kind: 'workday', name }
export function calendarHoliday(day: string): CalendarHoliday | null { return isCalendarDate(day) ? holidays[day] || null : null }
// null 既可能是普通日期，也可能是不支持的年份；界面须结合此函数提示“年份未维护”，
// 不能把未知年份展示成已核实“无节假日”。节假日标注不限制用户选择业务日期。
export function hasHolidayCalendar(year: number): boolean { return Object.hasOwn(holidaySources, year) }
