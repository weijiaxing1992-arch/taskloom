import type { JSONContent } from '@tiptap/core'

export type TapdField = { label: string; value: string }
export type PdfText = { str: string; transform: number[]; width: number; height?: number; fontName?: string }
export type TapdImage = { id: string; x: number; y: number; width: number; height: number; pixelWidth: number; pixelHeight: number; data: string }
export type TapdPage = { width: number; height: number; items: PdfText[]; images?: TapdImage[]; warnings?: string[] }
export type TapdParsed = { title: string; workspaceId: string; sourceId: string; displayId: string; fields: TapdField[]; rawText: string; description: string; descriptionDoc: JSONContent; warnings: string[] }
export const normalizeTapdText = (text: string) => text.normalize('NFKC').replace(/⻓/g, '长').replace(/⻚/g, '页')
export const tapdBlank = (text: string) => /^(?:\s*|--|—|未设置)$/.test(text.trim())

type BodyLine = { text: string; x: number; right: number; y: number; size: number; page: number; pageHeight: number; columns: string[]; image?: TapdImage }
const fontSize = (item: PdfText) => item.height || Math.hypot(item.transform[2]!, item.transform[3]!) || 10
function joinPdfText(items: PdfText[], columnSeparator = ' ') {
  let end = -1, text = ''
  for (const item of [...items].filter(x=>x.str.trim()).sort((a,b)=>a.transform[4]!-b.transform[4]!)) {
    const next=normalizeTapdText(item.str),gap=item.transform[4]!-end,size=fontSize(item)
    if (end>=0 && gap>Math.max(1,size*.22) && !/\s$/.test(text) && !/^\s/.test(next)) {
      if (gap>Math.max(18,size*2.2)) text+=columnSeparator
      else if (!(/\p{Script=Han}$/u.test(text)&&/^\p{Script=Han}/u.test(next)&&gap<size*.8)) text+=' '
    }
    text+=next;end=item.transform[4]!+item.width
  }
  return text.trim()
}
const textNode = (text: string): JSONContent => ({type:'text',text})
const paragraph = (text: string): JSONContent => ({type:'paragraph',content:text?[textNode(text)]:[]})
const wrapSeparator = (left: string, right: string) => /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}]$/u.test(left)||/^[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}，。；！？、]/u.test(right)||/[-/]$/.test(left)?'':' '

