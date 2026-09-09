import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as lowlight from 'lowlight'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'
function module(path,imports={}){const exports={};new Function('require','exports',ts.transpileModule(readFileSync(new URL('../'+path,import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(id=>imports[id],exports);return exports}
const code=module('src/codeHighlight.ts',{lowlight}),assets=module('src/attachmentAssets.ts',{'./codeHighlight':code})
for(const [name,category,language] of [['component.vue','code','vue'],['service.php','code','php'],['main.go','code','go'],['file.cpp','code','cpp'],['openapi.yaml','api','yaml'],['bug.png','bug',''],['界面.fig','design',''],['unknown.zip','other','']]){assert.equal(assets.assetCategory(name),category);assert.equal(assets.assetLanguage(name),language)}
assert.equal(assets.assetCategory('file.go','design'),'design')
assert.equal(assets.assetLanguage('无后缀','<?php echo "安全";'),'php')
for(const raw of ['','\t中文 & <script>alert(1)</script>\n\n','package main\n'+('func main(){}\n'.repeat(12000))])assert.equal(assets.decodeAssetText(new TextEncoder().encode(raw)),raw)
assert.equal(assets.decodeAssetText(new Uint8Array([255,254,45,78,135,101])), '中文')
assert.throws(()=>assets.decodeAssetText(new Uint8Array([0,1,0,2])),/二进制/)
assert.throws(()=>assets.decodeAssetText(new Uint8Array([255,255,255])),/编码/)
for(const component of ['AssetIcon','AssetCodePreview','RequirementResources','RichTextAsset']){const source=readFileSync(new URL('../src/components/'+component+'.vue',import.meta.url),'utf8'),{descriptor}=parse(source),script=compileScript(descriptor,{id:component}),template=compileTemplate({source:descriptor.template.content,filename:component+'.vue',id:component,compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[]);assert.ok(!source.includes('v-html'))}
console.log('附件资产测试通过：分类、语言、完整解码、二进制/编码拒绝、安全模板。')

// 使用真实 setup 与响应式状态验证完整文件、分段和迟到请求，避免只检查模板字符串。
const previewSource=readFileSync(new URL('../src/components/AssetCodePreview.vue',import.meta.url),'utf8').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
const flush=async()=>{for(let n=0;n<10;n++)await Vue.nextTick()}
function preview(props,download){const state=Vue.reactive(props),stops=[],copied=[],scope=Vue.effectScope(),exports={};const imports={vue:{...Vue,onBeforeUnmount:fn=>stops.push(fn)},'../api':{apiDownload:download},'../attachmentAssets':assets,'../richText':{base64ToBytes:s=>new Uint8Array(Buffer.from(s,'base64'))},'../codeHighlight':code,'../i18n':{t:s=>s}};scope.run(()=>new Function('require','exports','defineProps','defineEmits','navigator',ts.transpileModule(previewSource+'\nexport {source,error,loading,page,pages,visible,copy}',{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(id=>imports[id],exports,()=>state,()=>()=>{},{clipboard:{writeText:async text=>copied.push(text)}}));return {...exports,state,copied,stop(){stops.forEach(fn=>fn());scope.stop()}}}
const raw='package main\n\t// 中文\n'+('func main(){}\n'.repeat(10000))+'\n'
const p=preview({name:'main.go',data:Buffer.from(raw).toString('base64')});await flush();assert.equal(p.source.value,raw);let assembled='';for(let n=0;n<p.pages.value;n++){p.page.value=n;assembled+=p.visible.value}assert.equal(assembled,raw);await p.copy();assert.deepEqual(p.copied,[raw]);p.stop()
let resolveOld;const old=new Promise(resolve=>resolveOld=resolve)
const pending=preview({name:'main.go',requirementId:1,attachmentId:2},path=>path.includes('/1/')?old:Promise.resolve(new Blob(['new code'])));pending.state.requirementId=3;await new Promise(setImmediate);await flush();assert.equal(pending.source.value,'new code');resolveOld(new Blob(['old private content']));await new Promise(setImmediate);await flush();assert.equal(pending.source.value,'new code');pending.stop()
const binary=preview({name:'fake.go',data:Buffer.from([0,1,2,0]).toString('base64')});await flush();assert.match(binary.error.value,/二进制/);assert.equal(binary.source.value,'');binary.stop()
console.log('代码预览运行测试通过：全文复制、分段拼回原文、跨需求迟到响应隔离、伪装代码的二进制拒绝。')
