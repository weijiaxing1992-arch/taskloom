import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'
const root = new URL('../', import.meta.url)
const transpile = source => ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
async function helper(path, imports = {}) { const output={};new Function('exports','require',transpile(await readFile(new URL(path,root),'utf8')))(output,key=>imports[key]);return output }
const dates=await helper('src/calendarDates.ts'),holidays=await helper('src/calendarHolidays.ts',{'./calendarDates':dates})
const source=await readFile(new URL('src/components/DatePicker.vue',root),'utf8'),{descriptor}=parse(source),compiled=compileScript(descriptor,{id:'test-calendar'})
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}
function mount(extra={}){
 const props=Vue.reactive({modelValue:'2026-09-04',mode:'date',disabled:false,min:'',max:'',required:false,error:'',...extra}),events=[],hooks=[],scope=Vue.effectScope(),out={}
 const imports={vue:{...Vue,useAttrs:()=>({}),useId:()=> 'calendar-test',onBeforeUnmount:fn=>hooks.push(fn)},'../i18n':{locale:Vue.ref('zh-CN'),t:(text,params={})=>text.replace(/\{(\w+)\}/g,(_,key)=>params[key]??'')},'./ui/popover':{},'../calendarDates':dates,'../calendarHolidays':holidays}
 new Function('require','exports',transpile(compiled.content))(id=>imports[id],out)
 const state=scope.run(()=>out.default.setup(props,{expose:()=>{},emit:(event,value)=>{events.push([event,value]);if(event==='update:modelValue')props.modelValue=value}}))
 const input={value:props.modelValue,inert:false,validity:'',focused:0,closest(){return this.inert?this:null},setCustomValidity(value){this.validity=value},focus(){this.focused++},reportValidity(){return !this.validity}}
 state.input.value=Vue.markRaw(input)
 return {...state,props,events,element:input,updates:()=>events.filter(([name])=>name==='update:modelValue').map(([,value])=>value),type(value){input.value=value;state.typed({target:input})},stop(){hooks.forEach(fn=>fn());scope.stop()}}
}
function key(value){return{key:value,shiftKey:false,defaultPrevented:false,stopped:false,preventDefault(){this.defaultPrevented=true},stopPropagation(){this.stopped=true},stopImmediatePropagation(){this.stopped=true}}}
await test('Gregorian dates reject impossible days, year zero, whitespace and datetime strings',()=>{
 for(const day of ['2024-02-29','2000-02-29','0001-01-01','9999-12-31'])assert(dates.isCalendarDate(day),day)
 for(const day of ['2026-02-29','1900-02-29','2026-04-31','0000-01-01','2026-9-04',' 2026-09-04','2026-09-04T00:00:00Z','2026-00-01'])assert(!dates.isCalendarDate(day),day)
 assert.equal(dates.addCalendarDays('0001-01-01',1),'0001-01-02');assert.equal(dates.addCalendarDays('2026-12-31',1),'2027-01-01');assert.equal(dates.addCalendarDays('9999-12-31',1),'')
 assert.equal(dates.shiftCalendarMonth('2026-01-31',1),'2026-02-28');assert.equal(dates.shiftCalendarMonth('2024-03-31',-1),'2024-02-29')
})
await test('creation defaults use the containing Monday–Sunday week Friday, including weekend',()=>{
 for(const day of ['2026-08-31','2026-09-01','2026-09-02','2026-09-03','2026-09-04','2026-09-05','2026-09-06'])assert.deepEqual(dates.defaultSprintDates(day),{startDate:'2026-09-04',endDate:'2026-09-10',name:'0904迭代'})
 assert.deepEqual(dates.defaultSprintDates('2026-09-07'),{startDate:'2026-09-11',endDate:'2026-09-17',name:'0911迭代'})
 assert.equal(dates.calendarToday(new Date('2026-09-03T17:00:00Z')),'2026-09-04')
 assert.deepEqual(dates.defaultSprintDates('2025-12-31'),{startDate:'2026-01-02',endDate:'2026-01-08',name:'0102迭代'})
})
await test('changing start links only untouched defaults and preserves manual name/end including blanks',()=>{
 const previous={startDate:'2026-09-04',endDate:'2026-10-31',name:'客户自定义版本'}
 assert.deepEqual(dates.linkedSprintDates('2026-09-18',previous,{name:false,endDate:false}),{startDate:'2026-09-18',endDate:'2026-09-24',name:'0918迭代'})
 assert.deepEqual(dates.linkedSprintDates('2026-09-18',previous,{name:true,endDate:true}),{...previous,startDate:'2026-09-18'})
 assert.deepEqual(dates.linkedSprintDates('2026-09-18',{...previous,name:'',endDate:''},{name:true,endDate:true}),{startDate:'2026-09-18',endDate:'',name:''})
})
await test('2025/2026 official holidays and workdays are exact, ordinary weekends never claimed statutory',()=>{
 for(const day of ['2025-01-01','2025-02-04','2025-06-02','2025-10-08','2026-01-03','2026-02-23','2026-05-05','2026-09-27','2026-10-07'])assert.equal(holidays.calendarHoliday(day)?.kind,'holiday',day)
 for(const day of ['2025-01-26','2025-02-08','2025-04-27','2025-09-28','2025-10-11','2026-01-04','2026-02-14','2026-02-28','2026-05-09','2026-09-20','2026-10-10'])assert.equal(holidays.calendarHoliday(day)?.kind,'workday',day)
 for(const day of ['2025-01-04','2026-09-05','2026-09-28','2027-10-01'])assert.equal(holidays.calendarHoliday(day),null,day)
 assert(holidays.hasHolidayCalendar(2025));assert(holidays.hasHolidayCalendar(2026));assert(!holidays.hasHolidayCalendar(2027))
})
await test('month/date keyboard navigation preserves valid day, Monday week and calendar boundaries',()=>{
 assert.equal(dates.calendarKeyboardDate('2026-09-04','Home'),'2026-08-31');assert.equal(dates.calendarKeyboardDate('2026-09-04','End'),'2026-09-06')
 assert.equal(dates.calendarKeyboardDate('2026-01-31','PageDown'),'2026-02-28');assert.equal(dates.calendarKeyboardDate('2024-02-29','PageDown','date',true),'2025-02-28')
 assert.equal(dates.calendarKeyboardDate('2026-09','ArrowDown','month'),'2026-12');assert.equal(dates.calendarKeyboardDate('2026-09','Home','month'),'2026-01');assert.equal(dates.calendarKeyboardDate('0001-01','PageUp','month'),'')
 assert.equal(dates.calendarGrid('2026-09').length,42);assert.equal(dates.calendarGrid('2026-09')[0],'2026-08-31')
})
await test('invalid manual input remains visible, blocks native form validation and never updates v-model',async()=>{
 const m=mount();m.type('2026-02-30');assert.equal(m.raw.value,'2026-02-30');assert.deepEqual(m.updates(),[]);assert(m.element.validity.includes('有效日期'));assert.equal(m.validate(),false)
 m.type('2026-02-28');assert.deepEqual(m.updates(),['2026-02-28']);assert.equal(m.element.validity,'');const change=m.events.find(([name])=>name==='change')[1];assert.equal(change.target,m.element);assert.equal(change.currentTarget.value,'2026-02-28');await Vue.nextTick();m.stop()
})
await test('required, min/max, disabled/inert and external error never allow invalid commit',()=>{
 const m=mount({required:true,min:'2026-09-01',max:'2026-09-30'});m.commit('');m.commit('2026-08-31');m.commit('2026-10-01');assert.deepEqual(m.updates(),[])
 m.type('');assert(m.issue.value);m.props.disabled=true;m.commit('2026-09-15');assert.deepEqual(m.updates(),[]);m.props.disabled=false;m.element.inert=true;m.commit('2026-09-15');assert.deepEqual(m.updates(),[]);m.element.inert=false;m.commit('2026-09-15');assert.deepEqual(m.updates(),['2026-09-15']);m.stop()
})
await test('optional clear works, month mode validates independently and Escape does not close outer drawers',()=>{
 const m=mount({mode:'month',modelValue:'2026-09'});m.type('2026-13');assert.deepEqual(m.updates(),[]);m.type('2026-10');assert.deepEqual(m.updates(),['2026-10']);m.commit('');assert.deepEqual(m.updates(),['2026-10','']);m.open.value=true;const escape=key('Escape');m.gridKey(escape);assert(escape.stopped&&escape.defaultPrevented);assert.equal(m.open.value,false);m.stop()
})
await test('component exposes valid grid rows, labels and consistent theme tokens',()=>{
 assert.deepEqual(compileTemplate({id:'calendar',filename:'DatePicker.vue',source:descriptor.template.content,compilerOptions:{bindingMetadata:compiled.bindings}}).errors,[])
 assert.match(source,/role="row"/);assert.match(source,/aria-current/);assert.match(source,/setCustomValidity/);assert.match(source,/该年份尚未配置/);assert(!source.includes('inputmode="numeric"'));assert(!source.includes('var(--background'))
})
await test('every create action reinitializes defaults without a historical sprint watcher',async()=>{
 const sprint=await readFile(new URL('src/views/Sprints.vue',root),'utf8');assert.match(sprint,/@click="openSprint"/);assert.match(sprint,/function openSprint\(\).*defaultSprintDates\(\)/);assert.match(sprint,/sprintCustomized.name=true/);assert.match(sprint,/sprintCustomized.endDate=true/)
 for(const path of ['src/views/Editor.vue','src/views/Sprints.vue','src/views/Testing.vue','src/views/Fields.vue','src/components/CustomFieldInputs.vue','src/components/WorkItemFilters.vue'])assert(!/type="date"/.test(await readFile(new URL(path,root),'utf8')),path)
})
console.log(`Passed ${count} calendar and iteration-default tests.`)
