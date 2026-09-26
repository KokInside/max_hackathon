// Приёмка — бот и API мини-приложения для приёмки актов УК советом МКД.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"priemka/internal/api"
	"priemka/internal/app"
	"priemka/internal/bot"
	"priemka/internal/files"
	"priemka/internal/maxbot"
	"priemka/internal/pdf"
	"priemka/internal/rules"
	"priemka/internal/store"
)

type env struct {
	BotToken, BotMode, BotUsername, PublicURL, WebhookSecret string
	DatabaseURL, FilesDir, FilesSecret, ConfigDir, FontsDir  string
	ExtraCA, HTTPAddr, TestAccounts                          string
	DemoMode, DevAuth                                        bool
	InitDataTTL                                              time.Duration
	LogLevel                                                 slog.Level
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func loadEnv() (env, error) {
	e := env{
		BotToken:      os.Getenv("MAX_BOT_TOKEN"),
		BotMode:       getenv("BOT_MODE", "off"),
		BotUsername:   getenv("BOT_USERNAME", "t84_hakaton_max_bot"),
		PublicURL:     strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"),
		WebhookSecret: os.Getenv("WEBHOOK_SECRET"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		FilesDir:      getenv("FILES_DIR", "/data/files"),
		FilesSecret:   os.Getenv("FILES_SECRET"),
		ConfigDir:     getenv("CONFIG_DIR", "/app/config"),
		FontsDir:      getenv("FONTS_DIR", "/app/assets/fonts"),
		ExtraCA:       getenv("EXTRA_CA_FILE", ""),
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		TestAccounts:  os.Getenv("TEST_ACCOUNTS"),
		DemoMode:      getenv("DEMO_MODE", "false") == "true",
		DevAuth:       getenv("DEV_AUTH", "false") == "true",
	}
	var errs []error
	var err error
	if e.InitDataTTL, err = time.ParseDuration(getenv("INIT_DATA_TTL", "24h")); err != nil {
		errs = append(errs, fmt.Errorf("INIT_DATA_TTL: %w", err))
	}
	if err := e.LogLevel.UnmarshalText([]byte(getenv("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}
	if e.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL обязателен"))
	}
	if len(e.FilesSecret) < 16 {
		errs = append(errs, errors.New("FILES_SECRET обязателен (не короче 16 символов)"))
	}
	switch e.BotMode {
	case "off":
	case "polling", "webhook":
		if e.BotToken == "" {
			errs = append(errs, fmt.Errorf("BOT_MODE=%s требует MAX_BOT_TOKEN", e.BotMode))
		}
	default:
		errs = append(errs, fmt.Errorf("BOT_MODE=%q: ожидается off, polling или webhook", e.BotMode))
	}
	if e.BotMode == "webhook" {
		if !strings.HasPrefix(e.PublicURL, "https://") {
			errs = append(errs, errors.New("BOT_MODE=webhook требует PUBLIC_URL вида https://домен"))
		}
		if len(e.WebhookSecret) < 16 {
			errs = append(errs, errors.New("BOT_MODE=webhook требует WEBHOOK_SECRET (16+ символов [A-Za-z0-9_-])"))
		}
		if e.DevAuth {
			errs = append(errs, errors.New("DEV_AUTH=true запрещён в проде (BOT_MODE=webhook)"))
		}
	}
	if e.BotToken == "" && !e.DevAuth && e.TestAccounts == "" {
		errs = append(errs, errors.New("без MAX_BOT_TOKEN API недоступно: включите DEV_AUTH=true (локально) или задайте TEST_ACCOUNTS"))
	}
	return e, errors.Join(errs...)
}

// parseTestAccounts: «chairman:TOKEN,resident:TOKEN» → токен → max_user_id тестовой учётки.
func parseTestAccounts(s string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		role, tok, ok := strings.Cut(pair, ":")
		if !ok || len(tok) < 16 {
			return nil, fmt.Errorf("TEST_ACCOUNTS: ожидается роль:токен (токен 16+ символов)")
		}
		switch role {
		case "chairman":
			out[tok] = app.TestChairmanMaxID
		case "resident":
			out[tok] = app.TestResidentMaxID
		default:
			return nil, fmt.Errorf("TEST_ACCOUNTS: неизвестная роль %q", role)
		}
	}
	return out, nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("остановка", "err", err)
		os.Exit(1)
	}
}

func run() error {
	e, err := loadEnv()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: e.LogLevel}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rs, err := rules.Load(e.ConfigDir)
	if err != nil {
		return err
	}
	texts, err := rules.LoadTexts(e.ConfigDir)
	if err != nil {
		return err
	}
	if err := rs.ValidateTexts(texts); err != nil {
		return fmt.Errorf("config/templates: %w", err)
	}
	renderer, err := pdf.New(e.FontsDir)
	if err != nil {
		return err
	}
	fs, err := files.New(e.FilesDir, []byte(e.FilesSecret))
	if err != nil {
		return err
	}
	st, err := openStore(ctx, e.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("миграции: %w", err)
	}

	var client *maxbot.Client
	if e.BotMode != "off" {
		hc, err := maxbot.NewHTTPClient(e.ExtraCA)
		if err != nil {
			return err
		}
		if client, err = maxbot.New(ctx, e.BotToken, hc, log); err != nil {
			return err
		}
		e.BotUsername = client.Username
		log.Info("MAX: бот подключён", "username", client.Username, "id", client.BotID)
	}

	svc, err := app.New(
		app.Config{
			DemoMode:    e.DemoMode,
			BotUsername: e.BotUsername,
			InviteTTL:   app.DefaultInviteTTL(),
			ConfigDir:   e.ConfigDir,
			MaxActFile:  20 << 20,
			MaxPhoto:    10 << 20,
		},
		st, rs, texts, fs, renderer, log)
	if err != nil {
		return err
	}

	testTokens, err := parseTestAccounts(e.TestAccounts)
	if err != nil {
		return err
	}
	if len(testTokens) > 0 {
		if _, _, err := svc.EnsureTestAccounts(ctx); err != nil {
			return fmt.Errorf("тестовые учётки: %w", err)
		}
	}

	mux := http.NewServeMux()
	api.New(
		api.Config{
			BotToken:    e.BotToken,
			InitDataTTL: e.InitDataTTL,
			DevAuth:     e.DevAuth,
			TestTokens:  testTokens,
		}, svc, log).Routes(mux)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		c, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := st.Ping(c); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","rules_version":%q,"bot_mode":%q}`, rs.Version, e.BotMode)
	})

	if client != nil {
		b := bot.New(client, svc, log)
		svc.SetNotifier(b)
		switch e.BotMode {
		case "webhook":
			mux.Handle("POST /webhook", client.WebhookHandler(b.Handle, e.WebhookSecret))
			if err := client.Subscribe(ctx, e.PublicURL+"/webhook", e.WebhookSecret); err != nil {
				return fmt.Errorf("подписка на вебхук: %w", err)
			}
			log.Info("MAX: вебхук зарегистрирован", "url", e.PublicURL+"/webhook")
		case "polling":
			if err := client.UnsubscribeAll(ctx); err != nil {
				log.Warn("MAX: не удалось снять вебхуки", "err", err)
			}
			go client.Poll(ctx, b.Handle)
			log.Info("MAX: long polling (только для локальной разработки)")
		}
	}
	go svc.RunScheduler(ctx, time.Minute)

	srv := &http.Server{
		Addr:              e.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       time.Minute,
	}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()
	log.Info("HTTP", "addr", e.HTTPAddr, "demo_mode", e.DemoMode, "dev_auth", e.DevAuth, "test_accounts", len(testTokens))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// openStore ждёт готовности БД при старте контейнеров.
func openStore(ctx context.Context, dsn string, log *slog.Logger) (*store.Store, error) {
	var lastErr error
	for i := range 30 {
		st, err := store.Open(ctx, dsn)
		if err == nil {
			return st, nil
		}
		lastErr = err
		log.Info("ожидание БД", "attempt", i+1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, lastErr
}
