import { readdir, readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

// 此检查只读取代码与 Markdown，不连接服务、不读取配置/凭据、不修改业务数据。
// 注册入口覆盖不是 schema 或动态子路由行为验证；后者仍由接口测试承担。
export function collectAPIEntries(source) {
  const entries = new Map()
  const add = (path, kind) => {
    if (path === '/api/' || !path.startsWith('/api/')) return
    if (!entries.has(path)) entries.set(path, { path, kinds: new Set() })
    entries.get(path).kinds.add(kind)
  }
  // 忽略整行注释，避免把文档注释中的假想路径误当作已注册接口。
  const code = source.replace(/^\s*\/\/.*$/gm, '')
  for (const match of code.matchAll(/\.Handle(?:Func)?\(\s*"(\/api\/[^"\n]*)"/g)) add(match[1], 'mux')
  for (const match of code.matchAll(/r\.URL\.Path\s*===?\s*"(\/api\/[^"\n]*)"/g)) add(match[1], 'dispatch')
  for (const match of code.matchAll(/strings\.HasPrefix\(\s*r\.URL\.Path\s*,\s*"(\/api\/[^"\n]*)"/g)) add(match[1], 'prefix')
  return [...entries.values()].map(entry => ({ path: entry.path, kinds: [...entry.kinds] }))
}

export function documentedOperations(markdown) {
  const operations = new Set()
  // 只计入方法/路径表格，不把正文中“尚不存在 /api/...”的说明视为已覆盖。
  for (const line of markdown.split('\n')) {
    const match = line.match(/^\|\s*(GET|POST|PATCH|PUT|DELETE|HEAD|OPTIONS)([A-Z\s/]*)\|\s*`?(\/api\/[^`|\s]+)`?\s*\|/)
    if (!match) continue
    const methods = (match[1] + match[2]).match(/GET|POST|PATCH|PUT|DELETE|HEAD|OPTIONS/g)
    const path = match[3].split('?')[0]
    for (const method of methods) operations.add(`${method} ${path}`)
  }
  return [...operations]
}

export function missingEntries(entries, operations) {
  const paths = operations.map(value => value.slice(value.indexOf(' ') + 1))
  return entries.filter(entry => !paths.some(path => {
    if (entry.path.endsWith('/')) return path.startsWith(entry.path)
    // 某些安全中间件检查无尾斜杠的前缀，不代表存在同名集合 GET。
    if (!entry.kinds.includes('mux') && !entry.kinds.includes('dispatch') && entry.kinds.includes('prefix')) {
      return path === entry.path || path.startsWith(entry.path + '/')
    }
    return path === entry.path
  }))
}

export async function checkCoverage(root = resolve(dirname(fileURLToPath(import.meta.url)), '..')) {
  const directory = resolve(root, 'cmd/server')
  const sources = (await readdir(directory)).filter(name => name.endsWith('.go') && !name.endsWith('_test.go'))
  const entriesByPath = new Map()
  for (const name of sources) {
    for (const entry of collectAPIEntries(await readFile(resolve(directory, name), 'utf8'))) {
      const previous = entriesByPath.get(entry.path)
      entriesByPath.set(entry.path, {
        path: entry.path,
        kinds: [...new Set([...(previous?.kinds ?? []), ...entry.kinds])],
        files: [...new Set([...(previous?.files ?? []), name])],
      })
    }
  }
  const documents = ['docs/internal-api-reference.md', 'docs/api-reference.md']
  const markdown = (await Promise.all(documents.map(path => readFile(resolve(root, path), 'utf8')))).join('\n')
  const entries = [...entriesByPath.values()].sort((a, b) => a.path.localeCompare(b.path))
  const operations = documentedOperations(markdown)
  const missing = missingEntries(entries, operations)
  return { sourceFiles: sources.length, muxEntries: entries.filter(entry => entry.kinds.includes('mux')).length, explicitEntries: entries.length, documentedOperations: operations.length, missing }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const result = await checkCoverage()
    console.log(JSON.stringify(result, null, 2))
    if (result.missing.length) process.exitCode = 1
  } catch (error) {
    console.error(`API 文档入口覆盖检查失败：${error.message}`)
    process.exitCode = 1
  }
}
