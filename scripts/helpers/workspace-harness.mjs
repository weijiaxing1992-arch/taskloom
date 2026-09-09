import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import * as Pinia from 'pinia'

/** Use the production Pinia setup store, with a fresh app instance per test. */
export function createWorkspaceHarness(localStorage = { getItem: () => null }) {
  const source = readFileSync(new URL('../../src/stores/workspace.ts', import.meta.url), 'utf8')
  const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('require', 'exports', 'localStorage', code)(id => ({ vue: Vue, pinia: Pinia })[id], exports, localStorage)
  const pinia = Pinia.createPinia()
  const workspace = exports.useWorkspaceStore(pinia)
  return { pinia, workspace, imports: { pinia: Pinia, './stores/workspace': { useWorkspaceStore: () => workspace } }, stop: () => Pinia.disposePinia(pinia) }
}
