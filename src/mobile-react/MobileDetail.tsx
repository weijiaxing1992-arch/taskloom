import { useEffect, useRef, useState } from 'react'
import { mobileAPI, relativeTime } from './mobileApi'
import { MobileIcon } from './icons'
import { Attachment, hasContent, LoadState, MobileContent, StatusBadge } from './MobileContent'

export type DetailTarget = { kind: 'requirement' | 'defect'; id: number; projectId: string }
export function MobileDetail({ target, onClose, active = true }: { target: DetailTarget; onClose: () => void; active?: boolean }) {
  const [item,setItem]=useState<any>(null), [comments,setComments]=useState<any[]>([]), [assets,setAssets]=useState<any[]>([])
  const [loading,setLoading]=useState(true), [error,setError]=useState(''), [extraError,setExtraError]=useState(''), [attempt,setAttempt]=useState(0)
  const [attachmentsOpen,setAttachmentsOpen]=useState(false)
  const loadedTarget=useRef('')
  const back=useRef<HTMLButtonElement>(null)
  useEffect(()=>{ const y=window.scrollY, previous=document.body.style.overflow, trigger=document.activeElement; document.body.style.overflow='hidden';back.current?.focus({preventScroll:true}); const key=(e:KeyboardEvent)=>{if(e.key==='Escape')onClose()};window.addEventListener('keydown',key);return()=>{document.body.style.overflow=previous;window.scrollTo(0,y);if(trigger&&trigger instanceof HTMLElement&&trigger.isConnected)trigger.focus({preventScroll:true});window.removeEventListener('keydown',key)} },[onClose])
  useEffect(()=>{
    if(!active)return
    const targetKey=`${target.projectId}:${target.kind}:${target.id}`,retained=loadedTarget.current===targetKey
    let alive=true;const controller=new AbortController();setLoading(!retained);setError('');setExtraError('')
    if(!retained){setItem(null);setComments([]);setAssets([]);setAttachmentsOpen(false)}
    const path=`/${target.kind==='requirement'?'requirements':'defects'}/${target.id}`,options={signal:controller.signal,headers:{'X-TaskLoom-Project':target.projectId}}
    void mobileAPI<any>(path,options).then(data=>{if(alive){loadedTarget.current=targetKey;setItem(data)}}).catch(e=>{if(alive){loadedTarget.current='';setItem(null);setError(e instanceof Error?e.message:'暂时无法加载，请稍后重试')}}).finally(()=>{if(alive)setLoading(false)})
    void Promise.allSettled([mobileAPI<any>(`${path}/comments`,options),target.kind==='requirement'?mobileAPI<any>(`${path}/attachments`,options):Promise.resolve({items:[]})]).then(([c,a])=>{if(!alive)return;if(c.status==='fulfilled')setComments(c.value.items||[]);if(a.status==='fulfilled')setAssets(a.value.items||[]);if(c.status==='rejected'||a.status==='rejected')setExtraError('部分评论或附件暂未加载，可重试。')})
    return()=>{alive=false;controller.abort()}
  },[target.kind,target.id,target.projectId,attempt,active])
  const sections=target.kind==='requirement'?[['需求描述',item?.description,item?.descriptionDoc],['验收标准',item?.acceptance],['备注',item?.remarks]]:[['缺陷描述',item?.description],['复现步骤',item?.steps],['实际结果',item?.actual],['预期结果',item?.expected],['环境',item?.environment]]
  const names=(value:any)=>Array.isArray(value)?value.map(x=>typeof x==='string'?x:x.name).filter(Boolean).join('、'):typeof value==='string'?value:''
  const metadata=[['负责人',names(item?.owners)||names(item?.owner)],['处理人',names(item?.assignees)||names(item?.assignee)],['验证人',names(item?.verifier)],['迭代',item?.sprint==='待规划'?'':item?.sprint],['优先级',item?.priority],['计划开始',item?.startDate],['计划结束',item?.endDate]].filter(([,value])=>hasContent(value))
  return <div className="dfm-detail" role="dialog" aria-modal="true" aria-label={target.kind==='requirement'?'需求详情':'缺陷详情'}>
    <header className="dfm-detail-bar"><button ref={back} onClick={onClose}><MobileIcon name="back" size={20}/>返回</button><span>{target.kind==='requirement'?'需求详情':'缺陷详情'}</span></header>
    <div className="dfm-detail-scroll"><LoadState loading={loading} error={error} retry={()=>setAttempt(x=>x+1)}/>{item&&!loading&&!error&&<article>
      <div className="dfm-item-labels"><span className={`dfm-object-type ${target.kind==='defect'?'dfm-type-defect':''}`}>{target.kind==='requirement'?'需求':'缺陷'}</span><StatusBadge item={item}/><small>{target.kind==='requirement'?String(item.id).padStart(6,'0'):item.code}</small></div>
      <h1>{item.title}</h1>
      {sections.filter(([,text,doc])=>hasContent(text)||hasContent(doc)).map(([label,text,doc])=><section className="dfm-content-section" key={label}><h2>{label}</h2><MobileContent text={text||''} document={doc} requirementId={target.kind==='requirement'?target.id:0} projectId={target.projectId} active={active}/></section>)}
      {!sections.some(([,text,doc])=>hasContent(text)||hasContent(doc))&&<p className="dfm-muted">尚未补充正文</p>}
      {assets.length>0&&<details className="dfm-more" open={attachmentsOpen} onToggle={event=>setAttachmentsOpen(event.currentTarget.open)}><summary>附件 · {assets.length}</summary>{attachmentsOpen&&assets.map(a=><Attachment key={a.id} id={a.id} requirementId={target.id} projectId={target.projectId} name={a.name} image={String(a.contentType).startsWith('image/')} active={active}/>)}</details>}
      {metadata.length>0&&<details className="dfm-more"><summary>更多信息</summary><dl>{metadata.map(([label,value])=><div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl></details>}
      {comments.length>0&&<section className="dfm-content-section"><h2>评论 · {comments.length}</h2>{comments.map(c=><article className="dfm-comment" key={c.id}><header><strong>{names(c.author)||'成员'}</strong>{c.createdAt&&<time>{relativeTime(c.createdAt)}</time>}</header><MobileContent text={c.body||''} document={c.contentDoc} requirementId={target.kind==='requirement'?target.id:0} projectId={target.projectId} active={active}/></article>)}</section>}
      {extraError&&<p role="alert">{extraError}<button onClick={()=>setAttempt(x=>x+1)}>重试</button></p>}
    </article>}</div>
  </div>
}
