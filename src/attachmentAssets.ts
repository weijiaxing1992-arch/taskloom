import { detectCodeLanguage } from './codeHighlight'
export const assetCategories = [{value:'bug',label:'缺陷',icon:'bug'},{value:'api',label:'接口',icon:'api'},{value:'design',label:'设计稿',icon:'design'},{value:'code',label:'代码',icon:'code'},{value:'other',label:'其他',icon:'file'}] as const
export function assetLanguage(name:string, text=''):string {
 const ext=name.toLowerCase().split('.').at(-1)||''
 const aliases:Record<string,string>={vue:'vue',php:'php',go:'go',cpp:'cpp',cc:'cpp',cxx:'cpp',hpp:'cpp',hxx:'cpp',c:'c',h:'c',js:'javascript',mjs:'javascript',cjs:'javascript',jsx:'javascript',ts:'typescript',tsx:'typescript',py:'python',java:'java',dart:'dart',json:'json',sql:'sql',sh:'bash',bash:'bash',html:'html',htm:'html',xml:'xml',svg:'xml',css:'css',scss:'scss',less:'less',yaml:'yaml',yml:'yaml',md:'markdown',markdown:'markdown',rs:'rust',swift:'swift',kt:'kotlin',cs:'csharp',rb:'ruby',proto:'protobuf',graphql:'graphql',txt:'plaintext',log:'plaintext',csv:'plaintext',diff:'diff',patch:'diff',dockerfile:'dockerfile'}
 const language=aliases[ext]
 if(language && language!=='plaintext')return language
 const detected=detectCodeLanguage(text.slice(0,8192));return detected==='auto'?(language||''):detected
}
export function assetCategory(name:string,selected='auto'):string {
 if(assetCategories.some(item=>item.value===selected))return selected
 const lower=name.toLowerCase()
 if(/bug|缺陷|复现/.test(lower))return 'bug'
 if(/openapi|swagger|postman|接口|\.proto$/.test(lower))return 'api'
 if(/设计稿|\.(fig|sketch|psd|xd)$/.test(lower))return 'design'
 const language=assetLanguage(name);return language && !['plaintext','markdown'].includes(language)?'code':'other'
}
export function assetLabel(category:string){return assetCategories.find(item=>item.value===category)?.label||'其他'}
export function languageSymbol(language:string){return ({vue:'V',php:'PHP',go:'Go',cpp:'C++',c:'C',javascript:'JS',typescript:'TS',python:'Py',json:'{}',html:'HTML',xml:'XML',css:'CSS',markdown:'MD',sql:'SQL',yaml:'YML',bash:'$_',java:'Java',dart:'Dart'} as Record<string,string>)[language]||language.slice(0,4).toUpperCase()||'TXT'}
// 不对二进制做有损解码；支持 UTF-8 与带 BOM 的 UTF-16，原始下载始终不变。
export function decodeAssetText(bytes:Uint8Array):string {
 const encoding=bytes[0]===255&&bytes[1]===254?'utf-16le':bytes[0]===254&&bytes[1]===255?'utf-16be':'utf-8'
 let text:string
 try{text=new TextDecoder(encoding,{fatal:true}).decode(bytes)}catch{throw Error('无法安全解析此编码，请下载原文件；建议上传 UTF-8 或带 BOM 的 UTF-16 代码。')}
 if(/[\u0000-\u0008\u000e-\u001f]/.test(text))throw Error('检测到二进制内容，不作为代码执行或预览，请下载原文件。')
 return text
}
