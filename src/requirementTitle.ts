/** Only human-readable description text is eligible; embedded media stays local. */
export function titleDescriptionError(description: unknown, document: unknown, maxBytes: number): string {
  if (typeof description !== 'string' || !description.trim()) return '请先填写可用于总结的需求描述，或手动填写标题'
  if (document && typeof document === 'object') {
    const nodes: any[] = [document]
    let text = false
    while (nodes.length) {
      const node = nodes.pop()
      if (node?.type === 'text' && typeof node.text === 'string' && node.text.trim()) { text = true; break }
      if (Array.isArray(node?.content)) nodes.push(...node.content)
    }
    if (!text) return '仅图片、附件或人员提及不能生成标题，请补充文字描述'
  }
  if (/\bdata:[^\s<>"']*,/i.test(description)) return '描述包含嵌入资源数据，请移除后再生成标题'
  if (new TextEncoder().encode(description).length > maxBytes) return '需求描述超出 AI 输入上限，请精简描述或手动填写标题'
  return ''
}

export function requirementTitleError(title: unknown, maxLength = 80): string {
  if (typeof title !== 'string' || !title.trim()) return '请输入需求标题'
  const text = title.trim(), length = Array.from(text).length
  if (/[\r\n\u2028\u2029]/.test(title) || length < 2 || length > maxLength) return '需求标题应为 2–80 个字符的单行文本，请修改后保存'
  return ''
}
