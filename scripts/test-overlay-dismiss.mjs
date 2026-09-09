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
console.log(`遮罩关闭扫描通过：${checked} 个遮罩/原生对话框；面板内部点击不误关，关闭复用保存保护。`)
