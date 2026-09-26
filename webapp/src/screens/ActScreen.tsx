import { useState } from 'react'
import { Button, Input, Typography } from '@maxhub/max-ui'
import { api, ApiError, type ActView, type HeaderPatch, type S } from '../api/client'
import { channelTitle, date, eventTitle, plural, reviewTitle, rub } from '../format'
import { BasisLink, Card, Chip, ClaimRow, Sheet, useToast } from '../ui'
import { copy, download, share } from '../bridge'
import { AddLine } from './AddLine'

type Props = {
  view: ActView
  onChange: (v: ActView) => void
  reload: () => Promise<void>
  openLine: (id: string) => void
  openAct: (id: string) => void
}

export function ActScreen({ view, onChange, reload, openLine, openAct }: Props) {
  const { act } = view
  return (
    <>
      <StatusCard view={view} />
      {view.act.is_demo && <DemoCard view={view} onChange={onChange} />}
      <DeadlinesCard view={view} />
      <ChecksCard view={view} />
      <SumsCard view={view} />
      <LinesCard view={view} openLine={openLine} />
      {view.editable && <AddLine actId={act.id} onAdded={reload} />}
      <ResidentsCard view={view} />
      <DecisionCard view={view} reload={reload} />
      <SuccessorCard view={view} openAct={openAct} />
      <HeaderCard view={view} onChange={onChange} />
      <HistoryCard view={view} />
    </>
  )
}

function StatusCard({ view }: { view: ActView }) {
  const a = view.act
  const tone = a.status === 'deemed_accepted' ? 'bad' : a.status === 'closed_signed' ? 'ok' : undefined
  return (
    <Card tone={tone}>
      <Typography.Headline>Акт № {a.number || 'без номера'}{a.act_date ? ` от ${date(a.act_date)}` : ''}</Typography.Headline>
      <p className="muted">{a.address}</p>
      <p>
        <b>{view.status_title}</b>
        {a.is_demo && (
          <>
            {' '}
            <Chip tone="demo">ДЕМО · синтетические данные</Chip>
          </>
        )}
      </p>
      {a.parent_act_id && <p className="muted small">Новый акт после отказа: строки скопированы из предыдущего.</p>}
    </Card>
  )
}

