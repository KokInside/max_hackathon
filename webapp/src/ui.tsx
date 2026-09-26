import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { Button, Spinner, Typography } from '@maxhub/max-ui'
import type { Basis, Claim } from './api/client'
import { kindTitle } from './format'

export function Screen({ title, onBack, children, footer }: { title: string; onBack?: () => void; children: ReactNode; footer?: ReactNode }) {
  return (
    <div className="screen">
      <header className="topbar">
        {onBack && (
          <button className="back" onClick={onBack} aria-label="Назад">
            ‹ Назад
          </button>
        )}
        <Typography.Title className="topbar__title">{title}</Typography.Title>
      </header>
      <main className="content">{children}</main>
      {footer && <footer className="footer">{footer}</footer>}
    </div>
  )
}

export function Card({ title, children, tone, after }: { title?: ReactNode; children: ReactNode; tone?: 'warn' | 'bad' | 'ok' | 'demo'; after?: ReactNode }) {
  return (
    <section className={'card' + (tone ? ' card--' + tone : '')}>
      {(title || after) && (
        <div className="card__head">
          {title && <h3 className="card__title">{title}</h3>}
          {after}
        </div>
      )}
      {children}
    </section>
  )
}

export function Chip({ children, tone }: { children: ReactNode; tone?: string }) {
  return <span className={'chip' + (tone ? ' chip--' + tone : '')}>{children}</span>
}

export function KindChip({ kind }: { kind: string }) {
  return <Chip tone={'kind-' + kind}>{kindTitle[kind] ?? kind}</Chip>
}

export function Loading({ text = 'Загрузка…' }: { text?: string }) {
  return (
    <div className="center">
      <Spinner size={28} />
      <p className="muted">{text}</p>
    </div>
  )
}

export function ErrorBox({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="center">
      <p className="error-text">{message}</p>
      {onRetry && (
        <Button variant="secondary" onClick={onRetry}>
          Повторить
        </Button>
      )}
    </div>
  )
}

export function Sheet({ open, onClose, title, children }: { open: boolean; onClose: () => void; title: string; children: ReactNode }) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])
  if (!open) return null
  return (
    <div className="sheet" role="dialog" aria-modal="true" onClick={onClose}>
      <div className="sheet__body" onClick={(e) => e.stopPropagation()}>
        <div className="sheet__head">
          <h3>{title}</h3>
          <button className="sheet__close" onClick={onClose} aria-label="Закрыть">
            ✕
          </button>
        </div>
        {children}
      </div>
    </div>
  )
}

// Основание утверждения: откуда взялось правило и насколько оно проверено (ТЗ: разделять факты, расчёты и допущения).
export function BasisList({ basis }: { basis: Basis[] }) {
  if (!basis.length) return <p className="muted">Результат рассчитан сервисом по данным акта.</p>
  return (
    <ul className="basis">
      {basis.map((b) => (
        <li key={b.rule_id}>
          <KindChip kind={b.kind} />
          <p>{b.summary}</p>
          {b.source ? (
            <p className="muted small">
              Источник: {b.source.title}
              {b.point ? `, п. ${b.point}` : ''}. Редакция: {b.source.edition}. Сверено: {b.source.checked_at}
              {!b.source.verified && ' (текст не сверен с первоисточником)'}.{' '}
              {b.source.url && (
                <a href={b.source.url} target="_blank" rel="noreferrer">
                  Открыть
                </a>
              )}
            </p>
          ) : (
            <p className="muted small">Допущение сервиса: норма прямо этого не устанавливает.</p>
          )}
        </li>
      ))}
    </ul>
  )
}

export function BasisLink({ basis, label = 'Основание' }: { basis: Basis[]; label?: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button className="link" onClick={() => setOpen(true)}>
        {label} ›
      </button>
      <Sheet open={open} onClose={() => setOpen(false)} title={label}>
        <BasisList basis={basis} />
      </Sheet>
    </>
  )
}

export function ClaimRow({ c }: { c: Claim }) {
  const icon = c.severity === 'violation' ? '❗' : c.severity === 'warning' ? '⚠️' : c.severity === 'ok' ? '✅' : 'ℹ️'
  return (
    <div className="claim">
      <span className="claim__icon">{icon}</span>
      <div>
        <p>{c.text}</p>
        <div className="row gap">
          <KindChip kind={c.kind} />
          {c.basis.length > 0 && <BasisLink basis={c.basis} />}
        </div>
      </div>
    </div>
  )
}

// Уведомления о результате действия.
type Toast = { id: number; text: string; tone: 'ok' | 'bad' }
const ToastCtx = createContext<(text: string, tone?: 'ok' | 'bad') => void>(() => {})

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Toast[]>([])
  const push = useCallback((text: string, tone: 'ok' | 'bad' = 'ok') => {
    const id = Date.now() + Math.random()
    setItems((x) => [...x, { id, text, tone }])
    setTimeout(() => setItems((x) => x.filter((t) => t.id !== id)), tone === 'bad' ? 6000 : 2500)
  }, [])
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="toasts" aria-live="polite">
        {items.map((t) => (
          <div key={t.id} className={'toast toast--' + t.tone}>
            {t.text}
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  )
}

export const useToast = () => useContext(ToastCtx)
