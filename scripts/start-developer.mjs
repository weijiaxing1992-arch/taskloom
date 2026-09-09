#!/usr/bin/env node
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import net from 'node:net'
import http from 'node:http'
import crypto from 'node:crypto'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)))
const smoke = process.argv.includes('--smoke-test')
if (process.argv.slice(2).some(arg => arg !== '--smoke-test')) throw new Error('用法：node scripts/start-developer.mjs [--smoke-test]；此入口只创建新的隔离开发库')
const go = process.env.DEVFLOW_GO_BINARY || 'go'
const environment = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('DEVFLOW_')))
// 固定回环请求不使用系统/企业代理，也不跟随重定向；随机凭据仅交给本机服务。
function localRequest(route, body) {
  return new Promise((resolve, reject) => {
    const data = body ? JSON.stringify(body) : ''
    const request = http.request({ hostname: '127.0.0.1', port: 8080, path: route, method: body ? 'POST' : 'GET', agent: false,
      headers: body ? { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(data) } : {} }, response => {
      response.resume()
      response.once('end', () => resolve({ status: response.statusCode, ok: response.statusCode >= 200 && response.statusCode < 300 }))
      response.once('error', reject)
    })
    request.setTimeout(body ? 10000 : 1000, () => request.destroy(new Error('local request timed out')))
    request.once('error', reject); request.end(data)
  })
}
// 拒绝占用中的端口；不停止或连接用户已有服务。
await new Promise((resolve, reject) => {
  const probe = net.createServer()
  probe.once('error', () => reject(new Error('127.0.0.1:8080 已被占用；请先处理端口冲突，此脚本不会停止已有服务。')))
  probe.listen(8080, '127.0.0.1', () => probe.close(resolve))
})
process.umask(0o077)
const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'devflow-developer-'))
const binary = path.join(directory, process.platform === 'win32' ? 'server.exe' : 'server')
let child, stopping = false
for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => { stopping = true; child?.kill('SIGTERM') })
async function build() {
  await new Promise((resolve, reject) => {
    child = spawn(go, ['build', '-o', binary, './cmd/server'], { cwd: root, env: environment, stdio: 'inherit' })
    child.once('error', () => reject(new Error('无法运行 Go；请先完成开发环境检查与依赖安装。')))
    child.once('close', code => code === 0 && !stopping ? resolve() : reject(new Error('开发服务构建失败或已取消。')))
  })
}
await build()
let password = crypto.randomBytes(32).toString('base64url')
const env = {
  ...environment, DEVFLOW_ADDR: '127.0.0.1:8080', DEVFLOW_DB: path.join(directory, 'devflow.db'),
  DEVFLOW_WEB_DIR: path.join(root, 'dist-developer'), DEVFLOW_WECOM_MODE: 'mock',
  DEVFLOW_WECOM_KEY_FILE: path.join(directory, 'wecom.key'), DEVFLOW_SESSION_SECRET: crypto.randomBytes(48).toString('base64url'),
  DEVFLOW_COOKIE_SECURE: 'false', DEVFLOW_DEVELOPER_MODE: 'isolated', DEVFLOW_DEVELOPER_PASSWORD: password,
}
child = spawn(binary, [], { cwd: root, env, stdio: ['ignore', 'inherit', 'inherit'] })
delete env.DEVFLOW_DEVELOPER_PASSWORD
delete env.DEVFLOW_SESSION_SECRET
const completion = new Promise((resolve, reject) => {
  child.once('error', () => reject(new Error('开发服务未能启动。')))
  child.once('close', code => resolve(code))
})
try {
  let ready = false, lastFailure = ''
  // 首次 SQLite 迁移与逐账号 bcrypt 可能较慢；先等健康状态，再验证凭据。
  for (let attempt = 0; attempt < 360; attempt++) {
    if (stopping || child.exitCode !== null) break
    try {
      const response = await localRequest('/api/health')
      if (response.ok) { ready = true; break }
      lastFailure = `HTTP ${response.status}`
    } catch (error) { lastFailure = error.cause?.code || error.name || 'request failed' }
    if (attempt === 20) console.log(`等待首次迁移与本机健康检查（${lastFailure}）…`)
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  if (!ready) throw new Error(`隔离服务未能在 3 分钟内完成初始化（${lastFailure}）；开发进程将停止。`)
  const login = await localRequest('/api/auth/login', { email: 'linxia@devflow.local', password })
  if (!login.ok) throw new Error(`隔离服务随机凭据登录验证失败（HTTP ${login.status}）；开发进程将停止。`)
  console.log(`隔离开发数据目录：${directory}`)
  if (smoke) {
    console.log('隔离开发服务构建、随机凭据登录检查通过；未输出密码。')
    child.kill('SIGTERM')
  } else {
    console.log('API：http://127.0.0.1:8080；Web：另开终端运行 pnpm dev，访问 /projects。')
    console.log('Mac 客户端服务地址：http://127.0.0.1:8080')
    console.log('开发管理员：linxia@devflow.local')
    console.log(`本次随机密码（仅此本机隔离库）：${password}`)
    console.log('按 Ctrl+C 停止。每次启动会创建新库和新密码；旧目录保留供自行清理，不会复用。')
  }
  password = ''
  const code = await completion
  if (!smoke && !stopping && code !== 0) process.exitCode = code || 1
} catch (error) {
  password = ''; child.kill('SIGTERM'); await completion
  throw error
}