function DemoCard({ view, onChange }: { view: ActView; onChange: (v: ActView) => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState(0)
  const a = view.act
  if (!a.received_on || !view.editable) {
    return a.demo_shift_days ? (
      <Card tone="demo">
        <p className="small">🧪 Демо: время акта сдвинуто на {a.demo_shift_days} дн.</p>
      </Card>
    ) : null
  }
  const go = async (day: number) => {
    setBusy(day)
    try {
      onChange(await api.demoShift(a.id, day))
      toast(`Перемотано к ${day}-му дню. Напоминание придёт в чат с ботом.`)
    } catch (e) {
      toast((e as Error).message, 'bad')
    } finally {
      setBusy(0)
    }
  }
  const day = view.deadlines?.timing.day_number ?? 0
  return (
    <Card tone="demo" title="🧪 Демо-режим: перемотка времени">
      <p className="small">
        Сейчас {day}-й день после получения{a.demo_shift_days ? ` (сдвиг ${a.demo_shift_days} дн.)` : ''}. Время меняется только у этого демо-акта.
      </p>
      <div className="row gap wrap">
        {[9, 11, 31].map((d) => (
          <Button key={d} size="small" variant="secondary" loading={busy === d} disabled={!!busy || d <= day} onClick={() => go(d)}>
            К {d}-му дню
          </Button>
        ))}
      </div>
    </Card>
  )
}

function DeadlinesCard({ view }: { view: ActView }) {
  const d = view.deadlines
  if (!d) {
    return (
      <Card title="Сроки" tone="warn">
        <p>Укажите дату получения акта — от неё считаются 10 и 30 дней. Это можно сделать в чате с ботом или ниже в разделе «Данные акта».</p>
      </Card>
    )
  }
  const t = d.timing
  const final = !view.editable
  const progress = Math.min(100, Math.max(0, (t.day_number / 10) * 100))
  let headline = ''
  if (final) headline = view.status_title
  else if (t.stage === 'response_window') headline = t.days_left_response === 0 ? 'Сегодня последний день для ответа' : `Осталось ${t.days_left_response} ${plural(t.days_left_response, 'день', 'дня', 'дней')} на ответ`
  else if (t.stage === 'response_overdue') headline = `Срок ответа истёк. До молчаливой приёмки ${t.days_left_silent} ${plural(t.days_left_silent, 'день', 'дня', 'дней')}`
  else headline = 'Прошло 30 дней: акт считается оформленным'
  const tone = final ? undefined : t.stage === 'response_window' ? (t.days_left_response <= 2 ? 'warn' : undefined) : 'bad'
  return (
    <Card title="Сроки" tone={tone} after={<BasisLink basis={d.basis} />}>
      <p>
        <b>{headline}</b>
      </p>
      {!final && t.stage === 'response_window' && (
        <div className="progress" aria-label={`День ${t.day_number} из 10`}>
          <div style={{ width: progress + '%' }} />
        </div>
      )}
      <dl className="kv">
        <dt>Получен</dt>
        <dd>{date(view.act.received_on)}</dd>
        <dt>Ответить до (10-й день)</dt>
        <dd>{date(d.response_on)}</dd>
        <dt>Молчаливая приёмка после (30-й день)</dt>
        <dd>{date(d.silent_on)}</dd>
      </dl>
      <p className="muted small">Дни календарные, со следующего дня после получения — допущение сервиса.</p>
    </Card>
  )
}

function ChecksCard({ view }: { view: ActView }) {
  const items = [...view.checks, ...view.notes]
  return (
    <Card title="Проверка сроков УК">
      {view.checks.length === 0 && <p className="muted">Укажите дату акта и период в разделе «Данные акта» — проверю сроки исполнителя.</p>}
      {items.map((c) => (
        <ClaimRow key={c.code} c={c} />
      ))}
    </Card>
  )
}

function SumsCard({ view }: { view: ActView }) {
  const s = view.sums
  return (
    <Card title="Итоги проверки" tone={s.mismatch ? 'warn' : undefined}>
      <dl className="kv">
        <dt>Сумма строк</dt>
        <dd>{rub(s.lines_total)}</dd>
        <dt>Итог по п. 2 акта</dt>
        <dd>{rub(s.act_total)}</dd>
        <dt>Оспаривается</dt>
        <dd className={s.disputed_count ? 'bad' : ''}>
          {rub(s.disputed)} ({s.disputed_count} {plural(s.disputed_count, 'строка', 'строки', 'строк')})
        </dd>
        <dt>Не проверено</dt>
        <dd>{s.unchecked_count}</dd>
      </dl>
      {s.mismatch && <p className="warn-text small">Сумма строк не совпадает с итогом акта — проверьте строки или итог. Расхождение попадёт в отказ.</p>}
    </Card>
  )
}

const statusTone: Record<string, string> = { confirmed: 'ok', doubtful: 'warn', not_done: 'bad', unchecked: '' }

function LinesCard({ view, openLine }: { view: ActView; openLine: (id: string) => void }) {
  return (
    <Card title={`Строки акта (${view.lines.length})`}>
      {view.lines.length === 0 && <p className="muted">Строк пока нет. Перепишите строки из таблицы акта — с подсказками из перечня работ ПП № 290.</p>}
      <ul className="lines">
        {view.lines.map((l) => (
          <li key={l.id}>
            <button className="line" onClick={() => openLine(l.id)}>
              <span className="line__pos">{l.position}</span>
              <span className="line__main">
                <span className="line__name">{l.work_name}</span>
                <span className="line__meta">
                  <Chip tone={statusTone[l.review_status]}>{reviewTitle[l.review_status]}</Chip>
                  {l.prev_disputed && <Chip tone="warn">оспаривалась ранее</Chip>}
                  {l.evidence.length > 0 && <Chip>📷 {l.evidence.length}</Chip>}
                  {l.residents && (
                    <Chip>
                      👥 было {l.residents.yes} / не было {l.residents.no}
                    </Chip>
                  )}
                </span>
              </span>
              <span className="line__sum">{rub(l.amount)}</span>
            </button>
          </li>
        ))}
      </ul>
    </Card>
  )
}

function ResidentsCard({ view }: { view: ActView }) {
  const toast = useToast()
  const [inv, setInv] = useState<{ link: string; text: string } | null>(view.invite ? { link: view.invite.link, text: '' } : null)
  const [busy, setBusy] = useState(false)
  if (!view.editable && !view.respondents) return null
  const create = async () => {
    setBusy(true)
    try {
      const r = await api.invite(view.act.id)
      setInv({ link: r.link, text: r.text })
      toast('Ссылка готова. Бот тоже может прислать её в чат: кнопка «Пригласить жильцов».')
    } catch (e) {
      toast((e as Error).message, 'bad')
    } finally {
      setBusy(false)
    }
  }
  return (
    <Card title="Жильцы">
      <p>
        Ответили: <b>{view.respondents}</b> {plural(view.respondents, 'человек', 'человека', 'человек')}. Жильцы видят строки без цен и отмечают «было / не было» по своему подъезду.
      </p>
      {view.editable &&
        (inv?.text || inv?.link ? (
          <>
            <p className="mono small">{inv.link}</p>
            <div className="row gap wrap">
              <Button size="small" variant="secondary" onClick={async () => toast((await copy(inv.text || inv.link)) ? 'Скопировано' : 'Не удалось скопировать', 'ok')}>
                Скопировать текст
              </Button>
              <Button size="small" variant="secondary" onClick={async () => { if (!(await share(inv.text || 'Проверьте работы УК в нашем доме', inv.link))) toast('Перешлите ссылку вручную: кнопка «Скопировать».', 'bad') }}>
                Поделиться в MAX
              </Button>
              {!inv.text && (
                <Button size="small" variant="ghost" onClick={create} loading={busy}>
                  Текст приглашения
                </Button>
              )}
            </div>
          </>
        ) : (
          <Button variant="secondary" onClick={create} loading={busy}>
            Пригласить жильцов
          </Button>
        ))}
    </Card>
  )
}

function DecisionCard({ view, reload }: { view: ActView; reload: () => Promise<void> }) {
  const toast = useToast()
  const [confirm, setConfirm] = useState<'sign' | 'refuse' | null>(null)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<S['DecisionResult'] | null>(null)
  const [dispatchOpen, setDispatchOpen] = useState(false)
  const a = view.act
  const doc = view.documents[0]
  const run = async (d: 'sign' | 'refuse') => {
    setBusy(true)
    try {
      const r = await api.decide(a.id, d, d === 'sign')
      setResult(r)
      setConfirm(null)
      toast(r.delivered ? 'Документ готов и отправлен вам в чат с ботом' : r.warning ?? 'Документ готов')
      await reload()
    } catch (e) {
      const err = e as ApiError
      toast(err.message, 'bad')
      if (err.code !== 'DISPUTED_LINES') setConfirm(null)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Card title="Решение по акту">
      {view.editable ? (
        <>
          <p className="small">
            Подписать «с замечаниями» нельзя: либо подписание (работы приняты полностью), либо письменный обоснованный отказ с возражениями.
          </p>
          <div className="row gap wrap">
            <Button variant="destructive" onClick={() => setConfirm('refuse')} disabled={a.status === 'draft'}>
              Отказаться от подписания
            </Button>
            <Button variant="secondary" onClick={() => setConfirm('sign')} disabled={a.status === 'draft'}>
              Подписать
            </Button>
          </div>
          {a.status === 'draft' && <p className="warn-text small">Сначала укажите дату получения акта.</p>}
        </>
      ) : (
        <p className="muted">Решение принято: {view.status_title.toLowerCase()}.</p>
      )}
      {doc && (
        <div className="doc">
          <p>
            📄 {doc.kind === 'refusal' ? 'Мотивированный отказ' : 'Сопроводительное письмо'} · версия {doc.version} · {date(doc.created_at)}
          </p>
          <div className="row gap wrap">
            <Button size="small" variant="secondary" onClick={() => download(doc.url, `${doc.kind}_act-${a.number || 'b-n'}.pdf`)}>
              Скачать PDF
            </Button>
            {a.status === 'decided' && (
              <Button size="small" onClick={() => setDispatchOpen(true)}>
                Отметить отправку в УК
              </Button>
            )}
          </div>
          {result && !result.delivered && <p className="warn-text small">{result.warning}</p>}
          {result?.delivered && <p className="muted small">Файл также отправлен вам в чат с ботом.</p>}
        </div>
      )}
      {(view.dispatches ?? []).map((d) => (
        <p key={d.id} className="small">
          📮 Отправлено {date(d.sent_on)}: {channelTitle[d.channel]}
        </p>
      ))}
      <Sheet open={confirm !== null} onClose={() => setConfirm(null)} title={confirm === 'refuse' ? 'Отказ от подписания' : 'Подписание акта'}>
        {confirm === 'refuse' ? (
          <>
            <p>
              В отказ попадут {view.sums.disputed_count} {plural(view.sums.disputed_count, 'строка', 'строки', 'строк')} на {rub(view.sums.disputed)} с вашими возражениями, нарушения сроков УК и перечень доказательств.
            </p>
            {view.sums.disputed_count === 0 && <p className="warn-text">Нет оспоренных строк: отметьте «не выполнено» или «под сомнением» и напишите возражение.</p>}
          </>
        ) : (
          <>
            <p>Подписание означает, что работы по акту приняты полностью (п. 3–4 формы акта). Сформирую сопроводительное письмо к подписанному экземпляру.</p>
            {view.sums.disputed_count > 0 && <p className="warn-text">Есть спорные строки: {view.sums.disputed_count}. После подписания оспорить их этим актом будет нельзя.</p>}
          </>
        )}
        <Button stretched loading={busy} variant={confirm === 'refuse' ? 'destructive' : 'primary'} onClick={() => confirm && run(confirm)}>
          Сформировать PDF
        </Button>
      </Sheet>
      <DispatchSheet open={dispatchOpen} onClose={() => setDispatchOpen(false)} actId={a.id} reload={reload} />
    </Card>
  )
}

function DispatchSheet({ open, onClose, actId, reload }: { open: boolean; onClose: () => void; actId: string; reload: () => Promise<void> }) {
  const toast = useToast()
  const [channel, setChannel] = useState<S['Channel']>('in_person')
  const [sentOn, setSentOn] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async () => {
    setBusy(true)
    try {
      await api.dispatch(actId, channel, sentOn)
      toast('Отправка отмечена')
      onClose()
      await reload()
    } catch (e) {
      toast((e as Error).message, 'bad')
    } finally {
      setBusy(false)
    }
  }
  return (
    <Sheet open={open} onClose={onClose} title="Как вы направили документ?">
      <div className="choices">
        {Object.entries(channelTitle).map(([k, v]) => (
          <label key={k} className="choice">
            <input type="radio" name="ch" checked={channel === k} onChange={() => setChannel(k as S['Channel'])} /> {v}
          </label>
        ))}
      </div>
      <label className="field">
        Дата отправки (пусто — сегодня)
        <Input type="date" value={sentOn} onChange={(e) => setSentOn(e.target.value)} />
      </label>
      <p className="muted small">Используйте способ, который позволяет подтвердить получение: почта с уведомлением, e-mail, личный кабинет, вручение под подпись.</p>
      <Button stretched loading={busy} onClick={submit}>
        Отметить
      </Button>
    </Sheet>
  )
}

function SuccessorCard({ view, openAct }: { view: ActView; openAct: (id: string) => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  if (view.act.status !== 'refused_sent') return null
  return (
    <Card title="Новый акт" tone="warn">
      <p>После отказа исполнитель оформляет новый акт по тем же правилам. До этого возможны согласительное совещание и осмотр общего имущества.</p>
      <p className="small">Удобнее прислать новый акт боту файлом («Получил новый акт»). Или создайте карточку здесь — строки скопируются.</p>
      <Button
        variant="secondary"
        loading={busy}
        onClick={async () => {
          setBusy(true)
          try {
            const v = await api.successor(view.act.id)
            openAct(v.act.id)
          } catch (e) {
            toast((e as Error).message, 'bad')
          } finally {
            setBusy(false)
          }
        }}
      >
        Создать новый акт
      </Button>
    </Card>
  )
}

const headerFields: { key: keyof HeaderPatch; label: string; type?: 'date' | 'money' }[] = [
  { key: 'number', label: 'Номер акта' },
  { key: 'act_date', label: 'Дата акта', type: 'date' },
  { key: 'period_from', label: 'Период: с', type: 'date' },
  { key: 'period_to', label: 'Период: по', type: 'date' },
  { key: 'total_amount', label: 'Итого по п. 2, руб.', type: 'money' },
  { key: 'executor_name', label: 'Исполнитель' },
  { key: 'executor_signatory_name', label: 'Подписант исполнителя (ФИО)' },
  { key: 'executor_signatory_position', label: 'Должность подписанта' },
  { key: 'executor_basis', label: 'Действует на основании' },
  { key: 'contract_number', label: 'Договор №' },
  { key: 'contract_date', label: 'Дата договора', type: 'date' },
  { key: 'contract_end_date', label: 'Договор действует до', type: 'date' },
  { key: 'executor_sent_on', label: 'Дата отправки акта исполнителем (если известна)', type: 'date' },
  { key: 'received_on', label: 'Дата получения акта', type: 'date' },
  { key: 'customer_full_name', label: 'Заказчик: председатель (ФИО)' },
  { key: 'customer_apartment', label: 'Квартира председателя' },
  { key: 'customer_authority_text', label: 'Основание полномочий' },
  { key: 'address', label: 'Адрес дома' },
  { key: 'city', label: 'Город' },
]

function HeaderCard({ view, onChange }: { view: ActView; onChange: (v: ActView) => void }) {
  const toast = useToast()
  const [open, setOpen] = useState(!view.act.act_date && view.editable)
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const a = view.act as unknown as Record<string, string | null>
  const val = (k: string) => draft[k] ?? a[k] ?? ''
  const dirty = Object.keys(draft).length > 0
  const save = async () => {
    setBusy(true)
    const patch: Record<string, string | null> = {}
    for (const [k, v] of Object.entries(draft)) {
      const f = headerFields.find((x) => x.key === k)
      patch[k] = f?.type === 'date' ? v || null : v
    }
    if (patch.received_on === null) delete patch.received_on
    try {
      onChange(await api.patchAct(view.act.id, patch as HeaderPatch))
      setDraft({})
      toast('Данные акта сохранены')
    } catch (e) {
      toast((e as Error).message, 'bad')
    } finally {
      setBusy(false)
    }
  }
  return (
    <Card title="Данные акта (форма 761/пр)" after={<button className="link" onClick={() => setOpen(!open)}>{open ? 'Свернуть' : 'Показать'}</button>}>
      {open && (
        <>
          <p className="muted small">Перенесите реквизиты из полученного акта — они попадут в документ. Даты акта и периода нужны для проверки сроков УК.</p>
          {view.source_file_url && (
            <p className="small">
              <a href={view.source_file_url} target="_blank" rel="noreferrer">
                Открыть присланный файл акта
              </a>
            </p>
          )}
          <div className="form">
            {headerFields.map((f) => (
              <label key={f.key} className="field">
                {f.label}
                <Input
                  type={f.type === 'date' ? 'date' : 'text'}
                  inputMode={f.type === 'money' ? 'decimal' : undefined}
                  value={val(f.key)}
                  disabled={!view.editable}
                  onChange={(e) => setDraft({ ...draft, [f.key]: e.target.value })}
                />
              </label>
            ))}
          </div>
          {view.editable && (
            <Button stretched disabled={!dirty} loading={busy} onClick={save}>
              Сохранить
            </Button>
          )}
        </>
      )}
    </Card>
  )
}

function HistoryCard({ view }: { view: ActView }) {
  const [open, setOpen] = useState(false)
  return (
    <Card title="История" after={<button className="link" onClick={() => setOpen(!open)}>{open ? 'Свернуть' : 'Показать'}</button>}>
      {open && (
        <ul className="history">
          {view.events.map((e) => (
            <li key={e.id}>
              <span className="muted small">{new Date(e.created_at).toLocaleString('ru-RU')}</span> {eventTitle[e.type] ?? e.type}
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}
