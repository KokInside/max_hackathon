// Package civil — календарная дата без времени и часового пояса.
// Сроки приёмки считаются в календарных днях, поэтому время суток в них не участвует.
package civil

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"
)

const layout = "2006-01-02"

// Date хранится как полночь UTC. Нулевое значение означает «дата не указана».
type Date struct {
	t time.Time
}

func New(year int, month time.Month, day int) Date {
	return Date{t: time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
}

// Of возвращает календарную дату момента t в часовом поясе loc.
func Of(t time.Time, loc *time.Location) Date {
	y, m, d := t.In(loc).Date()
	return New(y, m, d)
}

// Допустимые годы: договоры и акты вне этого диапазона — почти наверняка опечатка.
const (
	MinYear = 1990
	MaxYear = 2100
)

// Parse принимает ISO (2026-09-25) и русский формат (25.09.2026).
func Parse(s string) (Date, error) {
	s = strings.TrimSpace(s)
	for _, l := range []string{layout, "02.01.2006", "2.1.2006"} {
		if t, err := time.Parse(l, s); err == nil {
			if t.Year() < MinYear || t.Year() > MaxYear {
				return Date{}, fmt.Errorf("год %d вне диапазона %d–%d", t.Year(), MinYear, MaxYear)
			}
			return Date{t: t}, nil
		}
	}
	return Date{}, fmt.Errorf("неверная дата %q", s)
}

func MustParse(s string) Date {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

func (d Date) IsZero() bool          { return d.t.IsZero() }
func (d Date) AddDays(n int) Date    { return Date{t: d.t.AddDate(0, 0, n)} }
func (d Date) Before(o Date) bool    { return d.t.Before(o.t) }
func (d Date) After(o Date) bool     { return d.t.After(o.t) }
func (d Date) Equal(o Date) bool     { return d.t.Equal(o.t) }
func (d Date) Time() time.Time       { return d.t }
func (d Date) Weekday() time.Weekday { return d.t.Weekday() }
func (d Date) String() string        { return d.format(layout) }
func (d Date) Russian() string       { return d.format("02.01.2006") }
func (d Date) format(l string) string {
	if d.IsZero() {
		return ""
	}
	return d.t.Format(l)
}

// DaysSince возвращает число календарных дней от o до d (d − o).
func (d Date) DaysSince(o Date) int {
	return int(d.t.Sub(o.t).Hours() / 24)
}

// Ptr возвращает nil для пустой даты — удобно для JSON и SQL.
func (d Date) Ptr() *Date {
	if d.IsZero() {
		return nil
	}
	return &d
}

func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + d.String() + `"`), nil
}

func (d *Date) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*d = Date{}
		return nil
	}
	p, err := time.Parse(layout, s)
	if err != nil {
		return fmt.Errorf("дата должна быть в формате ГГГГ-ММ-ДД: %q", s)
	}
	if p.Year() < MinYear || p.Year() > MaxYear {
		return fmt.Errorf("год %d вне диапазона %d–%d", p.Year(), MinYear, MaxYear)
	}
	*d = Date{t: p}
	return nil
}

// Value и Scan позволяют передавать Date в pgx как колонку типа date.
func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	return d.t, nil
}

func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = Date{}
	case time.Time:
		*d = New(v.Year(), v.Month(), v.Day())
	case string:
		p, err := Parse(v)
		if err != nil {
			return err
		}
		*d = p
	default:
		return fmt.Errorf("civil.Date: неподдерживаемый тип %T", src)
	}
	return nil
}
