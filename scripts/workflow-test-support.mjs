import {readFileSync} from 'node:fs'
import ts from 'typescript'

function evaluate(path, imports={}) {
  const exports={}
  const source=readFileSync(new URL('../'+path,import.meta.url),'utf8')
  const code=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
  new Function('require','exports',code)(id=>imports[id],exports)
  return exports
}
export const workflow=evaluate('src/requirementWorkflow.ts',{'./requirementFields':evaluate('src/requirementFields.ts')})
