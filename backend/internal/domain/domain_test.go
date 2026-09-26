package domain

import (
	"testing"

	"priemka/internal/civil"
)

var policy = Policy{
	ResponseDays: 10, SilentDays: 30, DraftingMaxDays: 30, SendingMaxDays: 5, CopiesRequired: 2,
	ApplicableFrom: civil.New(2026, 9, 1), CountingNextDay: true,
}

func d(s string) civil.Date { return civil.MustParse(s) }

func TestComputeDeadlines(t *testing.T) {
	cases := []struct {
		name, received, response, silent string
		nextDay                          bool
	}{
		{"обычный месяц", "2026-10-01", "2026-10-11", "2026-10-31", true},
		{"переход через месяц", "2026-10-25", "2026-11-04", "2026-11-24", true},
		{"високосный февраль", "2028-02-20", "2028-03-01", "2028-03-21", true},
		{"конец года", "2026-12-28", "2027-01-07", "2027-01-27", true},
		{"счёт с дня получения", "2026-10-01", "2026-10-10", "2026-10-30", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := policy
			p.CountingNextDay = c.nextDay
			got := p.ComputeDeadlines(d(c.received))
			if got.ResponseOn != d(c.response) || got.SilentOn != d(c.silent) {
				t.Fatalf("got %s / %s, want %s / %s", got.ResponseOn, got.SilentOn, c.response, c.silent)
			}
		})
	}
}

func TestTiming(t *testing.T) {
	rec := d("2026-10-01")
	cases := []struct {
		today string
		stage Stage
		day   int
		left  int
	}{
		{"2026-10-01", StageResponseWindow, 0, 10},
		{"2026-10-11", StageResponseWindow, 10, 0},
		{"2026-10-12", StageOverdue, 11, -1},
		{"2026-10-31", StageOverdue, 30, -20},
		{"2026-11-01", StageSilentAccepted, 31, -21},
	}
	for _, c := range cases {
		got := policy.Timing(rec, d(c.today))
		if got.Stage != c.stage || got.DayNumber != c.day || got.DaysLeftResponse != c.left {
			t.Errorf("%s: got %+v, want stage=%s day=%d left=%d", c.today, got, c.stage, c.day, c.left)
		}
	}
	if got := policy.Timing(civil.Date{}, d("2026-10-01")); got.Stage != StageNotReceived {
		t.Errorf("без даты получения: %+v", got)
	}
}

func codes(fs []Finding) map[string]Severity {
	m := map[string]Severity{}
	for _, f := range fs {
		m[f.Code] = f.Severity
	}
	return m
}

func TestCheckExecutor(t *testing.T) {
	two, one := 2, 1
	no := false

	t.Run("всё в срок", func(t *testing.T) {
		got := codes(policy.CheckExecutor(ActFacts{
			ActDate: d("2026-10-05"), PeriodFrom: d("2026-09-01"), PeriodTo: d("2026-09-30"),
			ReceivedOn: d("2026-10-08"), CopiesReceived: &two,
		}))
		if got["drafting_ok"] != SevOK || got["sending_ok"] != SevOK || len(got) != 2 {
			t.Fatalf("%v", got)
		}
	})

	t.Run("оформлен позже 30 дней", func(t *testing.T) {
		got := codes(policy.CheckExecutor(ActFacts{ActDate: d("2026-11-01"), PeriodTo: d("2026-09-30")}))
		if got["drafting_late"] != SevViolation {
			t.Fatalf("%v", got)
		}
	})

	t.Run("ровно 30 дней — не нарушение", func(t *testing.T) {
		got := codes(policy.CheckExecutor(ActFacts{ActDate: d("2026-10-30"), PeriodTo: d("2026-09-30")}))
		if got["drafting_ok"] != SevOK {
			t.Fatalf("%v", got)
		}
	})

	t.Run("известна дата отправки — нарушение", func(t *testing.T) {
		got := codes(policy.CheckExecutor(ActFacts{ActDate: d("2026-10-01"), ExecutorSentOn: d("2026-10-07"), ReceivedOn: d("2026-10-09")}))
		if got["sending_late"] != SevViolation {
			t.Fatalf("%v", got)
		}
	})

	t.Run("дата отправки неизвестна — предупреждение", func(t *testing.T) {
		got := codes(policy.CheckExecutor(ActFacts{ActDate: d("2026-10-01"), ReceivedOn: d("2026-10-09")}))
		if got["sending_maybe_late"] != SevWarning {
			t.Fatalf("%v", got)
		}
	})

	t.Run("после окончания договора, не хватает экземпляров, не подписан", func(t *testing.T) {
		got := codes(policy.CheckExecutor(ActFacts{
			ActDate: d("2026-10-05"), ContractEndDate: d("2026-10-01"), CopiesReceived: &one, ExecutorSigned: &no,
		}))
		for _, c := range []string{"drafting_after_contract_end", "copies_missing", "executor_not_signed"} {
			if got[c] != SevViolation {
				t.Errorf("нет %s: %v", c, got)
			}
		}
	})

	t.Run("период до вступления порядка в силу", func(t *testing.T) {
		got := codes(policy.CheckExecutor(ActFacts{PeriodFrom: d("2026-08-01")}))
		if got["period_before_applicability"] != SevWarning {
			t.Fatalf("%v", got)
		}
	})
}

