import assert from 'node:assert/strict'
import {readFileSync,readdirSync} from 'node:fs'
import {parse} from 'vue/compiler-sfc'
import {withModifiers} from 'vue'
let checked=0
function walk(path){for(const entry of readdirSync(path,{withFileTypes:true})){const file=path+'/'+entry.name;if(entry.isDirectory()){walk(file);continue}if(!file.endsWith('.vue'))continue;const {descriptor}=parse(readFileSync(file,'utf8'));function visit(node){if(node.type===1){const classes=node.props.find(prop=>prop.type===6&&prop.name==='class')?.value?.content||'';if(/(?:^|\s)[\w-]*(?:shade|backdrop)(?:\s|$)/.test(classes)&&node.tag!=='button'){const click=node.props.find(prop=>prop.type===7&&prop.name==='on'&&prop.arg?.content==='click');assert.ok(click,file+': missing backdrop dismissal');assert.ok(click.modifiers.some(m=>(m.content||m)==='self'),file+': content clicks must not dismiss');checked++}if(node.tag==='dialog'){assert.ok(node.props.some(prop=>prop.type===7&&prop.name==='on'&&prop.arg?.content==='click'&&prop.modifiers.some(m=>(m.content||m)==='self')),file+': native dialog backdrop must dismiss');checked++}}for(const child of node.children||[])visit(child)}visit(descriptor.template.ast)}}
walk(new URL('../src',import.meta.url).pathname)
assert.ok(checked>=30)
let closes=0;const target={},child={};const handler=withModifiers(()=>closes++,['self']);handler({target:child,currentTarget:target});assert.equal(closes,0);handler({target,currentTarget:target});assert.equal(closes,1)
const design=readFileSync(new URL('../src/components/testing/TestDesigns.vue',import.meta.url),'utf8');assert.match(design,/function closeLink\(\)\{if\(saving.value\)return/)
// 迭代页的“创建缺陷”位于可 inert 的工作区内。遮罩必须 Teleport 到 body，
// 否则它既可能被工作区层级裁切，也会跟随底层一起变成不可点击的 inert 内容。
// requestClose 仍是唯一关闭入口，继续复用保存中与未保存草稿保护。
const defectComposer=readFileSync(new URL('../src/components/DefectComposer.vue',import.meta.url),'utf8')
assert.match(defectComposer,/<component :is="OverlayHost" v-bind="canTeleport\?\{to:'body'\}:\{\}">\s*<div class="drawer-shade defect-composer-shade" @click\.self="requestClose">/)
assert.match(defectComposer,/const canTeleport=typeof document!=='undefined'&&!!document\.body/)
assert.match(defectComposer,/const OverlayHost=canTeleport\?Teleport:'div'/)
assert.match(defectComposer,/function requestClose\(\)\{if\(saving\.value\|\|disposed\|\|!confirmLeave\(\)\)return false/)
assert.match(defectComposer,/\.defect-composer-shade\{[^}]*position:fixed;[^}]*inset:0;[^}]*z-index:1200;[^}]*min-height:100dvh;[^}]*overflow:hidden/)
assert.match(defectComposer,/\.defect-composer-drawer\{height:100dvh;max-height:100dvh/)
console.log(`遮罩关闭扫描通过：${checked} 个遮罩/原生对话框；面板内部点击不误关，关闭复用保存保护。`)
