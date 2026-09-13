import { ImageKind, OPS, type PDFPageProxy } from 'pdfjs-dist'
import type { TapdImage } from './tapdImport'

type Matrix = [number,number,number,number,number,number]
type Raster = { width:number; height:number; kind?:number; data?:Uint8Array|Uint8ClampedArray; bitmap?:ImageBitmap }
const identity=():Matrix=>[1,0,0,1,0,0]
const multiply=(a:Matrix,b:Matrix):Matrix=>[a[0]*b[0]+a[2]*b[1],a[1]*b[0]+a[3]*b[1],a[0]*b[2]+a[2]*b[3],a[1]*b[2]+a[3]*b[3],a[0]*b[4]+a[2]*b[5]+a[4],a[1]*b[4]+a[3]*b[5]+a[5]]

/** 编码解码后的单个 PDF 位图，保持像素尺寸；从不渲染或截取 PDF 页面。 */
export function rasterToPng(raster:Raster):string {
  const {width,height}=raster
  if(!Number.isSafeInteger(width)||!Number.isSafeInteger(height)||width<1||height<1||width*height>16_000_000)throw Error('图片像素超过限制')
  const canvas=document.createElement('canvas');canvas.width=width;canvas.height=height
  try {
    const context=canvas.getContext('2d');if(!context)throw Error('浏览器无法提取图片')
    if(raster.bitmap)context.drawImage(raster.bitmap,0,0)
    else {
      const source=raster.data;if(!source)throw Error('图片数据缺失')
      const rgba=context.createImageData(width,height),output=rgba.data
      if(raster.kind===ImageKind.RGBA_32BPP) {
        if(source.length<width*height*4)throw Error('图片数据不完整')
        output.set(source.subarray(0,output.length))
      } else if(raster.kind===ImageKind.RGB_24BPP) {
        if(source.length<width*height*3)throw Error('图片数据不完整')
        for(let p=0,s=0;p<output.length;p+=4,s+=3){output[p]=source[s]!;output[p+1]=source[s+1]!;output[p+2]=source[s+2]!;output[p+3]=255}
      } else if(raster.kind===ImageKind.GRAYSCALE_1BPP) {
        const stride=Math.ceil(width/8);if(source.length<stride*height)throw Error('图片数据不完整')
        for(let y=0;y<height;y++)for(let x=0;x<width;x++){const p=(y*width+x)*4,value=(source[y*stride+(x>>3)]!>>(7-(x&7)))&1?255:0;output[p]=output[p+1]=output[p+2]=value;output[p+3]=255}
      } else throw Error('图片编码暂不支持')
      context.putImageData(rgba,0,0)
    }
    const data=canvas.toDataURL('image/png');if(!data.startsWith('data:image/png;base64,'))throw Error('图片编码失败')
    return data
  } finally {canvas.width=0;canvas.height=0}
}

function imageObject(page:PDFPageProxy,id:string,signal:AbortSignal):Promise<Raster> {
  return new Promise((resolve,reject)=>{
    const objects=id.startsWith('g_')?page.commonObjs:page.objs
    let timer:ReturnType<typeof setTimeout>
    const finish=(value?:Raster,error?:Error)=>{clearTimeout(timer);signal.removeEventListener('abort',abort);error?reject(error):value?resolve(value):reject(Error('图片解码失败'))}
    const abort=()=>finish(undefined,Error('已取消解析'))
    timer=setTimeout(()=>finish(undefined,Error('图片解码超时')),5000)
    signal.addEventListener('abort',abort,{once:true})
    if(signal.aborted){abort();return}
    try{objects.get(id,(value:Raster)=>finish(value))}catch{finish(undefined,Error('图片解码失败'))}
  })
}

export async function extractTapdImages(page:PDFPageProxy,pageNumber:number,signal:AbortSignal,budget:{bytes:number;count:number}) {
  const operations=await page.getOperatorList(),images:TapdImage[]=[],warnings:string[]=[],stack:Matrix[]=[]
  let matrix=identity(),unsupported=false
  const add=async(raster:Raster,placement=matrix)=>{
    if(signal.aborted)throw Error('已取消解析')
    try {
      if(budget.count>=100)throw Error('图片数量超过 100 张')
      const data=rasterToPng(raster)
      if(data.length>10*1024*1024||budget.bytes+data.length>10*1024*1024)throw Error('独立图片总量超过 10 MiB')
      const xs=[placement[4],placement[0]+placement[4],placement[2]+placement[4],placement[0]+placement[2]+placement[4]],ys=[placement[5],placement[1]+placement[5],placement[3]+placement[5],placement[1]+placement[3]+placement[5]]
      budget.count++;budget.bytes+=data.length
      images.push({id:`TAPD-image-${pageNumber}-${images.length+1}`,x:Math.min(...xs),y:Math.max(...ys),width:Math.max(...xs)-Math.min(...xs),height:Math.max(...ys)-Math.min(...ys),pixelWidth:raster.width,pixelHeight:raster.height,data})
      if(Math.abs(placement[1])>.01||Math.abs(placement[2])>.01)warnings.push(`第 ${pageNumber} 页有旋转或倾斜图片，已提取原图像素，显示方向请核对原始 PDF。`)
    } catch(cause) {warnings.push(`第 ${pageNumber} 页有图片未能独立迁移：${cause instanceof Error?cause.message:'解码失败'}；请从原始 PDF 核对补充，不以整页截图代替。`)}
  }
  for(let i=0;i<operations.fnArray.length;i++) {
    if(signal.aborted)throw Error('已取消解析')
    const op=operations.fnArray[i],args=operations.argsArray[i]||[]
    if(op===OPS.save||op===OPS.paintFormXObjectBegin||op===OPS.beginGroup) {
      stack.push([...matrix]);const transform=op===OPS.paintFormXObjectBegin?args[0]:op===OPS.beginGroup?args[0]?.matrix:null
      if(transform)matrix=multiply(matrix,transform)
    } else if(op===OPS.restore||op===OPS.paintFormXObjectEnd||op===OPS.endGroup)matrix=stack.pop()||identity()
    else if(op===OPS.transform)matrix=multiply(matrix,args as Matrix)
    else if(op===OPS.paintImageXObject||op===OPS.paintImageXObjectRepeat) {
      try {
        const raster=await imageObject(page,args[0],signal)
        if(op===OPS.paintImageXObject)await add(raster)
        else for(let p=0;p<args[3].length;p+=2)await add(raster,multiply(matrix,[args[1],0,0,args[2],args[3][p],args[3][p+1]]))
      } catch(cause) {if(signal.aborted)throw cause;warnings.push(`第 ${pageNumber} 页有图片解码失败或超时，未能独立迁移；请从原始 PDF 核对补充，不以整页截图代替。`)}
    } else if(op===OPS.paintInlineImageXObject)await add(args[0])
    else if([OPS.paintImageMaskXObject,OPS.paintImageMaskXObjectGroup,OPS.paintImageMaskXObjectRepeat,OPS.paintInlineImageXObjectGroup,OPS.paintSolidColorImageMask].includes(op!))unsupported=true
  }
  if(unsupported)warnings.push(`第 ${pageNumber} 页含复杂图片蒙版或拼接图，暂未完整重建；已迁移可提取的原图，其余内容请对照原始 PDF 补充。`)
  return {images,warnings:[...new Set(warnings)]}
}
