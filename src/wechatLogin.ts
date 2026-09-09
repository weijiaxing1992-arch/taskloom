// 只允许跳往微信官方的固定扫码入口，拒绝后端异常返回或开放重定向。
export function safeWechatAuthorization(value: unknown): string {
  if (typeof value !== 'string') throw Error('微信扫码地址无效')
  const url = new URL(value)
  if (url.protocol !== 'https:' || url.host !== 'open.weixin.qq.com' || url.pathname !== '/connect/qrconnect' || url.username || url.password ||
      url.searchParams.get('scope') !== 'snsapi_login' || url.searchParams.get('response_type') !== 'code' ||
      !/^wx[0-9a-f]{16}$/i.test(url.searchParams.get('appid') || '') ||
      !/^[a-f0-9]{64}$/.test(url.searchParams.get('state') || '')) throw Error('微信扫码地址无效')
  const callback = new URL(url.searchParams.get('redirect_uri') || '')
  if (callback.origin !== window.location.origin || callback.pathname !== '/api/auth/wechat/callback' || callback.search || callback.hash || callback.username || callback.password) throw Error('请从管理员配置的正式域名发起微信扫码')
  return url.href
}
export const wechatResultMessages: Record<string,string> = {
  expired:'二维码已过期或授权状态已变化，请重新扫码。',
  cancelled:'已取消微信授权，可以重新扫码或使用密码登录。',
  failed:'微信授权未完成，请重新扫码；仍失败请联系管理员检查配置。',
  conflict:'该微信或当前账号已有绑定，请确认账号后重试。',
  unbound:'微信尚未绑定可用账号，请先使用邮箱密码登录并绑定；停用账号请联系管理员。',
  bound:'微信绑定成功，下次可以扫码登录。',
}
