// Package api — REST API мини-приложения (/api/v1). Контракт описан в api/openapi.yaml.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"

	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"priemka/internal/app"
	"priemka/internal/maxbot"
	"priemka/internal/store"
)

type Config struct {
	BotToken    string
	InitDataTTL time.Duration
	DevAuth     bool
	// TestTokens — bearer-токен → max_user_id тестовой учётки.
	TestTokens map[string]int64
	// RateLimit и RateBurst — запросов в секунду на пользователя и допустимый всплеск (0 — по умолчанию 10 и 30).
	RateLimit, RateBurst float64
}

type API struct {
	cfg     Config
	svc     *app.Service
	log     *slog.Logger
	limiter *limiter
}

func New(cfg Config, svc *app.Service, log *slog.Logger) *API {
	if cfg.RateLimit <= 0 {
		cfg.RateLimit = 10
	}
	if cfg.RateBurst <= 0 {
		cfg.RateBurst = 30
	}
	return &API{cfg: cfg, svc: svc, log: log, limiter: newLimiter(cfg.RateLimit, cfg.RateBurst)}
}

// limiter — «ведро токенов» на пользователя: в среднем rate запросов в секунду, всплеск до burst.
type limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(rate, burst float64) *limiter {
	return &limiter{rate: rate, burst: burst, buckets: map[string]*bucket{}}
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 10000 { // забываем давно неактивных
		for k, b := range l.buckets {
			if now.Sub(b.last) > time.Minute {
				delete(l.buckets, k)
			}
		}
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

var methods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

type ctxKey int

const (
	keyUser ctxKey = iota
	keyStartParam
	keyReqID
)

// Routes регистрирует обработчики в mux.
func (a *API) Routes(mux *http.ServeMux) {
	auth := func(h func(w http.ResponseWriter, r *http.Request, u store.User) error) http.Handler {
		return a.wrap(a.authenticate(h))
	}
	mux.Handle("GET /api/v1/me", auth(a.me))
	mux.Handle("PUT /api/v1/me/profile", auth(a.saveProfile))
	mux.Handle("GET /api/v1/acts", auth(a.listActs))
	mux.Handle("POST /api/v1/acts/demo", auth(a.createDemoAct))
	mux.Handle("GET /api/v1/acts/{id}", auth(a.getAct))
	mux.Handle("PATCH /api/v1/acts/{id}", auth(a.patchAct))
	mux.Handle("DELETE /api/v1/acts/{id}", auth(a.deleteAct))
	mux.Handle("POST /api/v1/acts/{id}/lines", auth(a.addLine))
	mux.Handle("PATCH /api/v1/lines/{id}", auth(a.patchLine))
	mux.Handle("DELETE /api/v1/lines/{id}", auth(a.deleteLine))
	mux.Handle("POST /api/v1/lines/{id}/evidence", auth(a.addEvidence))
	mux.Handle("DELETE /api/v1/evidence/{id}", auth(a.deleteEvidence))
	mux.Handle("POST /api/v1/acts/{id}/invites", auth(a.createInvite))
	mux.Handle("GET /api/v1/invites/{token}", auth(a.getInvite))
	mux.Handle("PUT /api/v1/invites/{token}/votes", auth(a.putVotes))
	mux.Handle("POST /api/v1/acts/{id}/decision", auth(a.decide))
	mux.Handle("POST /api/v1/acts/{id}/dispatch", auth(a.dispatch))
	mux.Handle("POST /api/v1/acts/{id}/successor", auth(a.successor))
	mux.Handle("POST /api/v1/acts/{id}/demo-shift", auth(a.demoShift))
	mux.Handle("GET /api/v1/catalog/works", auth(a.catalog))
	mux.Handle("GET /api/v1/rules", a.wrap(a.rulesList))
	mux.Handle("GET /api/v1/files/{id}", a.wrap(a.file))
	mux.Handle("/api/", a.wrap(func(w http.ResponseWriter, r *http.Request) error {
		// Путь существует, но с другим HTTP-методом — 405 с Allow, а не 404.
		var allow []string
		for _, m := range methods {
			r2 := r.Clone(r.Context())
			r2.Method = m
			if _, p := mux.Handler(r2); p != "/api/" {
				allow = append(allow, m)
			}
		}
		if len(allow) > 0 {
			w.Header().Set("Allow", strings.Join(allow, ", "))
			return &app.Error{Status: http.StatusMethodNotAllowed, Code: "METHOD_NOT_ALLOWED", Message: "Метод " + r.Method + " для этого адреса не поддерживается."}
		}
		return &app.Error{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Метод не найден."}
	}))
}

// wrap назначает request_id, пишет ошибки в едином формате и логирует запрос.
func (a *API) wrap(h func(w http.ResponseWriter, r *http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := randomID()
		w.Header().Set("X-Request-Id", reqID)
		w.Header().Set("Cache-Control", "no-store")
		r = r.WithContext(context.WithValue(r.Context(), keyReqID, reqID))
		err := h(w, r)
		status := http.StatusOK
		if err != nil {
			e := app.AsError(err)
			status = e.Status
			if status >= 500 {
				a.log.Error("api", "method", r.Method, "path", r.URL.Path, "request_id", reqID, "err", err)
			}
			writeJSON(w, status, map[string]any{
				"error":      map[string]any{"code": e.Code, "message": e.Message, "details": e.Details},
				"request_id": reqID,
			})
		}
		a.log.Debug("api", "method", r.Method, "path", r.URL.Path, "status", status, "ms", time.Since(start).Milliseconds())
	})
}

func (a *API) authenticate(h func(w http.ResponseWriter, r *http.Request, u store.User) error) func(w http.ResponseWriter, r *http.Request) error {
	next := h
	h = func(w http.ResponseWriter, r *http.Request, u store.User) error {
		if !a.limiter.allow(u.ID, time.Now()) {
			w.Header().Set("Retry-After", "1")
			return &app.Error{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "Слишком много запросов. Повторите через секунду."}
		}
		return next(w, r, u)
	}
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		unauth := &app.Error{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED", Message: "Откройте приложение из чата с ботом в MAX."}

		if raw := r.Header.Get("X-Max-Init-Data"); raw != "" {
			d, err := maxbot.ValidateInitData(raw, a.cfg.BotToken, a.cfg.InitDataTTL, time.Now())
			if err != nil {
				unauth.Message = "Сессия недействительна или устарела. Закройте и откройте приложение заново."
				return unauth
			}
			u, err := a.svc.EnsureUser(ctx, d.User.ID, d.User.FirstName, d.User.LastName)
			if err != nil {
				return err
			}
			ctx = context.WithValue(ctx, keyStartParam, d.StartParam)
			return h(w, r.WithContext(ctx), u)
		}
		if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && tok != "" {
			for t, maxID := range a.cfg.TestTokens {
				if subtle.ConstantTimeCompare([]byte(t), []byte(tok)) == 1 {
					u, err := a.svc.EnsureUser(ctx, maxID, "", "")
					if err != nil {
						return err
					}
					return h(w, r, u)
				}
			}
			return unauth
		}
		if a.cfg.DevAuth {
			if id, err := strconv.ParseInt(r.Header.Get("X-Dev-User"), 10, 64); err == nil && id != 0 {
				u, err := a.svc.EnsureUser(ctx, id, "Dev", strconv.FormatInt(id, 10))
				if err != nil {
					return err
				}
				ctx = context.WithValue(ctx, keyStartParam, r.Header.Get("X-Dev-Start-Param"))
				return h(w, r.WithContext(ctx), u)
			}
		}
		return unauth
	}
}

func startParam(r *http.Request) string {
	s, _ := r.Context().Value(keyStartParam).(string)
	return s
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decode читает JSON-тело с лимитом и запретом неизвестных полей.
func decode(r *http.Request, v any) error {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "application/json" {
		return &app.Error{Status: http.StatusUnsupportedMediaType, Code: "BAD_CONTENT_TYPE", Message: "Ожидается Content-Type: application/json."}
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &app.Error{Status: http.StatusBadRequest, Code: "BAD_JSON", Message: "Некорректный JSON: " + err.Error()}
	}
	if dec.More() {
		return &app.Error{Status: http.StatusBadRequest, Code: "BAD_JSON", Message: "Некорректный JSON: лишние данные после объекта."}
	}
	return nil
}