/** 只合并达到正文右边界的排版折行；段落间距、标题、列表及图片会中断合并。 */
function bodyDocument(lines: BodyLine[], warnings: string[]) {
  const sizes=lines.filter(x=>!x.image).map(x=>x.size).sort((a,b)=>a-b),normal=sizes[Math.floor(sizes.length/2)]||10
  const right=Math.max(0,...lines.filter(x=>!x.image&&x.columns.length===1).map(x=>x.right))
  const content: JSONContent[]=[],plain:string[]=[]
  let last: {line:BodyLine; node:JSONContent; text:string; plainIndex:number; indent:number}|undefined
  let list:JSONContent|undefined, nextNumber=0
  for (const line of lines) {
    if (line.image) {
      const img=line.image
      content.push({type:'image',attrs:{name:`${img.id}.png`,alt:`TAPD 第 ${line.page+1} 页原图`,data:img.data.split(',')[1]}})
      last=undefined;list=undefined;continue
    }
    if (line.columns.length>1) {
      const warning=`第 ${line.page+1} 页含表格或分栏，已按行和列间隔保留文字；合并单元格及阅读顺序请对照原始 PDF 核对。`
      if(!warnings.includes(warning))warnings.push(warning)
      // 列边界用显式分隔保留，不推断表头或合并单元格关系。
      const value=line.columns.join(' | ');content.push(paragraph(value));plain.push(value);last=undefined;list=undefined;continue
    }
    const ordered=line.text.match(/^(\d{1,4})[.)、]\s+(.+)$/),bullet=line.text.match(/^[•●▪◦·]\s*(.+)$|^[-*]\s+(.+)$/)
    const heading=!ordered&&!bullet&&line.size>normal*1.2&&line.text.length<=100
    const samePage=last?.line.page===line.page
    const crossesPage=last&&line.page===last.line.page+1&&last.line.y<last.line.pageHeight*.14&&line.y>line.pageHeight*.84
    const wraps=last&&!ordered&&!bullet&&!heading&&last.line.right>=right-normal*2.5&&Math.abs(line.x-last.indent)<normal*2.5&&Math.abs(line.size-last.line.size)<normal*.25&&((samePage&&last.line.y-line.y>0&&last.line.y-line.y<=normal*2.05)||crossesPage)
    if(wraps&&last) {
      last.text+=wrapSeparator(last.text,line.text)+line.text
      last.node.content=[textNode(last.text)];plain[last.plainIndex]=plain[last.plainIndex]!+wrapSeparator(plain[last.plainIndex]!,line.text)+line.text;last.line=line
      continue
    }
    if(ordered||bullet) {
      const kind=ordered?'orderedList':'bulletList',number=ordered?Number(ordered[1]):0,value=ordered?ordered[2]!:bullet![1]||bullet![2]!
      if(!list||list.type!==kind||(ordered&&number!==nextNumber)) {list={type:kind,...(ordered?{attrs:{start:number}}:{}),content:[]};content.push(list)}
      nextNumber=number+1
      const node=paragraph(value);list.content!.push({type:'listItem',content:[node]});plain.push(line.text)
      last={line,node,text:value,plainIndex:plain.length-1,indent:line.x+normal*1.4};continue
    }
    list=undefined
    const node:JSONContent=heading?{type:'heading',attrs:{level:line.size>normal*1.6?2:3},content:[textNode(line.text)]}:paragraph(line.text)
    content.push(node);plain.push(line.text);last=heading?undefined:{line,node,text:line.text,plainIndex:plain.length-1,indent:line.x}
  }
  return {description:plain.join('\n\n'),descriptionDoc:{type:'doc',content:content.length?content:[paragraph('')]} as JSONContent}
}

