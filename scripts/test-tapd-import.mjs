import assert from 'node:assert/strict'
import fs from 'node:fs'
import ts from 'typescript'
const source=fs.readFileSync(new URL('../src/tapdImport.ts',import.meta.url),'utf8'), exports={}
new Function('exports',ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(exports)
const {parseTapdPages,suggestTapdMapping,buildTapdRequirement,normalizeTapdText,normalizeTapdName,tapdFileError}=exports
const item=(str,x,y)=>({str,width:str.length*6,transform:[1,0,0,1,x,y]})
const page={width:595,height:842,items:[
 item('STORY 【ID123】 ⼤模型需求',36,770),item('基础信息',36,730),
 item('状态',36,700),item('后端已完成',144,700),item('需求分类',315,700),item('客户端',423,700),
 item('处理⼈',36,670),item('张佳琪;李锐鸿',144,670),item('备注',315,670),item('feature/branch-',423,670),item('continued',423,655),
 item('未知字段',36,630),item('不能丢失',144,630),item('优先级',315,630),item('--',423,630),
 item('详细描述',36,600),item('正文必须完整',36,580),item('https://www.tapd.cn/321/prong/stories/print_story/11321123',18,44)
]}
const parsed=parseTapdPages([page,{width:595,height:842,items:[item('第二页正文',36,780)]}])
assert.equal(parsed.fields.length,6);assert.equal(parsed.title,'大模型需求');assert.equal(parsed.workspaceId,'321');assert.equal(parsed.sourceId,'11321123')
assert.equal(parsed.fields.find(x=>x.label==='备注').value,'feature/branch-\ncontinued')
assert.equal(parsed.fields.find(x=>x.label==='未知字段').value,'不能丢失');assert.equal(parsed.description,'正文必须完整\n\n第二页正文')
assert.deepEqual(parsed.descriptionDoc.content.map(x=>x.type),['paragraph','paragraph'])
assert.equal(normalizeTapdText('⻓⼯程师王欣⾬'),'长工程师王欣雨')
assert.throws(()=>parseTapdPages([{width:595,height:842,items:[]}]),/未识别/)
assert.throws(()=>parseTapdPages([page,{width:595,height:842,items:[item('STORY 【ID456】 另一条需求',36,770)]}]),/多个需求/)
const targets=[{key:'status',label:'状态',kind:'choice',options:[{value:'backend_done',label:'后端已完成'}]},{key:'sprint',label:'迭代',kind:'choice',options:[{value:'9.10迭代',label:'9.10迭代'}]},{key:'assigneeUserIds',label:'处理人',kind:'members'},{key:'role.backend.value',label:'后端开发难度',kind:'number'},{key:'role.ui.value',label:'UI 难度',kind:'number'},{key:'cf.testers',label:'测试人员',kind:'members'}]
const members=[{id:'a',name:'张佳琪',active:true},{id:'b',name:'李锐鸿',active:true},{id:'c',name:'重复',active:true},{id:'d',name:'重复',active:true},{id:'e',name:'离职',active:false}]
const map=(label,value)=>suggestTapdMapping({label,value},targets,members)
assert.equal(map('状态','后端已完成').value,'backend_done');assert.equal(map('迭代','9.10迭代（当前迭代）').value,'9.10迭代')
assert.deepEqual(map('处理人','张佳琪;李锐鸿').value,['a','b']);assert.match(map('处理人','重复;离职;不存在').issue,/重复.*离职.*不存在/)
assert.equal(map('UI 奖励','80').target,'');assert.equal(map('后端组长','李锐鸿').target,'');assert.equal(map('父需求','123').target,'')
assert.equal(map('测试人员','张佳琪').target,'cf.testers');assert.equal(map('后端开发难度','0').value,0)
const rows=[map('状态','后端已完成'),map('处理人','张佳琪;李锐鸿'),map('后端开发难度','160'),map('测试人员','李锐鸿')]
const payload=buildTapdRequirement('新需求','正文',rows,targets)
assert.equal(payload.status,'backend_done');assert.deepEqual(payload.assigneeUserIds,['a','b']);assert.equal(payload.roleWeights.backend.value,160);assert.deepEqual(payload.customFields.testers,['b']);assert.equal(payload.priority,'P2')
assert.throws(()=>buildTapdRequirement('x','',rows.concat(rows[0]),targets),/不能映射多次/)
assert.throws(()=>buildTapdRequirement('x','',[map('处理人','重复')],targets),/处理字段映射/)
console.log('TAPD coordinate table, multiline values, unknown/blank fields, identity, ambiguity, semantic mapping and duplicate targets passed.')
// isCurrent 来自登录身份，false 的有效项目成员仍必须按姓名匹配。
const directory=[{id:'front',name:'张佳琪',active:true,isCurrent:false},{id:'back',name:'李锐鸿',active:true,isCurrent:false},{id:'off',name:'停用人员',active:false,isCurrent:false},{id:'latin',name:'Ada Lovelace',active:true,isCurrent:false}]
const named=value=>suggestTapdMapping({label:'处理人',value},targets,directory)
assert.deepEqual(named('张佳琪；李锐鸿').value,['front','back'])
assert.deepEqual(named('张 佳 琪；李锐\n鸿').value,['front','back'])
assert.deepEqual(named('张佳琪\n李锐鸿').value,['front','back'])
assert.deepEqual(named('张佳琪;张佳琪、李锐鸿').value,['front','back'])
assert.equal(normalizeTapdName(' 王欣⾬\u200b '),'王欣雨')
assert.deepEqual(named('Ada  Lovelace').value,['latin'])
assert.match(named('停用人员').issue,/停用/)
assert.match(named('张佳祺').issue,/未找到/)
assert.match(suggestTapdMapping({label:'处理人',value:'张佳琪'},targets,[...directory,{id:'another',name:'张 佳琪',active:true,isCurrent:false}]).issue,/同名/)
assert.equal(tapdFileError([{name:'需求.PDF',size:10*1024*1024}]),'')
for(const files of [[],[{name:'x.pdf',size:1},{name:'y.pdf',size:1}],[{name:'x.txt',size:1}],[{name:'x.pdf',size:0}],[{name:'x.pdf',size:10*1024*1024+1}]])assert(tapdFileError(files))
console.log('Project-wide exact-name matching, PDF spacing, duplicates, inactive users and drop file validation passed.')

const restricted=[{key:'cf.testers',label:'测试人员',kind:'members',memberIds:['b']}]
const restrictedRow=suggestTapdMapping({label:'测试人员',value:'张佳琪;李锐鸿'},restricted,members)
assert.deepEqual(restrictedRow.value,['b']);assert.match(restrictedRow.issue,/张佳琪.*角色或部门/)
assert.throws(()=>buildTapdRequirement('x','',[{...restrictedRow,issue:'',value:['a']}],restricted),/角色、部门或项目/)
// Never silently resolve an ambiguous name merely because only one of the names meets the field constraint.
assert.match(suggestTapdMapping({label:'测试人员',value:'重复'},[{...restricted[0],memberIds:['c']}],members).issue,/同名/)
const dates=[{key:'startDate',label:'开始时间',kind:'date'}]
assert.equal(suggestTapdMapping({label:'开始时间',value:'2024-02-29'},dates,[]).value,'2024-02-29')
assert.match(suggestTapdMapping({label:'开始时间',value:'2026-02-29'},dates,[]).issue,/日期/)
assert.throws(()=>buildTapdRequirement('x','',[{field:{label:'开始时间',value:''},target:'startDate',value:'2026-04-31',issue:''}],dates),/有效的/)
console.log('Role-constrained member matching, manual-selection revalidation, ambiguity and actual calendar dates passed.')
