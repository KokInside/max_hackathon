// Package domain — правила приёмки акта: сроки, проверки исполнителя, суммы, статусы.
// Пакет не знает о БД, HTTP и MAX: на входе данные акта и политика из конфига,
// на выходе результат вместе с идентификаторами правил, на которых он основан.
package domain

import (
	"fmt"

	"priemka/internal/civil"
)

// Kind — происхождение утверждения (ТЗ требует разделять факты, расчёты, рекомендации, допущения).
type Kind string

const (
	KindNorm           Kind = "norm"
	KindCalculation    Kind = "calculation"
	KindRecommendation Kind = "recommendation"
	KindAssumption     Kind = "assumption"
)

// Идентификаторы правил из config/rules/acceptance.yaml.
const (
	RuleResponseDays     = "council_response_days"
	RuleSilentDays       = "silent_acceptance_days"
	RuleDraftingMaxDays  = "executor_drafting_max_days"
	RuleSendingMaxDays   = "executor_sending_max_days"
	RuleCopiesRequired   = "copies_required"
	RuleServiceDayIsEnd  = "service_day_is_period_end"
	RuleApplicableFrom   = "applicability_from"
	RuleCountingNextDay  = "counting_starts_next_day"
	RuleNoRemarksInForm  = "form_has_no_remarks_option"
	RuleNewActAfterRefus = "new_act_after_refusal"
)

// Policy — числовые параметры порядка приёмки, загруженные из конфига.
type Policy struct {
	ResponseDays    int
	SilentDays      int
	DraftingMaxDays int
	SendingMaxDays  int
	CopiesRequired  int
	ApplicableFrom  civil.Date
	// CountingNextDay: день 1 — следующий после дня получения (ASSUMPTION в конфиге).
	CountingNextDay bool
}

// Deadlines — ключевые даты по акту.
type Deadlines struct {
	ResponseOn civil.Date `json:"response_on"` // последний день для ответа (10-й)
	SilentOn   civil.Date `json:"silent_on"`   // последний день до молчаливой приёмки (30-й)
	Basis      []string   `json:"basis"`
}

// dayN возвращает дату N-го дня срока, отсчитанного от даты получения.
func (p Policy) dayN(received civil.Date, n int) civil.Date {
	if p.CountingNextDay {
		return received.AddDays(n)
	}
	return received.AddDays(n - 1)
}

// DayNumber — какой сейчас день срока (0 — день получения при счёте со следующего дня).
func (p Policy) DayNumber(received, today civil.Date) int {
	n := today.DaysSince(received)
	if !p.CountingNextDay {
		n++
	}
	return n
}

// DateOfDay — дата N-го дня срока (для напоминаний).
func (p Policy) DateOfDay(received civil.Date, n int) civil.Date { return p.dayN(received, n) }

func (p Policy) ComputeDeadlines(received civil.Date) Deadlines {
	return Deadlines{
		ResponseOn: p.dayN(received, p.ResponseDays),
		SilentOn:   p.dayN(received, p.SilentDays),
		Basis:      []string{RuleResponseDays, RuleSilentDays, RuleCountingNextDay},
	}
}

// Stage — положение акта во времени относительно сроков.
type Stage string

const (
	StageNotReceived    Stage = "not_received"    // дата получения не указана
	StageResponseWindow Stage = "response_window" // идут 10 дней
	StageOverdue        Stage = "response_overdue"
	StageSilentAccepted Stage = "silent_accepted" // 30 дней прошли
)

type Timing struct {
	Stage            Stage `json:"stage"`
	DayNumber        int   `json:"day_number"`
	DaysLeftResponse int   `json:"days_left_response"`
	DaysLeftSilent   int   `json:"days_left_silent"`
}

func (p Policy) Timing(received, today civil.Date) Timing {
	if received.IsZero() {
		return Timing{Stage: StageNotReceived}
	}
	d := p.ComputeDeadlines(received)
	t := Timing{
		DayNumber:        p.DayNumber(received, today),
		DaysLeftResponse: d.ResponseOn.DaysSince(today),
		DaysLeftSilent:   d.SilentOn.DaysSince(today),
	}
	switch {
	case today.After(d.SilentOn):
		t.Stage = StageSilentAccepted
	case today.After(d.ResponseOn):
		t.Stage = StageOverdue
	default:
		t.Stage = StageResponseWindow
	}
	return t
}

// Severity — насколько серьёзен результат проверки.
type Severity string

const (
	SevOK        Severity = "ok"
	SevInfo      Severity = "info"
	SevWarning   Severity = "warning"
	SevViolation Severity = "violation"
)

// Finding — результат одной проверки. Нарушения исполнителя становятся аргументами отказа.
type Finding struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Kind     Kind     `json:"kind"`
	Text     string   `json:"text"`
	Basis    []string `json:"basis"`
}

// ActFacts — данные акта, нужные для проверок сроков исполнителя.
type ActFacts struct {
	ActDate         civil.Date
	PeriodFrom      civil.Date
	PeriodTo        civil.Date
	ContractEndDate civil.Date
	ReceivedOn      civil.Date
	ExecutorSentOn  civil.Date
	CopiesReceived  *int
	ExecutorSigned  *bool
}

