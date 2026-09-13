import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import logoURL from '../assets/taskloom-mark.svg'
import { MobileIcon } from './icons'
import { MobileAPIError, localDay, mobileAPI, relativeTime, sameOriginPath } from './mobileApi'
import { inMobileTimeRange, sortMobileWorkItems, type MobileTimeRange, type MobileWorkItem } from './workItems'
import { MobileRequirements, MobileIterations } from './MobileLists'
import type { DetailTarget } from './MobileDetail'
import { ItemCard } from './MobilePrimitives'
import { createUnreadCounter } from './mobileRequests'
import { useMobilePageScroll, useMobileQuery } from './useMobilePage'
import { notificationBody } from '../notificationDisplay'

const MobileDetail = lazy(() => import('./MobileDetail').then(module => ({ default: module.MobileDetail })))
const MobileContent = lazy(() => import('./MobileContent').then(module => ({ default: module.MobileContent })))

type MobileScreen = 'work' | 'requirements' | 'iterations' | 'notifications'
type Session = { user?: { id?: string; name?: string; role?: string; projectRoles?: unknown; avatarColor?: string; operationDisabled?: boolean }; impersonation?: unknown; tenant?: {id?:string}; organizationPermissions?: unknown; project?: { id?: string; name?: string; code?: string } }
type MyWorkResponse = { items?: MobileWorkItem[]; counts?: Record<string, number> }
type Notice = { id: number; title?: string; body?: string; createdAt?: string; readAt?: string; eventType?: string; subjectType?: string; subjectId?: number; projectId?: string; projectName?: string; actor?: string; url?: string }
type NoticesResponse = { items?: Notice[]; unread?: number; total?: number; hasMore?: boolean; groupUnread?: Record<string, number> }

const categories = [
  { value: 'active', label: '待办' },
  { value: 'todo', label: '待处理' },
  { value: 'doing', label: '进行中' },
  { value: 'due', label: '即将到期' },
]

const timeRanges: Array<{ value: MobileTimeRange; label: string }> = [
  { value: 'all', label: '全部待办' },
  { value: 'today', label: '今日' },
  { value: 'week', label: '本周' },
]
const sessionScope = (session: Session | null) => JSON.stringify([session?.user?.id, session?.user?.role, session?.user?.projectRoles, session?.user?.operationDisabled, session?.impersonation, session?.tenant?.id, session?.organizationPermissions])
const mobileNoticeBody = (item: Notice) => notificationBody(item, (key, params) => key.replace(/\{(\w+)\}/g, (placeholder, name) => String(params?.[name] ?? placeholder)))

function screenFromLocation(): MobileScreen {
  const value = new URLSearchParams(window.location.search).get('tab')
  return ['requirements','iterations','notifications'].includes(value || '') ? value as MobileScreen : 'work'
}

function mobileURL(screen: MobileScreen): string {
  const url = new URL(window.location.href)
  url.pathname = '/mobile.html'
  url.searchParams.set('tab',screen)
  url.searchParams.delete('req'); url.searchParams.delete('bug'); url.searchParams.delete('sprint')
  url.hash = ''
  return `${url.pathname}${url.search}`
}

function greeting(): string {
  const hour = new Date().getHours()
  if (hour < 11) return '早上好'
  if (hour < 14) return '中午好'
  if (hour < 19) return '下午好'
  return '晚上好'
}

function getMobileError(error: unknown): string {
  if (error instanceof MobileAPIError) return error.message
  return '暂时无法加载，请稍后重试'
}

function workTypeLabel(type: string): string {
  return type === '缺陷' ? '缺陷' : type === '需求' ? '需求' : type || '工作项'
}

function isTerminal(status: string | undefined): boolean {
  return ['已完成', '已关闭', '已解决', '通过', '已终止', '已取消'].includes(status || '')
}

