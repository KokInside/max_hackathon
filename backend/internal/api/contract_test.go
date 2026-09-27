package api

// Контрактный тест собственного API: сквозной прогон всех эндпоинтов на настоящей PostgreSQL,
// каждый запрос и ответ (включая ошибки) проверяется по api/openapi.yaml (kin-openapi).
// Запуск: TEST_DATABASE_URL=postgres://… go test ./internal/api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/jackc/pgx/v5"

	"priemka/internal/app"
	"priemka/internal/files"
	"priemka/internal/pdf"
	"priemka/internal/rules"
	"priemka/internal/store"
)

const (
	chairmanToken = "contract-chairman-token"
	residentToken = "contract-resident-token"
)

type contract struct {
	t      *testing.T
	srv    *httptest.Server
	router routers.Router
	doc    *openapi3.T
	seen   map[string]bool // «МЕТОД путь-шаблон» → проверен
}

func newContract(t *testing.T) *contract {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL не задан — контрактный тест пропущен")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("tc_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	st, err := store.Open(ctx, dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join("..", "..", "..", "config")
	rs, err := rules.Load(conf)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := rules.LoadTexts(conf)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := pdf.New(filepath.Join("..", "..", "..", "assets", "fonts"))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := files.New(t.TempDir(), []byte("test-secret-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := app.New(app.Config{DemoMode: true, BotUsername: "test_bot", InviteTTL: app.DefaultInviteTTL(), ConfigDir: conf, MaxActFile: 20 << 20, MaxPhoto: 10 << 20},
		st, rs, tx, fs, pr, log)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.EnsureTestAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(Config{BotToken: "test-token", InitDataTTL: time.Hour, DevAuth: true, RateLimit: 1000, RateBurst: 1000,
		TestTokens: map[string]int64{chairmanToken: app.TestChairmanMaxID, residentToken: app.TestResidentMaxID}}, svc, log).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(ctx); err != nil {
		t.Fatalf("openapi.yaml невалиден: %v", err)
	}
	doc.Servers = openapi3.Servers{{URL: srv.URL + "/api/v1"}}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, ct := range []string{"application/pdf", "image/png", "image/jpeg", "image/webp"} {
		openapi3filter.RegisterBodyDecoder(ct, openapi3filter.FileBodyDecoder)
	}
	return &contract{t: t, srv: srv, router: router, doc: doc, seen: map[string]bool{}}
}

type call struct {
	method, path string
	token        string // bearer; "" — без аутентификации; "dev:<id>" — X-Dev-User
	body         any    // JSON
	form         *bytes.Buffer
	formType     string
	want         int
}

// do выполняет запрос, проверяет код и сверяет запрос и ответ со спецификацией. Возвращает тело.
func (c *contract) do(k call) map[string]any {
	c.t.Helper()
	var body io.Reader
	var raw []byte
	ct := ""
	switch {
	case k.form != nil:
		raw, ct = k.form.Bytes(), k.formType
	case k.body != nil:
		raw, _ = json.Marshal(k.body)
		ct = "application/json"
	}
	if raw != nil {
		body = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(k.method, c.srv.URL+"/api/v1"+k.path, body)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	switch {
	case strings.HasPrefix(k.token, "dev:"):
		req.Header.Set("X-Dev-User", strings.TrimPrefix(k.token, "dev:"))
	case k.token != "":
		req.Header.Set("Authorization", "Bearer "+k.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != k.want {
		c.t.Fatalf("%s %s: код %d, ожидался %d: %s", k.method, k.path, resp.StatusCode, k.want, respBody)
	}

	c.verify(req, raw, resp.StatusCode, resp.Header, respBody)
	return decodeJSON(respBody)
}

// verify сверяет запрос и ответ с openapi.yaml.
func (c *contract) verify(req *http.Request, raw []byte, status int, header http.Header, respBody []byte) {
	c.t.Helper()
	vreq, _ := http.NewRequest(req.Method, req.URL.String(), bytes.NewReader(raw))
	vreq.Header = req.Header.Clone()
	route, params, err := c.router.FindRoute(vreq)
	if err != nil {
		if status == http.StatusMethodNotAllowed || status == http.StatusNotFound && strings.Contains(req.URL.Path, "no-such") {
			return
		}
		c.t.Fatalf("%s %s нет в openapi.yaml: %v", req.Method, req.URL.Path, err)
	}
	c.seen[req.Method+" "+route.Path] = true
	in := &openapi3filter.RequestValidationInput{Request: vreq, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
	if status < 400 { // корректные запросы должны соответствовать схеме запроса
		if err := openapi3filter.ValidateRequest(context.Background(), in); err != nil {
			c.t.Errorf("%s %s: запрос не по контракту: %v", req.Method, req.URL.Path, err)
		}
	}
	out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: status, Header: header,
		Options: &openapi3filter.Options{IncludeResponseStatus: true}}
	out.SetBodyBytes(respBody)
	if err := openapi3filter.ValidateResponse(context.Background(), out); err != nil {
		c.t.Errorf("%s %s → %d: ответ не по контракту: %v", req.Method, req.URL.Path, status, err)
	}
	if status >= 400 && header.Get("Content-Type") != "application/json; charset=utf-8" {
		c.t.Errorf("%s %s: ошибка не в JSON", req.Method, req.URL.Path)
	}
}

func decodeJSON(b []byte) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func dig(m map[string]any, path ...any) any {
	var cur any = m
	for _, p := range path {
		switch k := p.(type) {
		case string:
			cur = cur.(map[string]any)[k]
		case int:
			cur = cur.([]any)[k]
		}
	}
	return cur
}

func TestContract(t *testing.T) {
	c := newContract(t)
	ch := chairmanToken

	me := c.do(call{method: "GET", path: "/me", token: ch, want: 200})
	if me["profile_complete"] != true || me["mode"] != "chairman" {
		t.Fatalf("me: %v", me)
	}
	c.do(call{method: "GET", path: "/me", want: 401})
	c.do(call{method: "GET", path: "/me", token: "wrong-token-0000000", want: 401})

	acts := c.do(call{method: "GET", path: "/acts", token: ch, want: 200})
	actID := dig(acts, "acts", 0, "id").(string)
	view := c.do(call{method: "GET", path: "/acts/" + actID, token: ch, want: 200})
	lineID := dig(view, "lines", 5, "id").(string)
	line0 := dig(view, "lines", 0, "id").(string)
	c.do(call{method: "GET", path: "/acts/" + actID, token: residentToken, want: 403})
	c.do(call{method: "GET", path: "/acts/00000000-0000-0000-0000-000000000000", token: ch, want: 404})
	c.do(call{method: "GET", path: "/acts/zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz", token: ch, want: 404})

	c.do(call{method: "PATCH", path: "/acts/" + actID, token: ch, body: map[string]any{"number": "17", "executor_sent_on": nil}, want: 200})
	c.do(call{method: "PATCH", path: "/acts/" + actID, token: ch, body: map[string]any{"received_on": "2099-01-01"}, want: 422})
	c.do(call{method: "PATCH", path: "/acts/" + actID, token: ch, body: map[string]any{"unknown": 1}, want: 400})

	newLine := c.do(call{method: "POST", path: "/acts/" + actID + "/lines", token: ch,
		body: map[string]any{"work_name": "Мытьё окон", "amount": "1000.00", "unit": "м²"}, want: 201})
	c.do(call{method: "PATCH", path: "/lines/" + lineID, token: ch,
		body: map[string]any{"review_status": "not_done", "comment": "снега не было", "disputed_amount": "5000"}, want: 200})
	c.do(call{method: "PATCH", path: "/lines/" + lineID, token: ch, body: map[string]any{"review_status": "maybe"}, want: 422})
	c.do(call{method: "DELETE", path: "/lines/" + newLine["id"].(string), token: ch, want: 204})

	// Доказательство: multipart.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("note", "фото")
	fw, _ := mw.CreateFormFile("file", "p.png")
	_, _ = fw.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde"))
	_ = mw.Close()
	ev := c.do(call{method: "POST", path: "/lines/" + lineID + "/evidence", token: ch, form: &buf, formType: mw.FormDataContentType(), want: 201})
	var buf2 bytes.Buffer
	mw3 := multipart.NewWriter(&buf2)
	fw3, _ := mw3.CreateFormFile("file", "p2.png")
	_, _ = fw3.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde"))
	_ = mw3.Close()
	ev2 := c.do(call{method: "POST", path: "/lines/" + lineID + "/evidence", token: ch, form: &buf2, formType: mw3.FormDataContentType(), want: 201})
	c.do(call{method: "DELETE", path: "/evidence/" + ev2["id"].(string), token: ch, want: 204})
	c.do(call{method: "DELETE", path: "/evidence/" + ev2["id"].(string), token: ch, want: 404})
	var bad bytes.Buffer
	mw2 := multipart.NewWriter(&bad)
	fw2, _ := mw2.CreateFormFile("file", "x.txt")
	_, _ = fw2.Write([]byte("просто текст"))
	_ = mw2.Close()
	c.do(call{method: "POST", path: "/lines/" + lineID + "/evidence", token: ch, form: &bad, formType: mw2.FormDataContentType(), want: 415})

	// Файл по подписанной ссылке.
	view = c.do(call{method: "GET", path: "/acts/" + actID, token: ch, want: 200})
	url := dig(view, "lines", 5, "evidence", 0, "url").(string)
	c.do(call{method: "GET", path: strings.TrimPrefix(url, "/api/v1"), want: 200})
	c.do(call{method: "GET", path: "/files/" + ev["file_id"].(string) + "?exp=1&sig=bad", want: 403})

	c.do(call{method: "GET", path: "/catalog/works?q=" + "мытье", token: ch, want: 200})
	c.do(call{method: "GET", path: "/catalog/works?q=x", token: ch, want: 422})
	c.do(call{method: "GET", path: "/rules", want: 200})

	inv := c.do(call{method: "POST", path: "/acts/" + actID + "/invites", token: ch, want: 201})
	tok := inv["token"].(string)
	c.do(call{method: "GET", path: "/invites/" + tok, token: residentToken, want: 200})
	c.do(call{method: "GET", path: "/invites/AAAAAAAAAAAAAAAA", token: residentToken, want: 404})
	c.do(call{method: "PUT", path: "/invites/" + tok + "/votes", token: residentToken,
		body: map[string]any{"entrance_no": 1, "votes": []any{map[string]any{"line_id": line0, "answer": "no"}}}, want: 422}) // без согласия
	c.do(call{method: "PUT", path: "/invites/" + tok + "/votes", token: residentToken,
		body: map[string]any{"consent": true, "entrance_no": 1, "votes": []any{map[string]any{"line_id": line0, "answer": "no", "comment": "не мыли"}}}, want: 200})

	c.do(call{method: "POST", path: "/acts/" + actID + "/dispatch", token: ch, body: map[string]any{"channel": "post"}, want: 409})
	c.do(call{method: "POST", path: "/acts/" + actID + "/decision", token: ch, body: map[string]any{"decision": "sign"}, want: 422})
	c.do(call{method: "POST", path: "/acts/" + actID + "/decision", token: ch, body: map[string]any{"decision": "refuse"}, want: 201})
	c.do(call{method: "POST", path: "/acts/" + actID + "/dispatch", token: ch, body: map[string]any{"channel": "pigeon"}, want: 422})
	c.do(call{method: "POST", path: "/acts/" + actID + "/dispatch", token: ch, body: map[string]any{"channel": "email", "sent_on": nil}, want: 201})
	c.do(call{method: "PATCH", path: "/lines/" + lineID, token: ch, body: map[string]any{"comment": "x"}, want: 409})

	succ := c.do(call{method: "POST", path: "/acts/" + actID + "/successor", token: ch, want: 201})
	newAct := dig(succ, "act", "id").(string)
	c.do(call{method: "POST", path: "/acts/" + actID + "/successor", token: ch, want: 409})
	c.do(call{method: "PATCH", path: "/acts/" + newAct, token: ch, body: map[string]any{"received_on": time.Now().Format("2006-01-02")}, want: 200})
	c.do(call{method: "POST", path: "/acts/" + newAct + "/demo-shift", token: ch, body: map[string]any{"to_day": 31}, want: 200})
	c.do(call{method: "POST", path: "/acts/" + newAct + "/demo-shift", token: ch, body: map[string]any{"to_day": 3}, want: 409})

	demo := c.do(call{method: "POST", path: "/acts/demo", token: "dev:900001", want: 201})
	if dig(demo, "act", "is_demo") != true {
		t.Fatal("демо-акт без пометки")
	}
	c.do(call{method: "PUT", path: "/me/profile", token: "dev:900002", body: map[string]any{
		"full_name": "Иванова Мария", "authority_type": "oss_decision", "address": "г. Казань, ул. Баумана, д. 1", "entrances_count": 2}, want: 200})
	c.do(call{method: "PUT", path: "/me/profile", token: "dev:900003", body: map[string]any{"full_name": "", "authority_type": "x", "address": "", "entrances_count": 0}, want: 422})

	// Прочие ошибки: неверный метод, нет пути, не тот Content-Type.
	c.do(call{method: "PUT", path: "/acts/" + actID, token: ch, want: 405})
	c.do(call{method: "GET", path: "/no-such-path", token: ch, want: 404})
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/v1/acts/"+actID+"/decision", strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer "+ch)
	req.Header.Set("Content-Type", "text/plain")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 415 {
		t.Fatalf("не тот Content-Type: %v %v", resp.StatusCode, err)
	}

	// Все операции из openapi.yaml проверены хотя бы раз.
	doc, _ := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
	for p, item := range doc.Paths.Map() {
		for m := range item.Operations() {
			if !c.seen[m+" "+p] {
				t.Errorf("операция %s %s не покрыта контрактным тестом", m, p)
			}
		}
	}
}

// Ограничение частоты: после всплеска — 429 с Retry-After, через секунду снова можно.
func TestRateLimiter(t *testing.T) {
	l := newLimiter(10, 30)
	now := time.Now()
	for i := 0; i < 30; i++ {
		if !l.allow("u", now) {
			t.Fatalf("запрос %d отклонён в пределах всплеска", i+1)
		}
	}
	if l.allow("u", now) {
		t.Fatal("31-й запрос в ту же секунду пропущен")
	}
	if !l.allow("other", now) {
		t.Fatal("лимит одного пользователя задел другого")
	}
	if !l.allow("u", now.Add(time.Second)) {
		t.Fatal("через секунду запросы снова должны проходить")
	}
}
