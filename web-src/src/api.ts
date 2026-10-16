// 调用公开 API（/api/v1）。错误一律是 {error:{code, message, hint?}}。
export interface ApiErrorBody {
  code: string
  message: string
  hint?: string
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public hint?: string,
    /** 完整的 error 对象（附带字段在此，如 422 missing_fields 的 fields） */
    public body?: any,
  ) {
    super(message)
  }
}

export async function api(path: string, init?: RequestInit): Promise<any> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...init,
    headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
  })
  if (res.status === 204) return null
  let body: any = null
  try {
    body = await res.json()
  } catch {
    // 非 JSON 响应按未知错误处理。
  }
  if (!res.ok) {
    const e: ApiErrorBody | undefined = body?.error
    throw new ApiError(res.status, e?.code ?? 'unknown', e?.message ?? `请求失败（${res.status}）`, e?.hint, e)
  }
  return body
}
