import { createRequire } from 'node:module'
import { dirname, join, basename } from 'node:path'
import { readdirSync, readFileSync } from 'node:fs'

// 中文 CMap、内置字体和解码器必须与 PDF.js 同版本，离线解析不依赖 CDN。
export function pdfAssetsPlugin() {
  const root = dirname(createRequire(import.meta.url).resolve('pdfjs-dist/package.json'))
  const files = new Map()
  for (const folder of ['cmaps', 'standard_fonts', 'wasm']) {
    for (const name of readdirSync(join(root, folder))) {
      if (/\.(bcmap|pfb|ttf|wasm)$/.test(name)) files.set(`/pdfjs/${folder}/${name}`, join(root, folder, name))
    }
  }
  return {
    name: 'devflow-local-pdf-assets',
    generateBundle() { for (const [url, file] of files) this.emitFile({ type: 'asset', fileName: url.slice(1), source: readFileSync(file) }) },
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const file = files.get((req.url || '').split('?')[0])
        if (!file) return next()
        res.setHeader('Content-Type', basename(file).endsWith('.wasm') ? 'application/wasm' : 'application/octet-stream')
        res.setHeader('X-Content-Type-Options', 'nosniff'); res.end(readFileSync(file))
      })
    },
  }
}
