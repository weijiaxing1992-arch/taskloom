import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { themedColor, themePalettePlugin } from './theme-palette.mjs'
const read = path => readFileSync(new URL('../'+path, import.meta.url), 'utf8')
function evaluate(source, imports, globals = {}) {
 const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
 const exports = {}; new Function('require','exports',...Object.keys(globals),code)(id => imports[id] || {},exports,...Object.values(globals)); return exports
}
const handlers = new Map(), timeouts = new Map(), timezone = Vue.ref('Asia/Shanghai'), document = {documentElement:{dataset:{},style:{}},addEventListener:(event,fn)=>handlers.set('document-'+event,fn),removeEventListener:event=>handlers.delete('document-'+event)}
const window = {setInterval:(fn,ms)=>{timeouts.set(1,{fn,ms});return 1},clearInterval:key=>timeouts.delete(key),addEventListener:(event,fn)=>handlers.set('window-'+event,fn),removeEventListener:event=>handlers.delete('window-'+event)}
const theme = evaluate(read('src/theme.ts'),{vue:Vue,'./i18n':{timezone}},{document,window})
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('automatic theme switches at exact local 07:00 and 19:00 boundaries',()=>{
 for(const [date,result] of [['2026-09-02T22:59:59Z','dark'],['2026-09-02T23:00:00Z','light'],['2026-09-03T10:59:59Z','light'],['2026-09-03T11:00:00Z','dark']])assert.equal(theme.resolveTheme('auto',new Date(date),'Asia/Shanghai'),result)
 assert.equal(theme.resolveTheme('auto',new Date('2026-09-03T11:00:00Z'),'UTC'),'light')
 assert.equal(theme.resolveTheme('auto',new Date('2026-09-03T00:00:00Z'),'America/New_York'),'dark')
 assert.equal(theme.resolveTheme('auto',new Date('2026-09-03T11:00:00Z'),'invalid'),'light')
})
await test('manual themes ignore clock and account reset applies light',()=>{
 for(const mode of ['light','dark']){theme.applyThemePreferences({themeMode:mode});assert.equal(document.documentElement.dataset.theme,mode);assert.equal(document.documentElement.style.colorScheme,mode);assert.equal(theme.resolveTheme(mode,new Date('2026-09-03T02:00:00Z')),mode)}
 theme.applyThemePreferences({themeMode:'constructor'});assert.equal(theme.themeMode.value,'light')
})
await test('auto clock watches timezone, focus and wakeup and removes all resources',async()=>{
 const stop=theme.startThemeClock();assert.equal(timeouts.get(1).ms,30000);assert.equal(handlers.size,2)
 theme.applyThemePreferences({themeMode:'auto'});timezone.value='UTC';await Vue.nextTick();assert.equal(theme.resolvedTheme.value,theme.resolveTheme('auto',new Date(),'UTC'));handlers.get('window-focus')();handlers.get('document-visibilitychange')();stop();assert.equal(timeouts.size,0);assert.equal(handlers.size,0)
})
await test('theme save failure rolls back and an outdated response cannot replace another account',async()=>{
 for(const success of [true,false]){
  let resolve,reject;const pending=new Promise((a,b)=>{resolve=a;reject=b}),unmounts=[],calls=[]
  const source=read('src/components/ThemePreference.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]+'\nexport {change,saving,error}'
  const m=evaluate(source,{vue:{...Vue,onBeforeUnmount:fn=>unmounts.push(fn)},'../theme':theme,'../i18n':{t:x=>x,timezone},'../api':{api:async(path,options)=>{calls.push({path,options});return pending}}})
  theme.applyThemePreferences({themeMode:'light'});const request=m.change('dark');await m.change('auto');assert.equal(calls.length,1);assert.deepEqual(JSON.parse(calls[0].options.body),{themeMode:'dark'})
  theme.applyThemePreferences({themeMode:'auto'});if(success)resolve({themeMode:'dark'});else reject(Error('offline'));await request;assert.equal(theme.themeMode.value,'auto');assert.equal(m.saving.value,false);unmounts.forEach(fn=>fn())
 }
})
await test('palette adapts surfaces and text without changing images, white button text or selected tag colors',()=>{
 assert.match(themedColor('background','#fff'),/palette-surface/);assert.match(themedColor('color','#344054'),/palette-text/);assert.match(themedColor('border','white'),/palette-border/)
 assert.equal(themedColor('color','white'),'white');assert.equal(themedColor('background','#665fe8'),'#665fe8');assert.equal(themedColor('background','#18223c28'),'#18223c28')
 const plugin=themePalettePlugin(),decl={prop:'background',value:'url(data:image/svg+xml,#fff)',source:{input:{file:'test.css'}}};plugin.Declaration(decl);assert.equal(decl.value,'url(data:image/svg+xml,#fff)')
 const preview={prop:'background',value:'#fff',source:{input:{file:'ThemePreference.vue'}}};plugin.Declaration(preview);assert.equal(preview.value,'#fff')
})
await test('failed PATCH really restores the saved mode and retry commits without unrelated preference fields',async()=>{
 let shouldFail=true
 const calls=[],unmounts=[]
 const source=read('src/components/ThemePreference.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]+'\nexport {change,saving,error}'
 const m=evaluate(source,{vue:{...Vue,onBeforeUnmount:fn=>unmounts.push(fn)},'../theme':theme,'../i18n':{t:x=>x,timezone},'../api':{api:async(path,options)=>{calls.push({path,options});if(shouldFail)throw Error('save failed');return{themeMode:JSON.parse(options.body).themeMode}}}})
 theme.applyThemePreferences({themeMode:'light'});await m.change('dark');assert.equal(theme.themeMode.value,'light');assert.equal(document.documentElement.dataset.theme,'light');assert.equal(m.error.value,'save failed');assert.equal(m.saving.value,false)
 shouldFail=false;await m.change('dark');assert.equal(theme.themeMode.value,'dark');assert.equal(document.documentElement.dataset.theme,'dark');assert.equal(m.error.value,'');assert.equal(m.saving.value,false);assert.deepEqual(JSON.parse(calls.at(-1).options.body),{themeMode:'dark'});unmounts.forEach(fn=>fn())
})
await test('unmount suppresses local notices but a failed optimistic theme still restores the global saved mode',async()=>{
 let reject;const pending=new Promise((_,no)=>{reject=no}),unmounts=[]
 const source=read('src/components/ThemePreference.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]+'\nexport {change,error}'
 const m=evaluate(source,{vue:{...Vue,onBeforeUnmount:fn=>unmounts.push(fn)},'../theme':theme,'../i18n':{t:x=>x,timezone},'../api':{api:()=>pending}})
 theme.applyThemePreferences({themeMode:'dark'});const saving=m.change('light');assert.equal(theme.themeMode.value,'light');unmounts.forEach(fn=>fn());reject(Error('offline'));await saving;assert.equal(theme.themeMode.value,'dark');assert.equal(m.error.value,'')
})
await test('automatic mode handles fractional offsets and daylight-saving dates at the promised local boundaries',()=>{
 for(const [date,zone,expected] of [['2026-09-03T01:14:59Z','Asia/Kathmandu','dark'],['2026-09-03T01:15:00Z','Asia/Kathmandu','light'],['2026-09-03T13:15:00Z','Asia/Kathmandu','dark'],['2026-03-08T10:59:59Z','America/New_York','dark'],['2026-03-08T11:00:00Z','America/New_York','light'],['2026-11-01T11:59:59Z','America/New_York','dark'],['2026-11-01T12:00:00Z','America/New_York','light']])assert.equal(theme.resolveTheme('auto',new Date(date),zone),expected,date+' '+zone)
})
await test('dark semantic text/tints and the white primary button retain readable contrast',()=>{
 const css=read('src/theme.css'),tokens=new Map([...css.matchAll(/(--(?:palette-)[\w-]+):\s*(#[a-f\d]{6})/gi)].map(match=>[match[1],match[2]]))
 const luminance=hex=>[1,3,5].map(offset=>{const value=parseInt(hex.slice(offset,offset+2),16)/255;return value<=.04045?value/12.92:((value+.055)/1.055)**2.4}).reduce((sum,value,index)=>sum+value*[.2126,.7152,.0722][index],0)
 const ratio=(a,b)=>(Math.max(luminance(a),luminance(b))+.05)/(Math.min(luminance(a),luminance(b))+.05)
 for(const name of ['blue','purple','green','amber','red'])assert.ok(ratio(tokens.get('--palette-text-'+name),tokens.get('--palette-tint-'+name))>=4.5,name+' text/tint contrast')
 for(const foreground of ['--palette-text','--palette-muted'])for(const background of ['--palette-surface','--palette-subtle'])assert.ok(ratio(tokens.get(foreground),tokens.get(background))>=4.5,foreground+' '+background)
 const primary=css.match(/:root\[data-theme="dark"\] \.btn\.primary\{([^}]+)\}/)[1],background=primary.match(/background:(#[a-f\d]{6})/i)[1];assert.ok(ratio('#ffffff',background)>=4.5);assert.equal(themedColor('color','white'),'white')
 assert.match(css,/:root\[data-theme="dark"\] :is\(\.requirement-tag,\.colored-tag,\.sprint-assignees>span\)\[style\*="--tag-dark-fg"\]\{--tag-foreground:var\(--tag-dark-fg\)\}/)
})
await test('boards, settings surfaces and native menus use defined semantic tokens in dark mode',()=>{
 const css=read('src/theme.css')
 assert.match(css,/--surface-soft:var\(--surface-subtle\)/)
 assert.match(css,/--text:var\(--ink/)
 assert.match(css,/select option,select optgroup[^}]+background:var\(--surface-raised\);color:var\(--ink\)/)
 assert.match(css,/\[style\*="--tag-dark-fg"\]\{--tag-foreground:var\(--tag-dark-fg\)\}/)
 for(const selector of ['.requirement-status-board>section','.defect-status-board>section','.iteration-board .board-col','.requirement-board-card','.application-settings .workflow-matrix .allowed-state'])assert(css.includes(selector),selector)
 assert.match(css,/--control-selected:#6255d9;--control-selected-text:#fff/)
 // Reka wraps the content, so inherited scoped root styles cannot set its flex layout.
 const transition=read('src/components/RequirementTransition.vue')
 assert.match(transition,/class="transition-menu flex flex-col overflow-hidden [^"]*p-0"/)
 assert.match(transition,/\.transition-options\{min-height:0;flex:1;/)
})
console.log(`Passed ${count} theme tests.`)
