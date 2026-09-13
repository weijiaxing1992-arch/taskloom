import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'

const read=path=>readFileSync(new URL('../'+path,import.meta.url),'utf8')
const compile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const releaseNotes={}
new Function('exports',compile(read('src/releaseNotes.ts')))(releaseNotes)
const center={}
new Function('require','exports',compile(read('src/releaseNotesCenter.ts')))(id=>{
  if(id==='./releaseNotes')return releaseNotes
  throw Error('Unexpected center import '+id)
},center)
const image=(extra={})=>({id:17,requirementId:7,name:'智能体工作流.png',url:'/api/requirements/7/attachments/17',sha256:'a'.repeat(64),contentType:'image/png',...extra})
const requirement=(extra={})=>({id:7,code:'REQ-0007',title:'智能体工作流支持配置检查',type:'产品需求',category:'智能体',status:'已完成',description:'保存的需求正文，用于让读者理解本次升级。',acceptance:'保存的验收标准，验证配置和回滚路径。',updatedAt:'2026-09-10T09:00:00Z',images:[image()],...extra})
const entry=(extra={})=>({category:'智能体类型',title:'智能体配置检查',description:'支持配置检查和清晰的升级说明。',requirementIds:[7],imageIds:[17],imageCaptions:{17:'智能体配置页面的真实截图'},...extra})
const detail=(extra={})=>({projectId:'project-a',sprintId:10,sprintCode:'SPR-001',versionName:'V10.3 智能体升级',releaseDate:'2026-09-10T09:00:00Z',revision:4,state:'draft',createdAt:'2026-09-10T09:00:00Z',updatedAt:'2026-09-10T09:00:00Z',snapshot:true,categories:[...releaseNotes.releaseNoteCategories],entries:[entry()],requirements:[requirement()],markdown:'# V10.3 智能体升级\n\n已保存的版本说明。\n',...extra})
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('saved details preserve full non-defect source data in the fixed seven-category order',()=>{
  const value=center.parseReleaseNotesCenterDetail(detail())
  assert.deepEqual(value.categories,releaseNotes.releaseNoteCategories)
  assert.equal(value.requirements[0].description,'保存的需求正文，用于让读者理解本次升级。')
  assert.equal(value.requirements[0].acceptance,'保存的验收标准，验证配置和回滚路径。')
  assert.equal(center.releaseNotesCenterImages(value,value.entries[0]).length,1)
  assert.equal(center.releaseNotesCenterImages(value,value.entries[0])[0].sha256,'a'.repeat(64))
  assert.equal(center.releaseNotesCenterRequirement(value,7)?.code,'REQ-0007')
  assert.deepEqual(center.releaseNotesCenterGroups(value).map(group=>group.category),releaseNotes.releaseNoteCategories)
  assert.equal(center.releaseNotesCenterGroups(value)[1].entries[0].imageCaptions?.[17],'智能体配置页面的真实截图')
})

await test('malformed snapshots, defect records and cross-requirement image paths fail closed',()=>{
  assert.throws(()=>center.parseReleaseNotesCenterDetail(detail({categories:[...releaseNotes.releaseNoteCategories].reverse()})))
  assert.throws(()=>center.parseReleaseNotesCenterDetail(detail({requirements:[requirement({type:'缺陷'})]})))
  assert.throws(()=>center.parseReleaseNotesCenterDetail(detail({requirements:[requirement({category:'bugfix'})]})))
  assert.throws(()=>center.parseReleaseNotesCenterDetail(detail({requirements:[requirement({images:[image({url:'/api/requirements/8/attachments/17'})]})]})))
  assert.throws(()=>center.parseReleaseNotesCenterDetail(detail({entries:[entry({imageIds:[18]})]})))
  assert.throws(()=>center.parseReleaseNotesCenterDetail(detail({requirements:[requirement(),requirement({id:8,code:'REQ-0008',images:[]})]})))
})

