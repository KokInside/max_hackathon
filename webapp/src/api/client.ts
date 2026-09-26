import type { components } from './schema'
import { devUser, initData, startParam } from '../bridge'

export type S = components['schemas']
export type ActView = S['ActView']
export type LineView = S['LineView']
export type Me = S['Me']
export type Basis = S['Basis']
export type Claim = S['Claim']
export type ResidentView = S['ResidentView']
export type LineInput = S['LineInput']
export type HeaderPatch = S['HeaderPatch']

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
  }
}

function headers(extra?: Record<string, string>): Record<string, string> {
  const h: Record<string, string> = { ...extra }
  const d = initData()
  if (d) h['X-Max-Init-Data'] = d
  else if (devUser) {
    h['X-Dev-User'] = devUser
    h['X-Dev-Start-Param'] = startParam()
  }
  return h
}

async function request<T>(method: string, path: string, body?: unknown, form?: FormData): Promise<T> {
  let res: Response
  try {
    res = await fetch('/api/v1' + path, {
      method,
      headers: headers(body !== undefined ? { 'Content-Type': 'application/json' } : undefined),
      body: form ?? (body !== undefined ? JSON.stringify(body) : undefined),
    })
  } catch {
    throw new ApiError(0, 'NETWORK', 'Нет связи с сервером. Проверьте интернет и повторите.')
  }
  if (res.status === 204) return undefined as T
  let data: unknown = null
  try {
    data = await res.json()
  } catch {
    /* пустое тело */
  }
  if (!res.ok) {
    const e = (data as { error?: { code: string; message: string } })?.error
    throw new ApiError(res.status, e?.code ?? 'HTTP_' + res.status, e?.message ?? `Ошибка сервера (${res.status}). Повторите позже.`)
  }
  return data as T
}

export const api = {
  me: () => request<Me>('GET', '/me'),
  acts: () => request<{ acts: S['ActSummary'][] }>('GET', '/acts'),
  createDemo: () => request<ActView>('POST', '/acts/demo'),
  act: (id: string) => request<ActView>('GET', `/acts/${id}`),
  patchAct: (id: string, p: HeaderPatch) => request<ActView>('PATCH', `/acts/${id}`, p),
  addLine: (actId: string, l: LineInput) => request<S['Line']>('POST', `/acts/${actId}/lines`, l),
  patchLine: (id: string, l: LineInput) => request<S['Line']>('PATCH', `/lines/${id}`, l),
  deleteLine: (id: string) => request<void>('DELETE', `/lines/${id}`),
  addEvidence: (lineId: string, file: File, note: string) => {
    const f = new FormData()
    f.append('note', note)
    f.append('file', file)
    return request<S['Evidence']>('POST', `/lines/${lineId}/evidence`, undefined, f)
  },
  deleteEvidence: (id: string) => request<void>('DELETE', `/evidence/${id}`),
  invite: (actId: string) => request<{ token: string; link: string; text: string }>('POST', `/acts/${actId}/invites`),
  resident: (token: string) => request<ResidentView>('GET', `/invites/${token}`),
  vote: (token: string, v: S['VotesInput']) => request<ResidentView>('PUT', `/invites/${token}/votes`, v),
  decide: (actId: string, decision: 'sign' | 'refuse', confirm_disputed = false) =>
    request<S['DecisionResult']>('POST', `/acts/${actId}/decision`, { decision, confirm_disputed }),
  dispatch: (actId: string, channel: S['Channel'], sent_on?: string) =>
    request<ActView>('POST', `/acts/${actId}/dispatch`, { channel, sent_on: sent_on || null }),
  successor: (actId: string) => request<ActView>('POST', `/acts/${actId}/successor`),
  demoShift: (actId: string, to_day: number) => request<ActView>('POST', `/acts/${actId}/demo-shift`, { to_day }),
  works: (q: string) => request<{ items: S['CatalogItem'][]; edition: string; note: string }>('GET', `/catalog/works?q=${encodeURIComponent(q)}`),
}