export default function MobileApp() {
  const [screen, setScreen] = useState<MobileScreen>(screenFromLocation)
  const [visited,setVisited]=useState<MobileScreen[]>(()=>[screenFromLocation()])
  const [foreground,setForeground]=useState(document.visibilityState!=='hidden')
  const [session, setSession] = useState<Session | null>(null)
  const [unread, setUnread] = useState(0)
  const unreadCounter=useMemo(()=>createUnreadCounter(signal=>mobileAPI<{unread?:number}>('/notifications/unread-count',{signal}),setUnread),[])
  const refreshUnread=useCallback(()=>unreadCounter.refresh(),[unreadCounter])
  const commitUnread=useCallback((value:number)=>unreadCounter.commit(value),[unreadCounter])
  const [appError, setAppError] = useState('')
  const [ready, setReady] = useState(false)
  const [detail,setDetail]=useState<DetailTarget|null>(null)
  const [projects,setProjects]=useState<Array<{id:string;name:string}>>([])
  const [projectId,setProjectId]=useState(localStorage.getItem('devflow-project')||'prj_orbit')
  const [iterationTarget,setIterationTarget]=useState<{id:number;projectId:string}|null>(null)
  const retrySession=useRef<()=>Promise<void>>(undefined)
  const closeDetail=useCallback(()=>setDetail(null),[])
  const openItem=useCallback((item:any)=>{
    if(item.type==='迭代') {
      const scope=item.projectId||projectId,id=Number(item.id)
      const url=new URL(mobileURL('iterations'),window.location.origin)
      url.searchParams.set('project',scope);if(id>0)url.searchParams.set('sprint',String(id))
      window.history.pushState({},'',url.pathname+url.search)
      setProjectId(scope);setIterationTarget({id,projectId:scope})
      setVisited(previous=>previous.includes('iterations')?previous:[...previous,'iterations']);setScreen('iterations')
      return
    }
    if(['测试用例','测试执行'].includes(item.type)) {const path=sameOriginPath(item.url);if(path){const url=new URL(path,location.origin);url.searchParams.set('project',item.projectId||projectId);url.searchParams.set('mobile','1');location.assign(url.pathname+url.search)}return}
    setDetail({kind:item.objectType==='defect'||item.type==='缺陷'?'defect':'requirement',id:Number(item.id),projectId:item.projectId||projectId})
  },[projectId])

  const chooseScreen = useCallback((next: MobileScreen) => {
    if (next === screen) return
    window.history.pushState({}, '', mobileURL(next))
    setVisited(previous=>previous.includes(next)?previous:[...previous,next])
    setScreen(next)
  }, [screen])

  useEffect(() => {
    const previousRestoration=window.history.scrollRestoration
    window.history.scrollRestoration='manual'
    const onPopState = () => { const next=screenFromLocation();setVisited(previous=>previous.includes(next)?previous:[...previous,next]);setScreen(next) }
    window.addEventListener('popstate', onPopState)
    return () => { window.removeEventListener('popstate', onPopState);window.history.scrollRestoration=previousRestoration }
  }, [])

  useEffect(() => {
    let alive = true
    let initial = true, hasSession = false, previousScope = '', pending: Promise<void> | null = null
    const controller=new AbortController()
    const refreshSession=():Promise<void> => {
      if(pending)return pending
      pending=mobileAPI<Session>('/session',{signal:controller.signal}).then(nextSession => {
        if (!alive) return
        const scopeKey=sessionScope(nextSession),changed=previousScope!==scopeKey
        if(previousScope&&changed){unreadCounter.commit(0);setDetail(null)}
        previousScope=scopeKey
        hasSession=true
        setSession(nextSession)
        if(initial){
          const query=new URLSearchParams(window.location.search)
          const scope=query.get('project')||nextSession.project?.id||projectId
          setProjectId(scope)
          const id=Number(query.get('req')||query.get('bug'))
          if(id>0)setDetail({kind:query.has('bug')?'defect':'requirement',id,projectId:scope})
          initial=false
        }
        if(changed){setProjects([]);void mobileAPI<{items?:Array<{id:string;name:string}>}>('/projects',{signal:controller.signal}).then(data=>{if(alive&&previousScope===scopeKey)setProjects(data.items||[])}).catch(()=>undefined)}
        setAppError('')
        setForeground(document.visibilityState!=='hidden')
        if(document.visibilityState!=='hidden')void refreshUnread().catch(()=>undefined)
      }).catch((error: unknown) => {
        if (!alive) return
        if(error instanceof MobileAPIError&&error.status>=400&&error.status<500){hasSession=false;setSession(null);setDetail(null);unreadCounter.commit(0)}
        else if(hasSession)setForeground(document.visibilityState!=='hidden')
        if (error instanceof MobileAPIError && error.status === 401) window.location.replace('/my-work?desktop=1')
        else setAppError(getMobileError(error))
      }).finally(() => { pending=null;if(alive)setReady(true) })
      return pending
    }
    retrySession.current=refreshSession
    void refreshSession()
    const refreshWhenVisible=()=>{
      if(document.visibilityState==='hidden'){setForeground(false);unreadCounter.invalidate();return}
      void refreshSession()
    }
    const timer=window.setInterval(()=>{
      if(document.visibilityState!=='hidden'&&!pending&&hasSession)void refreshUnread().catch(()=>undefined)
    },20_000)
    document.addEventListener('visibilitychange',refreshWhenVisible)
    window.addEventListener('focus',refreshWhenVisible)
    return () => {
      alive=false;controller.abort();unreadCounter.invalidate();window.clearInterval(timer)
      retrySession.current=undefined
      document.removeEventListener('visibilitychange',refreshWhenVisible);window.removeEventListener('focus',refreshWhenVisible)
    }
  }, [refreshUnread,unreadCounter])

  if (!ready) return <main className="dfm-loading" aria-live="polite"><span className="dfm-loader" />正在加载你的工作台…</main>

  return <div className="dfm-app">
    <header className="dfm-topbar">
      <button className="dfm-brand" type="button" onClick={() => chooseScreen('work')} aria-label="回到我的工作">
        <img src={logoURL} alt="" />
        <span>TaskLoom</span>
      </button>
      <button className="dfm-desktop-link" type="button" onClick={() => window.location.assign('/my-work?desktop=1')} aria-label="切换到桌面版">
        <MobileIcon name="desktop" size={18} />
        <span>桌面版</span>
      </button>
    </header>
    {appError ? <div className="dfm-app-error" role="alert"><span>{appError}</span><button type="button" onClick={() => void retrySession.current?.()}>重试</button></div> : null}
    {(screen==='requirements'||screen==='iterations')&&<div className="dfm-project-picker"><label>当前项目<select aria-label="选择移动端项目" value={projectId} onChange={e=>setProjectId(e.target.value)}>{projects.length?projects.map(p=><option key={p.id} value={p.id}>{p.name}</option>):<option value={projectId}>{session?.project?.name||'当前项目'}</option>}</select></label></div>}
    {session&&<div key={sessionScope(session)}>
      {visited.includes('work')&&<div hidden={screen!=='work'}><MobileWork active={foreground&&screen==='work'} session={session} onOpen={openItem}/></div>}
      {visited.includes('requirements')&&<div hidden={screen!=='requirements'}><MobileRequirements key={projectId} active={foreground&&screen==='requirements'} projectId={projectId} onOpen={openItem}/></div>}
      {visited.includes('iterations')&&<div hidden={screen!=='iterations'}><MobileIterations key={projectId} active={foreground&&screen==='iterations'} projectId={projectId} requestedSprint={iterationTarget} onOpen={openItem}/></div>}
      {visited.includes('notifications')&&<div hidden={screen!=='notifications'}><MobileNotifications active={foreground&&screen==='notifications'} session={session} unread={unread} onUnreadChange={commitUnread} onOpen={openItem}/></div>}
    </div>}
    <nav className="dfm-tabbar" aria-label="移动端主导航">
      <button type="button" className={screen === 'work' ? 'is-active' : ''} aria-current={screen === 'work' ? 'page' : undefined} onClick={() => chooseScreen('work')}>
        <MobileIcon name="work" /><span>我的</span>
      </button>
      <button type="button" className={screen==='requirements'?'is-active':''} aria-current={screen==='requirements'?'page':undefined} onClick={()=>chooseScreen('requirements')}><MobileIcon name="list"/><span>需求</span></button>
      <button type="button" className={screen==='iterations'?'is-active':''} aria-current={screen==='iterations'?'page':undefined} onClick={()=>chooseScreen('iterations')}><MobileIcon name="iteration"/><span>迭代</span></button>
      <button type="button" className={screen === 'notifications' ? 'is-active' : ''} aria-current={screen === 'notifications' ? 'page' : undefined} onClick={() => chooseScreen('notifications')}>
        <span className="dfm-tab-icon"><MobileIcon name="bell" />{unread > 0 ? <b>{unread > 99 ? '99+' : unread}</b> : null}</span><span>通知</span>
      </button>
    </nav>
    {detail&&<Suspense fallback={<div className="dfm-detail" role="dialog" aria-modal="true" aria-label="正在加载详情"><header className="dfm-detail-bar"><button onClick={closeDetail}>返回</button></header><div className="dfm-state" role="status">正在加载详情…</div></div>}><MobileDetail target={detail} active={foreground} onClose={closeDetail}/></Suspense>}
  </div>
}

