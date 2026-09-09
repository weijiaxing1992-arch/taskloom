export interface WecomDelivery { parts?:number; sentParts?:number; group?:string; id:number; title:string; status:string; attempts:number; errorCode:string; createdAt:string; sentAt:string }
export interface WecomWebhookSettings { publicUrlConfigured?:boolean; configured:boolean; enabled:boolean; version:number; updatedAt:string; maskedUrl:string; deliveryMode:'mock'|'live'; ready:boolean; deliveries:WecomDelivery[] }
export const wecomDeliveryStatuses:Record<string,string>={pending:'等待投递',sending:'投递中',sent:'已真实投递',mock_sent:'模拟投递完成',retry:'等待重试',failed:'投递失败',skipped:'已跳过'}
export const wecomDeliveryErrors:Record<string,string>={configuration_changed:'机器人配置已变化',recipient_or_configuration_changed:'接收人或机器人配置已变化',project_access_revoked:'项目访问权限已撤销',database_unavailable:'投递服务暂不可用',delivery_unavailable:'连接企业微信失败',http_error:'企业微信服务响应异常',invalid_response:'企业微信响应格式异常',rate_limited:'企业微信限流，稍后重试',attempts_exhausted:'已达到重试次数上限',invalid_webhook:'机器人配置无效',encryption_key_unavailable:'机器人加密配置不可用',wecom_rejected:'企业微信拒绝了消息'}
wecomDeliveryErrors.robot_rejected = '企业微信拒绝了消息'
export function validWecomWebhookURL(raw:string):boolean {
  try { const input=raw.trim();if(!input.startsWith('https://qyapi.weixin.qq.com/cgi-bin/webhook/send?'))return false;const value=new URL(input);return value.protocol==='https:'&&value.host==='qyapi.weixin.qq.com'&&!value.username&&!value.password&&value.pathname==='/cgi-bin/webhook/send'&&!value.hash&&[...value.searchParams.keys()].length===1&&value.searchParams.getAll('key').length===1&&/^[A-Za-z0-9_-]{20,200}$/.test(value.searchParams.get('key')||'') } catch { return false }
}
export function wecomWebhookEndpoint(userId?:string):string { return userId?'/organization/members/'+encodeURIComponent(userId)+'/wecom-webhook':'/profile/wecom-webhook' }
export function wecomWebhookPatch(url:string,enabled:boolean):{url?:string;enabled:boolean} { return url.trim()?{url:url.trim(),enabled}:{enabled} }
