package main

import (
	"strings"
	"testing"
)

// setEnv задаёт окружение теста: сначала очищает все переменные сервиса, затем применяет kv.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{"MAX_BOT_TOKEN", "BOT_MODE", "PUBLIC_URL", "WEBHOOK_SECRET", "DATABASE_URL", "FILES_SECRET",
		"DEMO_MODE", "DEV_AUTH", "POLLING_TAKEOVER", "TEST_ACCOUNTS", "INIT_DATA_TTL", "LOG_LEVEL"} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadEnvLocalDefaults(t *testing.T) {
	// Так запускает docker compose по умолчанию: бот выключен, вход через X-Dev-User.
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://priemka:priemka@db/priemka", "FILES_SECRET": defaultFilesSecret, "DEV_AUTH": "true", "DEMO_MODE": "true"})
	e, err := loadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if e.BotMode != "off" || !e.DevAuth || !e.DemoMode || e.PollingTakeover {
		t.Fatalf("%+v", e)
	}
}

func TestLoadEnvProduction(t *testing.T) {
	good := map[string]string{
		"MAX_BOT_TOKEN": "t", "BOT_MODE": "webhook", "PUBLIC_URL": "https://priemka.example.ru", "WEBHOOK_SECRET": "0123456789abcdef_-XY",
		"DATABASE_URL": "postgres://priemka:s3cret-pass@db/priemka", "FILES_SECRET": "a-long-random-files-secret", "DEMO_MODE": "true",
	}
	setEnv(t, good)
	if _, err := loadEnv(); err != nil {
		t.Fatalf("корректный прод отклонён: %v", err)
	}
	cases := map[string]map[string]string{
		"DEV_AUTH в проде":          {"DEV_AUTH": "true"},
		"http вместо https":         {"PUBLIC_URL": "http://priemka.example.ru"},
		"не 443 порт":               {"PUBLIC_URL": "https://priemka.example.ru:8443"},
		"путь в PUBLIC_URL":         {"PUBLIC_URL": "https://priemka.example.ru/bot"},
		"короткий секрет":           {"WEBHOOK_SECRET": "short"},
		"недопустимые символы":      {"WEBHOOK_SECRET": "0123456789abcdef!@#$"},
		"FILES_SECRET по умолчанию": {"FILES_SECRET": defaultFilesSecret},
		"пароль БД по умолчанию":    {"DATABASE_URL": "postgres://priemka:priemka@db/priemka"},
		"флаг с опечаткой":          {"DEMO_MODE": "yes please"},
		"неизвестный режим бота":    {"BOT_MODE": "hook"},
		"нет токена при вебхуке":    {"MAX_BOT_TOKEN": ""},
	}
	for name, override := range cases {
		t.Run(name, func(t *testing.T) {
			kv := map[string]string{}
			for k, v := range good {
				kv[k] = v
			}
			for k, v := range override {
				kv[k] = v
			}
			setEnv(t, kv)
			if _, err := loadEnv(); err == nil {
				t.Fatalf("окружение принято: %v", override)
			}
		})
	}
}

func TestParseTestAccounts(t *testing.T) {
	m, err := parseTestAccounts("chairman:aaaaaaaaaaaaaaaa, resident:bbbbbbbbbbbbbbbb")
	if err != nil || len(m) != 2 {
		t.Fatalf("%v %v", m, err)
	}
	for _, bad := range []string{"chairman:short", "admin:aaaaaaaaaaaaaaaa", "chairman:aaaaaaaaaaaaaaaa,resident:aaaaaaaaaaaaaaaa", "chairman"} {
		if _, err := parseTestAccounts(bad); err == nil || !strings.Contains(err.Error(), "TEST_ACCOUNTS") {
			t.Errorf("%q принято: %v", bad, err)
		}
	}
	if m, err := parseTestAccounts(""); err != nil || len(m) != 0 {
		t.Fatal("пустая строка — нет тестовых учёток")
	}
}
