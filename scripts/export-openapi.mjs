#!/usr/bin/env node
import { existsSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

const repo = fileURLToPath(new URL('..', import.meta.url))
const args = process.argv.slice(2)
if (args.length > 1 || (args.length === 1 && args[0] !== '--check')) {
  process.stderr.write('Usage: node scripts/export-openapi.mjs [--check]\n')
  process.exit(2)
}

const bundledGo = path.join(repo, 'work/toolchain/runtime/go/bin/go')
const go = process.env.DEVFLOW_GO_BINARY || (existsSync(bundledGo) ? bundledGo : 'go')
const env = { ...process.env }
// --check must never inherit an export flag from the caller.
delete env.DEVFLOW_UPDATE_OPENAPI
if (args[0] !== '--check') env.DEVFLOW_UPDATE_OPENAPI = '1'
if (go === bundledGo) {
  env.GOCACHE ||= path.join(repo, 'work/go-build-cache')
  env.GOMODCACHE ||= path.join(repo, 'work/gopath/pkg/mod')
}

const result = spawnSync(go, ['test', './cmd/server', '-run', '^TestIntegrationDocumentationOpenAPISnapshot$', '-count=1'], {
  cwd: repo,
  env,
  stdio: 'inherit',
})
if (result.error) {
  process.stderr.write(`OpenAPI export could not start: ${result.error.message}\n`)
  process.exit(1)
}
if (result.status !== 0) process.exit(result.status || 1)
process.stdout.write(args[0] === '--check' ? 'OpenAPI export matches the server schema.\n' : 'Generated docs/openapi.json from the server schema.\n')
