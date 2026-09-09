<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { t } from '../i18n'
import Icon from '../components/Icon.vue'
import SidebarCollapseButton from '../components/SidebarCollapseButton.vue'
import { useLayoutBoolean } from '../layoutScope'
import { helpDocuments, openAPIURL } from '../helpContent'
import { renderHelpSection, searchHelpDocuments } from '../helpDocumentation'

const route = useRoute(), router = useRouter()
const query = ref(''), notice = ref(''), directoryOpen = ref(false)
const helpDirectoryExpanded=useLayoutBoolean('help.directory.sidebar',true)
// 移动端点击“查看目录”时，即使桌面曾收起侧栏，也必须实际显示章节。
watch(directoryOpen, open => { if (open) helpDirectoryExpanded.value = true })
const reader = ref<HTMLElement | null>(null)
const documentID = computed(() => typeof route.params.document === 'string' && route.params.document ? route.params.document : 'guide')
const currentDocument = computed(() => helpDocuments.find(item => item.id === documentID.value))
const anchor = computed(() => { try { return decodeURIComponent(route.hash.slice(1)) } catch { return route.hash.slice(1) } })
const section = computed(() => {
  const sections = currentDocument.value?.sections || []
  return anchor.value ? sections.find(item => item.id === anchor.value || item.headings.some(heading => heading.id === anchor.value)) : sections[0]
})
const sectionIndex = computed(() => currentDocument.value?.sections.findIndex(item => item === section.value) ?? -1)
const previous = computed(() => currentDocument.value?.sections[sectionIndex.value - 1])
const next = computed(() => currentDocument.value?.sections[sectionIndex.value + 1])
const rendered = computed(() => section.value ? renderHelpSection(section.value, openAPIURL) : '')
const searching = computed(() => !!query.value.trim())
const results = computed(() => searchHelpDocuments(helpDocuments, query.value))
const sectionURL = (id: string, document = documentID.value) => `/help/${document}#${encodeURIComponent(id)}`

watch(() => route.fullPath, async () => {
  query.value = ''; notice.value = ''; directoryOpen.value = false
  await nextTick()
  if (!reader.value) return
  reader.value.scrollTop = 0
  // 只在当前阅读区查找锚点，不把 URL 拼进选择器；支持中文和同名章节的独立链接。
  const heading = [...reader.value.querySelectorAll<HTMLElement>('[id]')].find(item => item.id === anchor.value)
  if (heading && section.value?.id !== anchor.value) reader.value.scrollTop += heading.getBoundingClientRect().top - reader.value.getBoundingClientRect().top - 20
}, { immediate: true })