function MobileWork({ session, active,onOpen }: { session: Session | null; active:boolean;onOpen:(item:any)=>void }) {
  const [category, setCategory] = useState('active')
  const [range, setRange] = useState<MobileTimeRange>('all')
  const [query, setQuery] = useState('')
  const params = new URLSearchParams({ category, q: query, type: '', project: 'all', view: 'assigned', status: '', sort: 'updatedAt', order: 'desc', sprintId: '' })
  const {data,loading,error,reload:load}=useMobileQuery<MyWorkResponse>(`/my-work?${params}`,active,undefined,query?180:0)
  const items=data?.items||[],counts=data?.counts||{}
  useMobilePageScroll(active,loading)

  const visible = useMemo(() => sortMobileWorkItems(items).filter(item => inMobileTimeRange(item, range)), [items, range])
  const rangeCounts = useMemo(() => ({
    all: items.length,
    today: items.filter(item => inMobileTimeRange(item, 'today')).length,
    week: items.filter(item => inMobileTimeRange(item, 'week')).length,
  }), [items])

  function openWork(item: MobileWorkItem) {
    onOpen(item)
  }

  const userName = session?.user?.name || '同学'
  return <main className="dfm-page dfm-work-page">
    <section className="dfm-hero" aria-labelledby="mobile-work-title">
      <h1 id="mobile-work-title">{greeting()}，{userName}</h1>
      <p>与你相关的工作，都在这里。</p>
      <div className="dfm-time-summary" role="tablist" aria-label="待办时间范围">
        {timeRanges.map(option => <button key={option.value} type="button" role="tab" aria-selected={range === option.value} className={range === option.value ? 'is-active' : ''} onClick={() => setRange(option.value)}>
          <b>{rangeCounts[option.value]}</b><span>{option.label}</span>
        </button>)}
      </div>
    </section>
    <section className="dfm-work-section" aria-label="我的待办">
      <div className="dfm-section-heading"><div><span>我的待办</span><h2>{visible.length} 个工作项</h2></div><button className="dfm-icon-button" type="button" onClick={() => void load()} aria-label="刷新我的工作"><MobileIcon name="refresh" size={20} /></button></div>
      <div className="dfm-search"><MobileIcon name="filter" size={18} /><input value={query} onChange={event => setQuery(event.target.value)} type="search" enterKeyHint="search" placeholder="搜索标题、编号或项目" aria-label="搜索我的工作" /></div>
      <div className="dfm-chip-row" aria-label="工作状态筛选">
        {categories.map(option => <button key={option.value} type="button" className={category === option.value ? 'is-active' : ''} aria-pressed={category === option.value} onClick={() => setCategory(option.value)}>{option.label}<small>{Number(counts[option.value] || 0)}</small></button>)}
      </div>
      {loading ? <div className="dfm-state" aria-live="polite"><span className="dfm-loader" />正在整理待办…</div> : error ? <div className="dfm-state dfm-state-error" role="alert"><span>{error}</span><button type="button" onClick={() => void load()}>重试</button></div> : !visible.length ? <EmptyWork /> : <div className="dfm-work-list">
        {visible.map(item => <ItemCard key={`${item.projectId}-${item.type}-${item.id}`} item={item} onOpen={()=>openWork(item)}/>)}
      </div>}
    </section>
  </main>
}

