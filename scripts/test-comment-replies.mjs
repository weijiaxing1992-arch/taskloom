import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'
import { workflow } from './workflow-test-support.mjs'

const read=path=>readFile(new URL('../'+path,import.meta.url),'utf8')
const transpile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
function evaluate(source,imports={}){const result={};new Function('require','exports',transpile(source))(id=>imports[id],result);return result}
const mentions=evaluate(await read('src/mentions.ts')),fields=evaluate(await read('src/requirementFields.ts')),queries=evaluate(await read('src/workItemQuery.ts')),defectPeople=evaluate(await read('src/defectPeople.ts')),replies=evaluate(await read('src/commentReplies.ts'))
const translations=evaluate(await read('src/locales/enhancements.en.ts')).default
const flush=async()=>{for(let n=0;n<8;n++){await Promise.resolve();await Vue.nextTick()}}
const defer=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return{promise,resolve,reject}}
const parent=()=>({id:5,author:'本人',authorUserId:'me',body:'上一级内容',replyToId:3,replyToAuthor:'另一位成员',createdAt:'2026-09-04'})
const pendingImage=()=>({type:'doc',content:[{type:'image',attrs:{name:'截图.png',attachmentId:null,data:'cGljdHVyZQ=='}}]})
const requirement=()=>({id:9,title:'需求原文',description:'正文',descriptionDoc:null,descriptionMentionUserIds:[],roleWeights:fields.emptyRoleWeights(),tags:'',tagColors:{},remarks:'',remarksMentionUserIds:[],customFields:{},ownerUserIds:[],assigneeUserIds:[],category:'未分类',sprint:'待规划',status:'规划中'})
const compiled=new Map()
async function mount(kind,{handler,resource='test-cases',viewer=false}={}){
 const path=kind==='QualityComments'?'src/components/QualityComments.vue':'src/views/'+kind+'.vue'
 let component=compiled.get(path)
 if(!component){const source=await read(path),descriptor=parse(source).descriptor,script=compileScript(descriptor,{id:path}),template=compileTemplate({source:descriptor.template.content,filename:path,id:path,compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[]);component={code:transpile(script.content),source};compiled.set(path,component)}
 const calls=[],starts=[],ends=[],guards=[],updates=[],events=[],scope=Vue.effectScope(),props=Vue.reactive({resource,objectId:9,members:[],detailOnly:true}),locked=Vue.ref(false),window=new EventTarget(),route=Vue.reactive({query:{},params:{}}),confirmation={allowed:false,count:0},serverComments=[parent()]
 const user={id:'me',role:viewer?'viewer':'qa'},identity={user,project:{id:'test-project'}}
 window.confirm=()=>{confirmation.count++;return confirmation.allowed};window.addEventListener('devflow-notifications-changed',event=>events.push(event))
 const api=async(path,options={})=>{calls.push({path,options});if(handler){const answer=await handler(path,options);if(answer!==undefined)return answer}if(options.method==='POST'){const body=JSON.parse(options.body),created={...body,id:100+serverComments.length,author:'本人',authorUserId:'me',createdAt:'2026-09-04'};serverComments.unshift(created);return created}if(path==='/session')return identity;if(path.endsWith('/comments'))return{items:structuredClone(serverComments)};if(path==='/requirements/9')return requirement();return{items:[]}}
 const context={locked,project:'test-project',current:()=>!locked.value,request:api}
 const imports={vue:{...Vue,onMounted:callback=>starts.push(callback),onBeforeUnmount:callback=>ends.push(callback)},'vue-router':{useRoute:()=>route,useRouter:()=>({replace:async target=>{updates.push(target);route.query=target.query||{}},push:async target=>updates.push(target)}),onBeforeRouteLeave:callback=>guards.push(callback),onBeforeRouteUpdate:()=>{}},'../api':{api},'../i18n':{t:(value,params={})=>value.replace(/\{(\w+)\}/g,(_,key)=>params[key]??'{'+key+'}'),categoryLabel:value=>value,formatDate:String,locale:Vue.ref('zh-CN')},'../requirementWorkflow':workflow,'../requirementFields':fields,'../workItemQuery':queries,'../mentions':mentions,'../defectPeople':defectPeople,'../layoutScope':{useLayoutBoolean:(_key,fallback)=>Vue.ref(fallback)},'./settingsScope':{useSettingsScope:()=>context},'../components/settingsScope':{useSettingsScope:()=>context}}
 const module={}
 new Function('require','exports','window','localStorage',component.code)(id=>imports[id]||(id.endsWith('.vue')?{default:{name:id.split('/').at(-1)}}:undefined),module,window,{getItem:()=> 'test-project'})
 const state=scope.run(()=>module.default.setup(props,{expose:()=>{},emit:(...args)=>events.push(args)}))
 if(kind==='QualityComments'){await state.load()}
 else if(kind!=='Notifications'){state.session.value=identity;state.selected.value=kind==='Requirements'?requirement():{id:9,title:'缺陷原文',customFields:{}};state.comments.value=[parent()];if(kind==='Requirements'){state.resetDescription();state.resetAssessment();state.resetAssignments();state.resetOwners();state.detailLoading.value=false}}
 let stopped=false
 return{...state,kind,props,route,locked,calls,events,guards,updates,confirmation,window,source:component.source,serverComments,stop:()=>{if(stopped)return;stopped=true;ends.forEach(callback=>callback());scope.stop()}}
}
function controls(m){return m.kind==='QualityComments'?{body:m.body,ids:m.mentionUserIds,target:m.replyTarget,dirty:m.dirty,reply:m.reply,cancel:m.cancelReply,post:m.submit,error:m.error,reload:m.load,focus:m.composer}:{body:m.comment,ids:m.commentMentions,target:m.commentReply,dirty:m.kind==='Requirements'?m.commentDraftDirty:m.commentDirty,reply:m.replyComment,cancel:m.cancelCommentReply,post:m.addComment,error:m.kind==='Requirements'?m.detailError:m.notice,reload:m.kind==='Requirements'?m.refreshComments:m.reloadComments,focus:m.kind==='Requirements'?m.commentEditor:m.commentComposer}}
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('reply previews accept only safe IDs, normalize whitespace and truncate by Unicode codepoint',()=>{
 for(const value of [0,-1,1.5,'5',null,undefined,NaN,Infinity,Number.MAX_SAFE_INTEGER+1])assert.equal(replies.validCommentId(value),false)
 assert.equal(replies.validCommentId(5),true);assert.equal(replies.commentExcerpt(' \n原文\t remains  '),'原文 remains');assert.equal(replies.commentExcerpt('😀😀😀',2),'😀😀…');assert.equal(replies.commentExcerpt(null),'');assert.equal(replies.commentExcerpt('<script>alert(1)</script>'),'<script>alert(1)</script>')
})
await test('shared quote is shallow, localized, accessible, safe text and cancel never fires while disabled',async()=>{
 const source=await read('src/components/CommentReplyContext.vue'),descriptor=parse(source).descriptor,script=compileScript(descriptor,{id:'quote'}),template=compileTemplate({id:'quote',source:descriptor.template.content,filename:'Quote.vue',compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[])
 const props=Vue.reactive({target:{id:1,author:'张三 <script>',body:'用户原文'},composing:true,disabled:true}),emits=[],module={},render={}
 const imports={vue:Vue,'../commentReplies':replies,'../i18n':{t:value=>translations[value]||value}}
 new Function('require','exports',transpile(script.content))(id=>imports[id],module);new Function('require','exports',transpile(template.code))(id=>imports[id],render)
 const state=module.default.setup(props,{expose:()=>{},emit:event=>emits.push(event)});assert.equal(state.author.value,'张三 <script>');assert.equal(state.excerpt.value,'用户原文');state.cancel();assert.deepEqual(emits,[]);props.disabled=false;state.cancel();assert.deepEqual(emits,['cancel'])
 props.target={id:9,unavailable:true,author:'历史姓名'};assert.equal(state.excerpt.value,'The original comment is unavailable.');props.target={id:10,body:''};assert.match(state.excerpt.value,/images or attachments/i)
 const node=render.render({},[],props,Vue.proxyRefs(state),{},{});assert.equal(node.props.role,'status');assert.equal(node.props['aria-live'],'polite');assert.equal(node.props['aria-label'],'Current reply target')
 assert(!source.includes('v-html'));assert(!source.includes('<CommentReplyContext'));assert.match(source,/@media\(max-width:640px\)/)
})
for(const kind of ['Requirements','Defects','QualityComments']){
 await test(kind+': any comment including own replies can be targeted without auto-mentioning or changing the draft',async()=>{
  const m=await mount(kind),c=controls(m);let focused=0;c.focus.value={focus:()=>focused++};c.body.value='@明确选择 保留草稿';c.ids.value=['explicit-id'];if(m.commentDoc)m.commentDoc.value=pendingImage()
  await c.reply(5);assert.deepEqual(c.target.value,{id:5,author:'本人',body:'上一级内容'});assert.equal(focused,1);assert.deepEqual(c.ids.value,['explicit-id']);assert.equal(c.body.value,'@明确选择 保留草稿');assert(c.dirty.value)
  await c.reply(9999);assert.equal(c.target.value.id,5,'unknown IDs cannot become reply targets')
  c.cancel();assert.equal(c.target.value,null);assert.equal(c.body.value,'@明确选择 保留草稿');assert.deepEqual(c.ids.value,['explicit-id']);if(m.commentDoc)assert.deepEqual(m.commentDoc.value,pendingImage());m.stop()
 })
 await test(kind+': successful reply sends the direct stable target ID and only clears the posted comment',async()=>{
  const m=await mount(kind),c=controls(m);await c.reply(5);c.body.value='回复内容';c.ids.value=['selected-person'];if(m.detailDraft)m.detailDraft.remarks='保留备注草稿'
  await c.post({body:c.body.value,mentionUserIds:[...c.ids.value]});const payload=JSON.parse(m.calls.find(call=>call.options.method==='POST').options.body)
  assert.deepEqual(payload,{body:'回复内容',mentionUserIds:['selected-person'],replyToId:5});assert.equal(c.body.value,'');assert.deepEqual(c.ids.value,[]);assert.equal(c.target.value,null);assert.equal(m.comments.value[0].replyToId,5);assert.equal(m.saving.value,false);if(m.detailDraft)assert.equal(m.detailDraft.remarks,'保留备注草稿');assert(m.events.some(event=>event.type==='devflow-notifications-changed'));m.stop()
 })
 await test(kind+': validation failure and old-server reply downgrade both preserve the target and original draft',async()=>{
  for(const failure of ['reject','downgrade']){const m=await mount(kind,{handler:async(_path,options)=>{if(options.method==='POST'){if(failure==='reject')throw Error('回复目标不在当前工作项');return{id:77,body:'旧服务忽略回复',replyToId:null}}}}),c=controls(m);await c.reply(5);c.body.value='必须保留';c.ids.value=['explicit'];await c.post({body:c.body.value,mentionUserIds:[...c.ids.value]});assert.equal(c.body.value,'必须保留');assert.deepEqual(c.ids.value,['explicit']);assert.equal(c.target.value.id,5);assert.equal(m.comments.value.length,1);assert.equal(m.saving.value,false);assert.match(c.error.value,failure==='reject'?/回复目标/:/勿重复提交/);m.stop()}
 })
 await test(kind+': reply-only selection cannot submit an empty comment or leave silently',async()=>{
  const m=await mount(kind),c=controls(m);await c.reply(5);assert(c.dirty.value);await c.post({body:' \n',mentionUserIds:[]});assert.equal(m.calls.filter(call=>call.options.method==='POST').length,0)
  if(kind==='Requirements'){const pending=m.confirmDetailLeave();assert.equal(m.leavePrompt.value,true);await m.saveAndLeave();assert.equal(m.leavePrompt.value,true);m.finishLeave(false);assert.equal(await pending,false)}else assert.equal(m.canLeave(),false)
  assert.equal(c.target.value.id,5);c.cancel();assert.equal(c.dirty.value,false);m.stop()
 })
 await test(kind+': pending post blocks another submit, target cancellation, leaving and manual reload',async()=>{
  const pending=defer(),m=await mount(kind,{handler:async(_path,options)=>options.method==='POST'?pending.promise:undefined}),c=controls(m);await c.reply(5);c.body.value='正在发布';const sending=c.post({body:c.body.value,mentionUserIds:[]});await flush();const requestCount=m.calls.length
  await c.post({body:'重复提交',mentionUserIds:[]});await c.reload();c.cancel();assert.equal(c.target.value.id,5);assert.equal(m.calls.length,requestCount);assert.equal(kind==='Requirements'?m.confirmDetailLeave():m.canLeave(),false)
  pending.resolve({id:91,body:'正在发布',replyToId:5});await sending;assert.equal(m.saving.value,false);assert.equal(c.target.value,null);m.stop()
 })
 await test(kind+': late completion cannot erase a newer draft or mutate an unmounted discussion',async()=>{
  const pending=defer(),m=await mount(kind,{handler:async(_path,options)=>options.method==='POST'?pending.promise:undefined}),c=controls(m);await c.reply(5);c.body.value='原发送内容';const sending=c.post({body:c.body.value,mentionUserIds:[]});await flush();c.body.value='发送期间新草稿';c.ids.value=['new-id'];pending.resolve({id:92,body:'原发送内容',replyToId:5});await sending;assert.equal(c.body.value,'发送期间新草稿');assert.deepEqual(c.ids.value,['new-id']);assert.equal(c.target.value.id,5);m.stop()
  const late=defer(),n=await mount(kind,{handler:async(_path,options)=>options.method==='POST'?late.promise:undefined}),d=controls(n);await d.reply(5);d.body.value='已卸载';const stale=d.post({body:d.body.value,mentionUserIds:[]});await flush();n.stop();late.resolve({id:93,body:'不能写回',replyToId:5});await stale;assert.equal(n.comments.value.length,1);assert.equal(d.body.value,'已卸载');assert.equal(n.events.length,0)
 })
 await test(kind+': viewer cannot initiate or send a reply',async()=>{
  const m=await mount(kind,{viewer:true}),c=controls(m);await c.reply(5);assert.equal(c.target.value,null);await c.post({body:'无权回复',mentionUserIds:[]});assert.equal(m.calls.filter(call=>call.options.method==='POST').length,0);m.stop()
 })
}
await test('requirement image-only reply preserves pending attachment and mention payload without inventing text',async()=>{
 const m=await mount('Requirements'),c=controls(m);await c.reply(5);m.commentDoc.value=pendingImage();await c.post({body:'',mentionUserIds:['explicit'],contentDoc:m.commentDoc.value});const payload=JSON.parse(m.calls.find(call=>call.options.method==='POST').options.body);assert.equal(payload.replyToId,5);assert.equal(payload.body,'');assert.deepEqual(payload.contentDoc,pendingImage());assert.deepEqual(payload.mentionUserIds,['explicit']);assert.equal(m.commentDoc.value,null);assert.equal(c.target.value,null);assert.equal(m.resourceVersion.value,1);m.stop()
})
await test('requirements and defects distinguish a successful POST from failed refresh to avoid duplicate replies',async()=>{
 for(const kind of ['Requirements','Defects']){const m=await mount(kind,{handler:async(path,options)=>{if(path.endsWith('/comments')&&!options.method)throw Error('refresh offline')}}),c=controls(m);await c.reply(5);c.body.value='已保存回复';await c.post({body:c.body.value,mentionUserIds:[]});assert.equal(c.body.value,'');assert.equal(c.target.value,null);assert.equal(m.comments.value[0].replyToId,5);assert.match(c.error.value,/评论已发布.*刷新失败/);assert.equal(m.saving.value,false);m.stop()}
})
await test('test case, plan and execution replies all use their own exact resource endpoints',async()=>{
 for(const resource of ['test-cases','test-plans','test-executions']){const m=await mount('QualityComments',{resource});await m.reply(5);m.body.value='同一模块讨论';await m.submit({body:m.body.value,mentionUserIds:[]});assert.equal(m.calls.find(call=>call.options.method==='POST').path,'/'+resource+'/9/comments');assert.equal(m.comments.value[0].replyToId,5);m.stop()}
})
await test('an arbitrarily long valid chain renders only a one-hop quote and can target the last reply',async()=>{
 const m=await mount('QualityComments');m.comments.value=Array.from({length:20000},(_,index)=>({id:index+1,author:'用户',body:'层级 '+(index+1),replyToId:index||null}));await m.reply(20000);assert.deepEqual(m.replyTarget.value,{id:20000,author:'用户',body:'层级 20000'});assert.equal(m.commentMap.value.get(20000).replyToId,19999);assert.match(m.source,/v-for="comment in comments"/);assert(!/maxDepth|depth\s*[<>]/.test(m.source));m.stop()
})
await test('test-object changes invalidate in-flight replies and do not reset the new object after late success',async()=>{
 const pending=defer(),m=await mount('QualityComments',{handler:async(path,options)=>{if(options.method==='POST')return pending.promise;if(path.includes('/10/'))return{items:[{id:88,body:'新测试对象'}]}}});await m.reply(5);m.body.value='旧测试回复';const posting=m.submit({body:m.body.value,mentionUserIds:[]});await flush();m.props.objectId=10;await flush();m.body.value='新测试草稿';pending.resolve({id:99,body:'旧内容',replyToId:5});await posting;assert.equal(m.comments.value[0].id,88);assert.equal(m.body.value,'新测试草稿');assert.equal(m.replyTarget.value,null);assert.equal(m.saving.value,false);m.stop()
})
await test('switching defect targets hides the old composer before slow target details can complete',async()=>{
 const pending=defer(),m=await mount('Defects',{handler:async path=>path==='/defects/10'?pending.promise:undefined});await m.replyComment(5);const opening=m.open(10,false);await flush();assert.equal(m.selected.value,null);assert.deepEqual(m.comments.value,[]);assert.equal(m.commentReply.value,null);m.comment.value='新缺陷仍在加载';await m.replyComment(5);await m.addComment({body:m.comment.value,mentionUserIds:[]});assert.equal(m.calls.filter(call=>call.options.method==='POST').length,0,'must not POST to the previous defect during navigation');pending.resolve({id:10,title:'新缺陷',customFields:{}});await opening;assert.equal(m.selected.value.id,10);assert.equal(m.commentReply.value,null);m.stop()
})
await test('a route targeting another defect blocks posting before its asynchronous route watcher runs',async()=>{
 const pending=defer(),m=await mount('Defects',{handler:async path=>path==='/defects/10'?pending.promise:undefined});m.route.query.bug='10';await m.addComment({body:'不允许发送到旧缺陷',mentionUserIds:[]});assert.equal(m.calls.filter(call=>call.options.method==='POST').length,0);pending.resolve({id:10,title:'目标缺陷'});await flush();m.stop()
})
await test('parent test saves lock reply controls while retaining drafts and mentions on failure',async()=>{
 const m=await mount('QualityComments');await m.reply(5);m.body.value='父保存期间保留';m.mentionUserIds.value=['explicit'];m.props.disabled=true;await m.reply(3);m.cancelReply();await m.submit({body:m.body.value,mentionUserIds:['explicit']});assert.equal(m.blocked.value,true);assert.equal(m.replyTarget.value.id,5);assert.equal(m.body.value,'父保存期间保留');assert.deepEqual(m.mentionUserIds.value,['explicit']);assert.equal(m.canLeave(),false);assert.equal(m.calls.filter(call=>call.options.method==='POST').length,0);m.props.disabled=false;assert.equal(m.blocked.value,false);await m.submit({body:m.body.value,mentionUserIds:['explicit']});assert.equal(m.replyTarget.value,null);m.stop()
 const [library,operations]=await Promise.all([read('src/components/testing/TestCaseLibrary.vue'),read('src/components/testing/TestingOperations.vue')])
 const commentNodes=[]
 for(const source of [library,operations]){const {descriptor}=parse(source);function inspect(node){if(node.type===1&&node.tag==='QualityComments')commentNodes.push(node);for(const child of node.children||[])inspect(child)}inspect(descriptor.template.ast)}
 assert.equal(commentNodes.length,3)
 for(const node of commentNodes){const lock=node.props.find(prop=>prop.type===7&&prop.name==='bind'&&prop.arg?.content==='disabled');assert(lock?.exp?.content?.includes('saving'),'each parent detail must bind its save lock')}
})
await test('historical comments without an author ID remain valid reply targets',async()=>{
 for(const kind of ['Requirements','Defects','QualityComments']){const m=await mount(kind),c=controls(m);m.comments.value=[{id:5,author:'历史作者',authorUserId:'',body:'历史评论'}];await c.reply(5);assert.equal(c.target.value.id,5);c.body.value='回复历史评论';await c.post({body:c.body.value,mentionUserIds:[]});assert.equal(JSON.parse(m.calls.find(call=>call.options.method==='POST').options.body).replyToId,5);m.stop()}
})
await test('quality project-leave cancellation and identity locking protect reply-target-only drafts',async()=>{
 const m=await mount('QualityComments');await m.reply(5);const event=new Event('devflow-before-project-change',{cancelable:true});m.beforeProjectChange(event);assert.equal(event.defaultPrevented,true);assert.equal(m.replyTarget.value.id,5);m.confirmation.allowed=true;m.beforeProjectChange(new Event('devflow-before-project-change',{cancelable:true}));assert.equal(m.replyTarget.value.id,5,'confirm does not drop draft before actual navigation');m.cancelProjectLeave();m.confirmation.allowed=false;assert.equal(m.canLeave(),false);m.locked.value=true;await flush();assert.equal(m.replyTarget.value,null);assert.equal(m.comments.value.length,0);m.stop()
})
await test('all five reply notification types have English labels and retain their canonical AppSelect filter values',async()=>{
 const m=await mount('Notifications');for(const key of ['requirement','defect','test_case','test_plan','test_execution']){const event=key+'.replied';assert(m.eventNames[event]);assert.match(translations[m.eventNames[event]],/reply/i);assert(m.filterEventTypes.includes(event),'live reply event is selectable: '+event);assert.equal(m.eventOptions.value.find(option=>option.value===event)?.value,event);m.eventType.value=event;await flush();const request=m.calls.at(-1);assert.equal(new URL('http://local'+request.path).searchParams.get('eventType'),event)}assert.match(m.source,/filterEventTypes\.map\(value => \(\{ value, label: t\(eventNames\[value\]\) \}\)\)/);assert.match(m.source,/<AppSelect[^>]*:options="eventOptions"/);m.stop()
})
console.log(`Passed ${count} comment reply regressions.`)
