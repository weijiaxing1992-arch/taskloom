/** 临时口令只用于首次认证。新口令仍遵循正常强度规则，不能沿用临时口令。 */
export function initialPasswordError(current: string, next: string, confirmation: string): string {
  if (!current) return '请输入当前临时密码'
  if ([...next].length < 8 || new TextEncoder().encode(next).length > 72) return '新密码至少 8 个字符，最多 72 个 UTF-8 字节'
  if (!/\p{L}/u.test(next) || !/\p{N}/u.test(next)) return '新密码须同时包含字母和数字'
  if (/[\u0000-\u001f\u007f]/u.test(next)) return '新密码不能包含控制字符'
  if (next === current) return '新密码不能与当前临时密码相同'
  if (next !== confirmation) return '两次输入的新密码不一致'
  return ''
}
