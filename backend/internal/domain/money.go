package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// Kopecks — денежная сумма в копейках. Суммы акта складываются без округлений float.
type Kopecks int64

// MaxRubles — предел целой части: колонки numeric(14,2) вмещают 12 знаков до запятой.
const MaxRubles = 999_999_999_999

// ParseMoney принимает «1234.56», «1234,56», «1 234,5». Пустая строка — ошибка.
func ParseMoney(s string) (Kopecks, error) {
	s = strings.NewReplacer(" ", "", " ", "", ",", ".").Replace(strings.TrimSpace(s))
	if s == "" {
		return 0, fmt.Errorf("пустая сумма")
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("больше двух знаков после запятой: %q", s)
	}
	frac += strings.Repeat("0", 2-len(frac))
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 {
		return 0, fmt.Errorf("неверная сумма %q", s)
	}
	if w > MaxRubles {
		return 0, fmt.Errorf("слишком большая сумма %q", s)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("неверная сумма %q", s)
	}
	k := Kopecks(w*100 + f)
	if neg {
		k = -k
	}
	return k, nil
}

// String — формат для API и SQL numeric: «1234.50».
func (k Kopecks) String() string {
	sign := ""
	if k < 0 {
		sign, k = "-", -k
	}
	return fmt.Sprintf("%s%d.%02d", sign, k/100, k%100)
}

// Rubles — формат для документов: «1 234,50».
func (k Kopecks) Rubles() string {
	sign := ""
	if k < 0 {
		sign, k = "-", -k
	}
	whole := strconv.FormatInt(int64(k/100), 10)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return fmt.Sprintf("%s%s,%02d", sign, b.String(), k%100)
}
