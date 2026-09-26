package maxbot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	maxapi "github.com/max-messenger/max-bot-api-client-go/v2"
	"github.com/max-messenger/max-bot-api-client-go/v2/model"
)

func TestRetryable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&maxapi.NetworkError{Op: "x", Err: errors.New("reset")}, true},
		{&maxapi.TimeoutError{Op: "x"}, true},
		{&maxapi.Error{Code: "too.many.requests"}, true},
		{&maxapi.Error{Code: "service.unavailable"}, true},
		{&maxapi.Error{Code: "attachment.not.ready"}, true},
		{fmt.Errorf("wrap: %w", &maxapi.Error{Code: "internal.error"}), true},
		{errors.New("parse response error: invalid character '<'"), true},
		{&maxapi.Error{Code: "verify.token"}, false},
		{&maxapi.Error{Code: "chat.not.found"}, false},
		{errors.New("что-то другое"), false},
	}
	for _, c := range cases {
		if got := retryable(c.err); got != c.want {
			t.Errorf("retryable(%v) = %v", c.err, got)
		}
	}
}

func TestMaxHost(t *testing.T) {
	for host, want := range map[string]bool{"max.ru": true, "i.max.ru": true, "fu.oneme.ru": true, "MAX.RU": true,
		"evilmax.ru": false, "max.ru.evil.com": false, "example.com": false, "127.0.0.1": false} {
		if maxHost(host) != want {
			t.Errorf("maxHost(%q) != %v", host, want)
		}
	}
}

// Токен бота не уходит на сторонний хост, даже если тот отвечает 401; скачивание — только по HTTPS.
func TestDownloadDoesNotLeakToken(t *testing.T) {
	var sawAuth atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuth.Store(true)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := &Client{http: srv.Client(), token: "secret-token", log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, err := c.Download(context.Background(), srv.URL+"/file"); err == nil {
		t.Fatal("ожидалась ошибка 401")
	}
	if sawAuth.Load() {
		t.Fatal("токен бота отправлен стороннему хосту")
	}
	if _, err := c.Download(context.Background(), "http://max.ru/file"); err == nil || !strings.Contains(err.Error(), "недопустимый") {
		t.Fatalf("http принят: %v", err)
	}
}

// Вебхук: чужой секрет — 401; свой — ответ сразу, обработка в фоне.
func TestWebhookHandler(t *testing.T) {
	api, _ := maxapi.NewApi("t")
	c := &Client{api: api, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan model.Update, 1)
	h := c.WebhookHandler(func(_ context.Context, u model.Update) {
		time.Sleep(300 * time.Millisecond) // долгая обработка
		done <- u
	}, "right-secret")
	body := `{"update_type":"message_created","timestamp":1,"message":{"sender":{"user_id":7},"recipient":{"chat_id":1,"chat_type":"dialog"},"body":{"mid":"m1","text":"hi"}}}`

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set(maxapi.SecretHeader, "wrong")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("чужой секрет: %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set(maxapi.SecretHeader, "right-secret")
	rec = httptest.NewRecorder()
	start := time.Now()
	h(rec, req)
	if rec.Code != http.StatusOK || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("ответ %d за %s — обработка не в фоне", rec.Code, time.Since(start))
	}
	select {
	case u := <-done:
		if u.GetMessage().Body.Mid != "m1" {
			t.Fatalf("%+v", u)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("событие не обработано")
	}
}
