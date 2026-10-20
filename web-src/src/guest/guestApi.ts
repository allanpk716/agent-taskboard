// 访客链接 `/g/<密钥>` 只用于首次认领；认领返回的设备凭证保存在本机，
// 之后读取、回复、上传和下载都由 guestApi 自动改用它。
import { api, type ApiRequest } from '../api'

export type GuestRequest = ApiRequest

function deviceKey(linkKey: string) {
  return `tb:guest-device:${linkKey}`
}

export function guestDeviceToken(linkKey: string): string | null {
  return localStorage.getItem(deviceKey(linkKey))
}

export function saveGuestDeviceToken(linkKey: string, token: string) {
  localStorage.setItem(deviceKey(linkKey), token)
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
