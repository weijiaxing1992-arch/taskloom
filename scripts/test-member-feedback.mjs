import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
const read=file=>readFileSync(new URL('../'+file,import.meta.url),'utf8')
const evaluate=file=>{const exports={};new Function('exports',ts.transpileModule(read(file),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(exports);return exports}
const helpers=evaluate('src/memberRoles.ts'),mentions=evaluate('src/mentions.ts'),defects=evaluate('src/defectPeople.ts')
const members=[{id:'multi',name:'多角色',active:true,projectRole:'product',projectRoles:['product','qa','frontend'],departmentIds:['delivery']},{id:'legacy',name:'旧成员',active:true,projectRole:'qa'},{id:'inactive',name:'停用',active:false,projectRoles:['qa']},{id:'other',name:'其他',projectRole:'backend',departmentIds:['delivery']}]
assert.deepEqual(helpers.memberProjectRoles(members[0]),['product','qa','frontend'])
assert(helpers.memberHasProjectRole(members[0],['qa']))
assert(!helpers.memberHasProjectRole(members[0],['project_admin']))
assert.deepEqual(helpers.memberProjectRoles(members[1]),['qa'])
assert.deepEqual(helpers.memberProjectRoles({projectRoles:[],projectRole:'qa'}),[])
assert.deepEqual(helpers.memberProjectRoles(null),[])
assert.equal(helpers.memberHasProjectRole(undefined,['qa']),false)
assert.deepEqual(mentions.memberCandidates(members,['qa'],'delivery').map(item=>item.id),['multi'])
assert.deepEqual(mentions.memberCandidates(members,['frontend']).map(item=>item.id),['multi'])
assert.deepEqual(mentions.filterMentionMembers(members,'测试').map(item=>item.id),['multi','legacy'])
assert.deepEqual(defects.qaMembers(members).map(item=>item.id),['multi','legacy'])
assert.equal(defects.defaultVerifier(members,'multi')?.id,'multi')
console.log('Passed multi-role fallback, union, department intersection, search and verifier regressions.')
