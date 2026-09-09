export interface WatermarkMetadata {
  userId: string
  accountName: string
  serverTime: string
  ipAddress: string
  ipSource: 'connection'
}

/** 拒绝身份不匹配/非法时间响应，避免切账号时出现上一位成员的水印。 */
export function validWatermark(value: unknown, userId: string): value is WatermarkMetadata {
  if (!value || typeof value !== 'object') return false
  const data = value as Partial<WatermarkMetadata>
  return data.userId === userId && typeof data.accountName === 'string' && data.accountName.length > 0 &&
    typeof data.ipAddress === 'string' && data.ipAddress.length <= 64 && data.ipSource === 'connection' &&
    typeof data.serverTime === 'string' && Number.isFinite(Date.parse(data.serverTime))
}

export function watermarkName(name: string): string {
  return [...name.replace(/[\u0000-\u001f\u007f]/g, ' ').trim()].slice(0, 32).join('')
}

export function watermarkTime(timestamp: number, timeZone = 'Asia/Shanghai'): string {
  try {
    return new Intl.DateTimeFormat('sv-SE', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(timestamp)
  } catch {
    return new Date(timestamp).toISOString().replace('T', ' ').slice(0, 19) + ' UTC'
  }
}
