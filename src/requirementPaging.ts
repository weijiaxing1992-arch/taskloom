export const requirementExportLimit = 20_000
export type RequirementPage = { items: Array<Record<string, any>>; total: number; page: number; pageSize: number }

/** 服务端分页在完整筛选/排序之后执行；响应总数不能用当前页长度代替。 */
export function requirementPage(value: any, pageSize: number): RequirementPage {
  if (!value || !Array.isArray(value.items) || !Number.isSafeInteger(value.total) || value.total < 0 || !Number.isSafeInteger(value.page) || value.page < 1 || value.pageSize !== pageSize || value.items.length > pageSize || value.items.length > value.total) throw new Error('分页响应不完整，请刷新页面或确认服务版本')
  return value
}

/** 冻结筛选条件逐页取 CSV 数据；未完整读取或检测到并发变动时不下载半份文件。
 * 这不是跨多次请求的数据库快照：强一致大批量导出需后续服务端导出任务。
 */
export async function requirementExportPages(query: string, request: (path: string, signal: AbortSignal) => Promise<any>, signal: AbortSignal, progress: (done: number, total: number) => void = () => {}) {
  const snapshot = new URLSearchParams(query), size = 200
  const items: Array<Record<string, any>> = [], seen = new Set<number>()
  let total: number | null = null
  for (let page = 1; page <= Math.ceil(requirementExportLimit / size); page++) {
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    const params = new URLSearchParams(snapshot)
    params.set('page', String(page)); params.set('pageSize', String(size)); params.set('projection', 'list')
    const data = requirementPage(await request('/requirements?' + params, signal), size)
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    if (data.total > requirementExportLimit) throw new Error('最多导出 20000 条需求，请缩小筛选范围后分批导出')
    if (total !== null && total !== data.total || data.page !== page) throw new Error('导出期间数据发生变化，请重试；未下载不完整文件')
    total = data.total
    for (const item of data.items) {
      if (!Number.isSafeInteger(item.id) || item.id < 1 || seen.has(item.id)) throw new Error('导出期间数据发生变化，请重试；未下载不完整文件')
      seen.add(item.id); items.push(item)
    }
    progress(items.length, total)
    if (items.length === total) return items
    if (data.items.length !== size) throw new Error('导出期间数据发生变化，请重试；未下载不完整文件')
  }
  throw new Error('导出期间数据发生变化，请重试；未下载不完整文件')
}
