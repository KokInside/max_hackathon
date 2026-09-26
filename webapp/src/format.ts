export function rub(v: string | null | undefined): string {
  if (v == null || v === '') return '—'
  const n = Number(v)
  if (Number.isNaN(n)) return v
  return n.toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 }) + ' ₽'
}

export function date(v: string | null | undefined): string {
  if (!v) return '—'
  const [y, m, d] = v.slice(0, 10).split('-')
  return `${d}.${m}.${y}`
}

export function plural(n: number, one: string, few: string, many: string): string {
  const a = Math.abs(n) % 100
  const b = a % 10
  if (a > 10 && a < 20) return many
  if (b > 1 && b < 5) return few
  if (b === 1) return one
  return many
}

export const kindTitle: Record<string, string> = {
  norm: 'Норма',
  calculation: 'Расчёт сервиса',
  recommendation: 'Рекомендация',
  assumption: 'Допущение',
}

export const reviewTitle: Record<string, string> = {
  unchecked: 'Не проверено',
  confirmed: 'Подтверждено',
  doubtful: 'Под сомнением',
  not_done: 'Не выполнено',
}

export const channelTitle: Record<string, string> = {
  in_person: 'Лично',
  post: 'Почтой',
  email: 'E-mail',
  executor_portal: 'Личный кабинет УК',
  other: 'Иначе',
}

export const eventTitle: Record<string, string> = {
  act_created: 'Акт добавлен',
  received_set: 'Указана дата получения',
  invite_created: 'Создано приглашение жильцам',
  resident_votes: 'Ответили жильцы',
  decided: 'Сформирован документ',
  decision_outdated: 'Документ устарел после правок',
  dispatched: 'Отмечена отправка',
  reminder_sent: 'Отправлено напоминание',
  deemed_accepted: 'Принят молчанием',
  replaced: 'Заменён новым актом',
  demo_shift: 'Демо: время перемотано',
}
