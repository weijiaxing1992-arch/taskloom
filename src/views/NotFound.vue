<script setup lang="ts">
import { t } from '../i18n'
</script>
<template>
  <section class="missing-page">
    <span class="missing-page-code" aria-hidden="true">404</span>
    <h1>{{t('这个页面不存在')}}</h1>
    <p>{{t('链接可能已变更。可以返回我的工作，或通过全局搜索查找内容。')}}</p>
    <div><router-link class="btn primary" to="/my-work">{{t('返回我的工作')}}</router-link><router-link class="btn" to="/search">{{t('全局搜索')}}</router-link></div>
  </section>
</template>
<style scoped>
.missing-page{display:grid;justify-items:center;align-content:center;min-height:50dvh;padding:32px 20px;text-align:center;color:var(--ink);gap:12px}.missing-page-code{font-size:48px;font-weight:650;line-height:1;color:var(--primary);font-variant-numeric:tabular-nums}.missing-page h1,.missing-page p{margin:0}.missing-page p{max-width:460px;color:var(--muted);line-height:1.8}.missing-page>div{display:flex;justify-content:center;flex-wrap:wrap;gap:12px;margin-top:10px}.missing-page a{text-decoration:none}
</style>
