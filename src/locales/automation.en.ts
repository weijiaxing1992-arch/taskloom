// Automation-only copy stays separate from general settings so new recipient
// sources can evolve without widening any persisted backend mode identifiers.
const messages: Record<string, string> = {
  '前端工程师（需求职能）': 'Frontend engineer (requirement role)',
  '后端工程师（需求职能）': 'Backend engineer (requirement role)',
  '算法工程师（需求职能）': 'Algorithm engineer (requirement role)',
  'UI 设计师（需求职能）': 'UI designer (requirement role)',
  '产品负责人（需求职能）': 'Product owner (requirement role)',
  '前端负责人（字段）': 'Frontend lead (field)',
  '后端负责人（字段）': 'Backend lead (field)',
  '测试人员（字段）': 'Tester (field)',
  '处理人、负责人、需求职能成员及已绑定的前后端负责人/测试人员都会在执行时按当前项目可见性重新校验；无有效接收人时仅记录跳过。': 'Assignees, owners, requirement-role members, and bound frontend/backend leads or testers are revalidated against current project visibility when the rule runs. If no recipients remain valid, the execution is recorded as skipped.',
}

export default messages