func TestMoney(t *testing.T) {
	cases := map[string]Kopecks{"1234.5": 123450, "1 234,56": 123456, "0,07": 7, "15": 1500, "-3.10": -310}
	for in, want := range cases {
		got, err := ParseMoney(in)
		if err != nil || got != want {
			t.Errorf("ParseMoney(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "1.234", "abc", "1,2,3"} {
		if _, err := ParseMoney(bad); err == nil {
			t.Errorf("ParseMoney(%q) без ошибки", bad)
		}
	}
	if s := Kopecks(123456789).Rubles(); s != "1 234 567,89" {
		t.Errorf("Rubles = %q", s)
	}
	if s := Kopecks(-5).String(); s != "-0.05" {
		t.Errorf("String = %q", s)
	}
}

func TestSummarizeAndDecision(t *testing.T) {
	part := Kopecks(300)
	lines := []Line{
		{Amount: 1000, Status: ReviewConfirmed},
		{Amount: 500, Status: ReviewNotDone, Comment: "не убирали"},
		{Amount: 700, Status: ReviewDoubtful, DisputedAmount: &part},
		{Amount: 200, Status: ReviewUnchecked},
	}
	total := Kopecks(2500)
	s := Summarize(lines, &total)
	if s.LinesTotal != 2400 || s.Disputed != 800 || s.DisputedCount != 2 || s.UncheckedCount != 1 || !s.Mismatch {
		t.Fatalf("%+v", s)
	}

	if err := ValidateDecision(StatusInReview, DecisionRefuse, lines, false); err != nil {
		t.Errorf("отказ с возражением: %v", err)
	}
	if err := ValidateDecision(StatusInReview, DecisionSign, lines, false); err == nil || err.(*DecisionError).Code != "DISPUTED_LINES" {
		t.Errorf("подписание со спорными строками без подтверждения: %v", err)
	}
	if err := ValidateDecision(StatusInReview, DecisionSign, lines, true); err != nil {
		t.Errorf("подписание с подтверждением: %v", err)
	}
	clean := []Line{{Amount: 100, Status: ReviewConfirmed}}
	if err := ValidateDecision(StatusInReview, DecisionRefuse, clean, false); err == nil || err.(*DecisionError).Code != "NO_OBJECTIONS" {
		t.Errorf("отказ без возражений: %v", err)
	}
	if err := ValidateDecision(StatusDeemedAccepted, DecisionSign, clean, false); err == nil || err.(*DecisionError).Code != "ACT_LOCKED" {
		t.Errorf("закрытый акт: %v", err)
	}
	if err := ValidateDecision(StatusDraft, DecisionSign, clean, false); err == nil || err.(*DecisionError).Code != "RECEIVED_DATE_REQUIRED" {
		t.Errorf("черновик: %v", err)
	}
}

func TestCheckExecutorDataErrors(t *testing.T) {
	got := codes(policy.CheckExecutor(ActFacts{ActDate: d("2026-09-20"), PeriodTo: d("2026-09-30")}))
	if got["act_date_before_period_end"] != SevWarning || got["drafting_ok"] != "" {
		t.Errorf("акт раньше конца периода: %v", got)
	}
	got = codes(policy.CheckExecutor(ActFacts{ActDate: d("2026-10-05"), ExecutorSentOn: d("2026-10-01")}))
	if got["sent_before_act_date"] != SevWarning || got["sending_ok"] != "" {
		t.Errorf("отправлен раньше оформления: %v", got)
	}
	// У нарушений по экземплярам в основании есть правило о направлении двух подписанных экземпляров.
	one, no := 1, false
	for _, f := range policy.CheckExecutor(ActFacts{CopiesReceived: &one, ExecutorSigned: &no}) {
		found := false
		for _, b := range f.Basis {
			found = found || b == RuleSendingMaxDays
		}
		if !found {
			t.Errorf("%s: нет основания %s", f.Code, RuleSendingMaxDays)
		}
	}
}

func TestMoneyBounds(t *testing.T) {
	if _, err := ParseMoney("999999999999.99"); err != nil {
		t.Errorf("максимальная сумма отклонена: %v", err)
	}
	for _, bad := range []string{"1000000000000", "92233720368547758", "1.-5", "--5"} {
		if _, err := ParseMoney(bad); err == nil {
			t.Errorf("ParseMoney(%q) без ошибки", bad)
		}
	}
}

func TestStatusTransitions(t *testing.T) {
	allowed := []struct{ from, to Status }{
		{StatusDraft, StatusInReview},
		{StatusInReview, StatusDecided}, {StatusInReview, StatusDeemedAccepted},
		{StatusDecided, StatusDecided}, {StatusDecided, StatusInReview}, {StatusDecided, StatusClosedSigned},
		{StatusDecided, StatusRefusedSent}, {StatusDecided, StatusDeemedAccepted},
		{StatusRefusedSent, StatusReplaced},
	}
	all := []Status{StatusDraft, StatusInReview, StatusDecided, StatusClosedSigned, StatusRefusedSent, StatusDeemedAccepted, StatusReplaced}
	ok := map[[2]Status]bool{}
	for _, a := range allowed {
		ok[[2]Status{a.from, a.to}] = true
	}
	for _, from := range all {
		for _, to := range all {
			if got := from.CanTransition(to); got != ok[[2]Status{from, to}] {
				t.Errorf("%s → %s: %v", from, to, got)
			}
		}
		// Редактируемы только статусы до отправки документа.
		if from.Editable() != (from == StatusDraft || from == StatusInReview || from == StatusDecided) {
			t.Errorf("%s Editable", from)
		}
	}
}