/** 按页面坐标还原四列表格，而不是按空格切分：姓名、备注换行、未知字段均需保留。 */
export function parseTapdPages(pages: TapdPage[]): TapdParsed {
  const fields: TapdField[] = [], raw: string[] = [], body: BodyLine[] = [], warnings: string[] = []
  let title = '', workspaceId = '', sourceId = '', displayId = '', basic = false, detailed = false
  let previous: (TapdField | undefined)[] = []
  for (const [pageIndex,page] of pages.entries()) {
    warnings.push(...(page.warnings||[]))
    let bodyTop=detailed?page.height*.95:-1
    const lines: { y: number; items: PdfText[] }[] = []
    for (const item of [...page.items].filter(x => x.str.trim()).sort((a,b) => b.transform[5]! - a.transform[5]! || a.transform[4]! - b.transform[4]!)) {
      let line = lines.find(row => Math.abs(row.y - item.transform[5]!) < 2)
      if (!line) { line = { y: item.transform[5]!, items: [] }; lines.push(line) }
      line.items.push(item)
    }
    const join=joinPdfText
    for (const line of lines) {
      const text = join(line.items); raw.push(text)
      const source = text.match(/https:\/\/(?:www\.)?tapd\.cn\/(\d+)\/[^\s]*?print_story\/(\d+)/)
      if (source) {
        if ((workspaceId && workspaceId!==source[1]) || (sourceId && sourceId!==source[2])) throw Error('PDF 包含多个需求，请拆成单条需求文件后导入')
        workspaceId ||= source[1]!; sourceId ||= source[2]!
      }
      // 浏览器打印页眉/页脚不属于需求正文，但仍在 rawText 与原始 PDF 留档。
      if (line.y > page.height * .95 || line.y < page.height * .07) continue
      const heading = text.match(/(?:STORY\s*)?[【\[]?\s*ID\s*(\d+)\s*[】\]]\s*(.+)/i)
      if (heading) {
        if (displayId && displayId!==heading[1]) throw Error('PDF 包含多个需求，请拆成单条需求文件后导入')
        if (!title) {displayId = heading[1]!; title = heading[2]!.split(/[\uE000-\uF8FF]|立即打印/)[0]!.trim()}
        continue
      }
      if (text === '基础信息') { basic = true; detailed = false; previous = []; continue }
      if (text.startsWith('详细描述')) { basic = false; detailed = true; bodyTop=line.y; continue }
      if (basic) {
        const cells = [[],[],[],[]] as PdfText[][]
        for (const item of line.items) { const x = item.transform[4]! / page.width; cells[x < .20 ? 0 : x < .50 ? 1 : x < .69 ? 2 : 3]!.push(item) }
        for (let side = 0; side < 2; side++) {
          const label = join(cells[side * 2]!), value = join(cells[side * 2 + 1]!)
          if (label) { const field = { label, value }; fields.push(field); previous[side] = field }
          else if (value && previous[side]) previous[side]!.value += '\n' + value
        }
      } else if (detailed) {
        const size=Math.max(...line.items.map(fontSize))
        if(line.items.some(item=>Math.abs(item.transform[1]!)>.01||Math.abs(item.transform[2]!)>.01))warnings.push(`第 ${pageIndex+1} 页存在旋转或倾斜文字，已降级为位置排序，请对照原始 PDF 核对。`)
        body.push({text,x:Math.min(...line.items.map(x=>x.transform[4]!)),right:Math.max(...line.items.map(x=>x.transform[4]!+x.width)),y:line.y,size,page:pageIndex,pageHeight:page.height,columns:joinPdfText(line.items,'\t').split('\t')})
      }
    }
    for(const image of page.images||[])if(image.y<=bodyTop&&image.y>page.height*.07)body.push({text:'',x:image.x,right:image.x+image.width,y:image.y,size:0,page:pageIndex,pageHeight:page.height,columns:[],image})
  }
  if (!title || !workspaceId || !sourceId || !fields.length) throw Error('未识别到 TAPD 需求标题、来源编号或基础信息，请使用 TAPD 需求打印导出的 PDF')
  warnings.push('PDF 未包含的评论、子需求、测试用例和外部附件不会自动补造；如需迁移，请另行导出源数据。', '原始字段与 PDF 留档；可提取的原图独立迁移，整页截图不会写入正文。来源创建人及时间仅留档。', '复杂版式可能降级：多栏、合并表格、矢量图、公式及图片裁剪或叠加效果不能保证还原。请对照原始 PDF 核对，未识别内容不会补造。')
  const {description,descriptionDoc}=bodyDocument(body.sort((a,b)=>a.page-b.page||b.y-a.y||a.x-b.x),warnings)
  if (!description) warnings.push('未提取到正文文字，请核对原始 PDF，必要时手动补充正文。')
  if (/[\uFFFD\u0F00-\u109F]/.test(description)) warnings.push('正文可能存在字体编码异常，请逐页核对原始 PDF。')
  return { title, workspaceId, sourceId, displayId, fields, description, descriptionDoc, rawText: raw.join('\n'), warnings:[...new Set(warnings)] }
}

export type TapdTarget = { key: string; label: string; kind: 'text' | 'number' | 'boolean' | 'members' | 'choice' | 'date'; options?: { value: string; label: string }[]; single?: boolean; memberRoles?: string[]; departmentId?: string; memberIds?: string[] }
export type TapdMapping = { field: TapdField; target: string; value: any; issue: string }
/** PDF 常在中文姓名中插入空格或换行；只归一化字形与排版，不猜测谐音、拼音或近似姓名。 */
export const normalizeTapdName = (name:string) => normalizeTapdText(name).replace(/[\u200B-\u200D\uFEFF]/g,'').trim().replace(/\s+/g,' ').replace(/(?<=\p{Script=Han}) +(?=\p{Script=Han})/gu,'')

