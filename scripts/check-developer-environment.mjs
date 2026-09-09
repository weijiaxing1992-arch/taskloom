#!/usr/bin/env node
import fs from 'node:fs'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)))
const webOnly = process.argv.includes('--web-only')
if (process.argv.slice(2).some(arg => arg !== '--web-only')) throw new Error('用法：node scripts/check-developer-environment.mjs [--web-only]')
let failures = 0
function check(label, valid, detail) {
  console.log(`${valid ? '通过' : '缺少'}：${label} — ${detail}`)
  if (!valid) failures++
}
function run(tool, args) {
  const result = spawnSync(tool, args, { cwd: root, encoding: 'utf8', timeout: 45000 })
  return result.error || result.status !== 0 ? '' : result.stdout.trim()
}
const minimum = (actual, wanted) => {
  const a = String(actual).match(/\d+\.\d+(?:\.\d+)?/)?.[0].split('.').map(Number) || []
  return a.length >= 2 && wanted.every((part, index) => a.slice(0, index).some((value, i) => value > wanted[i]) || (a[index] || 0) >= part)
}
const pkg = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'))
check('Node.js', minimum(process.version, [22, 12, 0]), `${process.version}；要求 ${pkg.engines.node}`)
const pnpm = run(process.env.DEVFLOW_PNPM_BINARY || 'pnpm', ['--version'])
check('pnpm', pnpm === pkg.packageManager.split('@')[1], pnpm || `安装 ${pkg.packageManager}`)
const goRequirement = fs.readFileSync(path.join(root, 'go.mod'), 'utf8').match(/^go (.+)$/m)[1]
const go = run(process.env.DEVFLOW_GO_BINARY || 'go', ['version'])
check('Go', minimum(go, goRequirement.split('.').map(Number)), go || `安装 Go ${goRequirement} 或更新版本`)
for (const file of ['pnpm-lock.yaml', 'go.sum', 'clients/devflow_macos/pubspec.lock']) check(file, fs.existsSync(path.join(root, file)), '使用随包锁文件')
if (!webOnly) {
  check('macOS / Apple Silicon', process.platform === 'darwin' && run('uname', ['-m']) === 'arm64', '原生客户端工程目标为 macOS ARM64')
  const flutterOutput = run(process.env.DEVFLOW_FLUTTER_BINARY || 'flutter', ['--version', '--machine'])
  let flutter = {}; try { flutter = JSON.parse(flutterOutput) } catch {}
  const dartMinimum = fs.readFileSync(path.join(root, 'clients/devflow_macos/pubspec.lock'), 'utf8').match(/^  dart: "?>=(\d+)\.(\d+)\.(\d+)/m)?.slice(1).map(Number) || [3, 10, 0]
  check('Flutter / Dart', Boolean(flutter.frameworkVersion) && minimum(flutter.dartSdkVersion, dartMinimum) && !minimum(flutter.dartSdkVersion, [4, 0, 0]), flutter.frameworkVersion ? `Flutter ${flutter.frameworkVersion} / Dart ${flutter.dartSdkVersion}；锁文件要求 >=${dartMinimum.join('.')} <4.0.0` : `安装稳定版 Flutter；锁文件要求 Dart >=${dartMinimum.join('.')} <4.0.0`)
  const xcode = run('xcodebuild', ['-version'])
  check('完整 Xcode', /^Xcode /m.test(xcode), xcode.replace(/\n/g, ' ') || '安装并选择完整 Xcode；仅 Command Line Tools 不够')
  const ready = spawnSync('xcodebuild', ['-checkFirstLaunchStatus'], { encoding: 'utf8', timeout: 45000 })
  check('Xcode 首次配置', !ready.error && ready.status === 0, '许可与首次组件由开发者在 Xcode 中完成')
  check('macOS SDK', Boolean(run('xcrun', ['--sdk', 'macosx', '--show-sdk-path'])), '通过当前 Xcode 定位 SDK')
}
console.log(failures ? `有 ${failures} 项需要处理；检查没有安装 SDK、配置证书或更改系统安全设置。` : '环境检查通过。随后安装锁定依赖并执行构建/测试；工具可用不等于业务验收通过。')
process.exitCode = failures ? 1 : 0
