import { useEffect, useState } from 'react'
import { Button } from '@maxhub/max-ui'
import { api, type Me, type S } from '../api/client'
import { date } from '../format'
import { Card, Chip, ErrorBox, Loading, useToast } from '../ui'

export function ActList({ me, openAct }: { me: Me; openAct: (id: string) => void }) {
  const toast = useToast()
  const [acts, setActs] = useState<S['ActSummary'][] | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const load = () => {
    setErr('')
    api.acts().then((r) => setActs(r.acts)).catch((e) => setErr(e.message))
  }
  useEffect(load, [])

  if (!me.profile_complete) {
    return (
      <Card title="Сначала — профиль">
        <p>Заполните профиль председателя в чате с ботом: команда /start. Это займёт пару минут, данные попадут в шапку документов.</p>
      </Card>
    )
  }
  if (err) return <ErrorBox message={err} onRetry={load} />
  if (!acts) return <Loading />
  return (
    <>
      <Card>
        <p className="muted small">{me.house?.address}</p>
        <p>
          Получили акт от УК? Пришлите его <b>в чат с ботом</b> файлом или фото и укажите дату получения — бот посчитает сроки, а здесь вы проверите строки.
        </p>
        {me.demo_mode && (
          <Button
            variant="secondary"
            loading={busy}
            onClick={async () => {
              setBusy(true)
              try {
                const v = await api.createDemo()
                openAct(v.act.id)
                toast('Демо-акт создан. Укажите дату получения в разделе «Данные акта» или в боте.')
              } catch (e) {
                toast((e as Error).message, 'bad')
              } finally {
                setBusy(false)
              }
            }}
          >
            🧪 Создать демо-акт
          </Button>
        )}
      </Card>
      {acts.length === 0 && <p className="muted center">Актов пока нет.</p>}
      {acts.map((a) => (
        <button key={a.id} className="act-row" onClick={() => openAct(a.id)}>
          <span>
            <b>Акт № {a.number || 'б/н'}</b> {a.act_date && `от ${date(a.act_date)}`} {a.is_demo && <Chip tone="demo">ДЕМО</Chip>}
            <br />
            <span className="muted small">{a.status_title}</span>
          </span>
          <span className="small">{a.deadlines && a.status !== 'deemed_accepted' && !['closed_signed', 'replaced'].includes(a.status) ? `до ${date(a.deadlines.response_on)}` : ''} ›</span>
        </button>
      ))}
    </>
  )
}