export function tapdFileError(files: readonly {name:string;size:number}[]):string {
  if(files.length!==1)return '请一次选择一个 PDF 文件'
  if(!/\.pdf$/i.test(files[0]!.name))return '仅支持 TAPD 导出的 PDF 文件'
  if(!files[0]!.size||files[0]!.size>10*1024*1024)return 'PDF 文件须大于 0 且不超过 10 MiB'
  return ''
}
const nativeAliases: Record<string,string> = {
  状态:'status',需求分类:'category',迭代:'sprint',优先级:'priority',产品经理:'ownerUserIds',产品负责人:'ownerUserIds',处理人:'assigneeUserIds',
  前端工程师:'role.frontend.members',后端工程师:'role.backend.members',算法及架构师:'role.algorithm.members',算法工程师:'role.algorithm.members',UI设计师:'role.ui.members',
  前端开发难度:'role.frontend.value',后端开发难度:'role.backend.value',算法架构难度:'role.algorithm.value',
  标签:'tags',备注:'remarks',涉及敏感数据:'sensitive',涉及权限认证:'authImpact',开始时间:'startDate',结束时间:'endDate',
}
export function suggestTapdMapping(field: TapdField, targets: TapdTarget[], members: {id:string;name:string;active?:boolean;isCurrent?:boolean}[]): TapdMapping {
  const label = field.label.replace(/\s/g,''), key = nativeAliases[label]
  const target = targets.find(x=>x.key===key) || targets.find(x=>x.key.startsWith('cf.')&&x.label.replace(/\s/g,'')===label)
  const row: TapdMapping = { field, target: target?.key || '', value: '', issue: '' }
  if (!target || tapdBlank(field.value)) { row.target=''; row.issue=target?'原字段为空，保留原值，不覆盖系统默认值':'没有等义字段，保留在来源记录；可手动选择目标字段'; return row }
  const value = field.value.trim()
  if (target.kind==='members') {
    // /members 已限定项目范围；isCurrent 仅表示当前登录人，绝不能用于过滤其他项目成员。
    const names = [...new Set(value.split(/[;；、,，]/).flatMap(part=>members.some(x=>normalizeTapdName(x.name)===normalizeTapdName(part))?[part]:part.split(/\r?\n/)).map(normalizeTapdName).filter(Boolean))], ids:string[] = [], missing:string[] = []
    for (const name of names) {
      const named=members.filter(x=>normalizeTapdName(x.name)===name)
      const matches=[...new Map(named.filter(x=>x.active!==false).map(x=>[x.id,x])).values()]
      if(matches.length===1&&(!target.memberIds||target.memberIds.includes(matches[0]!.id)))ids.push(matches[0]!.id)
      else if(matches.length===1)missing.push(name+'（不符合目标字段的角色或部门限制）')
      else missing.push(name+(matches.length>1?'（同名，请确认）':named.length?'（账号停用或无项目权限）':'（项目中未找到）'))
    }
    row.value=[...new Set(ids)]
    if(missing.length)row.issue='请人工匹配人员：'+missing.join('、')
    if(target.single&&ids.length>1)row.issue='目标字段仅支持一人，请人工选择；原始多人信息保留'
  } else if (target.kind==='choice') {
    const lookup=target.key==='sprint'?value.replace(/\s*[（(]当前迭代[）)]\s*$/,''):value
    const options=(target.options||[]).filter(x=>x.label===lookup||x.value===lookup)
    if(options.length===1)row.value=options[0]!.value
    else {row.value='';row.issue='当前项目无唯一匹配项，请选择目标值或仅保留来源'}
  } else if(target.kind==='number') {
    row.value=Number(value);if(!/^\d+(?:\.\d+)?$/.test(value)||!Number.isFinite(row.value)||row.value<0){row.value='';row.issue='数值无法直接映射，请核对'}
  } else if(target.kind==='boolean') {
    if(['是','否','true','false'].includes(value))row.value=['是','true'].includes(value)?'true':'false'
    else {row.value='';row.issue='布尔值无法直接映射，请核对'}
  } else if(target.kind==='date') {
    if(validTapdDate(value))row.value=value
    else {row.value='';row.issue='日期须为 YYYY-MM-DD，请核对'}
  } else row.value=value
  return row
}

