import assert from 'node:assert/strict'
import fs from 'node:fs'
import {createRequire} from 'node:module'
import ts from 'typescript'
import * as pdfjs from 'pdfjs-dist/legacy/build/pdf.mjs'

function load(file,imports={}) {
 const exports={}
 const code=ts.transpileModule(fs.readFileSync(new URL('../src/'+file,import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
 new Function('require','exports',code)(id=>{if(id in imports)return imports[id];throw Error('Unexpected import: '+id)},exports)
 return exports
}
const helper=load('tapdImport.ts'),assets=load('tapdPdfImages.ts',{'pdfjs-dist':pdfjs})
const item=(str,x,y,width=str.length*10,size=10)=>({str,width,height:size,transform:[size,0,0,size,x,y]})
const base=[item('STORY 【ID123】 导入测试',36,770),item('基础信息',36,730),item('状态',36,700),item('开发中',144,700),item('详细描述',36,670),item('https://www.tapd.cn/321/prong/stories/print_story/11321123',18,44)]
const picture={id:'TAPD-image-1-1',x:36,y:485,width:280,height:80,pixelWidth:700,pixelHeight:200,data:'data:image/png;base64,AQID'}
const parsed=helper.parseTapdPages([{width:595,height:842,items:[...base,
 item('功能说明',36,640,100,16),item('这是在右边界发生折行的段落上半部',36,610,515),item('继续同一段落的后半部。',36,594,150),item('独立的新段落。',36,565,120),
 item('1.',36,535,10),item('这条规则在行末折行',50,535,501),item('后半部仍属于第一条规则。',50,518,170),item('2.',36,500,10),item('第二条规则。',50,500,100),
 item('• 项目一',36,380,150),item('• 项目二',36,363,150),item('列一',36,330,40),item('列二',230,330,40),item('值一',36,310,40),item('值二',230,310,40),
 ],images:[picture]}])
assert.deepEqual(parsed.descriptionDoc.content.map(x=>x.type),['heading','paragraph','paragraph','orderedList','image','bulletList','paragraph','paragraph'])
assert.equal(parsed.descriptionDoc.content[1].content[0].text,'这是在右边界发生折行的段落上半部继续同一段落的后半部。')
assert.equal(parsed.descriptionDoc.content[3].content.length,2)
assert.equal(parsed.descriptionDoc.content[3].content[0].content[0].content[0].text,'这条规则在行末折行后半部仍属于第一条规则。')
assert(parsed.warnings.some(x=>x.includes('表格或分栏')))
assert.equal(parsed.descriptionDoc.content[4].attrs.name,'TAPD-image-1-1.png')
assert(!parsed.description.includes('STORY'));assert(!parsed.description.includes('https://'))
const spanning=helper.parseTapdPages([{width:595,height:842,items:[...base,item('跨页长段落',36,80,515)]},{width:595,height:842,items:[item('接续的内容',36,780,130)]}])
assert.equal(spanning.description,'跨页长段落接续的内容')
const english=helper.parseTapdPages([{width:595,height:842,items:[...base,item('An English paragraph',36,620,515),item('continues here.',36,603,150)]}])
assert.equal(english.description,'An English paragraph continues here.')

// 真实像素编码不依赖 PDF 页面渲染；PDF.js 已将原图解码为 RGB、RGBA 或 1-bit 数据。
const requirePdf=createRequire(import.meta.resolve('pdfjs-dist/package.json'))
const {createCanvas}=requirePdf('@napi-rs/canvas')
globalThis.document={createElement(tag){assert.equal(tag,'canvas');return createCanvas(1,1)}}
const rgb={width:2,height:1,kind:pdfjs.ImageKind.RGB_24BPP,data:new Uint8Array([255,0,0,0,255,0])}
for(const raster of [rgb,{width:1,height:1,kind:pdfjs.ImageKind.RGBA_32BPP,data:new Uint8Array([10,20,30,128])},{width:9,height:2,kind:pdfjs.ImageKind.GRAYSCALE_1BPP,data:new Uint8Array([255,128,0,0])}]) {
 const png=Buffer.from(assets.rasterToPng(raster).split(',')[1],'base64');assert.equal(png.readUInt32BE(16),raster.width);assert.equal(png.readUInt32BE(20),raster.height)
}
assert.throws(()=>assets.rasterToPng({...rgb,width:100000}),/不完整/)
assert.throws(()=>assets.rasterToPng({...rgb,width:100000,height:100000}),/像素/)
const {OPS}=pdfjs
const page={getOperatorList:async()=>({fnArray:[OPS.save,OPS.transform,OPS.paintImageXObject,OPS.restore,OPS.paintImageMaskXObject],argsArray:[[],[200,0,0,50,36,300],['image'],[],[{}]]}),objs:{get(id,callback){callback(rgb)}},commonObjs:{get(){throw Error('unused')}},render(){throw Error('Page rendering is forbidden')}}
const extracted=await assets.extractTapdImages(page,1,new AbortController().signal,{bytes:0,count:0})
assert.equal(extracted.images.length,1);assert.equal(extracted.images[0].x,36);assert.equal(extracted.images[0].y,350);assert.equal(extracted.images[0].pixelWidth,2);assert(extracted.warnings.some(x=>x.includes('蒙版')))
const limited=await assets.extractTapdImages(page,1,new AbortController().signal,{bytes:0,count:100});assert.equal(limited.images.length,0);assert(limited.warnings.some(x=>x.includes('100')))
const aborted=new AbortController();aborted.abort();await assert.rejects(assets.extractTapdImages(page,1,aborted.signal,{bytes:0,count:0}),/取消/)

// The whole-file deadline must cancel pending image decodes as well as the worker.
const timeoutPage={...page,rotate:0,getViewport:()=>({width:595,height:842}),getTextContent:async()=>({items:base}),getOperatorList:async()=>({fnArray:[OPS.paintImageXObject],argsArray:[['pending']]}),objs:{get(){}},cleanup(){}}
let timeoutCallback,destroys=0
const task={promise:Promise.resolve({numPages:1,getPage:async()=>timeoutPage}),destroy:async()=>{destroys++}}
const timedReader=load('tapdPdf.ts',{'pdfjs-dist':{GlobalWorkerOptions:{},getDocument:()=>task},'pdfjs-dist/build/pdf.worker.min.mjs?url':{default:''},'./tapdImport':helper,'./tapdPdfImages':assets})
const originalSetTimeout=globalThis.setTimeout
try {
 globalThis.setTimeout=(fn,ms,...args)=>ms===90000?(timeoutCallback=fn,0):originalSetTimeout(fn,ms,...args)
 const pending=timedReader.readTapdPdf(new File(['%PDF-fake'],'timeout.pdf'),new AbortController().signal,()=>{})
 for(let i=0;i<20;i++)await Promise.resolve()
 assert.equal(typeof timeoutCallback,'function');timeoutCallback()
 await assert.rejects(pending,/90 秒/);assert(destroys>=1)
} finally {globalThis.setTimeout=originalSetTimeout}

// 可选用户样本只读运行；样本不会被复制到代码库或测试夹具。
if(process.argv[2]) {
 const pdfRoot=new URL('../node_modules/pdfjs-dist/',import.meta.url).pathname
 const imported=load('tapdPdf.ts',{'pdfjs-dist':{...pdfjs,GlobalWorkerOptions:{},getDocument:options=>pdfjs.getDocument({...options,cMapUrl:pdfRoot+'cmaps/',standardFontDataUrl:pdfRoot+'standard_fonts/',wasmUrl:pdfRoot+'wasm/'})},'pdfjs-dist/build/pdf.worker.min.mjs?url':{default:''},'./tapdImport':helper,'./tapdPdfImages':assets})
 const source=fs.readFileSync(process.argv[2]),file=new File([source],'sample.pdf',{type:'application/pdf'})
 const result=await imported.readTapdPdf(file,new AbortController().signal,()=>{})
 assert.equal(result.pageCount,2);assert.equal(result.fields.length,28);assert.equal(result.imageCount,2)
 assert(result.title.includes('大模型3.0'))
 const images=result.descriptionDoc.content.filter(x=>x.type==='image'),sizes=images.map(x=>{const p=Buffer.from(x.attrs.data,'base64');return[p.readUInt32BE(16),p.readUInt32BE(20)]})
 assert.deepEqual(sizes,[[700,31],[700,338]])
 const list=result.descriptionDoc.content.find(x=>x.type==='orderedList');assert.equal(list.content.length,5)
 assert(list.content[4].content[0].content[0].text.includes(helper.normalizeTapdText('替换规则，请调整后重新上传。')))
 assert(!result.description.includes('替换规\n'));assert(result.description.includes('原内容 | 替换内容'))
 assert(result.warnings.some(x=>x.includes('第 2 页含表格或分栏')))
 assert(!JSON.stringify(result.descriptionDoc).includes('TAPD-page-'))
 console.log('Read-only user sample passed: 2 pages, 28 fields, 2 original images (700×31, 700×338), 5 intact ordered items, explicit table warning.')
 if(process.env.TAPD_QA_OUTPUT){fs.writeFileSync(process.env.TAPD_QA_OUTPUT,JSON.stringify(result,null,2))}
}
console.log('TAPD paragraph reflow, headings, lists, page breaks, columns, original raster extraction, budgets and cancellation passed.')
