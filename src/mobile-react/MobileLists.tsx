import { useEffect, useRef, useState } from 'react'
import { ItemCard, LoadState, StatusBadge } from './MobilePrimitives'
import { MobileIcon } from './icons'
import { useMobilePageScroll, useMobileQuery } from './useMobilePage'

export function SearchBox({value,onChange,label}:{value:string;onChange:(value:string)=>void;label:string}) {return <label className="dfm-search"><MobileIcon name="search" size={19}/><input type="search" value={value} onChange={e=>onChange(e.target.value)} placeholder={label} aria-label={label} enterKeyHint="search"/></label>}
export function MobileRequirements({projectId,onOpen,active=true}:{projectId:string;onOpen:(item:any)=>void;active?:boolean}) {
  const [query,setQuery]=useState(''),[mine,setMine]=useState(false),[status,setStatus]=useState(''),[page,setPage]=useState(1)
  const params=new URLSearchParams({q:query,mine:mine?'1':'0',statusCategory:status,page:String(page),pageSize:'30',projection:'list',sort:'updatedAt',order:'desc'})
  const {data,loading,error,reload}=useMobileQuery<any>(`/requirements?${params}`,active,projectId,query?180:0)
  const items:any[]=data?.items||[],total=data?.total||0
  useMobilePageScroll(active,loading)
  return <main className="dfm-page"><div className="dfm-page-heading"><div><p>项目协作</p><h1>需求列表</h1></div><button className="dfm-icon-button" aria-label="刷新需求" onClick={reload}><MobileIcon name="refresh"/></button></div>
    <SearchBox value={query} onChange={v=>{setQuery(v);setPage(1)}} label="搜索需求、人员或部门"/>
    <div className="dfm-chip-row"><button className={mine?'is-active':''} aria-pressed={mine} onClick={()=>{setMine(!mine);setPage(1)}}>与我相关</button>{[['','全部'],['todo','规划中'],['doing','进行中'],['done','已完成']].map(([v,label])=><button key={v} className={status===v?'is-active':''} aria-pressed={status===v} onClick={()=>{setStatus(v);setPage(1)}}>{label}</button>)}</div>
    <LoadState loading={loading} error={error} retry={reload}/>{!loading&&!error&&<><p className="dfm-muted">{total} 条需求</p><div className="dfm-card-list">{items.map(item=><ItemCard key={item.id} item={item} onOpen={()=>onOpen({...item,projectId,objectType:'requirement'})}/>)}</div>{!items.length&&<p className="dfm-empty">当前筛选下没有需求</p>}{total>30&&<div className="dfm-pagination"><button disabled={page===1} onClick={()=>setPage(x=>x-1)}>上一页</button><span>{page} / {Math.ceil(total/30)}</span><button disabled={page*30>=total} onClick={()=>setPage(x=>x+1)}>下一页</button></div>}</>}
  </main>
}

export function MobileIterations({projectId,onOpen,active=true,requestedSprint}:{projectId:string;onOpen:(item:any)=>void;active?:boolean;requestedSprint?:{id:number;projectId:string}|null}) {
  const initialLinkHandled=useRef(false)
  const lastRequested=useRef<typeof requestedSprint>(undefined)
  const [query,setQuery]=useState(''),[status,setStatus]=useState(''),[selected,setSelected]=useState<any>(null),[mine,setMine]=useState(false),[itemQuery,setItemQuery]=useState('')
  const sprintList=useMobileQuery<any>('/sprints',active&&!selected,projectId)
  const sprintDetail=useMobileQuery<any>(`/sprints/${selected?.id||0}`,active&&!!selected,projectId)
  const sprints:any[]=sprintList.data?.items||[],items:any[]=sprintDetail.data?.items||[]
  const {loading,error,reload}=selected?sprintDetail:sprintList
  useMobilePageScroll(active&&!selected,sprintList.loading)
  useMobilePageScroll(active&&!!selected,sprintDetail.loading,selected?.id)
  const visible=sprints.filter(s=>(!status||s.status===status)&&`${s.name} ${s.code||''}`.toLowerCase().includes(query.toLowerCase())).sort((a,b)=>['进行中','规划中','已完成','已取消'].indexOf(a.status)-['进行中','规划中','已完成','已取消'].indexOf(b.status))
  useEffect(()=>{if(!active||initialLinkHandled.current||!sprints.length)return;initialLinkHandled.current=true;const id=Number(new URLSearchParams(location.search).get('sprint'));const sprint=sprints.find(s=>s.id===id);if(sprint)setSelected(sprint)},[sprints,active])
  useEffect(()=>{if(!active||!requestedSprint||requestedSprint.projectId!==projectId||lastRequested.current===requestedSprint||!sprints.length)return;lastRequested.current=requestedSprint;const sprint=sprints.find(s=>s.id===requestedSprint.id);if(sprint){setSelected(sprint);setMine(false);setItemQuery('')}},[requestedSprint,sprints,active,projectId])
  const work=items.filter(x=>(!mine||x.relatedToMe)&&`${x.title} ${x.code||''}`.toLowerCase().includes(itemQuery.toLowerCase())).sort((a,b)=>Number(a.objectType==='defect')-Number(b.objectType==='defect'))
  return <main className="dfm-page"><div className="dfm-page-heading"><div><p>交付节奏</p><h1>迭代列表</h1></div><button className="dfm-icon-button" aria-label="刷新迭代" onClick={reload}><MobileIcon name="refresh"/></button></div>
    {selected?<><button className="dfm-back-link" onClick={()=>setSelected(null)}><MobileIcon name="back" size={18}/>全部迭代</button>{!error&&<section className="dfm-sprint-summary"><StatusBadge item={selected}/><h2>{selected.name}</h2>{selected.goal&&<p>{selected.goal}</p>}</section>}<SearchBox value={itemQuery} onChange={setItemQuery} label="搜索迭代工作项"/><div className="dfm-chip-row"><button className={mine?'is-active':''} aria-pressed={mine} onClick={()=>setMine(!mine)}>与我相关</button></div></>:<><SearchBox value={query} onChange={setQuery} label="搜索迭代名称或编号"/><div className="dfm-chip-row">{['','进行中','规划中','已完成'].map(v=><button key={v} className={status===v?'is-active':''} aria-pressed={status===v} onClick={()=>setStatus(v)}>{v||'全部'}</button>)}</div></>}
    <LoadState loading={loading} error={error} retry={reload}/>{!loading&&!error&&(selected?<div className="dfm-card-list">{work.map(x=><ItemCard key={`${x.objectType}-${x.id}`} item={x} onOpen={()=>onOpen({...x,projectId})}/>)}{!work.length&&<p className="dfm-empty">当前筛选下没有工作项</p>}</div>:<div className="dfm-sprint-list">{visible.map(s=><button key={s.id} className="dfm-sprint-card" onClick={()=>{setSelected(s);setMine(false);setItemQuery('')}}><div className="dfm-item-labels"><StatusBadge item={s}/>{s.code&&<small>{s.code}</small>}</div><strong>{s.name}</strong>{s.startDate&&<p>{s.startDate}{s.endDate?` — ${s.endDate}`:''}</p>}<div className="dfm-progress" aria-label={`完成 ${s.done||0} / ${s.total||0}`}><span style={{width:`${s.total?Math.min(100,(s.done||0)/s.total*100):0}%`}}/></div><small>{s.done||0} / {s.total||0} 项完成</small></button>)}{!visible.length&&<p className="dfm-empty">当前筛选下没有迭代</p>}</div>)}
  </main>
}