function validTapdDate(value: unknown): value is string {
  if(typeof value!=='string'||!/^\d{4}-\d{2}-\d{2}$/.test(value))return false
  const date=new Date(value+'T00:00:00Z')
  return Number.isFinite(date.getTime())&&date.toISOString().slice(0,10)===value
}

export function tapdMemberIssue(value: unknown, target?: TapdTarget): string {
  if(target?.kind!=='members'||!Array.isArray(value))return ''
  if(target.memberIds&&value.some(id=>!target.memberIds!.includes(id)))return '所选人员不符合目标字段的角色、部门或项目范围，请重新选择'
  if(target.single&&value.length>1)return '此目标字段只能选择一人'
  return ''
}

/** Match the server's document bounds before mounting an editor or submitting; never truncate to fit. */
export function tapdDocumentError(doc: JSONContent, plainText: string): string {
  let count=0
  const pending=[{node:doc,depth:0}]
  while(pending.length){
    const {node,depth}=pending.pop()!
    if(++count>5000||depth>24)return '正文结构超过导入限制，请拆分或精简后重试；内容未被截断'
    for(const child of node.content||[])pending.push({node:child,depth:depth+1})
  }
  if([...plainText].length>100000)return '正文不能超过 100000 字，请拆分或精简后重试；内容未被截断'
  return ''
}

// 原始语义不等价时不自动映射，例如“UI 奖励”不是“UI 难度”，“前端组长”不是工程师。
export function buildTapdRequirement(title:string,description:string,rows:TapdMapping[],targets:TapdTarget[]) {
  const result:Record<string,any>={title,type:'产品需求',description,category:'未分类',sprint:'待规划',priority:'P2',status:'',customFields:{},roleWeights:{}}
  const used=new Set<string>()
  for(const row of rows){
    if(!row.target)continue
    if(used.has(row.target))throw Error('同一目标字段不能映射多次，请合并或保留来源')
    used.add(row.target)
    const target=targets.find(x=>x.key===row.target)
    if(!target||row.issue)throw Error('请处理字段映射提示后再导入')
    let value=row.value
    if(target.kind==='members'){if(!Array.isArray(value)||!value.length)throw Error('请选择人员或保留来源');const issue=tapdMemberIssue(value,target);if(issue)throw Error(issue);value=target.single?value[0]:value}
    if(target.kind==='choice'&&!target.options?.some(x=>x.value===value))throw Error('请选择有效的目标值')
    if(target.kind==='number'){if(value===''||!Number.isFinite(Number(value))||Number(value)<0)throw Error('请输入有效的非负数');value=Number(value)}
    if(target.kind==='boolean'){if(!['true','false'].includes(value))throw Error('请选择是或否');value=value==='true'}
    if(target.kind==='date'&&!validTapdDate(value))throw Error('日期须为有效的 YYYY-MM-DD，请核对')
    if(row.target.startsWith('role.')){const [,role,part]=row.target.split('.');const weight=result.roleWeights[role!]||{userIds:[],value:null};weight[part==='members'?'userIds':'value']=value;result.roleWeights[role!]=weight}
    else if(row.target.startsWith('cf.'))result.customFields[row.target.slice(3)]=value
    else result[row.target]=value
  }
  return result
}
