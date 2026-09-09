import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import * as Core from '@tiptap/core'
import CodeBlockLowlight from '@tiptap/extension-code-block-lowlight'
import * as lowlight from 'lowlight'
import StarterKit from '@tiptap/starter-kit'
import * as marked from 'marked'
import { EditorState, TextSelection } from '@tiptap/pm/state'
import { Fragment, Slice } from '@tiptap/pm/model'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read = path => readFile(new URL('../' + path, import.meta.url), 'utf8')
const transpile = source => ts.transpileModule(source, {compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
function evaluate(source, imports={}) { const exports={};new Function('require','exports',transpile(source))(id=>imports[id],exports);return exports }
const mentions=evaluate(await read('src/mentions.ts'))
const rich=evaluate(await read('src/richText.ts'),{'./mentions':mentions})
const codeHighlight=evaluate(await read('src/codeHighlight.ts'),{'lowlight':lowlight})
const markdown=evaluate(await read('src/markdownImport.ts'),{'marked':marked,'./richText':rich})
const pasteHelpers=evaluate(await read('src/editorPaste.ts'),{'./codeHighlight':codeHighlight,'./markdownImport':markdown})
const source=await read('src/components/RichTextEditor.vue'), assetSource=await read('src/components/RichTextAsset.vue')
const people=[{id:'u_one',name:'同名',email:'one@example.test',active:true,projectRole:'frontend'},{id:'u_two',name:'同名',email:'two@example.test',active:true,projectRole:'backend'},{id:'u_old',name:'已停用',active:false}]
const paragraph=text=>({type:'paragraph',...(text?{content:[{type:'text',text}]}:{})})
const doc=(...content)=>({type:'doc',content})
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}
const flush=async()=>{for(let i=0;i<8;i++)await Vue.nextTick()}
const png=rich.base64ToBytes('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j9r0AAAAASUVORK5CYII=')
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('legacy text keeps exact paragraphs, blank lines and literal HTML instead of parsing it',()=>{
  const text='<script>alert(1)</script>\n\n标题 & 内容\n'
  const result=rich.plainTextToDocument(text)
  assert.equal(rich.richTextPlain(result),text);assert.equal(result.content[0].content[0].type,'text')
  assert.equal(result.content.length,4)
})
await test('legacy mentions use explicit stable IDs and historical names, never infer same-name recipients',()=>{
  const result=rich.plainTextToDocument('@同名 评审\n@历史姓名 备注\ncontact@同名.invalid',['u_two','historical'],{historical:'历史姓名'},people)
  assert.deepEqual(rich.richMentionState(result),{ids:['u_two','historical'],names:{u_two:'同名',historical:'历史姓名'}})
  assert.equal(rich.richMentionState(rich.plainTextToDocument('@同名 自由文本',[],{},people)).ids.length,0)
  const duplicate=rich.plainTextToDocument('@同名 评审',['u_one','u_two'],{},people)
  assert.deepEqual(rich.richMentionState(duplicate).ids,['u_one','u_two']);assert.equal(rich.richTextPlain(duplicate),'@同名 @同名 评审')
})
await test('rich mention atoms distinguish same-name people and derive backend-compatible word boundaries',()=>{
  const result=doc({type:'paragraph',content:[{type:'text',text:'请'},{type:'mention',attrs:{id:'u_one',label:'同名'}},{type:'text',text:'评审；'},{type:'mention',attrs:{id:'u_two',label:'同名'}},{type:'text',text:' OK'}]})
  assert.equal(rich.richTextPlain(result),'请 @同名 评审；@同名 OK')
  assert.deepEqual(rich.richMentionState(result).ids,['u_one','u_two'])
})
await test('lists, quotes, headings, attachments and image-only comments produce useful plain text',()=>{
  const result=doc({type:'heading',attrs:{level:2},content:[{type:'text',text:'目标'}]},{type:'bulletList',content:[{type:'listItem',content:[paragraph('一')]},{type:'listItem',content:[paragraph('二')]}]},{type:'blockquote',content:[paragraph('引用')]},{type:'image',attrs:{attachmentId:1,name:'截图.png'}},{type:'attachment',attrs:{attachmentId:2,name:'验收.pdf'}})
  assert.equal(rich.richTextPlain(result),'目标\n一\n二\n引用\n截图.png\n验收.pdf')
  assert.equal(rich.richTextPlain(doc({type:'image',attrs:{attachmentId:1,name:'截图.png',alt:'验收截图'}})),'验收截图')
})
await test('links allow explicit web/mail addresses and reject active, credentialed or ambiguous URLs',()=>{
  for(const value of ['https://example.test/a?b=1','http://example.test','mailto:team@example.test'])assert.equal(rich.safeRichLink(value),value)
  for(const value of ['javascript:alert(1)','data:text/html,a','file:///tmp/a','//example.test','/relative','https://user:pass@example.test','https://exam\nple.test','https://example.test\\evil','mailto:invalid'])assert.equal(rich.safeRichLink(value),null,value)
})
await test('document normalization drops unsafe attributes/resources and keeps only canonical marks',()=>{
  const value=doc({type:'paragraph',attrs:{onclick:'evil'},content:[{type:'text',text:'safe',marks:[{type:'bold',attrs:{onclick:'evil'}},{type:'link',attrs:{href:'javascript:alert(1)'}},{type:'unknown',attrs:{style:'evil'}}]}]},{type:'image',attrs:{src:'https://tracking.invalid/image.png',name:'tracking'}},{type:'attachment',attrs:{attachmentId:7,name:'safe.txt',data:'ZXZpbA==',src:'https://evil.invalid'}},{type:'image',attrs:{data:'data:image/png;base64,bad'}})
  const result=rich.normalizeRichDocument(value)
  assert.equal(result.content.length,2);assert.deepEqual(result.content[0].content[0].marks,[{type:'bold'}])
  assert.equal(Object.hasOwn(result.content[0],'attrs'),false);assert.deepEqual(result.content[1].attrs,{attachmentId:7,name:'safe.txt',alt:''})
})
await test('pending file helpers use pure base64, signature-based image types and bounded file names',async()=>{
  const result=await rich.readRichFiles([new File([png],'screenshot.bin',{type:'application/octet-stream'}),new File(['验收内容'],'验收.txt')])
  assert.equal(result[0].type,'image');assert.equal(result[1].type,'attachment');assert.equal(result[0].attrs.attachmentId,null)
  assert.ok(!result[0].attrs.data.startsWith('data:'));assert.deepEqual(rich.base64ToBytes(result[0].attrs.data),png)
  assert.equal(rich.pendingRichBytes(doc(...result)),png.length+new TextEncoder().encode('验收内容').length)
  assert.equal(rich.richAssetName('C:\\fakepath\\需求\u0000.txt'),'需求.txt')
  assert.equal(rich.richImageType(new Uint8Array([255,216,255,0])),'image/jpeg');assert.equal(rich.richImageType(new TextEncoder().encode('GIF89a')),'image/gif')
})
await test('empty, SVG, oversized and oversized-batch uploads fail before publishing partial assets',async()=>{
  await assert.rejects(rich.readRichFiles([new File([],'empty.txt')]),/空文件/)
  await assert.rejects(rich.readRichFiles([new File(['<svg/>'],'picture.svg')]),/SVG/)
  await assert.rejects(rich.readRichFiles([new File(['not image'],'fake.png')],0,true),/PNG/)
  await assert.rejects(rich.readRichFiles([{size:rich.MAX_RICH_FILE_BYTES+1,name:'huge.bin'}]),/10 MiB/)
  await assert.rejects(rich.readRichFiles([{size:2,name:'x'}],rich.MAX_RICH_DOCUMENT_BYTES-1),/20 MiB/)
})

class MemoryEditor {
  constructor(options){this.options=options;this.schema=Core.getSchema(options.extensions);this.state=EditorState.create({schema:this.schema,doc:this.schema.nodeFromJSON(options.content)});this.isEditable=options.editable;this.replacements=0;this.insertions=[];this.destroyed=false;this.view={dom:{setAttribute(){},removeAttribute(){}},coordsAtPos:()=>({left:0,top:0,bottom:20})};this.commands={focus:()=>true,setContent:(value)=>{this.replacements++;this.state=EditorState.create({schema:this.schema,doc:this.schema.nodeFromJSON(value)});return true}}}
  getJSON(){return this.state.doc.toJSON()}
  setEditable(value){this.isEditable=value}
  getAttributes(){return {}}
  isActive(){return false}
  can(){return {undo:()=>false,redo:()=>false}}
  destroy(){this.destroyed=true}
  chain(){const actions=[];let chain;chain=new Proxy({}, {get:(_target,name)=>name==='run'?()=>{for(const [command,...args]of actions){if(command==='insertContentAt'){const [range,value]=args,nodes=(Array.isArray(value)?value:[value]).map(node=>this.schema.nodeFromJSON(node));this.insertions.push({range,value});this.state=this.state.apply(this.state.tr.replaceRange(range.from,range.to,new Slice(Fragment.fromArray(nodes),0,0)));this.options.onUpdate?.()}else if(command==='insertContent'){const nodes=(Array.isArray(args[0])?args[0]:[args[0]]).map(node=>this.schema.nodeFromJSON(node));this.state=this.state.apply(this.state.tr.replaceSelection(new Slice(Fragment.fromArray(nodes),0,0)));this.options.onUpdate?.()}}return true}:(...args)=>{actions.push([name,...args]);return chain}});return chain}
}
async function component(input={}){
  const props=Vue.reactive({modelValue:'',document:null,mentionUserIds:[],mentionNames:{},members:people,requirementId:9,...input}),events=[],mounts=[],unmounts=[],exports={},scope=Vue.effectScope()
  const imports={vue:{...Vue,onMounted:callback=>mounts.push(callback),onBeforeUnmount:callback=>unmounts.push(callback),provide:()=>{},useId:()=> 'rich-test'},'@tiptap/core':Core,'@tiptap/vue-3':{Editor:MemoryEditor,EditorContent:{},VueNodeViewRenderer:()=>()=>{}},'@tiptap/starter-kit':{default:StarterKit},'@tiptap/extension-code-block-lowlight':{default:CodeBlockLowlight},'../codeHighlight':codeHighlight,'../i18n':{t:value=>value},'../mentions':mentions,'../richText':rich,'../markdownImport':markdown,'../editorPaste':pasteHelpers,'../useRecentMentions':{useRecentMentions:(directory,query)=>({matches:Vue.computed(()=>mentions.filterMentionMembers(directory(),query())),usable:Vue.ref(true),accepts:id=>directory().some(member=>member.id===id&&member.active!==false),remember:()=>{},badge:()=>''})}}
  const exposed=['editor','insertFiles','reading','error','onKeydown','updateMention','mention','matches','chooseMember','syncContent','focused','addEmoji','applyLink','linkSelection','linkValue','beginMarkdown','closeMarkdown','markdownOpen','markdownSource','insertMarkdown','readMarkdown','copyCode','codeMessage','handleClipboardPaste','smartPaste']
  const raw=source.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]+'\nexport {'+exposed.join(',')+'}'
  scope.run(()=>new Function('require','exports','defineProps','defineEmits','defineExpose',transpile(raw))(id=>id.endsWith('.vue')?{}:imports[id],exports,()=>props,()=>(...event)=>events.push(event),()=>{}))
  for(const mount of mounts)mount()
  return {...exports,props,events,stop:()=>{for(const unmount of unmounts)unmount();scope.stop()}}
}
await test('smart paste converts code and Markdown, preserves code-block content, and can be disabled',async()=>{
  const m=await component();let prevented=0
  const event=text=>({clipboardData:{files:[],items:[],getData:type=>type==='text/plain'?text:''},preventDefault(){prevented++}})
  const code='package main\nfunc main() {\n\tprintln("ok")\n}\n'
  assert.equal(m.handleClipboardPaste(event(code)),true);assert.equal(prevented,1)
  const block=m.editor.value.getJSON().content.find(node=>node.type==='codeBlock');assert.equal(block.attrs.language,'go');assert.equal(block.content[0].text,code)
  assert.equal(m.handleClipboardPaste(event('###### 六级标题\n\n- [x] 完成')),true)
  assert.ok(m.editor.value.getJSON().content.some(node=>node.type==='heading'&&node.attrs.level===6))
  m.smartPaste.value=false;assert.equal(m.handleClipboardPaste(event('# 保留源码')),false)
  m.smartPaste.value=true;m.editor.value.isActive=()=>true;assert.equal(m.handleClipboardPaste(event('# 代码里的注释')),false)
  m.stop()
})
await test('Markdown preview preserves the original until insertion and clears busy on cancel or scope change',async()=>{
  const m=await component({modelValue:'保留原文'}),before=JSON.stringify(m.editor.value.getJSON())
  m.beginMarkdown();m.markdownSource.value='# 新章节\n\n|字段|值|\n|---|---|\n|姓名|同名|'
  assert.equal(m.markdownOpen.value,true);assert.equal(JSON.stringify(m.editor.value.getJSON()),before)
  m.closeMarkdown();assert.equal(JSON.stringify(m.editor.value.getJSON()),before);assert.equal(m.events.at(-1)[1],false)
  m.beginMarkdown();m.markdownSource.value='新增内容';m.insertMarkdown()
  assert.match(rich.richTextPlain(m.editor.value.getJSON()),/新增内容/);assert.equal(m.markdownOpen.value,false)
  m.beginMarkdown();const pending=deferred(),request=m.readMarkdown({target:{value:'x',files:[{size:12,text:()=>pending.promise}]}})
  m.props.requirementId=10;await flush();pending.resolve('旧需求 Markdown');await request
  assert.equal(m.markdownSource.value,'');assert.equal(m.markdownOpen.value,false);assert.equal(m.events.filter(e=>e[0]==='busy').at(-1)[1],false)
  m.beginMarkdown();m.props.readonly=true;await flush();assert.equal(m.markdownOpen.value,false);m.stop()
})
await test('code copying keeps indentation and trailing newlines and reports clipboard failure',async()=>{
  const text='func main() {\n\tprintln("ok")\n}\n',m=await component({document:doc({type:'codeBlock',attrs:{language:'go'},content:[{type:'text',text}]})})
  const old=Object.getOwnPropertyDescriptor(navigator,'clipboard');let copied=''
  try {
    Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async value=>{copied=value}}})
    await m.copyCode();assert.equal(copied,text);assert.equal(m.codeMessage.value,'代码已复制')
    Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async()=>{throw Error('denied')}}})
    await m.copyCode();assert.match(m.codeMessage.value,/复制失败/)
  } finally { if(old)Object.defineProperty(navigator,'clipboard',old);else delete navigator.clipboard;m.stop() }
})
await test('the real Tiptap schema accepts required rich formatting and rejects unknown nodes',async()=>{
  const m=await component(),schema=m.editor.value.schema
  for(const name of ['doc','paragraph','heading','text','hardBreak','bulletList','orderedList','listItem','blockquote','codeBlock','horizontalRule','mention','image','attachment'])assert.ok(schema.nodes[name],name)
  for(const name of ['bold','italic','underline','strike','code','link'])assert.ok(schema.marks[name],name)
  assert.throws(()=>schema.nodeFromJSON({type:'iframe'}));assert.equal(schema.nodes.image.spec.parseDOM.length,0);assert.equal(schema.nodes.mention.spec.parseDOM.length,0)
  assert.equal(m.events.length,0,'mount does not dirty untouched plain text');m.stop()
})
await test('v-model echo does not replace the document, move selection or reset editing history',async()=>{
  const m=await component({modelValue:'original'}),instance=m.editor.value
  instance.state=instance.state.apply(instance.state.tr.insertText('!',4));instance.options.onUpdate()
  const text=m.events.find(event=>event[0]==='update:modelValue')[1],document=m.events.find(event=>event[0]==='update:document')[1],selection=instance.state.selection
  m.props.modelValue=text;m.props.document=document;await flush()
  assert.equal(instance.replacements,0);assert.equal(instance.state.selection,selection)
  m.props.document=structuredClone(document);await flush();assert.equal(instance.replacements,0,'equivalent cloned JSON also preserves history')
  m.stop()
})
await test('file paste publishes one bounded draft and busy state, without performing an upload',async()=>{
  const m=await component({modelValue:'正文'});await m.insertFiles([new File([png],'截图.png')])
  assert.deepEqual(m.events.filter(event=>event[0]==='busy'),[['busy',true],['busy',false]])
  const result=m.events.find(event=>event[0]==='update:document')[1]
  assert.ok(JSON.stringify(result).includes('"type":"image"'));assert.ok(JSON.stringify(result).includes('"attachmentId":null'));assert.equal(m.reading.value,false)
  m.stop()
})
await test('late file reads cannot mutate a different requirement or unmounted editor',async()=>{
  const pending=deferred(),m=await component({modelValue:'保留'})
  const request=m.insertFiles([{size:4,name:'wait.txt',arrayBuffer:()=>pending.promise}]);m.props.requirementId=10;await flush();pending.resolve(new TextEncoder().encode('body').buffer);await request
  assert.equal(m.editor.value.insertions.length,0);assert.equal(m.events.some(event=>event[0]==='update:document'),false);assert.equal(m.reading.value,false);m.stop()
  const late=deferred(),removed=await component(),instance=removed.editor.value,saving=removed.insertFiles([{size:4,name:'late.txt',arrayBuffer:()=>late.promise}]);removed.stop();late.resolve(new TextEncoder().encode('body').buffer);await saving
  assert.equal(instance.insertions.length,0);assert.equal(instance.destroyed,true)
})
await test('changing requirements replaces the editor scope even if their initial text is identical',async()=>{
  const m=await component({modelValue:'相同内容'}),previous=m.editor.value
  m.props.requirementId=10;await flush()
  assert.notEqual(m.editor.value,previous);assert.equal(previous.destroyed,true);assert.equal(rich.richTextPlain(m.editor.value.getJSON()),'相同内容')
  assert.equal(m.events.some(event=>event[0]==='update:document'),false);m.stop()
})
await test('read errors preserve text, and disabled/read-only editors cannot insert files or emojis',async()=>{
  const m=await component({modelValue:'原始内容'});await m.insertFiles([new File(['svg'],'unsafe.svg')]);assert.match(m.error.value,/SVG/);assert.equal(rich.richTextPlain(m.editor.value.getJSON()),'原始内容');m.stop()
  for(const flag of ['disabled','readonly']){const locked=await component({[flag]:true});await locked.insertFiles([new File([png],'safe.png')]);locked.addEmoji('😀');assert.equal(locked.events.length,0);assert.equal(locked.editor.value.insertions.length,0);locked.stop()}
})
await test('member dropdown searches IDs accurately and Enter chooses only the selected same-name account',async()=>{
  const m=await component({modelValue:'@同'}),instance=m.editor.value
  instance.state=instance.state.apply(instance.state.tr.setSelection(TextSelection.create(instance.state.doc,3)));m.focused.value=true;m.updateMention()
  assert.equal(m.matches.value.length,2);let prevented=0
  m.onKeydown({key:'ArrowDown',preventDefault:()=>prevented++});m.onKeydown({key:'Enter',preventDefault:()=>prevented++})
  const ids=m.events.find(event=>event[0]==='update:mentionUserIds')[1]
  assert.deepEqual(ids,['u_two']);assert.equal(prevented,2);assert.equal(rich.richTextPlain(instance.getJSON()),'@同名 ');m.stop()
})
await test('ordinary Enter remains a newline, IME is untouched and comment shortcut delegates submit',async()=>{
  const m=await component({mode:'comment'});let prevented=0
  assert.equal(m.onKeydown({key:'Enter',preventDefault:()=>prevented++}),false)
  assert.equal(m.onKeydown({key:'Enter',isComposing:true,preventDefault:()=>prevented++}),false)
  assert.equal(m.onKeydown({key:'Enter',ctrlKey:true,preventDefault:()=>prevented++}),true)
  assert.equal(prevented,1);assert.deepEqual(m.events,[['submit']]);m.stop()
})
await test('paste accepts only event-provided file data and retains filtered HTML through the schema path',async()=>{
  const m=await component();let prevented=0
  assert.equal(m.editor.value.options.editorProps.handlePaste(null,{clipboardData:{files:[],items:[]},preventDefault:()=>prevented++}),false)
  const files=[new File([png],'pasted.png')]
  assert.equal(m.editor.value.options.editorProps.handlePaste(null,{clipboardData:{files,items:[]},preventDefault:()=>prevented++}),true)
  await flush();assert.equal(prevented,1);assert.equal(m.editor.value.insertions.length,1)
  assert.equal(source.includes('navigator.clipboard.read'),false);m.stop()
})

