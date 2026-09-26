// MAX Bridge (window.WebApp). В веб-версии и на десктопе часть методов отсутствует,
// поэтому каждый вызов защищён; основной сценарий не зависит от необязательных методов.

type BackButton = { show?: () => void; hide?: () => void; onClick?: (cb: () => void) => void; offClick?: (cb: () => void) => void }

interface WebApp {
  initData?: string
  initDataUnsafe?: { start_param?: string; user?: { id: number; first_name?: string } }
  platform?: 'ios' | 'android' | 'desktop' | 'web'
  ready?: () => void
  BackButton?: BackButton
  downloadFile?: (url: string, fileName: string) => Promise<unknown> | void
  openLink?: (url: string) => void
  shareMaxContent?: (p: { text?: string; link?: string }) => Promise<unknown> | void
  enableClosingConfirmation?: () => void
  disableClosingConfirmation?: () => void
}

declare global {
  interface Window {
    WebApp?: WebApp
  }
}

const wa = (): WebApp | undefined => window.WebApp

export const inMax = () => Boolean(wa()?.initData)

export function initData(): string {
  return wa()?.initData ?? ''
}

// Локальная отладка в браузере: ?dev_user=1&startapp=act_<id> (работает только с DEV_AUTH на сервере).
const params = new URLSearchParams(location.search)
export const devUser = params.get('dev_user') ?? ''

export function startParam(): string {
  return wa()?.initDataUnsafe?.start_param ?? params.get('startapp') ?? ''
}

export function ready() {
  try {
    wa()?.ready?.()
  } catch {
    /* необязательно */
  }
}

let backHandler: (() => void) | null = null

export function setBack(handler: (() => void) | null) {
  const bb = wa()?.BackButton
  if (!bb) return
  try {
    if (backHandler) bb.offClick?.(backHandler)
    backHandler = handler
    if (handler) {
      bb.onClick?.(handler)
      bb.show?.()
    } else {
      bb.hide?.()
    }
  } catch {
    /* BackButton поддерживается не везде — в интерфейсе есть своя кнопка «Назад» */
  }
}

// downloadFile требует абсолютный HTTPS-адрес. Если метода нет — открываем ссылку.
export async function download(url: string, fileName: string): Promise<boolean> {
  const abs = new URL(url, location.origin).toString()
  const w = wa()
  try {
    if (w?.downloadFile) {
      await w.downloadFile(abs + (abs.includes('?') ? '&' : '?') + 'download=1', fileName)
      return true
    }
  } catch {
    /* упадём на запасной вариант */
  }
  if (w?.openLink) {
    w.openLink(abs)
    return true
  }
  window.open(abs, '_blank')
  return true
}

export async function share(text: string, link: string): Promise<boolean> {
  const w = wa()
  if (!w?.shareMaxContent) return false
  try {
    await w.shareMaxContent({ text, link })
    return true
  } catch {
    return false
  }
}

export async function copy(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}
