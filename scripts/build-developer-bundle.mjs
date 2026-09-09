#!/usr/bin/env node
import fs from 'node:fs'
import path from 'node:path'
import os from 'node:os'
import crypto from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

export const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)))
export const documents = ['organization-initialization.md', 'developer-environment.md', 'product-handbook.md', 'api-reference.md', 'internal-api-reference.md', 'ai-collaboration.md', 'maintenance-zh.md', 'watermark-service.md', 'site-audit-20260906.md', 'openapi.json', 'delivery-acceptance.md', 'delivery-capacity.md', 'delivery-functions.md', 'delivery-deployment.md', 'delivery-topology.md', 'delivery-topology.svg']
const exact = new Set([
  '.env.example', '.gitignore', 'package.json', 'pnpm-lock.yaml', 'pnpm-workspace.yaml',
  'go.mod', 'go.sum', 'index.html', 'components.json', 'tsconfig.json', 'tsconfig.app.json', 'vite.config.ts',
  ...documents.map(name => `docs/${name}`),
  'cmd/server/assets/NotoSansSC-Regular.ttf', 'cmd/server/assets/OFL.txt',
  'clients/devflow_macos/pubspec.yaml', 'clients/devflow_macos/pubspec.lock', 'clients/devflow_macos/analysis_options.yaml',
  'scripts/build-developer-bundle.mjs', 'scripts/test-developer-bundle.mjs',
  'scripts/check-developer-environment.mjs', 'scripts/developer-web.mjs', 'scripts/start-developer.mjs',
  'scripts/theme-palette.mjs', 'scripts/ui-typography.mjs', 'scripts/run-tests.mjs', 'scripts/export-openapi.mjs',
])
const prohibited = /(?:^|\/)(?:\.git|\.dart_tool|\.idea|\.vscode|node_modules|work|data|build|dist|dist-developer|coverage|Pods|ephemeral|xcuserdata|acceptance|release|secrets?|credentials?)(?:\/|$)|(?:^|\/)\.env(?:\.|$)|(?:^|\/)(?:\.npmrc|\.netrc|\.DS_Store|id_rsa|id_ed25519|flutter_export_environment\.sh|Flutter-Generated\.xcconfig)$|\.(?:db(?:-wal|-shm)?|sqlite\d?(?:-wal|-shm)?|pem|key|p12|pfx|mobileprovision|cer|crt|dmg|app|log|zip|tar|gz|tsbuildinfo)$/i
export function allowed(relative) {
  if (!relative || relative.startsWith('/') || relative.includes('\\') || relative.split('/').some(part => part === '.' || part === '..')) return false
  if (relative !== '.env.example' && prohibited.test(relative)) return false
  if (exact.has(relative)) return true
  if (/^src\/(?:[^/]+\/)*[^/]+\.(?:vue|ts|css|svg)$/.test(relative)) return true
  if (/^cmd\/(?:server|admin-credentials|initial-passwords|org-bootstrap|organization-initialization|deployment-preflight)\/[^/]+\.go$/.test(relative)) return true
  if (/^clients\/devflow_macos\/(?:lib|test)\/(?:[^/]+\/)*[^/]+\.dart$/.test(relative)) return true
  if (/^clients\/devflow_macos\/macos\/(?:[^/]+\/)*[^/]+\.(?:swift|plist|pbxproj|xcworkspacedata|xcscheme|xcconfig|entitlements|xib)$/.test(relative)) return true
  if (/^clients\/devflow_macos\/macos\/Runner\/Assets\.xcassets\/AppIcon\.appiconset\/(?:Contents\.json|app_icon_\d+\.png)$/.test(relative)) return true
  if (/^scripts\/(?:test-(?!macos|portal)[\w-]+|[\w-]+\.test|[\w-]+-test-support|check-api-doc-coverage)\.mjs$/.test(relative)) return true
  return /^scripts\/helpers\/[\w-]+\.mjs$/.test(relative)
}
const digest = bytes => crypto.createHash('sha256').update(bytes).digest('hex')
export function inspectContent(relative, bytes) {
  if (bytes.length > 16 * 1024 * 1024) throw new Error(`白名单文件过大：${relative}`)
  if (/\.(?:png|ttf)$/.test(relative)) return
  const text = bytes.toString('utf8')
  // 高置信度秘密格式；测试文件也不能携带真实私钥。不会输出匹配内容。
  if (/-----BEGIN (?:RSA |EC |OPENSSH |DSA |ENCRYPTED )?PRIVATE KEY-----|\bAKIA[0-9A-Z]{16}\b|\bgh[pousr]_[A-Za-z0-9]{30,}\b|\bsk-(?:proj-|svcacct-)[A-Za-z0-9_-]{30,}/.test(text)) throw new Error(`检测到疑似秘密，拒绝打包：${relative}`)
  for (const match of text.matchAll(/https:\/\/qyapi\.weixin\.qq\.com\/cgi-bin\/webhook\/send\?key=([0-9a-f-]{32,})/gi)) {
    // 已人工检查的唯一合成测试值；不能把所有 *_test.go 都排除扫描。
    if (relative === 'cmd/server/user_wecom_test.go' && match[1] === '01234567-89ab-cdef-0123-456789abcdef') continue
    throw new Error(`检测到疑似真实机器人地址，拒绝打包：${relative}`)
  }
}
export function collect(sourceRoot) {
  const files = []
  function walk(directory, prefix = '') {
    for (const name of fs.readdirSync(directory).sort()) {
      const relative = prefix ? `${prefix}/${name}` : name
      if (relative !== '.env.example' && prohibited.test(relative)) continue
      const entry = path.join(directory, name), stat = fs.lstatSync(entry)
      if (stat.isSymbolicLink()) { if (allowed(relative)) throw new Error(`拒绝打包符号链接：${relative}`); continue }
      if (stat.isDirectory()) {
        // 只枚举明确放行的源码目录，不遍历私人工作区或整个文档集合。
        if (['src', 'cmd', 'clients', 'scripts'].some(base => relative === base || relative.startsWith(base + '/'))) walk(entry, relative)
      } else if (stat.isFile() && allowed(relative)) files.push(relative)
      else if (allowed(relative)) throw new Error(`白名单中不是普通文件：${relative}`)
    }
  }
  walk(sourceRoot)
  for (const name of documents) {
    const relative = `docs/${name}`, file = path.join(sourceRoot, relative)
    if (!fs.existsSync(file) || !fs.lstatSync(file).isFile() || fs.lstatSync(file).isSymbolicLink()) throw new Error(`缺少安全文档普通文件：${relative}`)
    if (fs.realpathSync(file) !== path.join(fs.realpathSync(sourceRoot), relative)) throw new Error(`拒绝通过目录符号链接读取文档：${relative}`)
    files.push(relative)
  }
  return files.sort()
}
export function stageBundle(sourceRoot, destination) {
  if (fs.existsSync(destination)) throw new Error('暂存目标必须是新的目录')
  fs.mkdirSync(destination, { recursive: true, mode: 0o700 })
  const files = collect(sourceRoot), entries = []
  for (const required of ['package.json', 'pnpm-lock.yaml', 'go.mod', 'go.sum', '.env.example', 'src/main.ts', 'cmd/server/main.go', 'clients/devflow_macos/lib/main.dart', 'clients/devflow_macos/pubspec.lock', 'scripts/start-developer.mjs']) if (!files.includes(required)) throw new Error(`缺少开发包必要文件：${required}`)
  for (const relative of files) {
    let bytes = fs.readFileSync(path.join(sourceRoot, relative)); inspectContent(relative, bytes)
    const sourceSha256 = digest(bytes)
    if (relative === 'package.json') {
      const pkg = JSON.parse(bytes)
      pkg.scripts.dev = 'node scripts/developer-web.mjs dev'
      pkg.scripts.build = 'node scripts/developer-web.mjs build'
      pkg.scripts.test = 'node scripts/test-developer-bundle.mjs && node scripts/test-search-layout.mjs && node scripts/test-watermark.mjs && node scripts/test-top-search.mjs'
      pkg.scripts['dev:api'] = 'node scripts/start-developer.mjs'
      pkg.scripts['dev:check'] = 'node scripts/check-developer-environment.mjs'
      bytes = Buffer.from(JSON.stringify(pkg, null, 2) + '\n')
    }
    if (relative === 'pnpm-workspace.yaml') {
      let settings = bytes.toString('utf8')
      if (!/^allowBuilds:\s*$/m.test(settings)) throw new Error('请先审阅 pnpm 依赖构建策略，再更新开发包转换规则')
      settings = settings.replace(/^  esbuild: set this to true or false\s*$/m, '  esbuild: true')
      if (!/^  vue-demi:/m.test(settings)) settings = settings.replace(/^allowBuilds:\s*$/m, 'allowBuilds:\n  vue-demi: true')
      if (!/^  esbuild: true\s*$/m.test(settings) || !/^  vue-demi: true\s*$/m.test(settings) || /dangerouslyAllowAllBuilds\s*:\s*true/.test(settings)) throw new Error('开发包需要明确且有限的 esbuild/vue-demi 构建许可；请先审阅 pnpm 策略')
      bytes = Buffer.from(settings.trimEnd() + '\n')
    }
    const target = path.join(destination, relative)
    fs.mkdirSync(path.dirname(target), { recursive: true, mode: 0o755 })
    fs.writeFileSync(target, bytes, { flag: 'wx', mode: relative.endsWith('.sh') ? 0o755 : 0o644 })
    entries.push({ path: relative, bytes: bytes.length, sha256: digest(bytes), sourceSha256, transformed: ['package.json', 'pnpm-workspace.yaml'].includes(relative) })
  }
  // 开发时允许原仓库有未提交改动，但打包期间的新增或改写必须重新打包。
  if (JSON.stringify(collect(sourceRoot)) !== JSON.stringify(files) || entries.some(entry => digest(fs.readFileSync(path.join(sourceRoot, entry.path))) !== entry.sourceSha256)) throw new Error('源码在打包期间发生变化；请等待编辑完成后重新生成')
  const readme = fs.readFileSync(path.join(sourceRoot, 'docs/developer-environment.md'))
  fs.writeFileSync(path.join(destination, 'README.md'), readme, { flag: 'wx', mode: 0o644 })
  entries.push({ path: 'README.md', bytes: readme.length, sha256: digest(readme), transformed: true })
  entries.sort((a, b) => a.path.localeCompare(b.path, 'en'))
  const manifest = {
    schemaVersion: 1, kind: 'developer-source', createdAt: new Date().toISOString(),
    description: 'Vue + Go + Flutter macOS source. No SDK, database, credentials, portal downloads, signed app or notarization.',
    transformations: ['README.md is copied from docs/developer-environment.md.', 'package.json development scripts use the standalone developer Web/API entries, and test runs the bundle, search-layout, watermark and top-search regressions; dependency versions and lockfiles are unchanged.', 'pnpm-workspace.yaml explicitly allows the required esbuild and vue-demi installation scripts; no blanket script approval.'],
    excluded: ['Private/unlisted documents', 'work, data, .git, node_modules, build, .dart_tool, Pods, ephemeral, xcuserdata', 'real .env files, databases, keys, certificates, archives, application and DMG releases', 'portal and native acceptance records'],
    files: entries,
  }
  fs.writeFileSync(path.join(destination, 'DEVELOPER-MANIFEST.json'), JSON.stringify(manifest, null, 2) + '\n', { flag: 'wx', mode: 0o644 })
  return manifest
}
export function buildBundle(outputRoot = path.join(root, 'work/releases/developer-source')) {
  const staging = fs.mkdtempSync(path.join(os.tmpdir(), 'devflow-source-stage-'))
  const stamp = new Date().toISOString().replace(/[-:]/g, '').replace(/\.\d+Z$/, 'Z')
  const name = `TaskLoom-Developer-${stamp}-${crypto.randomBytes(3).toString('hex')}`
  const output = path.join(path.resolve(outputRoot), name)
  try {
    const manifest = stageBundle(root, path.join(staging, 'TaskLoom-Developer'))
    fs.mkdirSync(output, { recursive: true, mode: 0o700 })
    const archive = path.join(output, `${name}.tar.gz`)
    const result = spawnSync('tar', ['-czf', archive, '-C', staging, 'TaskLoom-Developer'], { env: { ...process.env, COPYFILE_DISABLE: '1' }, encoding: 'utf8' })
    if (result.error || result.status !== 0) throw new Error('源码归档生成失败')
    const sha256 = digest(fs.readFileSync(archive))
    fs.writeFileSync(path.join(output, 'SHA256SUMS.txt'), `${sha256}  ${path.basename(archive)}\n`, { flag: 'wx' })
    fs.copyFileSync(path.join(staging, 'TaskLoom-Developer/DEVELOPER-MANIFEST.json'), path.join(output, 'DEVELOPER-MANIFEST.json'), fs.constants.COPYFILE_EXCL)
    return { archive, sha256, fileCount: manifest.files.length, bytes: fs.statSync(archive).size }
  } finally { fs.rmSync(staging, { recursive: true, force: true }) }
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2)
  if (args.length && (args.length !== 2 || args[0] !== '--output')) throw new Error('用法：node scripts/build-developer-bundle.mjs [--output 新包父目录]')
  console.log(JSON.stringify(buildBundle(args[1]), null, 2))
}
