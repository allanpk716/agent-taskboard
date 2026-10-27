import { api } from '../api'

export interface Health {
  status: string
  server_version: string
  web_version: string
}

export async function fetchHealth(): Promise<Health> {
  return api('/api/v1/health')
}
