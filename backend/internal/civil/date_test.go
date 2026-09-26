package civil

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]string{"2026-09-25": "2026-09-25", "25.09.2026": "2026-09-25", "5.9.2026": "2026-09-05", " 01.01.2027 ": "2027-01-01"} {
		d, err := Parse(in)
		if err != nil || d.String() != want {
			t.Errorf("Parse(%q) = %v, %v", in, d, err)
		}
	}
	for _, bad := range []string{"", "31.02.2026", "2026/09/25", "01.01.0001", "01.01.2200", "завтра"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) без ошибки", bad)
		}
	}
}

func TestDaysAndJSON(t *testing.T) {
	a, b := New(2028, 2, 28), New(2028, 3, 1)
	if b.DaysSince(a) != 2 || a.AddDays(2) != b || !a.Before(b) {
		t.Fatal("арифметика дат через 29 февраля")
	}
	// Полночь в Москве — ещё предыдущий день в UTC; Of берёт дату в заданном поясе.
	msk := time.FixedZone("MSK", 3*3600)
	if got := Of(time.Date(2026, 9, 24, 22, 30, 0, 0, time.UTC), msk); got != New(2026, 9, 25) {
		t.Fatalf("Of = %s", got)
	}
	raw, _ := json.Marshal(struct {
		A Date `json:"a"`
		B Date `json:"b"`
	}{A: New(2026, 9, 25)})
	if string(raw) != `{"a":"2026-09-25","b":null}` {
		t.Fatalf("JSON = %s", raw)
	}
	var v struct{ A Date }
	if err := json.Unmarshal([]byte(`{"A":"25.09.2026"}`), &v); err == nil {
		t.Fatal("JSON принимает только ГГГГ-ММ-ДД")
	}
	if err := json.Unmarshal([]byte(`{"A":"0001-01-01"}`), &v); err == nil {
		t.Fatal("JSON с годом 0001 принят")
	}
	var s Date
	if err := s.Scan(time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)); err != nil || s != New(2026, 9, 25) {
		t.Fatalf("Scan: %v %v", s, err)
	}
	if err := s.Scan(nil); err != nil || !s.IsZero() {
		t.Fatal("Scan(nil)")
	}
}
