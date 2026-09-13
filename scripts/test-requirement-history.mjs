import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import * as Vue from 'vue'
import { renderToString } from 'vue/server-renderer'
import { parse, compileScript, compileStyle } from 'vue/compiler-sfc'
import ts from 'typescript'

const root=new URL('../',import.meta.url)
const source=await readFile(new URL('src/components/RequirementHistory.vue',root),'utf8')
const descriptor=parse(source,{filename:'RequirementHistory.vue'}).descriptor
const compiled=compileScript(descriptor,{id:'history-test',inlineTemplate:true})
const querySource=await readFile(new URL('src/workItemQuery.ts',root),'utf8')
const queryModule={exports:{}}
new Function('module','exports',ts.transpileModule(querySource,{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText)(queryModule,queryModule.exports)
const componentModule={exports:{}}
const translate=(value,params={})=>String(value).replace(/\{(\w+)\}/g,(token,key)=>params[key]??token)
const dependencies={vue:Vue,'../i18n':{t:translate,formatDate:value=>value},'../workItemQuery':queryModule.exports,'../requirementWorkflow':{statusLabel:(item,states)=>states.find(state=>state.key===item.status)?.name||item.status}}
new Function('require','module','exports',ts.transpileModule(compiled.content,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(key=>{assert(key in dependencies,key);return dependencies[key]},componentModule,componentModule.exports)
const Component=componentModule.exports.default
const items=[{id:3,actor:'张三',createdAt:'2026-09-10T10:20:30Z',event:'updated',detail:'字段已更新',sprint:'迭代 B',status:'doing',snapshotAvailable:true,iterationDelay:true,iterationDelayCount:2,changes:[{field:'title',before:'标题一',after:'标题二'},{field:'customFields.review',before:'旧记录',after:'<script>alert(1)</script>'+ '长内容'.repeat(100)},{field:'roleWeights.frontend',before:{userIds:['u1'],value:1},after:{userIds:['u1'],value:3}}]},{id:1,actor:'旧成员',createdAt:'2026-08-01T00:00:00Z',detail:'旧记录',changes:[],snapshotAvailable:false}]
const markup=await renderToString(Vue.createSSRApp(Component,{items,count:2,members:[{id:'u1',name:'工程师'}],definitions:[{key:'review',name:'评审意见'}],statuses:[{key:'doing',name:'开发中'}]}))
for(const text of ['迭代延误 ×2','张三','2026-09-10T10:20:30Z','迭代 B','开发中','标题一','标题二','评审意见','工程师','未记录','此历史记录未保存字段前后值。'])assert(markup.includes(text),text)
assert(markup.includes('&lt;script&gt;alert(1)&lt;/script&gt;'))
assert(!markup.includes('<script>'),'history business content must stay text')
assert(markup.includes('长内容'.repeat(100)),'full recorded values must remain readable')
assert(markup.includes('<details open>'),'field changes should be expanded')
assert.equal(queryModule.exports.workItemFields().find(field=>field.key==='iterationDelayCount').kind,'number')
assert.deepEqual(queryModule.exports.queryWorkItems([{id:1,iterationDelayCount:0},{id:2,iterationDelayCount:2}],queryModule.exports.workItemFields(),[{field:'iterationDelayCount',operator:'gt',value:0}],'iterationDelayCount','desc').map(item=>item.id),[2])
const rich={type:'doc',content:[{type:'paragraph',content:[{type:'text',text:'<img src=x onerror=alert(1)>',marks:[{type:'bold'}]}]}]}
const richMarkup=await renderToString(Vue.createSSRApp(Component,{items:[{...items[0],changes:[{field:'descriptionDoc',before:{type:'doc',content:[]},after:rich},{field:'checklist',before:null,after:{id:1,text:'Complete checklist',done:false}}]}],count:2,members:[],definitions:[],statuses:[]}))
assert(richMarkup.includes('&lt;img src=x onerror=alert(1)&gt;')&&!richMarkup.includes('<img'),'rich history must remain escaped, inert data')
assert(richMarkup.includes('加粗')&&richMarkup.includes('Complete checklist'),'rich formatting and related values must stay readable without exposing document schema keys')
const legacyMarkup=await renderToString(Vue.createSSRApp(Component,{items:[{id:12,actor:'u_admin',actorId:'u_admin',event:'updated',detail:'更新了需求字段：description、descriptionDoc',createdAt:'2026-09-10T10:20:30Z',changes:[],snapshotAvailable:false}],count:0,members:[{id:'u_admin',name:'示例管理员'}],definitions:[],statuses:[]}))
assert(legacyMarkup.includes('示例管理员'),'legacy account IDs should resolve to a member name')
assert(legacyMarkup.includes('更新了需求字段：需求描述、富文本正文'),'legacy field keys should use Chinese business labels')
assert(legacyMarkup.includes('查看原始记录'),'technical identifiers should remain available only in a collapsed trace')
const traceStart=legacyMarkup.indexOf('class="history-technical-trace"')
assert(traceStart>0,'the technical trace must render after the readable activity')
assert(!legacyMarkup.slice(0,traceStart).includes('u_admin'),'technical account ID must not be exposed by default')
assert(legacyMarkup.slice(traceStart).includes('u_admin'),'the original identifier remains traceable when expanded')
const exportModule={exports:{}}
new Function('require','module','exports',ts.transpileModule(await readFile(new URL('src/workItemExport.ts',root),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(key=>({'./i18n':{t:translate},'./workItemQuery':queryModule.exports})[key],exportModule,exportModule.exports)
const filtered=queryModule.exports.queryWorkItems([{id:1,code:'REQ-1',title:'First',iterationDelayCount:1},{id:2,code:'REQ-2',title:'Second',iterationDelayCount:2},{id:3,code:'REQ-3',title:'Not delayed',iterationDelayCount:0}],queryModule.exports.workItemFields(),[{field:'iterationDelayCount',operator:'gt',value:0}],'iterationDelayCount','desc')
assert.equal(exportModule.exports.requirementCSV(filtered,[],[],['iterationDelayCount']),'\uFEFF"编号","标题","迭代延误次数"\r\n"REQ-2","Second","2"\r\n"REQ-1","First","1"\r\n','CSV must preserve the displayed linear counts and filtered order')
for(const style of descriptor.styles)assert.deepEqual(compileStyle({source:style.content,filename:'RequirementHistory.vue',id:'history-test',scoped:true}).errors,[])
const listSource=await readFile(new URL('src/views/Requirements.vue',root),'utf8')
const boardSource=listSource.slice(listSource.indexOf("v-else-if=\"listView==='board'\""),listSource.indexOf('v-else class="table-wrap"'))
assert(boardSource.includes('v-if="item.iterationDelayCount>0" class="iteration-delay-badge requirement-board-delay"'),'board must expose the same delay badge as the table')
console.log('Passed requirement history rendering, complete values, escaping, numeric filtering and responsive style checks.')