function followDocumentLink(event: MouseEvent) {
  if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || !(event.target instanceof Element)) return
  const link = event.target.closest('a'), href = link?.getAttribute('href')
  if (!href || link?.target === '_blank' || link?.hasAttribute('download') || !/^[/#]/.test(href)) return
  event.preventDefault()
  void router.push(href.startsWith('#') ? `/help/${documentID.value}${href}` : href)
}

function downloadDocument() {
  const current = currentDocument.value
  if (!current) return
  const url = URL.createObjectURL(new Blob([current.markdown], { type: 'text/markdown;charset=utf-8' }))
  const link = document.createElement('a')
  link.href = url; link.download = current.filename; document.body.appendChild(link); link.click(); link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
  notice.value = '已开始下载当前完整文档'
}

async function copyLink() {
  if (!section.value) return
  // 仅复制文档路径，不携带项目参数、凭据或业务上下文。
  const url = new URL(sectionURL(anchor.value || section.value.id), window.location.origin).href
  try { await navigator.clipboard.writeText(url); notice.value = '章节链接已复制' }
  catch { notice.value = '浏览器未允许复制，请复制地址栏中的文档链接' }
}
</script>

<template>
  <section class="help-center" :aria-label="t('帮助中心')">
    <header class="help-toolbar">
      <div class="help-brand"><Icon name="help" :size="20"/><h1>{{ t('帮助中心') }}</h1><small>{{ t('中文文档 · 随版本发布') }}</small></div>
      <label class="search help-search"><Icon name="search" :size="16"/><input v-model="query" type="search" :aria-label="t('搜索全部帮助与 API 文档')" :placeholder="t('搜索操作、接口或错误码…')" maxlength="160" @keydown.esc="query=''"/></label>
    </header>
    <nav class="help-document-nav" :aria-label="t('文档类型')">
      <router-link v-for="item in helpDocuments" :key="item.id" :to="'/help/'+item.id" :aria-current="currentDocument?.id === item.id ? 'page' : undefined">{{ t(item.title) }}</router-link>
    </nav>

    <div v-if="searching" class="help-search-results" role="region" :aria-label="t('文档搜索结果')">
      <div class="help-results-heading"><h2>{{ t('搜索结果') }} <small>{{ t('{count} 个章节', { count: results.length }) }}</small></h2><button class="btn" @click="query=''">{{ t('退出搜索') }}</button></div>
      <p v-if="!results.length" class="help-empty">{{ t('没有找到相关内容，请尝试“通知”“测试用例”“If-Match”等关键词。') }}</p>
      <router-link v-for="item in results" :key="item.documentId+':'+item.sectionId" class="help-result" :to="sectionURL(item.sectionId,item.documentId)" @click="query=''">
        <small>{{ t(item.documentTitle) }}</small><h3>{{ item.title }}</h3><p>{{ item.excerpt }}</p>
      </router-link>
    </div>
    <div v-else-if="!currentDocument" class="help-empty" role="status"><h2>{{ t('未找到这份文档') }}</h2><p>{{ t('请选择上方文档，或返回产品帮助手册。') }}</p><router-link class="btn" to="/help/guide">{{ t('打开产品帮助手册') }}</router-link></div>
    <div v-else class="help-workspace">
      <div class="help-directory-toggle-row">
        <button class="btn" :aria-expanded="directoryOpen" aria-controls="help-directory" @click="directoryOpen=!directoryOpen">{{ t(directoryOpen ? '收起目录' : '查看章节目录') }}</button>
      </div>
      <aside id="help-directory" class="help-directory" :class="{'is-open':directoryOpen,'is-collapsed':!helpDirectoryExpanded}"><SidebarCollapseButton :expanded="helpDirectoryExpanded" :label="t('帮助目录侧边栏')" @toggle="helpDirectoryExpanded=!helpDirectoryExpanded"/><div v-show="helpDirectoryExpanded" class="help-directory-content">
        <div class="help-directory-caption"><b>{{ t('章节目录') }}</b><small>{{ t('{count} 章', { count: currentDocument.sections.length }) }}</small></div>
        <nav :aria-label="t('章节目录')">
          <template v-for="item in currentDocument.sections" :key="item.id">
            <router-link :to="sectionURL(item.id)" :class="{'is-current':section===item}" :aria-current="section===item ? 'location' : undefined">{{ item.title }}</router-link>
            <template v-if="section===item">
              <router-link v-for="heading in item.headings.filter(heading=>heading.id!==item.id)" :key="heading.id" :to="sectionURL(heading.id)" class="help-subheading" :class="{'is-anchor':anchor===heading.id}">{{ heading.title }}</router-link>
            </template>
          </template>
        </nav>
        <small class="help-source">{{ t('代码包 / docs /') }}<br/>{{ currentDocument.filename }}</small>
        </div>
      </aside>
      <div class="help-reading-area">
        <div class="help-reading-toolbar">
          <span class="help-reading-label">{{ t(currentDocument.title) }}</span>
          <div class="help-reading-actions">
            <button class="btn" :disabled="!section" @click="copyLink">{{ t('复制链接') }}</button>
            <button class="btn" @click="downloadDocument">{{ t('下载文档') }}</button>
            <a v-if="currentDocument.id==='api'||currentDocument.id==='internal-api'" class="btn" :href="openAPIURL" download="devflow-openapi.json">{{ t('下载 OpenAPI') }}</a>
          </div>
        </div>
        <p v-if="notice" class="help-notice" role="status">{{ t(notice) }}</p>
        <div ref="reader" class="help-reader" tabindex="0" :aria-label="t('文档正文')">
          <div v-if="!section" class="help-empty" role="status"><h2>{{ t('未找到这个章节') }}</h2><p>{{ t('章节可能已调整，请从左侧目录重新选择。') }}</p><router-link class="btn" :to="'/help/'+documentID">{{ t('返回文档概览') }}</router-link></div>
          <template v-else>
            <div class="help-chapter-meta">{{ sectionIndex+1 }} / {{ currentDocument.sections.length }} · {{ t(currentDocument.description) }}</div>
            <!-- 只渲染经过白名单链接与文本转义处理的仓库文档，绝不直接渲染业务 HTML。 -->
            <article class="help-prose" v-html="rendered" @click="followDocumentLink"></article>
            <nav class="help-chapter-navigation" :aria-label="t('前后章节')">
              <router-link v-if="previous" :to="sectionURL(previous.id)"><small>{{ t('上一章') }}</small><span>← {{ previous.title }}</span></router-link><span v-else></span>
              <router-link v-if="next" :to="sectionURL(next.id)"><small>{{ t('下一章') }}</small><span>{{ next.title }} →</span></router-link>
            </nav>
          </template>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.help-center{height:100%;min-height:0;min-width:0;display:flex;flex-direction:column;background:var(--card);color:var(--foreground);overflow:hidden}
.help-toolbar{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:12px 20px;border-bottom:1px solid var(--border);flex:none}
.help-brand{display:flex;align-items:center;gap:10px;min-width:0}.help-brand h1{margin:0;white-space:nowrap}.help-brand>svg{color:var(--primary)}.help-brand small{color:var(--muted-foreground);white-space:nowrap}
.help-search{width:340px;max-width:45%;min-width:180px;box-shadow:none;border:1px solid var(--border);border-radius:var(--ui-control-radius);background:var(--card)}.help-search input{min-width:0;width:100%;box-shadow:none}
.help-document-nav{display:flex;flex:none;gap:18px;padding:0 20px;border-bottom:1px solid var(--border);overflow-x:auto;white-space:nowrap}.help-document-nav a{padding:11px 0 9px;border-bottom:2px solid transparent;color:var(--muted-foreground);text-decoration:none;font-weight:550}.help-document-nav a[aria-current]{color:var(--primary);border-bottom-color:var(--primary)}
.help-workspace{display:flex;flex:1;min-height:0;min-width:0;position:relative}.help-directory{display:flex;flex-direction:column;flex:none;width:252px;min-height:0;border-right:1px solid var(--border);background:var(--card);transition:width .16s ease,padding .16s ease}.help-directory>.sidebar-collapse-toggle{align-self:flex-end;margin:10px 10px -2px;z-index:1}.help-directory.is-collapsed{width:52px}.help-directory.is-collapsed>.sidebar-collapse-toggle{align-self:center;margin:10px auto}.help-directory-content{display:flex;min-height:0;flex:1;flex-direction:column}.help-directory-caption{display:flex;align-items:center;justify-content:space-between;padding:16px 16px 10px}.help-directory-caption small{color:var(--muted-foreground)}.help-directory nav{overflow-y:auto;min-height:0;flex:1;padding:0 8px 12px}.help-directory nav a{display:block;padding:8px;border-radius:4px;line-height:1.5;color:var(--muted-foreground);text-decoration:none;overflow-wrap:anywhere}.help-directory nav a:hover{background:var(--secondary);color:var(--foreground)}.help-directory nav .is-current{background:var(--accent);color:var(--primary);font-weight:600}.help-directory nav .help-subheading{padding:6px 8px 6px 20px;font-size:var(--ui-font-caption)}.help-directory nav .is-anchor{color:var(--primary)}.help-source{flex:none;line-height:1.6;padding:12px 16px;border-top:1px solid var(--border);color:var(--muted-foreground);overflow-wrap:anywhere}
.help-reading-area{display:flex;flex-direction:column;flex:1;min-width:0;min-height:0}.help-reading-toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:10px 20px;flex:none;border-bottom:1px solid var(--border)}.help-reading-label{color:var(--muted-foreground)}.help-reading-actions{display:flex;gap:8px;flex-wrap:wrap}.help-reading-actions a{text-decoration:none}.help-notice{margin:0;padding:8px 20px;background:var(--accent);color:var(--primary);flex:none}.help-reader{overflow:auto;min-width:0;min-height:0;flex:1;padding:24px 32px;position:relative}.help-chapter-meta{max-width:1060px;margin:0 auto 20px;color:var(--muted-foreground);font-size:var(--ui-font-caption);line-height:1.7}.help-prose{max-width:1060px;margin:0 auto;line-height:1.85;overflow-wrap:anywhere}.help-prose :deep(h2),.help-prose :deep(h3),.help-prose :deep(h4){margin:26px 0 12px;line-height:1.5;color:var(--foreground);scroll-margin-top:20px}.help-prose :deep(>h2:first-child){margin-top:0}.help-prose :deep(p){margin:12px 0}.help-prose :deep(a){color:var(--primary);text-decoration:underline;text-underline-offset:3px}.help-prose :deep(ul),.help-prose :deep(ol){padding-left:24px;margin:12px 0}.help-prose :deep(li){margin:6px 0}.help-prose :deep(code){font-family:ui-monospace,SFMono-Regular,Consolas,monospace;font-size:.93em;background:var(--secondary);padding:2px 5px;border-radius:3px;overflow-wrap:anywhere}.help-prose :deep(.help-code){margin:16px 0;border:1px solid var(--border);border-radius:6px;background:var(--secondary);overflow:hidden}.help-prose :deep(.help-code>span){display:block;padding:4px 12px;border-bottom:1px solid var(--border);color:var(--muted-foreground);font-size:var(--ui-font-caption)}.help-prose :deep(pre){padding:12px 16px;margin:0;overflow:auto;line-height:1.65}.help-prose :deep(pre code){padding:0;background:none;white-space:pre;overflow-wrap:normal}.help-prose :deep(.help-table-scroll){max-width:100%;overflow:auto;margin:16px 0;border:1px solid var(--border);border-radius:6px}.help-prose :deep(table){border-collapse:collapse;width:100%;font-size:var(--ui-font-body);table-layout:auto}.help-prose :deep(th),.help-prose :deep(td){text-align:left;vertical-align:top;padding:10px 12px;border-bottom:1px solid var(--border);min-width:100px;line-height:1.7;white-space:normal;color:inherit}.help-prose :deep(th){background:var(--secondary);font-weight:600}.help-prose :deep(tr:last-child td){border-bottom:0}.help-prose :deep(blockquote){margin:16px 0;padding:10px 16px;border-left:3px solid var(--primary);background:var(--accent);color:var(--foreground)}.help-prose :deep(hr){border:0;border-top:1px solid var(--border);margin:24px 0}
.help-chapter-navigation{display:flex;justify-content:space-between;gap:24px;border-top:1px solid var(--border);padding-top:20px;margin:32px auto 0;max-width:1060px}.help-chapter-navigation a{display:grid;gap:4px;color:var(--primary);text-decoration:none;max-width:48%;overflow-wrap:anywhere}.help-chapter-navigation a:last-child{text-align:right}.help-chapter-navigation small{color:var(--muted-foreground)}.help-empty{padding:32px;line-height:1.8;color:var(--muted-foreground)}.help-empty h2{color:var(--foreground)}.help-empty a{text-decoration:none}.help-search-results{flex:1;min-height:0;overflow:auto;padding:24px 32px}.help-results-heading{max-width:1060px;display:flex;align-items:center;justify-content:space-between;margin:0 auto 20px;gap:12px}.help-results-heading h2{margin:0}.help-results-heading small{font-weight:400;color:var(--muted-foreground)}.help-result{display:block;max-width:1060px;margin:0 auto;padding:16px 0;border-bottom:1px solid var(--border);text-decoration:none;color:var(--foreground)}.help-result:hover h3{color:var(--primary)}.help-result small{color:var(--primary)}.help-result h3{margin:6px 0}.help-result p{margin:0;line-height:1.7;color:var(--muted-foreground);overflow-wrap:anywhere}
.help-center a:focus-visible,.help-center button:focus-visible,.help-reader:focus-visible{outline:2px solid var(--primary);outline-offset:-2px}
/* 隐藏外层容器，避免通用按钮规范中的强制 display 影响响应式目录。 */
.help-directory-toggle-row{display:none}
@media(max-width:820px){.help-directory-toggle-row{display:block;align-self:flex-start;margin:8px 12px;flex:none}}
@media(max-width:1100px){.help-brand small{display:none}.help-directory{width:220px}.help-reader{padding:20px}.help-reading-label{display:none}.help-reading-toolbar{justify-content:flex-end}}
@media(max-width:820px){.help-toolbar{padding:10px 12px;flex-wrap:wrap;gap:10px}.help-search{width:100%;max-width:none}.help-document-nav{padding:0 12px;gap:16px}.help-workspace{flex-direction:column}.help-directory{display:none;width:100%;max-height:45%;border-right:0;border-bottom:1px solid var(--border)}.help-directory.is-open{display:flex}.help-directory.is-collapsed{width:100%;max-height:none}.help-directory.is-collapsed>.sidebar-collapse-toggle{margin-left:auto}.help-source{display:none}.help-reading-toolbar{padding:8px 12px;justify-content:flex-start}.help-reader,.help-search-results{padding:16px}.help-reading-actions{gap:6px}.help-chapter-navigation{gap:12px}.help-brand h1{font-size:var(--ui-font-title)}.help-empty{padding:20px}}
</style>
