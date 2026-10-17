// 访客链接 `/g/<密钥>` 的密钥直接作 Authorization: Bearer（docs/api.md「访客链接」）。
// 访客页面自己的 API 入口：复用成员端的 fetch 帮手（错误结构、Content-Type、
// Idempotency-Key 的处理都一样），只是多带一个 Bearer 头（评审 P3-16）。
import { api, type ApiRequest } from '../api'

export type GuestRequest = ApiRequest

export async function guestApi(linkKey: string, path: string, init?: GuestRequest): Promise<any> {
  return api(path, {
    ...init,
    headers: { Authorization: `Bearer ${linkKey}`, ...((init?.headers as object) ?? {}) },
  })
}
