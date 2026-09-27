// checkapi прогоняет обязательные проверки из DATA-API.yaml против развёрнутого API.
// Токены тестовых учёток — из переменных окружения, чтобы не оставлять их в истории команд:
//
//	CHAIRMAN_TOKEN=… RESIDENT_TOKEN=… go run ./cmd/checkapi -spec ../DATA-API.yaml -base https://<домен>/api/v1
//
// Токен роли ищется в переменной <РОЛЬ>_TOKEN. Код выхода 1, если хотя бы одна проверка не прошла.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"priemka/internal/apicheck"
)

func main() {
	specPath := flag.String("spec", "../DATA-API.yaml", "файл проверок")
	base := flag.String("base", "", "базовый адрес API (по умолчанию base_url из файла)")
	flag.Parse()

	spec, err := apicheck.Load(*specPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *base == "" {
		*base = spec.BaseURL
	}
	if strings.Contains(*base, "<") {
		fmt.Fprintf(os.Stderr, "base_url в файле — заглушка %q: укажите -base\n", *base)
		os.Exit(2)
	}
	tokens := map[string]string{}
	for role := range spec.Roles {
		tokens[role] = os.Getenv(strings.ToUpper(role) + "_TOKEN")
	}

	fmt.Printf("%s — %s, проверок: %d\n", spec.Solution, *base, len(spec.Checks))
	failed := 0
	for _, r := range spec.Run(context.Background(), apicheck.Options{BaseURL: *base, Tokens: tokens, Client: &http.Client{Timeout: 30 * time.Second}}) {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL %-24s %v\n", r.ID, r.Err)
			continue
		}
		fmt.Printf("OK   %-24s %d\n", r.ID, r.Status)
	}
	if failed > 0 {
		fmt.Printf("Не прошло: %d\n", failed)
		os.Exit(1)
	}
	fmt.Println("Все проверки пройдены.")
}