// CheckExecutor проверяет, соблюдены ли исполнителем сроки и порядок оформления акта.
func (p Policy) CheckExecutor(a ActFacts) []Finding {
	var out []Finding

	if !a.ActDate.IsZero() && !a.PeriodTo.IsZero() {
		days := a.ActDate.DaysSince(a.PeriodTo)
		basis := []string{RuleDraftingMaxDays, RuleServiceDayIsEnd}
		if days > p.DraftingMaxDays {
			out = append(out, Finding{
				Code: "drafting_late", Severity: SevViolation, Kind: KindCalculation, Basis: basis,
				Text: fmt.Sprintf("Акт оформлен %s, через %d дн. после окончания периода (%s) — больше %d дней.",
					a.ActDate.Russian(), days, a.PeriodTo.Russian(), p.DraftingMaxDays),
			})
		} else if days >= 0 {
			out = append(out, Finding{
				Code: "drafting_ok", Severity: SevOK, Kind: KindCalculation, Basis: basis,
				Text: fmt.Sprintf("Акт оформлен через %d дн. после окончания периода — в пределах %d дней.", days, p.DraftingMaxDays),
			})
		} else {
			out = append(out, Finding{
				Code: "act_date_before_period_end", Severity: SevWarning, Kind: KindCalculation,
				Text: fmt.Sprintf("Акт датирован %s — раньше окончания периода (%s): работы за оставшиеся дни ещё не могли быть оказаны. Проверьте даты.",
					a.ActDate.Russian(), a.PeriodTo.Russian()),
			})
		}
	}

	if !a.ActDate.IsZero() && !a.ContractEndDate.IsZero() && a.ActDate.After(a.ContractEndDate) {
		out = append(out, Finding{
			Code: "drafting_after_contract_end", Severity: SevViolation, Kind: KindCalculation,
			Basis: []string{RuleDraftingMaxDays},
			Text:  fmt.Sprintf("Акт оформлен %s — после окончания договора (%s).", a.ActDate.Russian(), a.ContractEndDate.Russian()),
		})
	}

	if !a.ActDate.IsZero() {
		basis := []string{RuleSendingMaxDays}
		switch {
		case !a.ExecutorSentOn.IsZero():
			days := a.ExecutorSentOn.DaysSince(a.ActDate)
			if days < 0 {
				out = append(out, Finding{
					Code: "sent_before_act_date", Severity: SevWarning, Kind: KindCalculation,
					Text: "Дата отправки акта раньше даты его оформления — проверьте даты.",
				})
			} else if days > p.SendingMaxDays {
				out = append(out, Finding{
					Code: "sending_late", Severity: SevViolation, Kind: KindCalculation, Basis: basis,
					Text: fmt.Sprintf("Акт направлен %s, через %d дн. после оформления — больше %d дней.",
						a.ExecutorSentOn.Russian(), days, p.SendingMaxDays),
				})
			} else {
				out = append(out, Finding{
					Code: "sending_ok", Severity: SevOK, Kind: KindCalculation, Basis: basis,
					Text: fmt.Sprintf("Акт направлен через %d дн. после оформления — в пределах %d дней.", days, p.SendingMaxDays),
				})
			}
		case !a.ReceivedOn.IsZero():
			days := a.ReceivedOn.DaysSince(a.ActDate)
			if days > p.SendingMaxDays {
				out = append(out, Finding{
					Code: "sending_maybe_late", Severity: SevWarning, Kind: KindCalculation, Basis: basis,
					Text: fmt.Sprintf("Акт получен через %d дн. после оформления. Если исполнитель отправил его позже чем через %d дней, это нарушение. Укажите дату отправки (штемпель, дата письма), чтобы проверить.",
						days, p.SendingMaxDays),
				})
			} else if days >= 0 {
				out = append(out, Finding{
					Code: "sending_ok", Severity: SevOK, Kind: KindCalculation, Basis: basis,
					Text: fmt.Sprintf("Акт получен через %d дн. после оформления — в пределах %d дней.", days, p.SendingMaxDays),
				})
			}
		}
	}

	if !a.ActDate.IsZero() && !a.ReceivedOn.IsZero() && a.ReceivedOn.Before(a.ActDate) {
		out = append(out, Finding{
			Code: "received_before_act_date", Severity: SevWarning, Kind: KindCalculation,
			Text: "Дата получения раньше даты акта — проверьте даты.",
		})
	}

	if a.CopiesReceived != nil && *a.CopiesReceived < p.CopiesRequired {
		out = append(out, Finding{
			Code: "copies_missing", Severity: SevViolation, Kind: KindNorm, Basis: []string{RuleCopiesRequired, RuleSendingMaxDays},
			Text: fmt.Sprintf("Получено экземпляров: %d. Исполнитель должен направить %d подписанных экземпляра.", *a.CopiesReceived, p.CopiesRequired),
		})
	}
	if a.ExecutorSigned != nil && !*a.ExecutorSigned {
		out = append(out, Finding{
			Code: "executor_not_signed", Severity: SevViolation, Kind: KindNorm, Basis: []string{RuleSendingMaxDays},
			Text: "Экземпляры акта не подписаны исполнителем, а направить он должен оба подписанных экземпляра.",
		})
	}

	if !p.ApplicableFrom.IsZero() && !a.PeriodFrom.IsZero() && a.PeriodFrom.Before(p.ApplicableFrom) {
		out = append(out, Finding{
			Code: "period_before_applicability", Severity: SevWarning, Kind: KindAssumption, Basis: []string{RuleApplicableFrom},
			Text: fmt.Sprintf("Период акта начинается до %s. Порядок приёмки может не применяться к работам, выполненным раньше этой даты.", p.ApplicableFrom.Russian()),
		})
	}

	return out
}
