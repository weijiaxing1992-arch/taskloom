import type { JSONContent } from '@tiptap/core'
import { workflowColor, stateInfo } from '../requirementWorkflow'

export function hasContent(value: unknown): boolean {
  if (value == null) return false
  if (typeof value === 'string') return !!value.trim()
  if (typeof value === 'object') { const node = value as JSONContent; return !!node.text?.trim() || ['image','attachment','mention'].includes(node.type || '') || !!node.content?.some(hasContent) }
  return true
}
export function StatusBadge({ item }: { item: any }) {
  if (!item?.status) return null
  const color = workflowColor(item)
  return <span className="dfm-status" style={{ color, backgroundColor: `${color}10`, borderColor: `${color}30` }}>{stateInfo(item).name}</span>
}
export function ItemCard({ item, onOpen }: { item: any; onOpen: () => void }) {
  const type = item.objectType === 'defect' || item.type === '缺陷' ? '缺陷' : ['迭代','测试用例','测试执行'].includes(item.type) ? item.type : '需求'
  const code = type === '需求' ? String(item.id).padStart(6, '0') : item.code
  return <button type="button" className="dfm-item" onClick={onOpen}>
    <span className="dfm-item-labels"><span className={`dfm-object-type ${type === '缺陷' ? 'dfm-type-defect' : ''}`}>{type}</span><StatusBadge item={item}/>{code && <small>{code}</small>}</span>
    <strong>{item.title || '未命名工作项'}</strong>
    {hasContent(item.snippet) && <p>{item.snippet}</p>}
    {(item.projectName || item.assignee || item.sprint && item.sprint !== '待规划') && <span className="dfm-item-meta">{[item.projectName, item.assignee, item.sprint !== '待规划' ? item.sprint : ''].filter(Boolean).join(' · ')}</span>}
  </button>
}
export function LoadState({ loading, error, retry }: { loading: boolean; error: string; retry: () => void }) {
  return loading ? <div className="dfm-state" role="status"><span className="dfm-loader"/>正在加载…</div> : error ? <div className="dfm-state dfm-state-error" role="alert">{error}<button onClick={retry}>重试</button></div> : null
}