await test('list snapshots validate pagination, counts and only contain summary fields',()=>{
  const item={sprintId:10,sprintCode:'SPR-001',versionName:'V10.3 智能体升级',releaseDate:'2026-09-10T09:00:00Z',revision:4,state:'draft',createdAt:'2026-09-10T09:00:00Z',updatedAt:'2026-09-10T09:00:00Z',requirementCount:1,entryCount:1,selectedImageCount:1}
  assert.deepEqual(center.parseReleaseNotesCenterList({projectId:'project-a',items:[item],total:1,page:1,pageSize:20}),{projectId:'project-a',items:[item],total:1,page:1,pageSize:20})
  for(const bad of [{...item,selectedImageCount:-1},{...item,revision:0},{...item,releaseDate:'not-a-date'}])assert.throws(()=>center.parseReleaseNotesCenterList({projectId:'project-a',items:[bad],total:1,page:1,pageSize:20}))
  assert.throws(()=>center.parseReleaseNotesCenterList({projectId:'project-a',items:[item],total:0,page:1,pageSize:20}))
  assert.throws(()=>center.parseReleaseNotesCenterList({projectId:'project-a',items:[item],total:1,page:0,pageSize:20}))
})

await test('team-insights page compiles and only loads images through verified internal attachment paths',()=>{
  const source=read('src/views/ReleaseNotesCenter.vue'),{descriptor,errors}=parse(source)
  assert.deepEqual(errors,[])
  const script=compileScript(descriptor,{id:'release-notes-center'})
  const template=compileTemplate({source:descriptor.template.content,filename:'ReleaseNotesCenter.vue',id:'release-notes-center',compilerOptions:{bindingMetadata:script.bindings}})
  assert.deepEqual(template.errors,[])
  assert.match(source,/\/reports\/release-notes\?page=1&pageSize=100/)
  assert.match(source,/&q='\+encodeURIComponent\(query\)/)
  assert.match(source,/searchInput=ref\(''\)/)
  assert.match(source,/搜索版本、需求或内容/)
  assert.match(source,/\/reports\/release-notes\/'\+item\.sprintId/)
  assert.match(source,/apiDownload\(releaseImagePath\(image\)/)
  assert.match(source,/需求正文与验收/)
  assert.match(source,/requirement\(entry\)\?\.description/)
  assert.match(source,/requirement\(entry\)\?\.acceptance/)
  assert.match(source,/下载图文包/)
  assert.match(source,/<header class="release-center-heading">/)
  assert.match(source,/class="release-center-title"/)
  assert.doesNotMatch(source,/release-center-heading compact-page-heading/)
  assert.match(source,/\.release-center-heading\{display:grid;grid-template-columns:minmax\(0,1fr\) auto;/)
  assert.match(source,/\.release-center>\.release-center-empty\{min-height:clamp\(220px,30vh,300px\);/)
  assert.match(source,/@media\(max-width:620px\)\{[\s\S]*?\.release-center-heading\{grid-template-columns:1fr;/)
  assert.match(source,/\.release-center-heading h1\{[^}]*overflow-wrap:anywhere/)
  assert.doesNotMatch(source,/v-html|<img[^>]*image\.url/)
})

await test('route, team-insights entry and credential UI recognize the dedicated read scope',()=>{
  assert.match(read('src/main.ts'),/path:'\/reports\/release-notes',component:ReleaseNotesCenter/)
  assert.match(read('src/App.vue'),/to="\/reports\/release-notes"/)
  const platform={}
  new Function('exports',compile(read('src/integrationPlatform.ts')))(platform)
  assert.equal(platform.integrationScopeLabels['release-notes:read'],'读取升级日志')
  const scopes=[...platform.contextReadScopes,'release-notes:read']
  const snapshot={projectId:'project-a',userId:'user-a',tokens:[],scopes:scopes.map(key=>({key,label:platform.integrationScopeLabels[key]})),canWrite:false,canManage:true,mcpPath:'/api/open/mcp',apiPath:'/api/open/v1',maxExpiryDays:90}
  assert.equal(platform.readIntegrationSnapshot(snapshot,'project-a').scopes.at(-1).key,'release-notes:read')
  assert(platform.credentialRequest('Release reader',['release-notes:read'],30,platform.readIntegrationSnapshot(snapshot,'project-a')).scopes.includes('release-notes:read'))
})

console.log(`${count} release-notes-center checks passed`)
