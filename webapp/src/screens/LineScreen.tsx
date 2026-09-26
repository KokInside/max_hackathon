import { useRef, useState } from 'react'
import { Button, Input, Textarea } from '@maxhub/max-ui'
import { api, type ActView, type LineInput, type LineView, type S } from '../api/client'
import { reviewTitle, rub } from '../format'
import { Card, Chip, Sheet, useToast } from '../ui'

const statuses: S['ReviewStatus'][] = ['confirmed', 'doubtful', 'not_done']

export function LineScreen({ view, line, reload, onDeleted }: { view: ActView; line: LineView; reload: () => Promise<void>; onDeleted: () => void }) {
  const toast = useToast()
  const editable = view.editable
  const [comment, setComment] = useState(line.comment)
  const [disputed, setDisputed] = useState(line.disputed_amount ?? '')
  const [busy, setBusy] = useState<string>('')
  const [edit, setEdit] = useState(false)
  const [fields, setFields] = useState({ work_name: line.work_name, periodicity_qty: line.periodicity_qty, unit: line.unit, unit_price: line.unit_price ?? '', amount: line.amount })
  const fileRef = useRef<HTMLInputElement>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [note, setNote] = useState('')

  const save = async (patch: LineInput, key: string, msg = 'Сохранено') => {
    setBusy(key)
    try {
      await api.patchLine(line.id, patch)
      await reload()
      toast(msg)
    } catch (e) {
      toast((e as Error).message, 'bad')
    } finally {
      setBusy('')
    }
  }

  const upload = async (f: File) => {
    setBusy('upload')
    try {
      await api.addEvidence(line.id, f, note)
      setNote('')
      await reload()
      toast('Доказательство добавлено')
    } catch (e) {
      toast((e as Error).message, 'bad')
    } finally {
      setBusy('')
      if (fileRef.current) fileRef.current.value = ''
    }
  }

  const disputedNow = line.review_status === 'doubtful' || line.review_status === 'not_done'
  return (
    <>
      <Card>
        <p className="muted small">Строка {line.position}{line.pp290_ref ? ` · ПП № 290, ${line.pp290_ref}` : ''}</p>
        <h2 className="line-title">{line.work_name}</h2>
        <dl className="kv">
          <dt>Периодичность / объём</dt>
          <dd>{line.periodicity_qty || '—'}</dd>
          <dt>Ед. изм. / за единицу</dt>
          <dd>
            {line.unit || '—'} / {rub(line.unit_price)}
          </dd>
          <dt>Цена по акту</dt>
          <dd>
            <b>{rub(line.amount)}</b>
          </dd>
        </dl>
        {line.prev_disputed && <p className="warn-text small">Эта работа оспаривалась в предыдущем акте.</p>}
      </Card>

      <Card title="Оценка">
        <div className="seg">
          {statuses.map((s) => (
            <button key={s} className={'seg__btn seg__btn--' + s + (line.review_status === s ? ' is-active' : '')} disabled={!editable || !!busy} onClick={() => save({ review_status: s }, s, reviewTitle[s])}>
              {reviewTitle[s]}
            </button>
          ))}
        </div>
        <label className="field">
          Возражение / комментарий {disputedNow && <span className="warn-text">— попадёт в отказ</span>}
          <Textarea value={comment} disabled={!editable} onChange={(e) => setComment(e.target.value)} placeholder="Что именно не выполнено, когда проверяли, кто видел" rows={4} />
        </label>
        {disputedNow && (
          <label className="field">
            Оспариваемая сумма, руб. (пусто — вся цена строки)
            <Input inputMode="decimal" value={disputed} disabled={!editable} onChange={(e) => setDisputed(e.target.value)} />
          </label>
        )}
        {editable && (
          <Button stretched variant="secondary" loading={busy === 'comment'} disabled={comment === line.comment && disputed === (line.disputed_amount ?? '')} onClick={() => save({ comment, disputed_amount: disputedNow ? disputed : undefined }, 'comment')}>
            Сохранить комментарий
          </Button>
        )}
      </Card>

      <Card title={`Доказательства (${line.evidence.length})`}>
        <div className="photos">
          {line.evidence.map((e) => (
            <figure key={e.id}>
              {e.mime.startsWith('image/') ? (
                <a href={e.url} target="_blank" rel="noreferrer">
                  <img src={e.url} alt={e.note || e.code} loading="lazy" />
                </a>
              ) : (
                <a className="pdf" href={e.url} target="_blank" rel="noreferrer">
                  PDF
                </a>
              )}
              <figcaption>
                <b>{e.code}</b> {e.note}
                {editable && (
                  <button className="link bad" onClick={async () => { await api.deleteEvidence(e.id).catch((x) => toast(x.message, 'bad')); await reload() }}>
                    удалить
                  </button>
                )}
              </figcaption>
            </figure>
          ))}
        </div>
        {editable && (
          <>
            <label className="field">
              Подпись к фото (необязательно)
              <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="Где и когда снято" />
            </label>
            <input ref={fileRef} type="file" accept="image/jpeg,image/png,image/webp,application/pdf" hidden onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
            <Button stretched variant="secondary" loading={busy === 'upload'} onClick={() => fileRef.current?.click()}>
              📷 Добавить фото или PDF
            </Button>
            <p className="muted small">До 10 МБ: JPEG, PNG, WebP или PDF. В отказе доказательства получат номера Д-1, Д-2…</p>
          </>
        )}
      </Card>

      <Card title="Жильцы">
        {line.residents ? (
          <>
            <p>
              Было: <b>{line.residents.yes}</b> · Не было: <b>{line.residents.no}</b> · Не знают: {line.residents.unknown}
            </p>
            {line.residents.comments.map((c, i) => (
              <p key={i} className="small">«{c}»</p>
            ))}
          </>
        ) : (
          <p className="muted">{line.resident_visible ? 'Ответов пока нет.' : 'Строка скрыта от жильцов.'}</p>
        )}
        {editable && (
          <label className="choice">
            <input type="checkbox" checked={line.resident_visible} onChange={(e) => save({ resident_visible: e.target.checked }, 'vis')} /> Показывать строку жильцам
          </label>
        )}
      </Card>

      {editable && (
        <Card title="Данные строки" after={<button className="link" onClick={() => setEdit(!edit)}>{edit ? 'Свернуть' : 'Изменить'}</button>}>
          {edit && (
            <>
              {(
                [
                  ['work_name', 'Наименование'],
                  ['periodicity_qty', 'Периодичность / объём'],
                  ['unit', 'Ед. изм.'],
                  ['unit_price', 'Стоимость за единицу, руб.'],
                  ['amount', 'Цена, руб.'],
                ] as const
              ).map(([k, label]) => (
                <label key={k} className="field">
                  {label}
                  <Input value={fields[k]} onChange={(e) => setFields({ ...fields, [k]: e.target.value })} />
                </label>
              ))}
              <div className="row gap">
                <Button variant="secondary" loading={busy === 'fields'} onClick={() => save(fields, 'fields')}>
                  Сохранить
                </Button>
                <Button variant="destructive" onClick={() => setConfirmDelete(true)}>
                  Удалить строку
                </Button>
              </div>
            </>
          )}
        </Card>
      )}
      {!editable && <Chip>Акт закрыт для изменений</Chip>}
      {/* window.confirm в webview мини-приложений может быть заблокирован — подтверждаем своим окном */}
      <Sheet open={confirmDelete} onClose={() => setConfirmDelete(false)} title="Удалить строку?">
        <p>
          «{line.work_name}» и её доказательства ({line.evidence.length}) будут удалены. Действие нельзя отменить.
        </p>
        <Button
          stretched
          variant="destructive"
          loading={busy === 'delete'}
          onClick={async () => {
            setBusy('delete')
            try {
              await api.deleteLine(line.id)
              toast('Строка удалена')
              setConfirmDelete(false)
              onDeleted()
            } catch (e) {
              toast((e as Error).message, 'bad')
            } finally {
              setBusy('')
            }
          }}
        >
          Удалить
        </Button>
      </Sheet>
    </>
  )
}