async function asset({id=1,requirementId=9,data=null,kind='image',download}={}){
  const props=Vue.reactive({node:{type:{name:kind},attrs:{attachmentId:id,data,name:'截图.png'}},deleteNode:()=>{removed++},selected:false}),context=Vue.computed(()=>state),state=Vue.reactive({requirementId,readonly:false,disabled:false}),events=[],unmounts=[],scope=Vue.effectScope(),exports={}
  let removed=0,urlIndex=0
  const urls={createObjectURL:()=>{const url='blob:test-'+(++urlIndex);events.push(['create',url]);return url},revokeObjectURL:url=>events.push(['revoke',url])}
  const imports={vue:{...Vue,inject:()=>context,onMounted:()=>{},onBeforeUnmount:callback=>unmounts.push(callback)},'@tiptap/vue-3':{NodeViewWrapper:{},nodeViewProps:{}},'../api':{apiDownload:download||(async()=>new Blob([png]))},'../i18n':{t:value=>value},'../richText':rich}
  const raw=assetSource.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]+'\nexport {imageURL,loading,error,load,remove}'
  scope.run(()=>new Function('require','exports','defineProps','URL',transpile(raw))(id=>imports[id],exports,()=>props,urls))
  return {...exports,props,state,events,get removed(){return removed},stop:()=>{for(const unmount of unmounts)unmount();scope.stop()}}
}
await test('asset node views fetch authenticated requirement URLs and revoke previews on teardown',async()=>{
  const paths=[],m=await asset({download:async path=>{paths.push(path);return new Blob([png])}});await flush()
  assert.deepEqual(paths,['/requirements/9/attachments/1']);assert.match(m.imageURL.value,/^blob:/)
  const url=m.imageURL.value;m.stop();assert.ok(m.events.some(event=>event[0]==='revoke'&&event[1]===url))
})
await test('late asset downloads cannot display an old requirement image and readonly blocks removing it',async()=>{
  const pending=deferred(),m=await asset({download:path=>path.includes('/9/')?pending.promise:Promise.resolve(new Blob([png]))});m.state.requirementId=10;await flush()
  const current=m.imageURL.value;pending.resolve(new Blob([png]));await flush();assert.equal(m.imageURL.value,current);assert.equal(m.events.filter(event=>event[0]==='create').length,1)
  m.state.readonly=true;m.remove();assert.equal(m.removed,0);m.state.readonly=false;m.state.disabled=true;m.remove();assert.equal(m.removed,0);m.stop()
})
await test('both Vue templates compile and all interactive controls avoid implicit form submission',()=>{
  for(const [filename,content]of[['RichTextEditor.vue',source],['RichTextAsset.vue',assetSource]]){
    const {descriptor}=parse(content),script=compileScript(descriptor,{id:filename}),template=compileTemplate({source:descriptor.template.content,filename,id:filename,compilerOptions:{bindingMetadata:script.bindings}})
    assert.deepEqual(template.errors,[],filename)
    for(const button of content.matchAll(/<button\b[^>]*>/g))assert.match(button[0],/type="button"/,filename)
  }
})
console.log(`Passed ${count} rich-text tests.`)
