<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { t } from '../i18n'
import Icon from './Icon.vue'

type PreviewTab = 'work' | 'requirements' | 'iterations' | 'notifications'
const pages: Array<{value: PreviewTab;label:string}> = [{value:'work',label:'我的'},{value:'requirements',label:'需求'},{value:'iterations',label:'迭代'},{value:'notifications',label:'通知'}]
const revision = ref(0)

const opened = ref(false)
const tab = ref<PreviewTab>('work')
const trigger = ref<HTMLButtonElement | null>(null)
const closeButton = ref<HTMLButtonElement | null>(null)

// 地址为固定同源入口，不拼接用户输入；预览复用当前 HttpOnly 会话，既不复制凭据，
// 也不会把管理员身份暴露给第三方页面。preview 参数仅用于区分预览语义，移动端可忽略。
const previewURL = computed(() => `/mobile.html?tab=${tab.value}&preview=1&v=${revision.value}`)

async function openPreview() {
  revision.value = Date.now()
  opened.value = true
  await nextTick()
  closeButton.value?.focus()
}

async function closePreview() {
  opened.value = false
  await nextTick()
  trigger.value?.focus()
}

function chooseTab(next: PreviewTab) {
  tab.value = next
}

function keyboard(event: KeyboardEvent) {
  if (!opened.value || event.key !== 'Escape') return
  event.preventDefault()
  void closePreview()
}

onMounted(() => window.addEventListener('keydown', keyboard))
onBeforeUnmount(() => window.removeEventListener('keydown', keyboard))
</script>

<template>
  <button ref="trigger" type="button" class="plain-icon mobile-preview-entry" :aria-label="t('移动端预览')" :title="t('移动端预览')" @click="openPreview">
    <Icon name="mobile" :size="18" />
  </button>

  <Teleport to="body">
    <div v-if="opened" class="mobile-preview-shade" @click.self="closePreview">
      <section class="mobile-preview-dialog" role="dialog" aria-modal="true" :aria-label="t('移动端预览')">
        <header>
          <div>
            <b>{{ t('移动端预览') }}</b>
            <small>{{ t('与手机端使用相同页面和数据') }}</small>
          </div>
          <div class="mobile-preview-actions">
            <button type="button" class="preview-reload" @click="revision = Date.now()">{{ t('刷新') }}</button>
            <a :href="previewURL" target="_blank" rel="noopener noreferrer">{{ t('独立打开') }}</a>
            <button ref="closeButton" type="button" :aria-label="t('关闭预览')" @click="closePreview">×</button>
          </div>
        </header>
        <nav class="mobile-preview-tabs" role="tablist" :aria-label="t('移动端页面')">
          <button v-for="page in pages" :key="page.value" :id="`mobile-preview-${page.value}`" type="button" role="tab" aria-controls="mobile-preview-frame" :aria-selected="tab === page.value" :class="{ active: tab === page.value }" @click="chooseTab(page.value)">{{ page.label }}</button>
        </nav>
        <div id="mobile-preview-frame" class="mobile-preview-stage" role="tabpanel" :aria-labelledby="`mobile-preview-${tab}`">
          <div class="mobile-preview-device">
            <span class="mobile-preview-speaker" aria-hidden="true"></span>
            <iframe :key="previewURL" :src="previewURL" :title="`${pages.find(page => page.value === tab)?.label}移动端预览`" referrerpolicy="no-referrer"></iframe>
          </div>
        </div>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.mobile-preview-actions .preview-reload{width:auto;padding:0 7px;font-size:12px}.mobile-preview-tabs button{flex:1}
.mobile-preview-shade{position:fixed;inset:0;z-index:1000;display:grid;place-items:center;padding:24px;background:#07122580;backdrop-filter:blur(4px)}
.mobile-preview-dialog{display:flex;width:min(560px,100%);max-height:calc(100dvh - 48px);flex-direction:column;overflow:hidden;border:1px solid color-mix(in srgb,var(--border,#dce5f2) 90%,transparent);border-radius:18px;background:var(--surface,#fff);box-shadow:0 28px 90px #07122554;color:var(--ink,#17233d)}
.mobile-preview-dialog>header{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:16px 18px;border-bottom:1px solid var(--border,#dce5f2)}
.mobile-preview-dialog header>div:first-child{display:grid;gap:3px;min-width:0}.mobile-preview-dialog b{font-size:15px}.mobile-preview-dialog small{color:var(--muted,#718099);font-size:12px;line-height:1.5}
.mobile-preview-actions{display:flex;align-items:center;gap:5px;flex:none}.mobile-preview-actions a,.mobile-preview-actions button{display:grid;min-height:32px;place-items:center;border:1px solid transparent;border-radius:8px;background:transparent;color:var(--muted,#718099);font:inherit;text-decoration:none}.mobile-preview-actions a{padding:0 9px;font-size:12px}.mobile-preview-actions a:hover,.mobile-preview-actions button:hover{background:var(--surface-subtle,#f3f7fc);color:var(--primary,#276bff)}.mobile-preview-actions button{width:32px;font-size:23px;line-height:1}
.mobile-preview-tabs{display:flex;gap:4px;padding:10px 16px;border-bottom:1px solid var(--border,#dce5f2);background:var(--surface-subtle,#f8faff)}.mobile-preview-tabs button{min-height:32px;padding:5px 11px;border:1px solid transparent;border-radius:8px;background:transparent;color:var(--muted,#718099);font:inherit;font-size:12px;font-weight:650}.mobile-preview-tabs button.active{border-color:#bdd4ff;background:#eaf2ff;color:#276bff}
.mobile-preview-stage{display:grid;min-height:0;place-items:center;overflow:auto;padding:20px;background:linear-gradient(135deg,#eff5ff,#f9fbff)}.mobile-preview-device{position:relative;width:min(100%,414px);height:min(690px,calc(100dvh - 190px));min-height:410px;overflow:hidden;border:7px solid #17233d;border-radius:30px;background:#fff;box-shadow:0 20px 46px #17376c35}.mobile-preview-speaker{position:absolute;z-index:1;top:7px;left:50%;width:66px;height:5px;border-radius:99px;background:#17233d;transform:translateX(-50%)}.mobile-preview-device iframe{width:100%;height:100%;border:0;background:#f5f8ff}
@media(max-width:820px){.mobile-preview-entry{display:none}.mobile-preview-shade{padding:12px}.mobile-preview-dialog{max-height:calc(100dvh - 24px);border-radius:14px}.mobile-preview-stage{padding:12px}.mobile-preview-device{height:calc(100dvh - 170px);min-height:360px;border-width:5px;border-radius:23px}}
</style>
