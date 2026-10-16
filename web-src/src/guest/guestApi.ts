// 访客链接 `/g/<密钥>` 的密钥直接作 Authorization: Bearer（docs/api.md「访客链接」）。
// 访客页面自己的 API 入口：错误结构复用成员端的 ApiError。
import { ApiError } from '../api'

export interface GuestRequest extends RequestInit {
  /** 产生新内容的请求带 Idempotency-Key，重试返回首次结果（docs/api.md「幂等」）。 */
  idempotencyKey?: string
}

export async function guestApi(linkKey: string, path: string, init?: GuestRequest): Promise<any> {
  const headers: Record<string, string> = { Authorization: `Bearer ${linkKey}` }
  if (init?.body !== undefined && !(init.body instanceof FormData)) {
    headers['Content-Type'] = 'application/json'
  }
  if (init?.idempotencyKey) headers['Idempotency-Key'] = init.idempotencyKey
  const res = await fetch(path, { ...init, headers: { ...headers, ...(init?.headers as object) } })
  if (res.status === 204) return null
  let body: any = null
  try {
    body = await res.json()
  } catch {
    // 非 JSON 响应按未知错误处理。
  }
  if (!res.ok) {
    const e = body?.error
    throw new ApiError(res.status, e?.code ?? 'unknown', e?.message ?? `请求失败（${res.status}）`, e?.hint)
  }
  return body
}
