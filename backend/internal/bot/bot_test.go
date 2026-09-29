package bot

// Сквозной тест диалога с ботом по сценарию PLAN §7: поддельный сервер MAX (httptest, TLS) + настоящая PostgreSQL.
// Запуск: TEST_DATABASE_URL=postgres://… go test ./internal/bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	maxapi "github.com/max-messenger/max-bot-api-client-go/v2"
	"github.com/max-messenger/max-bot-api-client-go/v2/model"

	"priemka/internal/app"
	"priemka/internal/domain"
	"priemka/internal/files"
	"priemka/internal/maxbot"
	"priemka/internal/pdf"
	"priemka/internal/rules"
	"priemka/internal/store"
)

type sent struct {
	Text    string
	Buttons []model.Button
	File    bool
}

// fakeMAX — минимальный Bot API: /me, /messages, /answers, /me/commands, /uploads и раздача файла вложения.
type fakeMAX struct {
	mu   sync.Mutex
	msgs []sent
	srv  *httptest.Server
}

func newFakeMAX(t *testing.T) *fakeMAX {
	f := &fakeMAX{}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/me":
			fmt.Fprint(w, `{"user_id":1,"username":"test_bot","name":"Тест","is_bot":true}`)
		case r.URL.Path == "/me/commands":
			fmt.Fprint(w, `{"commands":[]}`)
		case r.URL.Path == "/answers":
			fmt.Fprint(w, `{"success":true}`)
		case r.URL.Path == "/messages" && r.Method == http.MethodPost:
			var body model.NewMessageBody
			_ = json.NewDecoder(r.Body).Decode(&body)
			m := sent{Text: body.Text}
			for _, a := range body.Attachments {
				if a.Type == model.AttachFile {
					m.File = true
				}
				for _, row := range a.Payload.Buttons {
					for _, b := range row {
						m.Buttons = append(m.Buttons, *b)
					}
				}
			}
			f.mu.Lock()
			f.msgs = append(f.msgs, m)
			n := len(f.msgs)
			f.mu.Unlock()
			fmt.Fprintf(w, `{"message":{"body":{"mid":"mid.%d"}}}`, n)
		case r.URL.Path == "/uploads":
			fmt.Fprintf(w, `{"url":"%s/upload-target"}`, f.srv.URL)
		case r.URL.Path == "/upload-target":
			_, _ = io.Copy(io.Discard, r.Body)
			fmt.Fprint(w, `{"token":"file-token"}`)
		case r.URL.Path == "/act.pdf":
			_, _ = w.Write([]byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\nтестовый акт"))
		default:
			http.Error(w, `{"code":"not.found","message":"`+r.URL.Path+`"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMAX) all() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sent(nil), f.msgs...)
}

func (f *fakeMAX) since(n int) []sent { return f.all()[n:] }

type harness struct {
	t   *testing.T
	b   *Bot
	svc *app.Service
	max *fakeMAX
	uid int64
	seq int
}

func newHarness(t *testing.T) *harness {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL не задан — интеграционный тест пропущен")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("tb_%d", time.Now().UnixNano())
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
	f := newFakeMAX(t)
	c, err := maxbot.New(ctx, "test-token", f.srv.Client(), log, maxapi.WithBaseURL(f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	b := New(c, svc, log)
	svc.SetNotifier(b)
	return &harness{t: t, b: b, svc: svc, max: f, uid: 777001}
}

func (h *harness) text(s string) []sent {
	h.seq++
	n := len(h.max.all())
	h.b.Handle(context.Background(), model.Update{UpdateType: model.UpdateMessageCreated, Message: &model.MessageUpdate{
		Recipient: model.Recipient{ChatType: model.ChatTypeDialog, ChatID: 1}, Sender: model.Sender{UserID: h.uid, FirstName: "Мария"},
		Body: model.MessageBody{Mid: fmt.Sprintf("in.%d", h.seq), Text: s}}})
	return h.max.since(n)
}

func (h *harness) file(url, name string) []sent {
	h.seq++
	n := len(h.max.all())
	h.b.Handle(context.Background(), model.Update{UpdateType: model.UpdateMessageCreated, Message: &model.MessageUpdate{
		Recipient: model.Recipient{ChatType: model.ChatTypeDialog, ChatID: 1}, Sender: model.Sender{UserID: h.uid},
		Body: model.MessageBody{Mid: fmt.Sprintf("in.%d", h.seq), Attachments: []model.Attachment{{Type: model.AttachFile, FileName: name, Payload: model.Payload{URL: url}}}}}})
	return h.max.since(n)
}

func (h *harness) press(payload string) []sent {
	h.seq++
	n := len(h.max.all())
	h.b.Handle(context.Background(), model.Update{UpdateType: model.UpdateMessageCallback,
		Callback: &model.Callback{CallbackID: fmt.Sprintf("cb.%d", h.seq), Payload: payload, User: model.User{UserID: h.uid}}})
	return h.max.since(n)
}

// expect проверяет, что среди ответов есть текст с подстрокой, и возвращает это сообщение.
func (h *harness) expect(msgs []sent, substr string) sent {
	h.t.Helper()
	for _, m := range msgs {
		if strings.Contains(m.Text, substr) {
			return m
		}
	}
	var texts []string
	for _, m := range msgs {
		texts = append(texts, m.Text)
	}
	h.t.Fatalf("нет ответа с %q; ответы: %q", substr, texts)
	return sent{}
}

func btn(m sent, prefix string) string {
	for _, b := range m.Buttons {
		if strings.HasPrefix(b.Payload, prefix) {
			return b.Payload
		}
	}
	return ""
}

func TestBotScenario(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// 1–2. Старт, согласие, онбординг; фото во время онбординга не сбрасывает шаги.
	h.b.Handle(ctx, model.Update{UpdateType: model.UpdateBotStarted, Timestamp: 1, User: &model.User{UserID: h.uid, FirstName: "Мария"}})
	start := h.expect(h.max.all(), "Здравствуйте")
	if btn(start, "consent") == "" {
		t.Fatal("нет кнопки согласия")
	}
	onb := h.expect(h.press("consent"), "Шаг 1 из 8")
	if btn(onb, "demo_profile") == "" {
		t.Fatal("нет кнопки демо-профиля")
	}
	h.expect(h.file(h.max.srv.URL+"/act.pdf", "act.pdf"), "Сначала закончим профиль")
	menu := h.expect(h.press("demo_profile"), "Дом: г. Демоград")
	_ = menu

	// 3–4. Демо-акт, дата получения (ошибка ввода и повтор), способ, экземпляры, сводка сроков.
	ask := h.expect(h.press("act_demo"), "Когда вы получили акт?")
	rd := btn(ask, "rd:")
	actID := strings.Split(rd, ":")[1]
	h.expect(h.text("позавчера"), "Не понял дату")
	h.expect(h.press("rd:"+actID+":today"), "Как акт поступил?")
	h.expect(h.press("ch:"+actID+":in_person"), "Получили два экземпляра")
	sum := h.expect(h.press("cp:"+actID+":2"), "Ответить исполнителю до")
	for _, want := range []string{"Основание: порядок, утверждённый приказом", "Проверка сроков исполнителя", "демо-акт"} {
		if !strings.Contains(sum.Text, want) {
			t.Errorf("в сводке нет %q", want)
		}
	}
	hasApp := false
	for _, b := range sum.Buttons {
		hasApp = hasApp || (b.Type == model.ButtonOpenApp && b.Payload == "act_"+actID && b.WebApp == "test_bot")
	}
	if !hasApp || btn(sum, "inv:") == "" {
		t.Fatalf("в сводке нет кнопок «Открыть проверку» (open_app) и «Пригласить жильцов»: %+v", sum.Buttons)
	}

	// Профиль доступен из меню и при текущем акте; изменение можно отменить, прежний профиль остаётся.
	menuAct := h.expect(h.text("/menu"), "Акт № 17")
	if btn(menuAct, "profile") == "" || btn(menuAct, "adel:") == "" {
		t.Fatalf("в меню при акте нет «Профиль» или кнопок акта: %+v", menuAct.Buttons)
	}
	prof := h.expect(h.text("/profile"), "Профиль:")
	h.expect(h.press(btn(prof, "profile_edit")), "Изменим профиль")
	h.expect(h.press("menu"), "Дом: г. Демоград")
	if cmds := Commands(true); cmds[0].Name != "start" {
		t.Fatalf("первая команда меню MAX: %s", cmds[0].Name)
	}

	// 8. Приглашение жильцов — текстовая ссылка со startapp.
	h.expect(h.press("inv:"+actID), "startapp=inv_")

	// 11. Перемотка к 9-му дню → одно напоминание «завтра последний день».
	shift := h.press("demo:" + actID + ":9")
	h.expect(shift, "завтра")
	h.expect(shift, "Перемотано: сейчас 9-й день")

	// 12. Решение из мини-приложения — PDF приходит файлом с кнопкой отметки отправки.
	user, _ := h.svc.EnsureUser(ctx, h.uid, "", "")
	lines, _ := h.svc.Store().Q().Lines(ctx, actID)
	st, c := domain.ReviewNotDone, "снега не было"
	if _, err := h.svc.UpdateLine(ctx, user.ID, lines[5].ID, app.LineInput{ReviewStatus: &st, Comment: &c}); err != nil {
		t.Fatal(err)
	}
	n := len(h.max.all())
	res, err := h.svc.Decide(ctx, user.ID, actID, domain.DecisionRefuse, false)
	if err != nil || !res.Delivered {
		t.Fatalf("решение: %+v %v", res, err)
	}
	doc := h.expect(h.max.since(n), "Мотивированный отказ")
	if !doc.File || btn(doc, "disp:") == "" {
		t.Fatalf("документ без файла или кнопки отправки: %+v", doc)
	}

	// 13. Отметка отправки: способ → дата.
	h.expect(h.press("disp:"+actID), "Как вы направили")
	h.expect(h.press("dc:"+actID+":in_person"), "Когда отправили?")
	sentMsg := h.expect(h.press("dd:"+actID+":today"), "Отмечено: документ направлен лично")
	next := btn(sentMsg, "next:")
	if next == "" {
		t.Fatal("нет кнопки «Получил новый акт»")
	}
	// Устаревшая кнопка даты после смены состояния — не ошибка, а повтор вопроса о способе.
	h.expect(h.press("dd:"+actID+":today"), "Отмечать отправку уже не нужно: отказ направлен, ожидается новый акт.")

	// 14. Новый акт после отказа: файл скачивается с «MAX», акт связан с прежним.
	h.expect(h.press(next), "Пришлите новый акт")
	got := h.file(h.max.srv.URL+"/act.pdf", "act-2.pdf")
	h.expect(got, "Файл принят")
	ask2 := h.expect(got, "Когда вы получили акт?")
	newID := strings.Split(btn(ask2, "rd:"), ":")[1]
	na, _ := h.svc.Store().Q().ActByID(ctx, newID)
	if na.ParentActID == nil || *na.ParentActID != actID || na.SourceFileID == nil {
		t.Fatalf("новый акт не связан с прежним или без файла: %+v", na)
	}

	// Удаление акта кнопкой: подтверждение, удаление, прежний акт цепочки остаётся.
	del := btn(h.expect(h.press("open:"+newID), "Акт № без номера"), "adel:")
	if del == "" {
		t.Fatal("нет кнопки «Удалить акт»")
	}
	h.expect(h.press(del), "Это необратимо")
	h.expect(h.press("adelc:"+newID), "Акт № без номера удалён")
	if _, err := h.svc.Store().Q().ActByID(ctx, newID); err != store.ErrNotFound {
		t.Fatalf("акт не удалён: %v", err)
	}
	if _, err := h.svc.Store().Q().ActByID(ctx, actID); err != nil {
		t.Fatalf("прежний акт пострадал: %v", err)
	}
	h.expect(h.press("open:"+newID), "Акт не найден")

	// Повторная доставка того же события ничего не делает.
	dup := model.Update{UpdateType: model.UpdateMessageCreated, Message: &model.MessageUpdate{
		Recipient: model.Recipient{ChatType: model.ChatTypeDialog, ChatID: 1}, Sender: model.Sender{UserID: h.uid},
		Body: model.MessageBody{Mid: "dup.1", Text: "/help"}}}
	h.b.Handle(ctx, dup)
	before := len(h.max.all())
	h.b.Handle(ctx, dup)
	if len(h.max.all()) != before {
		t.Fatal("повторная доставка обработана второй раз")
	}

	// 16. Непонятный текст — подсказка, а не тишина; /delete удаляет данные.
	h.press("menu")
	h.expect(h.text("привет"), "Я понимаю кнопки и файлы")
	h.expect(h.text("/delete"), "Удалить все ваши данные")
	h.expect(h.press("delete_confirm"), "Ваши данные удалены")
	if _, err := h.svc.Store().Q().ActByID(ctx, actID); err != store.ErrNotFound {
		t.Fatalf("акт не удалён: %v", err)
	}
}
