import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import {startSearchHighlights} from './searchHighlight'
// 路由按需加载，减少首屏脚本体积；发布时保留旧哈希资源，保障已打开页面后续跳转。
const Requirements = () => import('./views/Requirements.vue')
const Editor = () => import('./views/Editor.vue')
const Organization = () => import('./views/Organization.vue')
const JoinOrganization = () => import('./views/JoinOrganization.vue')
const Fields = () => import('./views/Fields.vue')
const Sprints = () => import('./views/Sprints.vue')
const Defects = () => import('./views/Defects.vue')
const Testing = () => import('./views/Testing.vue')
const Notifications = () => import('./views/Notifications.vue')
const Projects = () => import('./views/Projects.vue')
const MyWork = () => import('./views/MyWork.vue')
const Search = () => import('./views/Search.vue')
const Profile = () => import('./views/Profile.vue')
const Workload = () => import('./views/Workload.vue')
const ReleaseNotesCenter = () => import('./views/ReleaseNotesCenter.vue')
const Roadmap = () => import('./views/Roadmap.vue')
const Dashboard = () => import('./views/Dashboard.vue')
const AISettings = () => import('./views/AISettings.vue')
const AuditLog = () => import('./views/AuditLog.vue')
const Integrations = () => import('./views/Integrations.vue')
const Help = () => import('./views/Help.vue')
const NotFound = () => import('./views/NotFound.vue')
// 首先声明全站控件契约，阻止异步页面和工具类重新定义同类控件规范。
import './ui-standards.css'
import './style.css'
// 样式导入顺序有意保留：基础 → 历史模块 → 组件层 → 移动适配 → 小范围协作视觉覆盖。
// 调整顺序后需同时验证暗色、移动端、抽屉和浮层，不应靠大范围 !important 修补。
import './v2.css'
import './v3.css'
import './tailwind.css'
import './mobile.css'
import './collaboration.css'
import './sidebar.css'
// shadcn-vue 语义色板与保守的控件基线位于历史样式之后，避免重置既有业务模块布局。
import './shadcn-theme.css'
// 工作项列表、看板与详情抽屉的统一视觉层；只使用语义令牌，不接管业务交互。
import './work-items-visual.css'
// 公开页、账号页和组织管理页同样从语义色板取值，静态导入也避免异步路由切换时
// 出现一帧历史浅色样式。
import './account-visual.css'
import './organization-visual.css'
// 页面滚动、下拉和分页的收口规则最后导入，避免历史页面覆盖公共可用性基线。
import './app-page-controls.css'
// 列表页采用紧凑页头和扁平工具栏，不影响编辑表单、详情抽屉与弹窗。
import './compact-page-layout.css'
// 移动端收口位于历史布局之后；宽表仅在自己的容器内横向滚动。
import './mobile-refinements.css'
// 原生 select 渐进增强与现有弹层共用外观，不接管值、事件或权限。
import './dropdown-controls.css'
// 表单与配置在桌面侧向展开、手机底部展开；危险确认仍保留独立提示。
import './slide-panels.css'
// 最后一层仅提供轻玻璃材质与微动效，不改变各业务页面的布局和交互语义。
import './glass-system.css'
// Vue 原生共享微动效：按压波纹、可点击卡片抬升和抽起层过渡，不改变业务事件。
import './motion-system.css'

// 路由仅负责页面切换；登录/禁用界面守卫在 App.vue，真正权限校验在 Go API。
const router=createRouter({history:createWebHistory(),routes:[
 // 首页是跨项目工作台，品牌入口与根路由始终回到项目空间而非某个项目的需求列表。
 {path:'/',redirect:'/projects'}, {path:'/requirements',component:Requirements},
 {path:'/requirements/new',component:Editor,beforeEnter:to=>{
   if(typeof to.query.parentId==='string'&&/^[1-9]\d*$/.test(to.query.parentId)){
     const {parentId,...query}=to.query
     return {path:'/requirements',query:{...query,req:parentId,createChild:'1'},replace:true}
   }
 }}, {path:'/requirements/:id/edit',component:Editor},
 {path:'/join/:token',component:JoinOrganization,meta:{public:true}},
 {path:'/share/requirements/:token',component:()=>import('./views/SharedRequirement.vue'),meta:{public:true}},
 {path:'/organization/:section?',component:Organization}, {path:'/members',redirect:'/organization/members'}, {path:'/settings/fields',component:Fields},
 {path:'/iterations',component:Sprints}, {path:'/defects',component:Defects}, {path:'/tests',component:Testing},
 {path:'/notifications',component:Notifications}, {path:'/projects',component:Projects},
 {path:'/my-work',component:MyWork}, {path:'/search',component:Search}, {path:'/profile',component:Profile},
 {path:'/help/:document?',component:Help},
 {path:'/reports/workload',component:Workload}, {path:'/reports/release-notes',component:ReleaseNotesCenter}, {path:'/roadmap',component:Roadmap}, {path:'/dashboard',component:Dashboard}, {path:'/settings/ai',component:AISettings}, {path:'/audit',component:AuditLog}, {path:'/settings/integrations',component:Integrations},
 {path:'/:pathMatch(.*)*',component:NotFound}
]})
createApp(App).use(createPinia()).use(router).mount('#app')
const stopSearchHighlights=startSearchHighlights()
if(import.meta.hot)import.meta.hot.dispose(stopSearchHighlights)
