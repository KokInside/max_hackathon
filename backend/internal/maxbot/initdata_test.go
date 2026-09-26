package maxbot

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sign формирует initData так, как это делает MAX (алгоритм из документации dev.max.ru).
func sign(t *testing.T, token string, fields map[string]string) string {
	t.Helper()
	var pairs []string
	for k, v := range fields {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	k := hmac.New(sha256.New, []byte("WebAppData"))
	k.Write([]byte(token))
	m := hmac.New(sha256.New, k.Sum(nil))
	m.Write([]byte(strings.Join(pairs, "\n")))
	v := url.Values{}
	for k, val := range fields {
		v.Set(k, val)
	}
	v.Set("hash", hex.EncodeToString(m.Sum(nil)))
	return v.Encode()
}

func TestValidateInitData(t *testing.T) {
	const token = "test-token"
	now := time.Unix(1790000000, 0)
	fields := map[string]string{
		"auth_date":   strconv.FormatInt(now.Add(-time.Hour).Unix(), 10),
		"query_id":    "q1",
		"start_param": "inv_abc",
		"user":        `{"id":42,"first_name":"Мария","last_name":"Иванова"}`,
	}
	raw := sign(t, token, fields)

	d, err := ValidateInitData(raw, token, 24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.User.ID != 42 || d.StartParam != "inv_abc" {
		t.Fatalf("%+v", d)
	}
	if _, err := ValidateInitData(raw, "other-token", 24*time.Hour, now); err == nil {
		t.Error("чужой токен принят")
	}
	if _, err := ValidateInitData(strings.Replace(raw, "inv_abc", "inv_xyz", 1), token, 24*time.Hour, now); err == nil {
		t.Error("подменённые данные приняты")
	}
	if _, err := ValidateInitData(raw, token, 30*time.Minute, now); err != ErrInitDataExpired {
		t.Errorf("просроченная initData: %v", err)
	}
}
