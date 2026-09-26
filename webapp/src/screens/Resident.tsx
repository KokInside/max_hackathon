import { useEffect, useState } from 'react'
import { Button, Textarea } from '@maxhub/max-ui'
import { api, type ResidentView } from '../api/client'
import { date } from '../format'
import { Card, Chip, ErrorBox, Loading, useToast } from '../ui'

type Answer = 'yes' | 'no' | 'unknown'
const answers: [Answer, string][] = [
  ['yes', 'Было'],
  ['no', 'Не было'],
  ['unknown', 'Не знаю'],
]

// Экран жильца: открыт по ссылке ?startapp=inv_<token>. Цены и имена не показываются.
export function Resident({ token }: { token: string }) {
  const toast = useToast()
  const [view, setView] = useState<ResidentView | null>(null)
  const [err, setErr] = useState('')
  const [entrance, setEntrance] = useState(0)
  const [consent, setConsent] = useState(false)
  const [votes, setVotes] = useState<Record<string, { answer: Answer; comment: string }>>({})
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)

  const load = () => {
    setErr('')
    api
      .resident(token)
      .then((v) => {
        setView(v)
        setConsent(v.consented)
        const m: typeof votes = {}
        for (const x of v.my_votes) {
          m[x.line_id] = { answer: x.answer, comment: x.comment ?? '' }
          if (x.entrance_no) setEntrance(x.entrance_no)
        }
        setVotes(m)
      })
      .catch((e) => setErr(e.message))
  }
  useEffect(load, [token])

  if (err) return <ErrorBox message={err} onRetry={load} />
  if (!view) return <Loading />

  const visible = view.lines.filter((l) => !entrance || !l.entrances?.length || l.entrances.includes(entrance))
  const submit = async () => {
    setBusy(true)
    try {
      const list = Object.entries(votes)
        .filter(([id]) => visible.some((l) => l.id === id))
        .map(([line_id, v]) => ({ line_id, answer: v.answer, comment: v.comment }))
      setView(await api.vote(token, { consent, entrance_no: entrance, votes: list }))
      setDone(true)
      toast('Спасибо! Ответ учтён.')
    } catch (e) {
      toast((e as Error).message, 'bad')
    } finally {
      setBusy(false)
    }
  }

  if (done) {
    return (
      <Card tone="ok" title="Спасибо!">
        <p>Ваши ответы переданы совету дома. Их можно изменить, открыв ссылку ещё раз, пока идёт проверка акта.</p>
        <Button variant="secondary" onClick={() => setDone(false)}>
          Изменить ответы
        </Button>
      </Card>
    )
  }

  return (
    <>
      <Card>
        <h2 className="line-title">Проверка работ управляющей компании</h2>
        <p>{view.address}</p>
        <p className="muted small">
          Акт № {view.act_number || 'б/н'}, период {date(view.period_from)} — {date(view.period_to)}
        </p>
        {view.is_demo && <Chip tone="demo">ДЕМО · синтетические данные</Chip>}
        <p className="small">Отметьте, видели ли вы эти работы в своём подъезде и во дворе. Ваше имя в документы не попадает — совет видит только число ответов и комментарии.</p>
      </Card>
      {!view.open ? (
        <Card tone="warn">
          <p>Проверка этого акта уже завершена. Спасибо!</p>
        </Card>
      ) : (
        <>
          <Card title="Ваш подъезд">
            <div className="row gap wrap">
              {Array.from({ length: view.entrances_count }, (_, i) => i + 1).map((n) => (
                <button key={n} className={'pill' + (entrance === n ? ' is-active' : '')} onClick={() => setEntrance(n)}>
                  {n}
                </button>
              ))}
            </div>
          </Card>
          {entrance > 0 &&
            visible.map((l) => {
              const v = votes[l.id]
              return (
                <Card key={l.id}>
                  <p>
                    <b>{l.work_name}</b>
                  </p>
                  {l.periodicity_qty && <p className="muted small">По акту: {l.periodicity_qty}</p>}
                  <div className="seg">
                    {answers.map(([a, t]) => (
                      <button key={a} className={'seg__btn seg__btn--' + a + (v?.answer === a ? ' is-active' : '')} onClick={() => setVotes({ ...votes, [l.id]: { answer: a, comment: v?.comment ?? '' } })}>
                        {t}
                      </button>
                    ))}
                  </div>
                  {v?.answer === 'no' && (
                    <Textarea placeholder="Что видели? Например: окна не мыли с весны" value={v.comment} rows={2} onChange={(e) => setVotes({ ...votes, [l.id]: { ...v, comment: e.target.value } })} />
                  )}
                </Card>
              )
            })}
          {entrance > 0 && (
            <Card>
              {!view.consented && (
                <label className="choice">
                  <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} /> Согласен на обработку моих ответов для проверки акта советом дома
                </label>
              )}
              <Button stretched loading={busy} disabled={!consent || Object.keys(votes).length === 0} onClick={submit}>
                Отправить ответы
              </Button>
            </Card>
          )}
        </>
      )}
    </>
  )
}
