import { useEffect, useState } from 'react'
import { Button, Input } from '@maxhub/max-ui'
import { api, type S } from '../api/client'
import { Card, useToast } from '../ui'

// Добавление строки акта с подсказками из минимального перечня работ (ПП РФ № 290).
export function AddLine({ actId, onAdded }: { actId: string; onAdded: () => Promise<void> }) {
  const toast = useToast()
  const [name, setName] = useState('')
  const [ref, setRef] = useState('')
  const [amount, setAmount] = useState('')
  const [periodicity, setPeriodicity] = useState('')
  const [unit, setUnit] = useState('')
  const [hints, setHints] = useState<S['CatalogItem'][]>([])
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    const q = name.trim()
    if (q.length < 3 || ref) {
      setHints([])
      return
    }
    const t = setTimeout(() => {
      api.works(q).then((r) => setHints(r.items)).catch(() => setHints([]))
    }, 300)
    return () => clearTimeout(t)
  }, [name, ref])

  const submit = async () => {
    setBusy(true)
    try {
      await api.addLine(actId, { work_name: name, pp290_ref: ref, amount, periodicity_qty: periodicity, unit })
      setName('')
      setRef('')
      setAmount('')
      setPeriodicity('')
      setUnit('')
      toast('Строка добавлена')
      await onAdded()
    } catch (e) {
      toast((e as Error).message, 'bad')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title="Добавить строку">
      <label className="field">
        Наименование работы (как в акте)
        <Input value={name} onChange={(e) => { setName(e.target.value); setRef('') }} placeholder="например, мытьё окон" />
      </label>
      {hints.length > 0 && (
        <ul className="hints">
          {hints.map((h) => (
            <li key={h.ref}>
              <button
                onClick={() => {
                  setName(h.text.charAt(0).toUpperCase() + h.text.slice(1))
                  setRef(h.ref)
                  setHints([])
                }}
              >
                <span>{h.text}</span>
                <span className="muted small">ПП № 290, {h.ref}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {ref && <p className="muted small">Позиция минимального перечня: ПП № 290, {ref}</p>}
      <div className="grid2">
        <label className="field">
          Периодичность / объём
          <Input value={periodicity} onChange={(e) => setPeriodicity(e.target.value)} />
        </label>
        <label className="field">
          Ед. изм.
          <Input value={unit} onChange={(e) => setUnit(e.target.value)} />
        </label>
      </div>
      <label className="field">
        Цена по акту, руб.
        <Input inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="0.00" />
      </label>
      <Button stretched variant="secondary" disabled={!name.trim() || !amount.trim()} loading={busy} onClick={submit}>
        Добавить
      </Button>
    </Card>
  )
}
