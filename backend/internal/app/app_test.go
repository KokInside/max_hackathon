package app

// Интеграционные тесты сервиса на настоящей PostgreSQL.
// Запуск: TEST_DATABASE_URL=postgres://user:pass@host:port/db go test ./internal/app
// Каждый тест работает в собственной схеме и удаляет её после себя.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"priemka/internal/civil"
	"priemka/internal/domain"
	"priemka/internal/files"
	"priemka/internal/pdf"
	"priemka/internal/rules"
	"priemka/internal/store"
)

type recorder struct {
	texts []string
	docs  int
	fail  error
}

func (r *recorder) Notify(_ context.Context, _ int64, _ string, text string) error {
	if r.fail != nil {
		return r.fail
	}
	r.texts = append(r.texts, text)
	return nil
}

func (r *recorder) SendDocument(context.Context, int64, store.Act, store.Document, string, string) (string, error) {
	r.docs++
	return "mid", nil
}

type env struct {
	svc   *Service
	notif *recorder
	dir   string
	now   time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL не задан — интеграционный тест пропущен")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("t_%d", time.Now().UnixNano())
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
	confDir := filepath.Join("..", "..", "..", "config")
	rs, err := rules.Load(confDir)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := rules.LoadTexts(confDir)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := pdf.New(filepath.Join("..", "..", "..", "assets", "fonts"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fs, err := files.New(dir, []byte("test-secret-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{DemoMode: true, BotUsername: "test_bot", InviteTTL: DefaultInviteTTL(), ConfigDir: confDir, MaxActFile: 20 << 20, MaxPhoto: 10 << 20},
		st, rs, tx, fs, pr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{svc: svc, notif: &recorder{}, dir: dir, now: time.Date(2026, 10, 1, 12, 0, 0, 0, rs.Location)}
	svc.now = func() time.Time { return e.now }
	svc.SetNotifier(e.notif)
	return e
}

// demoAct — пользователь с демо-профилем и демо-актом, полученным «сегодня».
func (e *env) demoAct(t *testing.T, maxID int64) (store.User, store.Act) {
	t.Helper()
	ctx := context.Background()
	u, err := e.svc.EnsureUser(ctx, maxID, "Тест", "Тестов")
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.svc.CreateDemoAct(ctx, u.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	two, yes := 2, true
	if a, err = e.svc.SetReceived(ctx, u.ID, a.ID, ReceivedInput{ReceivedOn: e.svc.RealToday(), Channel: "in_person", CopiesReceived: &two, ExecutorSigned: &yes}); err != nil {
		t.Fatal(err)
	}
	return u, a
}

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\xff\xff?\x00\x05\xfe\x02\xfe\xa7\x35\x81\x84\x00\x00\x00\x00IEND\xaeB`\x82")

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// /delete удаляет не только строки БД, но и записи files, и сами файлы на диске.
func TestDeleteUserDataRemovesFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, a := e.demoAct(t, 1001)
	lines, err := e.svc.Store().Q().Lines(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	st := domain.ReviewNotDone
	comment := "не выполнено"
	if _, err := e.svc.UpdateLine(ctx, u.ID, lines[0].ID, LineInput{ReviewStatus: &st, Comment: &comment}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddEvidence(ctx, u.ID, lines[0].ID, bytes.NewReader(pngBytes), "p.png", "фото"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Decide(ctx, u.ID, a.ID, domain.DecisionRefuse, false); err != nil {
		t.Fatal(err)
	}
	if n := countFiles(t, e.dir); n != 2 {
		t.Fatalf("до удаления файлов на диске: %d, ожидалось 2 (фото и PDF)", n)
	}

	// Чужие данные не должны пострадать.
	_, other := e.demoAct(t, 1002)

	if err := e.svc.DeleteUserData(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if n := countFiles(t, e.dir); n != 0 {
		t.Fatalf("после удаления на диске осталось файлов: %d", n)
	}
	if _, err := e.svc.Store().Q().UserByID(ctx, u.ID); err != store.ErrNotFound {
		t.Fatalf("пользователь не удалён: %v", err)
	}
	if _, err := e.svc.Store().Q().ActByID(ctx, a.ID); err != store.ErrNotFound {
		t.Fatalf("акт не удалён: %v", err)
	}
	if _, err := e.svc.Store().Q().ActByID(ctx, other.ID); err != nil {
		t.Fatalf("акт другого пользователя пострадал: %v", err)
	}
}

// Напоминание дня срока приходит не в полночь, а в час из конфига; у перемотанных демо-актов — сразу.
func TestReminderHour(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	hour := *e.svc.Rules().Params.ReminderHour
	received := civil.New(2026, 10, 1)
	e.now = time.Date(2026, 10, 1, 12, 0, 0, 0, e.svc.Rules().Location)
	u, a := e.demoAct(t, 2001)
	_ = u
	if !a.ReceivedOn.Equal(received) {
		t.Fatalf("дата получения %s", a.ReceivedOn)
	}

	// 7-й день, за час до времени отправки — напоминания ещё нет.
	e.now = time.Date(2026, 10, 8, hour-1, 30, 0, 0, e.svc.Rules().Location)
	e.svc.Tick(ctx)
	if len(e.notif.texts) != 0 {
		t.Fatalf("напоминание пришло раньше %d:00: %q", hour, e.notif.texts)
	}
	// Наступил час отправки — одно напоминание.
	e.now = time.Date(2026, 10, 8, hour, 1, 0, 0, e.svc.Rules().Location)
	e.svc.Tick(ctx)
	e.svc.Tick(ctx)
	if len(e.notif.texts) != 1 {
		t.Fatalf("ожидалось одно напоминание, получено %d", len(e.notif.texts))
	}

	// Демо-перемотка к 9-му дню ночью — напоминание приходит сразу.
	_, a2 := e.demoAct(t, 2002)
	e.now = time.Date(2026, 10, 8, 2, 0, 0, 0, e.svc.Rules().Location)
	before := len(e.notif.texts)
	if _, err := e.svc.DemoShift(ctx, a2HouseChairman(t, e, a2), a2.ID, 0, 9); err != nil {
		t.Fatal(err)
	}
	if len(e.notif.texts) <= before {
		t.Fatal("после перемотки напоминание не пришло")
	}
}

func a2HouseChairman(t *testing.T, e *env, a store.Act) string {
	t.Helper()
	h, err := e.svc.Store().Q().HouseByID(context.Background(), a.HouseID)
	if err != nil {
		t.Fatal(err)
	}
	return h.ChairmanUserID
}

// При перемотке сразу к 9-му дню приходит одно актуальное напоминание, а не пачка с устаревшими числами.
func TestStaleRemindersSkipped(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, a := e.demoAct(t, 3001)
	if _, err := e.svc.DemoShift(ctx, u.ID, a.ID, 0, 9); err != nil {
		t.Fatal(err)
	}
	if len(e.notif.texts) != 1 || !strings.Contains(e.notif.texts[0], "завтра") {
		t.Fatalf("ожидалось одно напоминание «завтра последний день», получено: %q", e.notif.texts)
	}
	if _, err := e.svc.DemoShift(ctx, u.ID, a.ID, 0, 31); err != nil {
		t.Fatal(err)
	}
	last := e.notif.texts[len(e.notif.texts)-1]
	if len(e.notif.texts) != 2 || !strings.Contains(last, "считается оформленным") {
		t.Fatalf("после перемотки к 31-му дню ожидалось сообщение о молчаливой приёмке, получено: %q", e.notif.texts)
	}
}

// Параллельные загрузки доказательств к одному акту получают разные номера Д-N без ошибок.
func TestConcurrentEvidence(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, a := e.demoAct(t, 4001)
	lines, _ := e.svc.Store().Q().Lines(ctx, a.ID)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.svc.AddEvidence(ctx, u.ID, lines[i%len(lines)].ID, bytes.NewReader(pngBytes), "p.png", "")
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	evs, _ := e.svc.Store().Q().EvidenceForAct(ctx, a.ID)
	seen := map[string]bool{}
	for _, ev := range evs {
		if seen[ev.Code] {
			t.Fatalf("повтор номера %s", ev.Code)
		}
		seen[ev.Code] = true
	}
	if len(evs) != 8 {
		t.Fatalf("доказательств %d", len(evs))
	}
}

// Удаление доказательства и строки удаляет и файлы с диска.
func TestDeleteEvidenceAndLineRemoveFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, a := e.demoAct(t, 4002)
	lines, _ := e.svc.Store().Q().Lines(ctx, a.ID)
	ev, err := e.svc.AddEvidence(ctx, u.ID, lines[0].ID, bytes.NewReader(pngBytes), "p.png", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddEvidence(ctx, u.ID, lines[1].ID, bytes.NewReader(pngBytes), "p.png", ""); err != nil {
		t.Fatal(err)
	}
	if n := countFiles(t, e.dir); n != 2 {
		t.Fatalf("файлов %d", n)
	}
	if err := e.svc.DeleteEvidence(ctx, u.ID, ev.ID); err != nil {
		t.Fatal(err)
	}
	if n := countFiles(t, e.dir); n != 1 {
		t.Fatalf("после удаления доказательства файлов %d", n)
	}
	if err := e.svc.DeleteLine(ctx, u.ID, lines[1].ID); err != nil {
		t.Fatal(err)
	}
	if n := countFiles(t, e.dir); n != 0 {
		t.Fatalf("после удаления строки файлов %d", n)
	}
}

// Неудачная отправка напоминания повторяется не сразу, а после паузы.
func TestReminderBackoff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, a := e.demoAct(t, 4003)
	e.notif.fail = errors.New("MAX недоступен")
	if _, err := e.svc.DemoShift(ctx, u.ID, a.ID, 0, 9); err != nil {
		t.Fatal(err)
	}
	e.svc.Tick(ctx)
	e.svc.Tick(ctx)
	attempts := func() int {
		var n int
		if err := e.svc.Store().RawQueryRow(ctx, `SELECT max(attempts) FROM reminders WHERE act_id = $1`, a.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := attempts(); n != 1 {
		t.Fatalf("после сбоя попыток %d, ожидалась 1 (повтор только после паузы)", n)
	}
	// Пауза прошла — повтор успешен.
	if err := e.svc.Store().RawExec(ctx, `UPDATE reminders SET next_attempt_at = now() - interval '1 second' WHERE act_id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	e.notif.fail = nil
	e.svc.Tick(ctx)
	if len(e.notif.texts) != 1 {
		t.Fatalf("после паузы напоминание не отправлено: %q", e.notif.texts)
	}
}

// Параллельные правки разных полей одной строки не затирают друг друга.
func TestConcurrentLineUpdates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, a := e.demoAct(t, 5001)
	lines, _ := e.svc.Store().Q().Lines(ctx, a.ID)
	id := lines[0].ID
	for i := 0; i < 5; i++ {
		st := domain.ReviewNotDone
		comment := fmt.Sprintf("комментарий %d", i)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = e.svc.UpdateLine(ctx, u.ID, id, LineInput{ReviewStatus: &st}) }()
		go func() { defer wg.Done(); _, _ = e.svc.UpdateLine(ctx, u.ID, id, LineInput{Comment: &comment}) }()
		wg.Wait()
		l, _ := e.svc.Store().Q().LineByID(ctx, id)
		if l.ReviewStatus != domain.ReviewNotDone || l.Comment != comment {
			t.Fatalf("потеряна правка: статус %s, комментарий %q", l.ReviewStatus, l.Comment)
		}
		unchecked := domain.ReviewUnchecked
		_, _ = e.svc.UpdateLine(ctx, u.ID, id, LineInput{ReviewStatus: &unchecked})
	}
}

func TestLineAndHeaderValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, a := e.demoAct(t, 5002)
	lines, _ := e.svc.Store().Q().Lines(ctx, a.ID)
	too := "999999"
	if _, err := e.svc.UpdateLine(ctx, u.ID, lines[0].ID, LineInput{DisputedAmount: &too}); AsError(err).Code != "VALIDATION" {
		t.Fatalf("оспариваемая сумма больше цены принята: %v", err)
	}
	// Решение → правка без изменений не делает документ устаревшим, правка с изменением — делает.
	st, c := domain.ReviewNotDone, "не выполнено"
	if _, err := e.svc.UpdateLine(ctx, u.ID, lines[0].ID, LineInput{ReviewStatus: &st, Comment: &c}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Decide(ctx, u.ID, a.ID, domain.DecisionRefuse, false); err != nil {
		t.Fatal(err)
	}
	same := a.Number
	if err := e.svc.UpdateHeader(ctx, u.ID, a.ID, HeaderPatch{Number: &same}); err != nil {
		t.Fatal(err)
	}
	if cur, _ := e.svc.Store().Q().ActByID(ctx, a.ID); cur.Status != domain.StatusDecided {
		t.Fatalf("правка без изменений сбросила решение: %s", cur.Status)
	}
	other := "18"
	if err := e.svc.UpdateHeader(ctx, u.ID, a.ID, HeaderPatch{Number: &other}); err != nil {
		t.Fatal(err)
	}
	if cur, _ := e.svc.Store().Q().ActByID(ctx, a.ID); cur.Status != domain.StatusInReview {
		t.Fatalf("после изменения документ должен устареть: %s", cur.Status)
	}
	long := strings.Repeat("я", 501)
	if err := e.svc.UpdateHeader(ctx, u.ID, a.ID, HeaderPatch{ExecutorName: &long}); AsError(err).Code != "VALIDATION" {
		t.Fatalf("слишком длинное поле принято: %v", err)
	}
	// Чужой пользователь не может править акт.
	stranger, _ := e.svc.EnsureUser(ctx, 5099, "Чужой", "")
	if err := e.svc.UpdateHeader(ctx, stranger.ID, a.ID, HeaderPatch{Number: &other}); AsError(err).Status != 403 {
		t.Fatalf("чужая правка: %v", err)
	}
}

// Переход с демо-профиля на настоящий снимает флаг демо у дома: новые акты — не демо.
func TestDemoFlagFollowsProfile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, _ := e.svc.EnsureUser(ctx, 5003, "Мария", "")
	if _, err := e.svc.FillDemoProfile(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.SaveProfile(ctx, u.ID, ProfileInput{FullName: "Иванова Мария Петровна", AuthorityType: "oss_decision",
		City: "Казань", Address: "г. Казань, ул. Баумана, д. 1", EntrancesCount: 2, ExecutorName: "ООО «Настоящая УК»"})
	if err != nil {
		t.Fatal(err)
	}
	if b.House.IsDemo {
		t.Fatal("дом остался демо после заполнения настоящего профиля")
	}
	a, err := e.svc.CreateAct(ctx, u.ID, nil)
	if err != nil || a.IsDemo {
		t.Fatalf("настоящий акт помечен демо: %v %v", a.IsDemo, err)
	}
}

// Если акт не удалось создать, файл, присланный ботом, не остаётся на диске.
func TestReceiveActFileCleanup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, _ := e.svc.EnsureUser(ctx, 5004, "Без", "профиля")
	if _, _, err := e.svc.ReceiveActFile(ctx, u.ID, bytes.NewReader(pngBytes), "act.png", ""); AsError(err).Code != "PROFILE_REQUIRED" {
		t.Fatalf("ожидалась PROFILE_REQUIRED: %v", err)
	}
	if n := countFiles(t, e.dir); n != 0 {
		t.Fatalf("на диске остался файл: %d", n)
	}
}

func pdfText(t *testing.T, path string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("нет pdftotext")
	}
	out, err := exec.Command("pdftotext", "-layout", path, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	info, _ := exec.Command("pdfinfo", path).Output()
	pages := 0
	for _, l := range strings.Split(string(info), "\n") {
		if strings.HasPrefix(l, "Pages:") {
			fmt.Sscan(strings.TrimSpace(strings.TrimPrefix(l, "Pages:")), &pages)
		}
	}
	return string(out), pages
}

// Отказ содержит всё из PLAN §6: колонку «ед.», ответы жильцов с «не знают», нарушения с основанием,
// доказательства с ролью, длинное возражение полностью; документ укладывается в разумное число страниц.
func TestRefusalDocumentContent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, a := e.demoAct(t, 6001)
	one := 1
	if err := e.svc.UpdateHeader(ctx, u.ID, a.ID, HeaderPatch{CopiesReceived: &one}); err != nil {
		t.Fatal(err)
	}
	lines, _ := e.svc.Store().Q().Lines(ctx, a.ID)
	st := domain.ReviewNotDone
	long := strings.Repeat("Уборка не проводилась, подъезд грязный, жильцы подтверждают. ", 32)[:1990]
	if _, err := e.svc.UpdateLine(ctx, u.ID, lines[0].ID, LineInput{ReviewStatus: &st, Comment: &long}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddEvidence(ctx, u.ID, lines[0].ID, bytes.NewReader(pngBytes), "p.png", "лестница, 2 подъезд"); err != nil {
		t.Fatal(err)
	}
	inv, _ := e.svc.CreateInvite(ctx, u.ID, a.ID)
	res, _ := e.svc.EnsureUser(ctx, 6002, "Жилец", "")
	if _, err := e.svc.SubmitVotes(ctx, res.ID, inv.Token, VotesInput{Consent: true, EntranceNo: 1, Votes: []store.Vote{{LineID: lines[0].ID, Answer: "no"}}}); err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.Decide(ctx, u.ID, a.ID, domain.DecisionRefuse, false)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := e.svc.Store().Q().FileByID(ctx, r.Document.FileID)
	text, pages := pdfText(t, e.svc.Files().Path(f.StorageKey))
	if os.Getenv("DUMP_PDF") != "" {
		t.Log(text)
	}
	flat := strings.Join(strings.Fields(text), " ")
	for _, want := range []string{"Мотивированный отказ", "Ед. изм.", "не знают", "Основание: порядок, утверждённый приказом",
		"предоставил председатель совета", "Возражения полностью", "ДЕМО", "Подтвердить пункты 3 и 4"} {
		if !strings.Contains(flat, want) {
			t.Errorf("в отказе нет %q", want)
		}
	}
	if !strings.Contains(strings.ReplaceAll(flat, " ", ""), "0/1/0") {
		t.Error("в колонке жильцов нет «0 / 1 / 0»")
	}
	if !strings.Contains(flat, strings.Join(strings.Fields(long[len(long)-200:]), " ")) {
		t.Error("длинное возражение не выведено полностью")
	}
	if pages < 1 || pages > 4 {
		t.Errorf("страниц в отказе: %d", pages)
	}
}

func TestCoverLetterDocument(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, a := e.demoAct(t, 6003)
	r, err := e.svc.Decide(ctx, u.ID, a.ID, domain.DecisionSign, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Document.Kind != "cover_letter" || !strings.HasPrefix(docFileName("cover_letter", a, 1), "cover_letter_act-17_") {
		t.Fatalf("%+v", r.Document)
	}
	f, _ := e.svc.Store().Q().FileByID(ctx, r.Document.FileID)
	text, pages := pdfText(t, e.svc.Files().Path(f.StorageKey))
	flat := strings.Join(strings.Fields(text), " ")
	for _, want := range []string{"Сопроводительное письмо", "Направляем подписанный", "69 350,00", "г. Демоград", "Приложение: акт № 17"} {
		if !strings.Contains(flat, want) {
			t.Errorf("в письме нет %q", want)
		}
	}
	if pages != 1 {
		t.Errorf("страниц в письме: %d", pages)
	}
	if placeLine("") != "" || placeLine("г. Казань") != "г. Казань" || placeLine("Казань") != "г. Казань" {
		t.Error("placeLine")
	}
}

type permErr struct{}

func (permErr) Error() string   { return "dialog.not.found" }
func (permErr) Permanent() bool { return true }

// Постоянная ошибка отправки (чат не найден) не повторяется.
func TestReminderPermanentError(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, a := e.demoAct(t, 4004)
	e.notif.fail = permErr{}
	if _, err := e.svc.DemoShift(ctx, u.ID, a.ID, 0, 9); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.svc.Store().RawQueryRow(ctx, `SELECT max(attempts) FROM reminders WHERE act_id = $1 AND sent_at IS NULL`, a.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != store.MaxReminderAttempts {
		t.Fatalf("после постоянной ошибки попыток %d, ожидалось %d (без повторов)", n, store.MaxReminderAttempts)
	}
}

// Тестовая учётка: когда демо-акт уходит в финальный статус, планировщик создаёт свежий — проверки DATA-API.yaml
// остаются повторяемыми весь период проверки.
func TestTestAccountKeepsWorkableAct(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ch, _, err := e.svc.EnsureTestAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	acts, err := e.svc.ActsOfUser(ctx, ch)
	if err != nil || len(acts) != 1 || acts[0].Status != domain.StatusInReview {
		t.Fatalf("после создания: %v %+v", err, acts)
	}
	e.svc.Tick(ctx)
	if acts, _ = e.svc.ActsOfUser(ctx, ch); len(acts) != 1 {
		t.Fatalf("лишний акт при рабочем: %d", len(acts))
	}

	e.now = e.now.AddDate(0, 0, 40) // прошли 30 дней — прежний акт «принят молчанием»
	e.svc.Tick(ctx)
	acts, err = e.svc.ActsOfUser(ctx, ch)
	if err != nil || len(acts) != 2 {
		t.Fatalf("после 40 дней: %v, актов %d", err, len(acts))
	}
	if acts[0].Status != domain.StatusInReview || acts[1].Status != domain.StatusDeemedAccepted {
		t.Fatalf("статусы: %s, %s", acts[0].Status, acts[1].Status)
	}
	if acts[0].ReceivedOn != e.svc.RealToday() {
		t.Fatalf("новый акт получен %v, ожидалось %s", acts[0].ReceivedOn, e.svc.RealToday())
	}
	e.svc.Tick(ctx)
	if acts, _ = e.svc.ActsOfUser(ctx, ch); len(acts) != 2 {
		t.Fatalf("повторный тик создал акт: %d", len(acts))
	}
}
