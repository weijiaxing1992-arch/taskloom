import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
const source=readFileSync(new URL('../src/api.ts',import.meta.url),'utf8'),exports={},events=[]
let response={error:{code:'login_challenge_required',message:'Security check',challenge:{id:'c'.repeat(32),image:'data:image/png;base64,aA==',expiresAt:Math.floor(Date.now()/1000)+120},retryAfterSeconds:30}}
new Function('require','exports','window','localStorage','fetch','CustomEvent',ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(id=>{assert.equal(id,'./i18n');return{locale:{value:'zh-CN'},t:value=>value}},exports,{dispatchEvent:event=>events.push(event)},{getItem:()=>null},async()=>new Response(JSON.stringify(response),{status:401}),class extends Event{constructor(type,options){super(type);this.detail=options?.detail}})
await assert.rejects(exports.api('/auth/login',{method:'POST',body:'{}'}),error=>error instanceof exports.APIError&&error.code==='login_challenge_required'&&error.details.challenge.id===response.error.challenge.id&&error.details.retryAfterSeconds===30)
assert.equal(events.length,0,'login verification must not invalidate an existing workspace session')
response={error:{code:'unauthorized',message:'Expired',challenge:{unexpected:true}}}
await assert.rejects(exports.api('/requirements/1'),error=>error instanceof exports.APIError&&error.details===undefined)
assert.equal(events.length,1);assert.equal(events[0].type,'devflow-auth-expired');assert.deepEqual(events[0].detail,{path:'/requirements/1'})
console.log('Passed login challenge metadata isolation and bounded session-recheck event contracts.')
