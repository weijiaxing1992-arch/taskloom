import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'
const read=path=>readFileSync(new URL('../'+path,import.meta.url),'utf8')
const compile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const helper={};new Function('exports',compile(read('src/releaseNotes.ts')))(helper)
const entry=(extra={})=>({category:'大模型类型',title:'支持导入文档',description:'已支持将文档导入变量替换流程。',requirementIds:[7],imageIds:[17],...extra})
const metadata=(extra={})=>({state:'none',revision:0,entries:[],categories:[...helper.releaseNoteCategories],markdown:'',canGenerate:true,canEdit:false,sourceChanged:false,configured:true,enabled:true,autoEnabled:false,model:'gpt-5-mini',baseUrl:'https://model.example/v1',settingsVersion:4,sourceCount:1,error:'',updatedAt:'',sprintName:'2026/09 版本',completedAt:'2026-09-10T00:00:00Z',images:[{id:17,requirementId:7,name:'原需求截图.png',url:'/api/requirements/7/attachments/17'}],sources:[{id:7,code:'REQ-7',title:'导入文档'}],...extra})
const draft=(extra={})=>metadata({state:'draft',revision:3,entries:[entry()],markdown:'# 2026/09 版本\n\n服务器 Markdown\n',canEdit:true,...extra})
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
const flush=async()=>{for(let i=0;i<8;i++){await Promise.resolve();await Vue.nextTick()}}
const png=()=>new Blob([new Uint8Array([137,80,78,71,13,10,26,10,0,0,0,0])],{type:'application/octet-stream'})
function fixture(handler=async()=>metadata(),download=async()=>png()){
 const source=read('src/components/ReleaseNotesPanel.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1],props=Vue.reactive({projectId:'p-a',sprintId:10,sprintName:'版本 10'}),scope=Vue.effectScope(),locked=Vue.ref(false),mounts=[],unmounts=[],calls=[],downloads=[],exports=[],emits=[],routes=[],timers=new Map(),listeners=new Map(),confirmations=[],created=[],revoked=[]
 let confirm=true,sequence=0,stopped=false
 const settingsScope={project:'p-a',locked,current:()=>!locked.value,request:async(path,options)=>{calls.push({path,options});return handler(path,options)}}
 const imports={vue:{...Vue,onMounted:fn=>mounts.push(fn),onBeforeUnmount:fn=>unmounts.push(fn)},'vue-router':{onBeforeRouteLeave:fn=>routes.push(fn),onBeforeRouteUpdate:fn=>routes.push(fn)},'../api':{apiDownload:async(path,options)=>{downloads.push({path,options});return download(path,options)}},'../i18n':{t:(value,params={})=>String(value).replace(/\{(\w+)\}/g,(all,key)=>String(params[key]??all)),formatDate:String},'../workItemExport':{downloadFile:(blob,name)=>exports.push({blob,name})},'../releaseNotes':helper,'./settingsScope':{useSettingsScope:()=>settingsScope}}
 const names='note,entries,loading,writing,editing,error,notice,conflict,imageURLs,imageErrors,imageLoading,dirty,pending,groups,missingImageCount,releaseReady,canGenerate,canEdit,load,generate,save,discard,availableImages,imagesFor,toggleImage,loadImage,toggleImagePreview,exportNotes,canLeave,close,setCaption,beforeProjectChange,cancelProjectLeave,beforeUnload',result={}
 scope.run(()=>new Function('require','exports','defineProps','defineEmits','defineExpose','window','setTimeout','clearTimeout','URL',compile(source+'\nexport {'+names+'}'))(id=>imports[id]||{},result,()=>props,()=>(...args)=>emits.push(args),()=>{},{confirm:message=>{confirmations.push(message);return confirm},addEventListener:(name,fn)=>listeners.set(name,fn),removeEventListener:name=>listeners.delete(name)},fn=>{const id=++sequence;timers.set(id,fn);return id},id=>timers.delete(id),{createObjectURL:blob=>{const url='blob:release-'+created.length;created.push({url,blob});return url},revokeObjectURL:url=>revoked.push(url)}))
 return {...result,props,locked,calls,downloads,exports,emits,routes,timers,listeners,confirmations,created,revoked,setConfirm(value){confirm=value},mount(){mounts.forEach(fn=>fn())},async tick(){const next=[...timers.values()];timers.clear();next.forEach(fn=>fn());await flush()},stop(){if(stopped)return;stopped=true;unmounts.forEach(fn=>fn());scope.stop()}}
}
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('seven fixed groups, exact local attachment routes and complete source coverage are enforced',()=>{
 assert.deepEqual(helper.releaseNoteCategories,['大模型类型','智能体类型','AI呼叫类型','CRM/短信/账单','管理端/代理端','API接口','其他'])
 const note=helper.parseReleaseNotes(draft());assert.equal(helper.releaseNoteGroups(note.entries).length,7);assert.equal(helper.releaseImagePath(note.images[0]),'/requirements/7/attachments/17')
 for(const url of ['https://external.example/p.png','//external.example/p.png','/api/requirements/8/attachments/17','/api/requirements/7/attachments/17?project=other'])assert.throws(()=>helper.parseReleaseNotes(draft({images:[{...note.images[0],url}]})))
 assert.throws(()=>helper.parseReleaseNotes(draft({entries:[entry({imageIds:[99]})]})))
 assert.throws(()=>helper.parseReleaseNotes(draft({entries:[entry(),entry()]})))
 assert.throws(()=>helper.parseReleaseNotes(draft({sources:[...note.sources,{id:8,code:'REQ-8',title:'Second'}]})))
 assert.throws(()=>helper.parseReleaseNotes(metadata({categories:[...note.categories].reverse()})))
})
await test('editable prose and captions match server limits without accepting executable markup or foreign images',()=>{
 const note=draft(),problem=e=>helper.releaseEntriesError([e],note.images,note.sources)
 assert.equal(problem(entry({title:'字'.repeat(120),description:'字'.repeat(2000),imageCaptions:{17:'图'.repeat(500)}})),'')
 for(const change of [{title:'字'.repeat(121)},{description:'字'.repeat(2001)},{description:'段落\n换行'},{title:'<script>'},{description:'https://external.example'},{imageCaptions:{17:''}},{imageCaptions:{18:' чужой'}},{requirementIds:[7,8]}])assert(problem(entry(change)))
 assert.equal(problem(entry({imageIds:[],imageCaptions:{}})),'')
})
await test('generation requires destination and cost consent, sends versioned POST once and polls background work',async()=>{
 const post=deferred();let reads=0
 const m=fixture(async(path,options)=>options?post.promise:++reads===1?metadata():draft())
 await m.load();m.setConfirm(false);await m.generate();assert.equal(m.calls.length,1);assert.match(m.confirmations[0],/https:\/\/model.example\/v1/);assert.match(m.confirmations[0],/费用/);assert.match(m.confirmations[0],/图片候选 ID 和文件名/);assert.match(m.confirmations[0],/不发送图片字节/)
 m.setConfirm(true);const request=m.generate();await m.generate();assert.equal(m.calls.length,2);assert.equal(m.canLeave(),false);assert.deepEqual(JSON.parse(m.calls[1].options.body),{confirmed:true,expectedRevision:0,expectedSettingsVersion:4})
 post.resolve(metadata({state:'queued',revision:1}));await request;assert.equal(m.pending.value,true);assert.equal(m.canLeave(),true);assert.equal(m.timers.size,1)
 await m.tick();assert.equal(m.note.value.state,'draft');assert.equal(m.timers.size,0);assert(!m.calls.some(c=>c.path.includes('/complete')));m.stop()
})
await test('failed jobs can retry; replacing a saved draft needs two confirmations and replaceDraft',async()=>{
 const m=fixture(async(path,options)=>options?metadata({state:'queued',revision:4}):draft());await m.load();await m.generate();assert.equal(m.confirmations.length,2);assert.equal(JSON.parse(m.calls.at(-1).options.body).replaceDraft,true);m.stop()
 const retry=fixture(async(path,options)=>options?metadata({state:'queued',revision:6}):metadata({state:'failed',revision:5,error:'提供商暂不可用'}));await retry.load();assert.equal(retry.canGenerate.value,true);await retry.generate();assert.deepEqual(JSON.parse(retry.calls[1].options.body),{confirmed:true,expectedRevision:5,expectedSettingsVersion:4});retry.stop()
})
await test('server permissions, disabled AI and no completed sources fail closed without paid requests',async()=>{
 for(const fields of [{canGenerate:false},{configured:false},{enabled:false},{canGenerate:false,sourceCount:0},{state:'generating'}]){
  const m=fixture(async()=>metadata(fields));await m.load();await m.generate();assert.equal(m.calls.length,1);assert.equal(m.confirmations.length,0);m.stop()
 }
})
await test('editing title, explanation, category and caption saves only a versioned entry patch; failures preserve edits',async()=>{
 const saving=deferred(),m=fixture(async(path,options)=>options?saving.promise:draft());await m.load();m.editing.value=true;m.entries.value[0].title='已审核的功能标题';m.entries.value[0].description='人工核实后的说明';m.entries.value[0].category='API接口';m.setCaption(m.entries.value[0],m.note.value.images[0],'真实截图的人工图注')
 const request=m.save();await m.save();assert.equal(m.calls.length,2);assert.equal(m.canLeave(),false);const body=JSON.parse(m.calls[1].options.body);assert.equal(m.calls[1].options.method,'PATCH');assert.equal(body.expectedRevision,3);assert.equal(body.entries[0].imageCaptions[17],'真实截图的人工图注');assert.deepEqual(Object.keys(body).sort(),['entries','expectedRevision'])
 saving.reject(Object.assign(Error('版本冲突'),{status:409}));await request;assert.equal(m.dirty.value,true);assert.equal(m.conflict.value,true);assert.equal(m.entries.value[0].title,'已审核的功能标题');assert.equal(m.canGenerate.value,false)
 await m.save();assert.equal(m.calls.length,2);m.setConfirm(false);await m.load();assert.equal(m.calls.length,2);assert.equal(m.canLeave(),false);m.setConfirm(true);await m.load();assert.equal(m.conflict.value,false);assert.equal(m.dirty.value,false);m.stop()
})
await test('reviewers can select, remove and caption only owned real images with an eight-image limit',async()=>{
 const images=Array.from({length:9},(_,index)=>({id:17+index,requirementId:7,name:`真实截图-${index+1}.png`,url:`/api/requirements/7/attachments/${17+index}`}))
 const m=fixture(async()=>draft({entries:[entry({imageIds:[]})],images}));await m.load();m.editing.value=true
 assert.equal(m.availableImages(m.entries.value[0]).length,9);assert.equal(m.missingImageCount.value,1);assert.equal(m.releaseReady.value,false)
 for(const image of images.slice(0,8))m.toggleImage(m.entries.value[0],image,true)
 await flush();assert.deepEqual(m.entries.value[0].imageIds,images.slice(0,8).map(image=>image.id));assert.equal(m.missingImageCount.value,0);assert.equal(m.releaseReady.value,false)
 m.toggleImage(m.entries.value[0],images[8],true);assert.match(m.error.value,/最多选择 8 张/);assert.equal(m.entries.value[0].imageIds.length,8)
 m.setCaption(m.entries.value[0],images[0],'人工核验后的图注');m.toggleImage(m.entries.value[0],images[0],false);assert.equal(m.entries.value[0].imageIds.includes(images[0].id),false);assert.equal(m.entries.value[0].imageCaptions,undefined)
 const foreign={id:99,requirementId:8,name:'其他需求.png',url:'/api/requirements/8/attachments/99'};m.toggleImage(m.entries.value[0],foreign,true);assert.equal(m.entries.value[0].imageIds.includes(99),false);m.stop()
})
await test('network poll failure stops retries and refresh recovers without duplicate writes',async()=>{
 let failed=false;const m=fixture(async()=>{if(failed)throw Error('Offline');return metadata({state:'queued',revision:1})});await m.load();failed=true;await m.tick();assert.equal(m.timers.size,0);assert.equal(m.note.value.state,'queued');assert.match(m.error.value,/Offline/);failed=false;await m.load();assert.equal(m.timers.size,1);assert(m.calls.every(c=>!c.options));m.stop()
})
await test('sprint changes invalidate old reads, writes and image results before they can cross contexts',async()=>{
 const old=deferred();let first=true;const m=fixture(async()=>first?(first=false,old.promise):metadata({sprintName:'新迭代'}));const loading=m.load();m.props.sprintId=11;await flush();old.resolve(draft());await loading;assert.equal(m.note.value.sprintName,'新迭代');assert.equal(m.note.value.entries.length,0);assert.equal(m.calls.at(-1).path,'/sprints/11/release-notes');m.stop()
 for(const action of ['generate','save']){
  const pending=deferred(),m=fixture(async(path,options)=>options?pending.promise:draft());await m.load();if(action==='save')m.entries.value[0].title='人工修改';const write=m[action]();m.locked.value=true;pending.resolve(draft());await write;assert.equal(m.note.value,null);assert.deepEqual(m.entries.value,[]);assert.equal(m.timers.size,0);m.stop()
 }
})
await test('preview downloads carry exact project header, sniff bitmaps and revoke URLs on reload or unmount',async()=>{
 const m=fixture(async()=>draft());await m.load();await m.loadImage(m.note.value.images[0]);assert.equal(m.downloads[0].path,'/requirements/7/attachments/17');assert.equal(m.downloads[0].options.headers['X-TaskLoom-Project'],'p-a');assert.equal(m.created[0].blob.type,'image/png')
 await m.loadImage(m.note.value.images[0]);assert.equal(m.downloads.length,1);await m.load();assert.deepEqual(m.revoked,['blob:release-0']);await m.loadImage(m.note.value.images[0]);m.stop();assert.deepEqual(m.revoked,['blob:release-0','blob:release-1']);assert.equal(m.downloads[1].options.signal.aborted,true)
 const evil=fixture(async()=>draft(),async()=>new Blob(['<svg onload="evil()">']));await evil.load();await evil.loadImage(evil.note.value.images[0]);assert.equal(evil.created.length,0);assert.match(evil.imageErrors.value[17],/安全预览/);evil.stop()
})
await test('late image download cannot survive a project switch; a failed metadata refresh does not strand image loading',async()=>{
 const image=deferred();let failed=false;const m=fixture(async()=>{if(failed)throw Error('Offline');return draft()},()=>image.promise);await m.load();const loadImage=m.loadImage(m.note.value.images[0]);failed=true;await m.load();image.resolve(png());await loadImage;assert.equal(m.imageLoading.value[17],false);assert.equal(m.created.length,1);m.props.projectId='p-b';assert.deepEqual(m.revoked,['blob:release-0']);assert.equal(m.note.value,null);m.stop()
 const later=deferred(),n=fixture(async()=>draft(),()=>later.promise);await n.load();const old=n.loadImage(n.note.value.images[0]);n.locked.value=true;later.resolve(png());await old;assert.equal(n.created.length,0);n.stop()
})
await test('exports use saved server Markdown and portable JSON metadata, not unsaved text or private settings',async()=>{
 const m=fixture(async()=>draft());await m.load();m.exportNotes('md');m.exportNotes('json');assert.equal(m.exports[0].name,'2026_09 版本.md');assert.equal(await m.exports[0].blob.text(),m.note.value.markdown)
 const data=JSON.parse(await m.exports[1].blob.text());assert.deepEqual(data.categories,helper.releaseNoteCategories);assert.equal(data.entries[0].title,'支持导入文档');assert.equal('baseUrl' in data,false);assert.equal('canEdit' in data,false)
 m.entries.value[0].title='未保存';m.exportNotes('md');assert.equal(m.exports.length,2);m.stop()
 const ordered=JSON.parse(helper.releaseNoteJSON(draft({entries:[entry({category:'其他',requirementIds:[9],imageIds:[]}),entry({category:'API接口',requirementIds:[8],imageIds:[]}),entry({category:'API接口',requirementIds:[7],imageIds:[]})]})))
 assert.deepEqual(ordered.entries.map(item=>[item.category,item.requirementIds[0]]),[['API接口',7],['API接口',8],['其他',9]])
})
await test('navigation, project change and unload guard dirty edits while close/unmount cancel background polls',async()=>{
 const m=fixture(async()=>draft());m.mount();await flush();m.entries.value[0].title='未保存';m.setConfirm(false);let prevented=false;m.beforeProjectChange({preventDefault(){prevented=true}});assert.equal(prevented,true);assert(m.routes.every(guard=>guard()===false));m.close();assert.equal(m.emits.some(([event])=>event==='close'),false)
 m.setConfirm(true);m.beforeProjectChange({preventDefault(){throw Error('should leave')}});assert.equal(m.canLeave(),true);m.cancelProjectLeave();m.setConfirm(false);assert.equal(m.canLeave(),false);m.stop();assert.equal(m.listeners.size,0)
 const queued=fixture(async()=>metadata({state:'queued'}));await queued.load();queued.close();assert.equal(queued.timers.size,0);assert(queued.emits.some(([event])=>event==='close'));queued.stop()
})
await test('drawer template compiles, uses AIButton, contains real-image placeholders and never injects HTML',()=>{
 const source=read('src/components/ReleaseNotesPanel.vue'),{descriptor,errors}=parse(source);assert.deepEqual(errors,[]);const script=compileScript(descriptor,{id:'release'}),template=compileTemplate({source:descriptor.template.content,filename:'ReleaseNotesPanel.vue',id:'release',compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[])
 assert.match(source,/<OrganizationModal[^>]*wide/);assert.match(source,/<AIButton/);assert.match(source,/待补图/);assert.doesNotMatch(source,/v-html|<img[^>]*:src="image\.url"/)
 assert.match(source,/<AppSelect/);assert.match(source,/选择真实配图/);assert.match(source,/发布检查/)
})
console.log(count+' release-notes checks passed')
