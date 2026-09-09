import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const read=file=>readFileSync(new URL('../'+file,import.meta.url),'utf8')
globalThis.window={location:{origin:'https://app.example'}}
const exports={}
new Function('exports',ts.transpileModule(read('src/wechatLogin.ts'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(exports)
const {safeWechatAuthorization}=exports
const valid=new URL('https://open.weixin.qq.com/connect/qrconnect')
valid.search=new URLSearchParams({appid:'wx0123456789abcdef',scope:'snsapi_login',response_type:'code',state:'a'.repeat(64),redirect_uri:'https://app.example/api/auth/wechat/callback'}).toString()
assert.equal(safeWechatAuthorization(valid.href),valid.href)
for(const raw of ['javascript:alert(1)','https://evil.example/',null,valid.href.replace('open.weixin.qq.com','user:pass@open.weixin.qq.com'),valid.href.replace('snsapi_login','snsapi_userinfo'),valid.href.replace('https%3A%2F%2Fapp.example','https%3A%2F%2Fevil.example'),valid.href.replace('https://','http://')]){
 assert.throws(()=>safeWechatAuthorization(raw))
}
assert(read('src/views/Login.vue').includes('<WechatLoginButton />'))
assert(read('src/views/Profile.vue').includes('<WechatBinding ref="wechatEditor"/>'))
assert(read('src/views/Organization.vue').includes('section===\'wechat-login\'&&context.isTenantAdmin'))
for(const file of ['src/components/WechatSettings.vue','src/components/WechatBinding.vue']){
 assert(read(file).includes('type="password"'))
 assert(!/localStorage\.setItem|sessionStorage\.setItem/.test(read(file)))
}
assert(read('src/components/WechatLoginButton.vue').includes(':disabled="loading||busy||!enabled"'))
assert(read('src/components/WechatSettings.vue').includes("method:'PATCH'"))
assert(read('src/components/WechatSettings.vue').includes('version:form.version'))
assert(read('docs/internal-api-reference.md').includes('/api/auth/wechat/callback'))
console.log('WeChat URL allowlist, UI entry points, disabled state and credential handling passed.')
