<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { t } from '../i18n'
import { useSettingsDialog, useSettingsScope } from './settingsScope'

type Status = { id: number; key: string; name: string; color: string; category: string; enabled: boolean; sortOrder: number; system: boolean }
const emit = defineEmits<{ (event: 'changed'): void }>()
const scope = useSettingsScope(), items = ref<Status[]>([]), canManage = ref(false), loading = ref(false), saving = ref(false), error = ref(''), notice = ref('')
const editing = ref<Status | null>(null), opened = ref(false), baseline = ref('')
const modal = useSettingsDialog(opened, () => close())
const form = reactive({ name: '', color: '#665FE8', category: 'todo', enabled: true, sortOrder: 10 })
const categories: Record<string, string> = { todo: '待办阶段', doing: '进行阶段', done: '完成阶段', cancelled: '取消阶段' }
const explanations: Record<string, string> = { todo: '待安排或尚未开始的需求。', doing: '研发、测试、验收等进行中的需求。', done: '计入已完成数量与迭代完成率。', cancelled: '已取消或已拒绝，不计入交付完成数量。' }
const dirty = computed(() => opened.value && JSON.stringify(form) !== baseline.value)
const mutable = computed(() => canManage.value && !loading.value && !saving.value && !scope.locked.value)
let version = 0
const message = (cause: unknown) => cause instanceof Error ? cause.message : '设置保存失败，请稍后重试'
function color(value: string) { return /^#[0-9a-f]{6}$/i.test(value) ? value : '#665FE8' }
async function load() {
  if (saving.value || !scope.current()) return
  const request = ++version; loading.value = true; error.value = ''
  try { const result = await scope.request<{ items: Status[]; canManage: boolean }>('/requirement-statuses'); if (request === version) { items.value = result.items; canManage.value = result.canManage } }
  catch (cause) { if (request === version && scope.current()) error.value = message(cause) }
  finally { if (request === version) loading.value = false }
}
function open(item?: Status) {
  if (!mutable.value) return
  editing.value = item || null
  Object.assign(form, item ? { name: item.name, color: color(item.color), category: item.category, enabled: item.enabled, sortOrder: item.sortOrder } : { name: '', color: '#665FE8', category: 'todo', enabled: true, sortOrder: Math.max(0, ...items.value.map(item => item.sortOrder)) + 10 })
  baseline.value = JSON.stringify(form); error.value = ''; opened.value = true
}
function close() { if (saving.value) return false; if (dirty.value && !window.confirm(t('放弃尚未保存的设置修改？'))) return false; opened.value = false; return true }
function keydown(event: KeyboardEvent) { if (event.key === 'Escape') { event.preventDefault(); event.stopImmediatePropagation(); close() } }
async function save() {
  if (!mutable.value || !opened.value) return
  if (!form.name.trim()) { error.value = '请填写状态名称'; return }
  if (!/^#[0-9a-f]{6}$/i.test(form.color) || !Number.isSafeInteger(Number(form.sortOrder)) || Number(form.sortOrder) < 0) { error.value = '颜色或排序值不正确'; return }
  const request = version; saving.value = true; error.value = ''
  try {
    const result = await scope.request<Status>(editing.value ? '/requirement-statuses/' + editing.value.id : '/requirement-statuses', { method: editing.value ? 'PATCH' : 'POST', body: JSON.stringify({ ...form, name: form.name.trim(), sortOrder: Number(form.sortOrder) }) })
    if (request !== version) return
    items.value = [...items.value.filter(item => item.id !== result.id), result].sort((a, b) => a.sortOrder - b.sortOrder || a.id - b.id)
    opened.value = false; notice.value = '状态配置已保存'; emit('changed')
  } catch (cause) { if (request === version && scope.current()) error.value = message(cause) }
  finally { if (request === version) saving.value = false }
}
async function fillDefaults() {
  if (!mutable.value || opened.value) return
  saving.value = true; error.value = ''; const request = version; let applied = false
  try { await scope.request('/requirement-statuses/defaults', { method: 'POST', body: '{}' }); if (request === version) { applied = true; notice.value = '缺失的默认状态已补齐，已有配置保持不变'; emit('changed') } }
  catch (cause) { if (request === version && scope.current()) error.value = message(cause) }
  finally { if (request === version) saving.value = false }
  if (applied && scope.current()) await load()
}
onMounted(load)
defineExpose({ dirty, saving, reload: load })
</script>

<template>
  <section class="status-settings" :aria-busy="loading || saving">
    <header class="settings-section-head"><div><h2>{{ t('需求状态') }}</h2><p>{{ t('名称用于展示，稳定标识用于历史关联；停用后不可新选，已有需求仍保留原状态。') }}</p></div><div class="settings-buttons"><button type="button" class="btn" :disabled="!mutable || opened" @click="fillDefaults">{{ t('一键补齐默认配置') }}</button><button type="button" class="btn primary" :disabled="!mutable" @click="open()">＋ {{ t('新增状态') }}</button></div></header>
    <p v-if="scope.locked.value" class="settings-error" role="alert">{{ t('项目或账号已变化，请刷新页面后继续') }}</p>
    <p v-if="!canManage && !loading" class="settings-note">{{ t('仅企业管理员和项目管理员可以修改配置。') }}</p>
    <div class="status-categories"><div v-for="(label,key) in categories" :key="key"><span :class="['phase-dot',key]"></span><b>{{ t(label) }}</b><p>{{ t(explanations[key]!) }}</p></div></div>
    <p v-if="error && !opened" class="settings-error" role="alert">{{ t(error) }} <button type="button" class="link" :disabled="loading || saving" @click="load">{{ t('重新加载') }}</button></p><p v-if="notice" class="settings-success" role="status">{{ t(notice) }}</p>
    <div class="settings-table-wrap"><table class="settings-table"><thead><tr><th>{{ t('状态名称') }}</th><th>{{ t('稳定标识') }}</th><th>{{ t('统计阶段') }}</th><th>{{ t('排序') }}</th><th>{{ t('来源') }}</th><th>{{ t('启停') }}</th><th>{{ t('操作') }}</th></tr></thead><tbody><tr v-if="loading"><td colspan="7">{{ t('正在加载…') }}</td></tr><tr v-for="item in items" :key="item.id"><td><span class="settings-status-chip" :style="{borderColor:color(item.color)}"><i :style="{background:color(item.color)}"></i>{{ item.system && item.name === item.key ? t(item.name) : item.name }}</span></td><td><code>{{ item.key }}</code></td><td>{{ t(categories[item.category] || item.category) }}</td><td>{{ item.sortOrder }}</td><td>{{ item.system ? t('系统默认') : t('自定义') }}</td><td><span :class="item.enabled?'enabled-label':'disabled-label'">{{ item.enabled ? t('已启用') : t('已停用') }}</span></td><td><button type="button" class="link" :disabled="!mutable" @click="open(item)">{{ t('编辑') }}</button></td></tr><tr v-if="!loading && !items.length"><td colspan="7" class="settings-empty">{{ t('暂无状态，可补齐默认配置后继续。') }}</td></tr></tbody></table></div>
    <p class="settings-note">{{ t('补齐仅创建缺失配置，不重命名、不重置颜色、不覆盖已停用状态。') }}</p>
    <div v-if="opened" class="settings-modal-shade" @click.self="close" @keydown="keydown"><form ref="modal" tabindex="-1" class="settings-modal" role="dialog" aria-modal="true" :aria-label="t(editing?'编辑状态':'新增状态')" @submit.prevent="save"><header><div><h2>{{ t(editing?'编辑状态':'新增状态') }}</h2><p v-if="editing"><code>{{ editing.key }}</code> · {{ t('稳定标识不可修改') }}</p></div><button type="button" :disabled="saving" :aria-label="t('关闭')" @click="close">×</button></header><fieldset :disabled="saving || scope.locked.value"><label>{{ t('状态名称') }} *<input v-model="form.name" required maxlength="80" :aria-label="t('状态名称')"></label><label>{{ t('状态颜色') }}<div class="color-input"><input v-model="form.color" type="color" :aria-label="t('选择状态颜色')"><input v-model="form.color" pattern="#[0-9a-fA-F]{6}" maxlength="7" :aria-label="t('状态颜色')"></div></label><label>{{ t('统计阶段') }}<select v-model="form.category" :aria-label="t('统计阶段')"><option v-for="(label,key) in categories" :key="key" :value="key">{{ t(label) }}</option></select><small>{{ t(explanations[form.category]!) }}</small></label><label>{{ t('排序') }}<input v-model.number="form.sortOrder" type="number" min="0" step="1" :aria-label="t('排序')"></label><label class="settings-check"><input v-model="form.enabled" type="checkbox">{{ t('启用此状态') }}</label><small>{{ t('起始状态或最后一个有效结束状态不能停用；状态调整可能同步更新工作流。') }}</small></fieldset><p v-if="error" class="settings-error" role="alert">{{ t(error) }}</p><footer><button type="button" class="btn" :disabled="saving" @click="close">{{ t('取消') }}</button><button type="submit" class="btn primary" :disabled="!mutable">{{ saving ? t('保存中…') : t('保存状态') }}</button></footer></form></div>
  </section>
</template>

<style scoped>
.status-categories{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;margin:20px 0}.status-categories>div{border:1px solid var(--line,#e6e9f0);border-radius:10px;padding:14px;background:var(--surface,#fff)}.status-categories b{font-size:12px}.status-categories p{margin:8px 0 0;font-size:11px;color:var(--muted,#7a8597);line-height:1.6}.phase-dot{display:inline-block;width:7px;height:7px;border-radius:50%;background:#8b94a5;margin-right:7px}.phase-dot.doing{background:#6a63e8}.phase-dot.done{background:#19a782}.phase-dot.cancelled{background:#da8799}.color-input{display:flex;gap:10px}.color-input input[type=color]{width:50px;flex:none;padding:4px}.color-input input{min-width:0}.settings-status-chip{display:inline-flex;gap:7px;align-items:center;white-space:nowrap;border:1px solid;border-radius:6px;padding:5px 8px;font-size:12px}.settings-status-chip i{width:6px;height:6px;border-radius:50%}@media(max-width:1000px){.status-categories{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>

<style scoped>
/* 状态配置卡片只定义业务色，表面与弱文本使用统一令牌以适配深色皮肤。 */
.status-categories>div{background:var(--card);border-color:var(--border)}
.status-categories p{color:var(--muted-foreground)}
.phase-dot{background:var(--muted)}
.phase-dot.doing{background:var(--primary)}
.phase-dot.done{background:var(--success)}
.phase-dot.cancelled{background:var(--danger)}
.settings-status-chip{background:var(--background);color:var(--foreground)}
</style>
