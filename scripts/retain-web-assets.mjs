import { constants, copyFileSync, existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, realpathSync } from 'node:fs'
import { dirname, join, relative, resolve, sep } from 'node:path'
import { pathToFileURL } from 'node:url'

function regularFiles(directory, prefix = '') {
  if (!existsSync(directory)) return []
  if (!lstatSync(directory).isDirectory() || lstatSync(directory).isSymbolicLink()) throw Error(`Not a real asset directory: ${directory}`)
  return readdirSync(directory).sort().flatMap(name => {
    const file = join(directory, name), key = join(prefix, name), stat = lstatSync(file)
    if (stat.isSymbolicLink()) throw Error(`Asset symlinks are not supported: ${file}`)
    if (stat.isDirectory()) return regularFiles(file, key)
    if (!stat.isFile()) throw Error(`Not a regular asset: ${file}`)
    return [key]
  })
}

export function retainWebAssets(previousWeb, nextWeb) {
  // 只在两个独立构建目录之间保留资源，不复制 HTML 入口，也不允许符号链接越出目录。
  const previous = realpathSync(previousWeb), next = realpathSync(nextWeb)
  if (previous === next || previous.startsWith(next + sep) || next.startsWith(previous + sep)) throw Error('Use separate, non-nested web release directories')
  for (const web of [previous, next]) {
    const entry = join(web, 'index.html')
    if (!lstatSync(entry).isFile() || lstatSync(entry).isSymbolicLink()) throw Error(`Missing regular HTML entry: ${entry}`)
  }
  const source = join(previous, 'assets'), destination = join(next, 'assets')
  if (!existsSync(source)) throw Error(`Previous release has no assets: ${source}`)
  const oldFiles = regularFiles(source), newFiles = new Set(regularFiles(destination))
  let reused = 0
  // 复制前先验证所有哈希冲突；同名不同内容必须失败，不能让旧页面加载不兼容的新模块。
  for (const key of oldFiles) {
    const target = join(destination, key)
    if (newFiles.has(key)) {
      if (!readFileSync(join(source, key)).equals(readFileSync(target))) throw Error(`Asset collision with different contents: ${key}`)
      reused++
    } else if (existsSync(target)) {
      throw Error(`Asset path collision: ${key}`)
    }
    for (let parent = dirname(target); parent !== destination; parent = dirname(parent)) {
      if (existsSync(parent) && !lstatSync(parent).isDirectory()) throw Error(`Asset directory collision: ${relative(destination, parent)}`)
    }
  }
  for (const key of oldFiles) {
    if (newFiles.has(key)) continue
    const target = join(destination, key)
    mkdirSync(dirname(target), { recursive: true })
    copyFileSync(join(source, key), target, constants.COPYFILE_EXCL)
  }
  return { copied: oldFiles.length - reused, reused, total: new Set([...newFiles, ...oldFiles]).size }
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  if (process.argv.length !== 4) {
    console.error('Usage: node scripts/retain-web-assets.mjs <previous-web-directory> <next-web-directory>')
    process.exitCode = 1
  } else {
    try { console.log(JSON.stringify(retainWebAssets(process.argv[2], process.argv[3]))) }
    catch (error) { console.error(error.message); process.exitCode = 1 }
  }
}
