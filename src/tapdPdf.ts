import { getDocument, GlobalWorkerOptions } from 'pdfjs-dist'
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import { parseTapdPages, type TapdPage, type PdfText } from './tapdImport'
import { extractTapdImages } from './tapdPdfImages'

GlobalWorkerOptions.workerSrc = workerUrl
export async function readTapdPdf(file: File, signal: AbortSignal, progress: (message:string)=>void) {
  if(!/\.pdf$/i.test(file.name)||file.size===0||file.size>10*1024*1024)throw Error('请选择不超过 10 MiB 的 TAPD PDF')
  const bytes=new Uint8Array(await file.arrayBuffer())
  if(signal.aborted)throw Error('已取消解析')
  const task=getDocument({data:bytes.slice(),cMapUrl:'/pdfjs/cmaps/',cMapPacked:true,standardFontDataUrl:'/pdfjs/standard_fonts/',wasmUrl:'/pdfjs/wasm/',useSystemFonts:false,stopAtErrors:true})
  // The timeout also aborts pending image-object waits, not just the PDF worker.
  const parsing=new AbortController()
  let timedOut=false
  const cancel=()=>{parsing.abort();void task.destroy()};signal.addEventListener('abort',cancel,{once:true})
  const timeout=setTimeout(()=>{timedOut=true;cancel()},90000)
  try {
    const doc=await task.promise
    if(doc.numPages>30)throw Error('单次支持最多 30 页；请拆分 PDF 后导入')
    const pages:TapdPage[]=[],budget={bytes:0,count:0}
    for(let n=1;n<=doc.numPages;n++){
      if(parsing.signal.aborted)throw Error('已取消解析')
      progress(`${n} / ${doc.numPages}`)
      const page=await doc.getPage(n), viewport=page.getViewport({scale:1}), text=await page.getTextContent()
      if(page.rotate!==0)throw Error('暂不支持旋转页面，请将 PDF 页面旋转为正向后重试')
      const {images,warnings}=await extractTapdImages(page,n,parsing.signal,budget)
      pages.push({width:viewport.width,height:viewport.height,items:text.items.filter((x):x is typeof x & PdfText=>'str'in x),images,warnings})
      page.cleanup()
    }
    const parsed=parseTapdPages(pages)
    if(parsed.rawText.length>300000||parsed.fields.length>128)throw Error('文档内容超过单次导入限制，请拆分 PDF')
    let binary='';for(let i=0;i<bytes.length;i+=8192)binary+=String.fromCharCode(...bytes.subarray(i,i+8192))
    const imageCount=parsed.descriptionDoc.content?.filter(node=>node.type==='image').length||0
    return {...parsed,imageCount,pageCount:doc.numPages,pdfBase64:btoa(binary),fileName:file.name}
  } catch(cause) {
    if(timedOut)throw Error('PDF 解析超过 90 秒，已取消；请拆分文件后重试')
    if(signal.aborted)throw Error('已取消解析')
    throw cause
  } finally {clearTimeout(timeout);signal.removeEventListener('abort',cancel);await task.destroy()}
}
