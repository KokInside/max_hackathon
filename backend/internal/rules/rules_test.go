package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Настоящий config/ должен загружаться без ошибок: иначе сервис не стартует.
func TestLoadRealConfig(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "config")
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Policy.ResponseDays != 10 || s.Policy.SilentDays != 30 || s.Policy.SendingMaxDays != 5 || !s.Policy.CountingNextDay {
		t.Fatalf("политика: %+v", s.Policy)
	}
	if got := s.Cite("council_response_days"); got != "порядок, утверждённый приказом Минстроя России от 22.05.2026 № 318/пр" {
		t.Fatalf("Cite = %q", got)
	}
	if len(s.Catalog.Search("мытье окон", 5)) == 0 {
		t.Fatal("поиск по справочнику не нашёл «мытьё окон»")
	}
	tx, err := LoadTexts(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateTexts(tx); err != nil {
		t.Fatal(err)
	}
	// Все шаблоны напоминаний рендерятся на типичных данных без ошибок и без незаполненных полей.
	data := map[string]any{"Act": map[string]string{"Number": "17", "Address": "г. Демоград"}, "Received": "01.10.2026",
		"ResponseOn": "11.10.2026", "SilentOn": "31.10.2026", "DaysLeftResponse": 3, "DaysLeftSilent": 5,
		"ResponseDays": 10, "SilentDays": 30, "CountingNextDay": true, "Cite": "порядок", "Demo": ""}
	for _, r := range append(s.Reminders, Reminder{Template: "deadlines_summary"}) {
		out, err := tx.Msg(r.Template, data)
		if err != nil || strings.Contains(out, "<no value>") {
			t.Errorf("%s: %v %q", r.Template, err, out)
		}
	}
	if got, _ := tx.Msg("remind_3_days_left", data); !strings.Contains(got, "осталось 3 дня") {
		t.Errorf("склонение: %q", got)
	}
	// Все обязательные поля документов есть и рендерятся.
	for _, k := range []string{"common.from", "common.to", "common.footer", "cover_letter.subject", "refusal.subject",
		"refusal.objections_heading", "refusal.disputed_total", "refusal.requests_heading", "refusal.evidence_empty"} {
		if _, err := tx.Doc(k, docData()); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
}

func docData() map[string]any {
	return map[string]any{"Address": "а", "CustomerName": "б", "Apartment": "1", "Authority": "в", "ExecutorName": "г",
		"Generated": "д", "RulesVersion": "1", "Number": "17", "ActDate": "е", "PeriodFrom": "ж", "PeriodTo": "з", "Total": "1",
		"Received": "и", "Channel": "", "CiteShort": "к", "HasViolations": true, "Disputed": "1", "LinesTotal": "2",
		"DisputedCount": 1, "Respondents": 1, "RequestsNo": 3}
}

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "дней", 1: "день", 2: "дня", 4: "дня", 5: "дней", 11: "дней", 14: "дней", 21: "день", 22: "дня", 111: "дней", -1: "день"} {
		if got := Plural(n, "день", "дня", "дней"); got != want {
			t.Errorf("Plural(%d) = %s", n, got)
		}
	}
}

func TestRejectsUnsupportedParams(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join("..", "..", "..", "config")
	for _, sub := range []string{"rules", "catalog"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(src, "rules", "acceptance.yaml"))
	cat, _ := os.ReadFile(filepath.Join(src, "catalog", "pp290.json"))
	_ = os.WriteFile(filepath.Join(dir, "catalog", "pp290.json"), cat, 0o644)
	for _, bad := range []struct{ from, to string }{
		{"day_type: calendar", "day_type: working"},
		{"shift_if_non_working: false", "shift_if_non_working: true"},
		{"reminder_hour: 10", "reminder_hour: 25"},
		{"    value: 10\n", "    value: -10\n"},
	} {
		if !strings.Contains(string(raw), bad.from) {
			t.Fatalf("в конфиге нет строки %q — тест устарел", bad.from)
		}
		mod := strings.Replace(string(raw), bad.from, bad.to, 1)
		_ = os.WriteFile(filepath.Join(dir, "rules", "acceptance.yaml"), []byte(mod), 0o644)
		if _, err := Load(dir); err == nil {
			t.Errorf("конфиг с %q принят", bad.to)
		}
	}
}
