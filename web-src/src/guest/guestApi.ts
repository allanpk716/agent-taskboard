// 访客链接 `/g/<密钥>` 只用于首次认领；认领返回的设备凭证保存在本机，
// 之后读取、回复、上传和下载都由 guestApi 自动改用它。
// #328 起访客是长期身份：设备凭证只存一份（不再按链接分键），
// 认领任一链接后这台设备对其名下全部链接可信。
import { api, type ApiRequest } from '../api'

export type GuestRequest = ApiRequest

const deviceKey = 'tb:guest-device'

function legacyDeviceKey(linkKey: string) {
  return `tb:guest-device:${linkKey}`
}

export function guestDeviceToken(linkKey?: string): string | null {
  const token = localStorage.getItem(deviceKey)
  if (token) return token
  // 兼容 #328 之前按链接分键保存的设备凭证。
  if (linkKey) return localStorage.getItem(legacyDeviceKey(linkKey))
  return null
}

export function saveGuestDeviceToken(_linkKey: string, token: string) {
  localStorage.setItem(deviceKey, token)
}

export async function guestRawApi(linkKey: string, path: string, init?: GuestRequest): Promise<any> {
  return api(path, {
    ...init,
    headers: { Authorization: `Bearer ${linkKey}`, ...((init?.headers as object) ?? {}) },
  })
}

export async function guestApi(linkKey: string, path: string, init?: GuestRequest): Promise<any> {
  return guestRawApi(guestDeviceToken(linkKey) ?? linkKey, path, init)
}
