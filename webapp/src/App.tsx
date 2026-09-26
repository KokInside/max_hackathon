import { useCallback, useEffect, useState } from 'react'
import { api, type ActView, type Me } from './api/client'
import { inMax, devUser, setBack, startParam } from './bridge'
import { ErrorBox, Loading, Screen } from './ui'
import { ActList } from './screens/ActList'
import { ActScreen } from './screens/ActScreen'
import { LineScreen } from './screens/LineScreen'
import { Resident } from './screens/Resident'

type Route = { name: 'list' } | { name: 'act'; id: string } | { name: 'line'; actId: string; lineId: string }

function initialRoutes(): Route[] {
  const sp = startParam()
  if (sp.startsWith('act_')) return [{ name: 'list' }, { name: 'act', id: sp.slice(4) }]
  return [{ name: 'list' }]
}

export function App() {
  const [me, setMe] = useState<Me | null>(null)
  const [err, setErr] = useState('')
  const [stack, setStack] = useState<Route[]>(initialRoutes)
  const [view, setView] = useState<ActView | null>(null)
  const [viewErr, setViewErr] = useState('')
  const route = stack[stack.length - 1]
  const actId = route.name === 'act' ? route.id : route.name === 'line' ? route.actId : null

  const loadMe = useCallback(() => {
    setErr('')
    api.me().then(setMe).catch((e) => setErr(e.message))
  }, [])
  useEffect(loadMe, [loadMe])

  const reload = useCallback(async () => {
    if (!actId) return
    setViewErr('')
    try {
      setView(await api.act(actId))
    } catch (e) {
      setViewErr((e as Error).message)
    }
  }, [actId])

  useEffect(() => {
    setView((v) => (v && v.act.id === actId ? v : null))
    void reload()
  }, [reload, actId])

  const push = (r: Route) => setStack((s) => [...s, r])
  const back = useCallback(() => setStack((s) => (s.length > 1 ? s.slice(0, -1) : s)), [])
  const canBack = stack.length > 1
  useEffect(() => {
    setBack(canBack ? back : null)
  }, [canBack, back])

  if (!inMax() && !devUser) {
    return (
      <Screen title="Приёмка">
        <ErrorBox message="Откройте приложение из чата с ботом в MAX: кнопка «Открыть проверку»." />
      </Screen>
    )
  }
  if (err) return <Screen title="Приёмка"><ErrorBox message={err} onRetry={loadMe} /></Screen>
  if (!me) return <Screen title="Приёмка"><Loading /></Screen>

  if (me.mode === 'resident' && me.invite_token) {
    return (
      <Screen title="Проверка работ по дому">
        <Resident token={me.invite_token} />
      </Screen>
    )
  }

  if (route.name === 'list') {
    return (
      <Screen title="Мои акты">
        <ActList me={me} openAct={(id) => push({ name: 'act', id })} />
      </Screen>
    )
  }

  const body = viewErr ? <ErrorBox message={viewErr} onRetry={reload} /> : !view ? <Loading /> : null
  if (route.name === 'act') {
    return (
      <Screen title="Проверка акта" onBack={back}>
        {body ?? <ActScreen view={view!} onChange={setView} reload={reload} openLine={(lineId) => push({ name: 'line', actId: route.id, lineId })} openAct={(id) => setStack((s) => [...s.slice(0, -1), { name: 'act', id }])} />}
      </Screen>
    )
  }
  const line = view?.lines.find((l) => l.id === route.lineId)
  return (
    <Screen title="Строка акта" onBack={back}>
      {body ?? (line ? <LineScreen key={line.id + line.updated_at} view={view!} line={line} reload={reload} onDeleted={() => { back(); void reload() }} /> : <ErrorBox message="Строка удалена." />)}
    </Screen>
  )
}
