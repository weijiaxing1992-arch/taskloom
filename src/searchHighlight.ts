export function searchMatchRanges(text:string, query:string):[number,number][] {
 const terms=[...new Set(query.trim().slice(0,200).split(/\s+/u).filter(Boolean))].slice(0,12)
 if(!terms.length)return []
 // 关键词按普通文字处理，避免特殊字符被当成正则或 HTML 执行。
 const pattern=new RegExp(terms.sort((a,b)=>b.length-a.length).map(x=>x.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')).join('|'),'giu')
 return [...text.matchAll(pattern)].map(m=>[m.index!,m.index!+m[0].length])
}

/** 使用浏览器绘制层高亮，不插入 mark、不改写 Vue 文本节点或富文本数据。 */
export function startSearchHighlights():()=>void {
 if(typeof CSS==='undefined'||!CSS.highlights||typeof Highlight==='undefined')return ()=>{}
 const name='devflow-search',skip='script,style,noscript,input,textarea,select,option, [contenteditable="true"], [data-search-highlight-ignore], [hidden], [inert],dialog:not([open])'
 let timer:ReturnType<typeof setTimeout>|undefined,stopped=false
 const visible=(el:Element)=>!!el.getClientRects().length&&!el.closest('[hidden],[inert],dialog:not([open])')
 function render(){
  timer=undefined;if(stopped)return
  const highlight=new Highlight(),seen=new WeakMap<Node,Set<string>>()
  let count=0
  for(const input of document.querySelectorAll<HTMLInputElement>('input')){
   if(!input.value.trim()||!visible(input)||['password','email','number','date','checkbox','radio'].includes(input.type))continue
   const label=[input.placeholder,input.getAttribute('aria-label'),...(input.labels?Array.from(input.labels).map(x=>x.textContent||''):[])].join(' ')
   if(input.type!=='search'&&!/搜索|检索|查找|search|find|keyword/i.test(label))continue
   // 人员浮层优先使用 aria-controls 关联结果；页面检索不污染全站导航。
   const controlled=(input.getAttribute('aria-controls')||'').split(/\s+/).map(id=>document.getElementById(id)).filter((x):x is HTMLElement=>!!x&&visible(x))
   const local=input.closest('[data-search-highlight-scope],.member-options-floating,.member-multi,.top-search-widget,.status-filter-menu,.transition-menu,aside,[class*="sidebar"],[role="dialog"],dialog,main')
   const roots=controlled.length?controlled:local?[local]:[]
   for(const root of roots){
    const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT)
    let node:Node|null,visited=0
    while((node=walker.nextNode())&&visited++<15000&&count<3000){
     const parent=node.parentElement
     if(!parent||parent.closest(skip)||!visible(parent)||!node.textContent?.trim())continue
     const key=input.value.trim(),keys=seen.get(node)||new Set<string>();if(keys.has(key))continue;keys.add(key);seen.set(node,keys)
     for(const [start,end] of searchMatchRanges(node.textContent,key)){
      const range=document.createRange();range.setStart(node,start);range.setEnd(node,end);highlight.add(range);if(++count>=3000)break
     }
    }
   }
  }
  CSS.highlights.set(name,highlight)
 }
 function schedule(){if(!stopped&&timer===undefined)timer=setTimeout(render,100)}
 const observer=new MutationObserver(schedule)
 observer.observe(document.body,{subtree:true,childList:true,characterData:true,attributes:true,attributeFilter:['hidden','class','open','aria-expanded','aria-controls']})
 const events=['input','change','click','compositionend','focusin']
 events.forEach(event=>document.addEventListener(event,schedule,true));window.addEventListener('resize',schedule);schedule()
 return ()=>{stopped=true;clearTimeout(timer);observer.disconnect();events.forEach(event=>document.removeEventListener(event,schedule,true));window.removeEventListener('resize',schedule);CSS.highlights.delete(name)}
}
