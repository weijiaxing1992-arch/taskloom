import assert from 'node:assert/strict'
import { createServer } from 'vite'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { readFileSync } from 'node:fs'
const server=await createServer({server:{middlewareMode:true},appType:'custom'})
try {
  const {hasContent,MobileContent,ItemCard,StatusBadge}=await server.ssrLoadModule('/src/mobile-react/MobileContent.tsx')
  for(const value of [null,undefined,'','  ',{type:'doc',content:[{type:'paragraph'}]}])assert.equal(hasContent(value),false)
  for(const value of ['正文',0,{type:'doc',content:[{type:'mention',attrs:{label:'同事'}}]}])assert.equal(hasContent(value),true)
  const html=renderToStaticMarkup(createElement(MobileContent,{text:'# 登录体验\n\n详细正文。\n\n```go\nfunc main() {}\n```\n\n<script>alert(1)</script>'}))
  assert(html.includes('<h2>'));assert(html.includes('详细正文'));assert(html.includes('func main() {}'));assert(!html.includes('<script>'))
  const {default:MobileCodeBlock}=await server.ssrLoadModule('/src/mobile-react/MobileCodeBlock.tsx')
  const highlighted=renderToStaticMarkup(createElement(MobileCodeBlock,{text:'func main() {}',language:'go'}))
  assert(highlighted.includes('hljs'));assert(highlighted.includes('复制代码'))
  const card=renderToStaticMarkup(createElement(ItemCard,{item:{id:12,title:'正文优先',status:'已完成',type:'产品需求'},onOpen:()=>{}}))
  assert(card.indexOf('已完成')<card.indexOf('正文优先'));assert(card.includes('000012'));assert(!card.includes('dfm-item-meta'));assert(!card.includes('未纳入迭代'))
  const bug=renderToStaticMarkup(createElement(ItemCard,{item:{id:12,code:'BUG-0012',title:'缺陷',objectType:'defect',status:'修复中'},onOpen:()=>{}}))
  assert(bug.includes('dfm-type-defect'));assert(bug.includes('BUG-0012'))
  assert.equal(renderToStaticMarkup(createElement(StatusBadge,{item:{}})),'')
  const index=readFileSync('index.html','utf8'),script=index.match(/<script type="module">([\s\S]*?)<\/script>/)[1]
  let redirected=''
  const evaluate=new Function('window',script.replace("else import('/src/main.ts')",'else {}'))
  evaluate({location:{pathname:'/iterations',search:'?sprint=2&req=3&project=prj_a',replace:v=>{redirected=v}},matchMedia:()=>({matches:true})})
  const params=new URL(redirected,'http://localhost').searchParams
  assert.equal(params.get('tab'),'iterations');assert.equal(params.get('req'),'3');assert.equal(params.get('sprint'),'2');assert.equal(params.get('project'),'prj_a')
  console.log('通过：空字段、Markdown/代码安全渲染、前置状态、需求/缺陷区分、移动深链接上下文。')
} finally { await server.close() }