function EmptyWork() {
  return <div className="dfm-empty"><span className="dfm-empty-icon"><MobileIcon name="check" size={32} /></span><h2>这里已经清空了</h2><p>当前筛选下没有需要你处理的工作项。</p></div>
}

function MobileNotifications({ session, active, unread, onUnreadChange,onOpen }: { session: Session | null; active:boolean; unread: number; onUnreadChange: (value: number) => void;onOpen:(item:any)=>void }) {
  const [readMode, setReadMode] = useState<'unread' | 'read' | 'all'>('unread')
  const [offset,setOffset]=useState(0),[opened,setOpened]=useState<Notice|null>(null)
  const [group,setGroup]=useState('')
  const [saving, setSaving] = useState(false)
  const [mutationError, setError] = useState('')
  const [notice, setNotice] = useState('')
  const mutation=useRef({saving:false,alive:true,active})
  mutation.current.active=active
  useEffect(()=>{mutation.current.alive=true;return()=>{mutation.current.alive=false}},[])
  const isReadOnly = Boolean(session?.impersonation || session?.user?.operationDisabled)
  const params = new URLSearchParams({ read: readMode==='all'?'':readMode, project: '', eventType: '', group, offset: String(offset) })
  const result=useMobileQuery<NoticesResponse>(`/notifications?${params}`,active&&!saving,undefined,0,unread)
  const {loading}=result,items=result.data?.items||[],hasMore=Boolean(result.data?.hasMore),error=mutationError||result.error
  const load=()=>{setError('');result.reload()}
  useMobilePageScroll(active,loading)
  useEffect(()=>{if(!opened||!active)return;const previous=document.body.style.overflow;document.body.style.overflow='hidden';return()=>{document.body.style.overflow=previous}},[opened,active])

  async function setRead(item: Notice, nextRead: boolean): Promise<boolean> {
    if (mutation.current.saving || !mutation.current.active || isReadOnly) return false
    mutation.current.saving=true
    setSaving(true)
    setNotice('')
    try {
      const data = await mobileAPI<{ unread?: number; updated?: number }>(`/notifications/${item.id}`, { method: 'PATCH', body: JSON.stringify({ read: nextRead }) })
      if(!mutation.current.alive)return false
      setOpened(previous=>previous?.id===item.id?{...previous,readAt:nextRead?previous.readAt||new Date().toISOString():''}:previous)
      onUnreadChange(Math.max(0, Number(data.unread) || 0))
      setNotice(nextRead ? '已标记为已读' : '已标记为未读')
      return true
    } catch (cause) {
      if(mutation.current.alive)setError(getMobileError(cause))
      return false
    } finally { mutation.current.saving=false;if(mutation.current.alive)setSaving(false) }
  }

  async function markAll() {
    if (mutation.current.saving || !mutation.current.active || isReadOnly || !unread) return
    mutation.current.saving=true
    setSaving(true)
    setNotice('')
    try {
      const data = await mobileAPI<{ unread?: number; updated?: number }>('/notifications/read-all', { method: 'POST', body: '{}' })
      if(!mutation.current.alive)return
      onUnreadChange(Math.max(0, Number(data.unread) || 0))
      setNotice(`已标记 ${Number(data.updated) || 0} 条通知为已读`)
    } catch (cause) { if(mutation.current.alive)setError(getMobileError(cause)) }
    finally { mutation.current.saving=false;if(mutation.current.alive)setSaving(false) }
  }

  async function openNotice(item: Notice) {
    setOpened(item)
    if(!item.readAt&&!isReadOnly)void setRead(item,true)
  }
  function openRelated(item:Notice) {
    const path=sameOriginPath(item.url);if(!path)return
    const url=new URL(path,window.location.origin),req=Number(url.searchParams.get('req')),bug=Number(url.searchParams.get('bug'))
    if(req||bug)onOpen({id:req||bug,objectType:bug?'defect':'requirement',projectId:item.projectId})
    else {url.searchParams.set('mobile','1');if(item.projectId)url.searchParams.set('project',item.projectId);window.location.assign(url.pathname+url.search)}
  }

  return <main className="dfm-page dfm-notice-page">
    <section className="dfm-notice-hero" aria-labelledby="mobile-notice-title">
      <div><h1 id="mobile-notice-title">通知中心</h1><p>{unread ? `${unread} 条未读消息` : '所有消息均已读'}</p></div>
      <span className="dfm-notice-orb" aria-hidden="true"><MobileIcon name="bell" size={28} /></span>
    </section>
    <section className="dfm-notice-section" aria-label="站内通知">
      <div className="dfm-section-heading"><div><span>收件箱</span><h2>协作动态</h2></div><button className="dfm-icon-button" type="button" onClick={() => void load()} aria-label="刷新通知"><MobileIcon name="refresh" size={20} /></button></div>
      <div className="dfm-notice-controls">
        <div className="dfm-segmented" role="tablist" aria-label="通知状态筛选">{[['unread','未读'],['read','已读'],['all','全部']].map(([v,label])=><button key={v} type="button" role="tab" aria-selected={readMode===v} className={readMode===v?'is-active':''} onClick={()=>{setReadMode(v as typeof readMode);setOffset(0)}}>{label}</button>)}</div>
        <button className="dfm-text-button" type="button" disabled={!unread || saving || isReadOnly} onClick={() => void markAll()}>全部已读</button>
      </div>
      <div className="dfm-chip-row" aria-label="通知分类">{[['','全部分类'],['mentions','提及与回复'],['handoffs','分配与交接'],['changes','状态变更'],['activity','其他动态']].map(([v,label])=><button key={v} className={group===v?'is-active':''} aria-pressed={group===v} onClick={()=>{setGroup(v);setOffset(0)}}>{label}</button>)}</div>
      {isReadOnly ? <p className="dfm-read-only">当前为只读访问，通知状态不会修改。</p> : null}
      {notice ? <p className="dfm-live-result" role="status">{notice}</p> : null}
      {loading ? <div className="dfm-state" aria-live="polite"><span className="dfm-loader" />正在同步通知…</div> : error ? <div className="dfm-state dfm-state-error" role="alert"><span>{error}</span><button type="button" onClick={() => void load()}>重试</button></div> : !items.length ? <div className="dfm-empty"><span className="dfm-empty-icon"><MobileIcon name="inbox" size={32} /></span><h2>{readMode === 'unread' ? '没有未读消息' : '暂无通知'}</h2><p>新的分配、提及或状态变更会出现在这里。</p></div> : <div className="dfm-notice-list">
        {items.map((item, index) => <article key={item.id} className={`dfm-notice-card ${item.readAt ? 'is-read' : 'is-unread'}`} style={{ '--entry': `${Math.min(index, 10) * 28}ms` } as React.CSSProperties}>
          <button className="dfm-notice-open" type="button" onClick={() => void openNotice(item)}>
            <span className="dfm-notice-mark">{item.readAt ? <MobileIcon name="clock" size={18} /> : <MobileIcon name="bell" size={18} />}</span>
            <span className="dfm-notice-copy"><span className={`dfm-read-badge ${item.readAt?'is-read':''}`}>{item.readAt?'已读':'未读'}</span><b>{item.title || '工作项有新动态'}</b>{item.body&&<span>{mobileNoticeBody(item)}</span>}<small>{[item.projectName,item.actor,item.createdAt?relativeTime(item.createdAt):''].filter(Boolean).join(' · ')}</small></span>
            <MobileIcon name="chevron" size={18} />
          </button>
          <button className="dfm-notice-state" type="button" disabled={saving || isReadOnly} onClick={() => void setRead(item, !item.readAt)}>{item.readAt ? '设为未读' : '标为已读'}</button>
        </article>)}
      </div>}
      {(offset>0||hasMore)&&<div className="dfm-pagination"><button disabled={!offset||loading} onClick={()=>setOffset(x=>Math.max(0,x-40))}>上一页</button><button disabled={!hasMore||loading} onClick={()=>setOffset(x=>x+40)}>下一页</button></div>}
    </section>
    {opened&&<div className="dfm-detail dfm-notice-detail" role="dialog" aria-modal="true" aria-label="通知详情"><header className="dfm-detail-bar"><button onClick={()=>setOpened(null)}><MobileIcon name="back"/>返回</button><span>通知详情</span></header><div className="dfm-detail-scroll"><h1>{opened.title}</h1><p className="dfm-muted">{[opened.projectName,opened.actor].filter(Boolean).join(' · ')}</p>{opened.body&&<Suspense fallback={<p>{mobileNoticeBody(opened)}</p>}><MobileContent text={mobileNoticeBody(opened)} active={active}/></Suspense>} {sameOriginPath(opened.url)&&<button className="dfm-primary" onClick={()=>openRelated(opened)}>查看关联内容</button>}</div></div>}
  </main>
}
