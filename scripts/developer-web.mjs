#!/usr/bin/env node
// 源码开发包的业务 Web 入口。构建不依赖发布门户或任何下载制品。
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)))
export function developerHTML(html) {
  let count = 0
  const output = html.replace(/<script\s+type="module"[^>]*>[\s\S]*?<\/script>/g, script => {
    if (!script.includes("'/src/main.ts'")) return script
    count++
    return '<script type="module" src="/src/main.ts"></script>'
  })
  if (count !== 1) throw new Error('开发入口需要唯一的 /src/main.ts 引导脚本；请检查 index.html 后再构建。')
  return output
}
async function main() {
const mode = process.argv[2] || 'dev'
if (!['dev', 'build'].includes(mode) || process.argv.length > 3) {
  console.error('用法：node scripts/developer-web.mjs [dev|build]')
  process.exit(1)
}
const { createServer, build } = await import('vite')
// 保留项目插件与别名；开发包不加载本机 .env，也不复制门户发布缓存。
const config = {
  root, configFile: path.join(root, 'vite.config.ts'),
  publicDir: false, envFile: false,
  plugins: [{ name: 'devflow-source-developer-entry', transformIndexHtml: { order: 'pre', handler: developerHTML } }],
  server: { host: '127.0.0.1', port: 5173, strictPort: true },
  build: { outDir: 'dist-developer', emptyOutDir: false },
}
if (mode === 'build') {
  const check = spawnSync(process.execPath, [path.join(root, 'node_modules/vue-tsc/bin/vue-tsc.js'), '-b'], { cwd: root, stdio: 'inherit' })
  if (check.error || check.status !== 0) process.exit(check.status || 1)
  await build(config)
  console.log('开发版业务 Web 已生成：dist-developer；本包不包含门户或 Mac 安装下载。')
} else {
  const server = await createServer(config)
  await server.listen()
  console.log('业务 Web：http://127.0.0.1:5173/projects；API 由独立开发服务提供。')
  const close = async () => { await server.close(); process.exit(0) }
  process.once('SIGINT', close); process.once('SIGTERM', close)
}
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main()
