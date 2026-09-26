package domain

import "fmt"

// Status — статус акта (PLAN.md §3).
type Status string

const (
	StatusDraft          Status = "draft"
	StatusInReview       Status = "in_review"
	StatusDecided        Status = "decided"
	StatusClosedSigned   Status = "closed_signed"
	StatusRefusedSent    Status = "refused_sent"
	StatusDeemedAccepted Status = "deemed_accepted"
	StatusReplaced       Status = "replaced"
)

// transitions — допустимые переходы статуса акта (PLAN.md §3).
var transitions = map[Status][]Status{
	StatusDraft:        {StatusInReview},
	StatusInReview:     {StatusDecided, StatusDeemedAccepted},
	StatusDecided:      {StatusDecided, StatusInReview, StatusClosedSigned, StatusRefusedSent, StatusDeemedAccepted},
	StatusRefusedSent:  {StatusReplaced},
	StatusClosedSigned: nil, StatusDeemedAccepted: nil, StatusReplaced: nil,
}

// CanTransition — разрешён ли переход статуса. Повторное «сформировать документ» (decided → decided) допустимо.
func (s Status) CanTransition(to Status) bool {
	for _, t := range transitions[s] {
		if t == to {
			return true
		}
	}
	return false
}

// Editable — можно ли менять шапку и строки акта.
func (s Status) Editable() bool {
	return s == StatusDraft || s == StatusInReview || s == StatusDecided
}

// Final — акт больше не участвует в приёмке.
func (s Status) Final() bool {
	return s == StatusClosedSigned || s == StatusDeemedAccepted || s == StatusReplaced
}

func (s Status) Title() string {
	switch s {
	case StatusDraft:
		return "Черновик: не указана дата получения"
	case StatusInReview:
		return "На проверке"
	case StatusDecided:
		return "Документ сформирован, не отмечена отправка"
	case StatusClosedSigned:
		return "Подписан и направлен исполнителю"
	case StatusRefusedSent:
		return "Отказ направлен, ожидается новый акт"
	case StatusDeemedAccepted:
		return "Принят молчанием (30 дней без ответа)"
	case StatusReplaced:
		return "Заменён новым актом"
	}
	return string(s)
}

// Decision — решение председателя. Графы «с замечаниями» в форме 761/пр нет, поэтому выбор бинарный.
type Decision string

const (
	DecisionSign   Decision = "sign"
	DecisionRefuse Decision = "refuse"
)

// ReviewStatus — статус строки акта.
type ReviewStatus string

const (
	ReviewUnchecked ReviewStatus = "unchecked"
	ReviewConfirmed ReviewStatus = "confirmed"
	ReviewDoubtful  ReviewStatus = "doubtful"
	ReviewNotDone   ReviewStatus = "not_done"
)

func (r ReviewStatus) Valid() bool {
	switch r {
	case ReviewUnchecked, ReviewConfirmed, ReviewDoubtful, ReviewNotDone:
		return true
	}
	return false
}

// Disputed — строка попадает в возражения.
func (r ReviewStatus) Disputed() bool { return r == ReviewDoubtful || r == ReviewNotDone }

func (r ReviewStatus) Title() string {
	switch r {
	case ReviewConfirmed:
		return "подтверждено"
	case ReviewDoubtful:
		return "под сомнением"
	case ReviewNotDone:
		return "не выполнено"
	}
	return "не проверено"
}

// Line — строка акта в объёме, нужном для расчётов.
type Line struct {
	Amount         Kopecks
	DisputedAmount *Kopecks
	Status         ReviewStatus
	Comment        string
}

// Sums — итоги по строкам акта.
type Sums struct {
	LinesTotal     Kopecks  `json:"lines_total"`
	Disputed       Kopecks  `json:"disputed"`
	DisputedCount  int      `json:"disputed_count"`
	UncheckedCount int      `json:"unchecked_count"`
	ActTotal       *Kopecks `json:"act_total"`
	// Mismatch — сумма строк не совпадает с итогом в п. 2 акта.
	Mismatch bool `json:"mismatch"`
}

// Summarize считает итоги. Оспариваемая сумма строки по умолчанию равна её цене.
func Summarize(lines []Line, actTotal *Kopecks) Sums {
	s := Sums{ActTotal: actTotal}
	for _, l := range lines {
		s.LinesTotal += l.Amount
		if l.Status == ReviewUnchecked {
			s.UncheckedCount++
		}
		if l.Status.Disputed() {
			s.DisputedCount++
			if l.DisputedAmount != nil {
				s.Disputed += *l.DisputedAmount
			} else {
				s.Disputed += l.Amount
			}
		}
	}
	s.Mismatch = actTotal != nil && *actTotal != s.LinesTotal
	return s
}

// DecisionError — решение нельзя принять; Code уходит в API, Message — пользователю.
type DecisionError struct {
	Code    string
	Message string
}

func (e *DecisionError) Error() string { return e.Message }

// ValidateDecision проверяет, можно ли сформировать документ.
// Подписание при спорных строках требует явного подтверждения: подпись означает согласие с п. 3–4 формы.
func ValidateDecision(st Status, d Decision, lines []Line, confirmDisputed bool) error {
	if !st.Editable() {
		return &DecisionError{"ACT_LOCKED", fmt.Sprintf("Решение уже нельзя изменить: %s.", st.Title())}
	}
	if st == StatusDraft {
		return &DecisionError{"RECEIVED_DATE_REQUIRED", "Сначала укажите дату получения акта — от неё считаются сроки."}
	}
	if len(lines) == 0 {
		return &DecisionError{"NO_LINES", "Добавьте строки акта — без них документ не сформировать."}
	}
	sums := Summarize(lines, nil)
	switch d {
	case DecisionRefuse:
		for _, l := range lines {
			if l.Status.Disputed() && l.Comment != "" {
				return nil
			}
		}
		return &DecisionError{"NO_OBJECTIONS", "Для отказа нужна хотя бы одна строка «не выполнено» или «под сомнением» с возражением в комментарии."}
	case DecisionSign:
		if sums.DisputedCount > 0 && !confirmDisputed {
			return &DecisionError{"DISPUTED_LINES", fmt.Sprintf("В форме акта нет графы «с замечаниями»: подписание означает, что работы приняты полностью. Спорных строк: %d. Подтвердите подписание или откажитесь.", sums.DisputedCount)}
		}
		return nil
	}
	return &DecisionError{"BAD_DECISION", "Решение должно быть sign или refuse."}
}
