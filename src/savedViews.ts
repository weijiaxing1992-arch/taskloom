import type { WorkFilter } from './workItemQuery'
// 保存视图是查询配置而非查询结果。key/ID 为稳定业务标识，name/label 仅供显示。
// 分类与迭代名称可修改，referenceId/categoryId/sprintId 防止旧名称误指向同名新记录。
// 当前目录解析、失效引用阻止查询在 Requirements.vue；本模块仅验证结构并复制数据。
export type SavedViewFilter=WorkFilter&{referenceId?:number}
export type RequirementViewConfig={schema:1;relatedToMe?:boolean;q:string;statuses:string[];statusCategory:string;assigneeMode:'any'|'me'|'member';assigneeUserId:string;sprint:string;sprintId?:number;category:string;categoryId?:number;priority:string;filters:SavedViewFilter[];columns:string[];sort:string;order:'asc'|'desc';view:'list'|'board'}
export type SavedRequirementView={id:number;name:string;scope:'personal'|'shared';ownerUserId:string;version:number;config:RequirementViewConfig;canManage:boolean;updatedAt:string}
export function viewConfig(value:any):RequirementViewConfig{
 // 拒绝损坏/超限配置，不删除“无法理解”的筛选后继续查，否则会意外放宽可见结果。
 // schema 升级需显式迁移；不能直接把旧版本按新字段含义解释。
 if(!value||value.relatedToMe!==undefined&&typeof value.relatedToMe!=='boolean'||value.schema!==1||!['list','board'].includes(value.view)||!['asc','desc'].includes(value.order)||!['any','me','member'].includes(value.assigneeMode)||!['','todo','doing','done','cancelled'].includes(value.statusCategory)||!['','P0','P1','P2','P3'].includes(value.priority))throw Error('保存视图数据格式不正确，请重试')
 for(const key of ['q','assigneeUserId','sprint','category','sort'])if(typeof value[key]!=='string')throw Error('保存视图数据格式不正确，请重试')
 for(const key of ['sprintId','categoryId'])if(value[key]!==undefined&&(!Number.isSafeInteger(value[key])||value[key]<0))throw Error('保存视图数据格式不正确，请重试')
 for(const [key,limit] of Object.entries({q:2000,assigneeUserId:128,sprint:500,category:500}))if(new TextEncoder().encode(value[key]).length>limit)throw Error('保存视图数据格式不正确，请重试')
 if(!Array.isArray(value.statuses)||new Set(value.statuses).size!==value.statuses.length||value.statuses.some((key:any)=>typeof key!=='string'||!key||new TextEncoder().encode(key).length>128))throw Error('保存视图数据格式不正确，请重试')
 if(value.assigneeMode==='member'&&!value.assigneeUserId||value.assigneeMode!=='member'&&value.assigneeUserId||!Array.isArray(value.statuses)||value.statuses.length>100||value.statuses.some((key:any)=>typeof key!=='string')||!Array.isArray(value.columns)||value.columns.length>100||value.columns[0]!=='code'||value.columns[1]!=='title'||value.columns.some((key:any)=>typeof key!=='string')||new Set(value.columns).size!==value.columns.length||!Array.isArray(value.filters)||value.filters.length>30)throw Error('保存视图数据格式不正确，请重试')
 for(const rule of value.filters){if(!rule||typeof rule.field!=='string'||!rule.field||!['eq','neq','contains','not_contains','gt','gte','lt','lte','includes','not_includes','is_empty','not_empty'].includes(rule.operator)||(!['is_empty','not_empty'].includes(rule.operator)?!['string','number','boolean'].includes(typeof rule.value)||rule.value===''||typeof rule.value==='number'&&(!Number.isFinite(rule.value)||Math.abs(rule.value)>1e15):rule.value!==undefined))throw Error('保存视图数据格式不正确，请重试')}
 for(const rule of value.filters)if(rule.referenceId!==undefined&&(!Number.isSafeInteger(rule.referenceId)||rule.referenceId<1||!['category','sprint'].includes(rule.field)||!['eq','neq'].includes(rule.operator)||typeof rule.value!=='string'))throw Error('保存视图数据格式不正确，请重试')
 return JSON.parse(JSON.stringify(value))
}
export function savedView(value:any):SavedRequirementView{if(!value||!Number.isSafeInteger(value.id)||value.id<1||!Number.isSafeInteger(value.version)||value.version<1||typeof value.name!=='string'||!value.name||!['personal','shared'].includes(value.scope)||typeof value.ownerUserId!=='string'||typeof value.canManage!=='boolean')throw Error('保存视图数据格式不正确，请重试');return{...value,config:viewConfig(value.config)}}
// version 由保存请求用于乐观并发控制；canManage 是服务端返回的界面能力，
// 不能据此替代服务端对个人所有权/共享视图管理权限的再次验证。
