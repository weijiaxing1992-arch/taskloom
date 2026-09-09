<script setup lang="ts">
import { computed, ref } from 'vue'
import { t } from '../i18n'
import { Button } from './ui/button'
import { parseHelpDocument, renderHelpSection } from '../helpDocumentation'
import acceptance from '../../docs/delivery-acceptance.md?raw'
import capacity from '../../docs/delivery-capacity.md?raw'
import features from '../../docs/delivery-functions.md?raw'
import deployment from '../../docs/delivery-deployment.md?raw'
import topology from '../../docs/delivery-topology.md?raw'
import diagram from '../../docs/delivery-topology.svg?url'

// 与离线交付同源；这里只提供静态知识，不暴露主机路径、业务库或私有凭据。
const documents = [
  { id:'acceptance',title:'验收报告',filename:'delivery-acceptance.md',markdown:acceptance },
  { id:'capacity',title:'服务器清单',filename:'delivery-capacity.md',markdown:capacity },
  { id:'features',title:'功能清单',filename:'delivery-functions.md',markdown:features },
  { id:'deployment',title:'部署流程',filename:'delivery-deployment.md',markdown:deployment },
  { id:'topology',title:'系统拓扑',filename:'delivery-topology.md',markdown:topology },
].map(item=>parseHelpDocument({...item,description:''}))
const selected=ref('acceptance'),query=ref('')
const current=computed(()=>documents.find(item=>item.id===selected.value)!)
const sections=computed(()=>current.value.sections.filter(item=>!query.value.trim()||(item.title+' '+item.markdown).toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase())))
function download(){
  const url=URL.createObjectURL(new Blob([current.value.markdown],{type:'text/markdown;charset=utf-8'}))
  const link=document.createElement('a');link.href=url;link.download=current.value.filename;link.click()
  setTimeout(()=>URL.revokeObjectURL(url),1000)
}
</script>
<template>
  <section class="delivery-center">
    <header><p>{{t('文档与交付源码同源；正式上线前请完成保留项确认。')}}</p><Button variant="outline" size="toolbar" @click="download">{{t('下载当前文档')}}</Button></header>
    <nav :aria-label="t('交付资料')"><Button v-for="item in documents" :key="item.id" :variant="selected===item.id?'default':'outline'" size="toolbar" :aria-pressed="selected===item.id" @click="selected=item.id;query=''">{{t(item.title)}}</Button></nav>
    <input v-model="query" type="search" :aria-label="t('搜索交付资料')" :placeholder="t('搜索交付资料')" class="delivery-search"/>
    <img v-if="selected==='topology'&&!query" :src="diagram" :alt="t('系统拓扑')" class="delivery-topology"/>
    <p v-if="!sections.length" role="status">{{t('无匹配章节')}}</p>
    <article v-for="section in sections" :key="current.id+section.id" class="delivery-document" v-html="renderHelpSection(section)"/>
  </section>
</template>
<style scoped>
.delivery-center{min-width:0;display:grid;gap:14px}.delivery-center header,.delivery-center nav{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.delivery-center header{justify-content:space-between}.delivery-center header p{font-size:13px;color:var(--muted-foreground);margin:0}.delivery-search{width:min(100%,400px);min-width:0;height:34px;padding:6px 10px;font-size:13px;border:1px solid var(--border);border-radius:6px;background:var(--background);box-shadow:none}.delivery-topology{width:100%;max-width:1040px;height:auto}.delivery-document{min-width:0;font-size:14px;line-height:1.8;overflow-wrap:anywhere}.delivery-document :deep(h2){font-size:19px;margin:12px 0}.delivery-document :deep(h3){font-size:16px}.delivery-document :deep(pre){overflow:auto;max-width:100%;padding:14px;border:1px solid var(--border);border-radius:6px;background:var(--secondary);font-size:12px}.delivery-document :deep(table){display:block;width:100%;overflow:auto;border-collapse:collapse;font-size:13px}.delivery-document :deep(th),.delivery-document :deep(td){border:1px solid var(--border);padding:8px 10px;text-align:left;min-width:110px}.delivery-document :deep(th){background:var(--secondary)}.delivery-document :deep(a){color:var(--primary);text-decoration:underline}.delivery-document :deep(p){margin:9px 0}
</style>
