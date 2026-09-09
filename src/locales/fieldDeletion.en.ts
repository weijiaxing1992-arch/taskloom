const messages: Record<string,string> = {
  '删除自定义字段':'Delete custom field',
  '删除字段 {name}':'Delete field {name}',
  '确认删除字段「{name}」？':'Delete the field “{name}”?',
  '删除后，字段将从表单、列表与筛选配置中移除。已有字段值和删除审计会保留，不会删除需求、缺陷或测试数据。':'The field will be removed from forms, lists, and filter settings. Existing field values and deletion audit records will be retained; requirements, defects, and test data will not be deleted.',
  '已保存视图若引用此字段，将提示配置失效，不会自动放宽筛选。':'Saved views referencing this field will report an unavailable configuration instead of silently broadening filters.',
  '默认配置补齐与应用重启不会恢复已删除字段。':'Restoring preset fields or restarting the application will not restore deleted fields.',
  '确认删除字段':'Confirm field deletion',
  '删除中…':'Deleting…',
  '字段已删除，已保留 {count} 条历史字段值。':'Field deleted. {count} historical field values retained.',
  '删除结果未确认，请刷新字段列表核实后再操作。':'The deletion result could not be confirmed. Refresh the field list and verify before trying again.',
  '删除确认与当前字段不符，请重新打开确认框':'The deletion confirmation does not match the current field. Reopen the confirmation dialog.',
}
export default messages
