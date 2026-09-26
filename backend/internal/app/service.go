// Package app — сценарии продукта поверх domain и store. Его вызывают бот, REST API и планировщик,
// поэтому поведение одинаково, откуда бы ни пришло действие.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"priemka/internal/civil"
	"priemka/internal/domain"
	"priemka/internal/files"
	"priemka/internal/pdf"
	"priemka/internal/rules"
	"priemka/internal/store"
)

type Config struct {
	DemoMode    bool
	BotUsername string
	InviteTTL   time.Duration
	ConfigDir   string
	MaxActFile  int64
	MaxPhoto    int64
}

// Notifier — исходящие сообщения пользователю. Реализует пакет bot.
type Notifier interface {
	Notify(ctx context.Context, chairmanMaxID int64, actID, text string) error
	SendDocument(ctx context.Context, chairmanMaxID int64, act store.Act, doc store.Document, path, name string) (string, error)
}

type Service struct {
	cfg    Config
	st     *store.Store
	rules  *rules.Set
	texts  *rules.Texts
	files  *files.Storage
	pdf    *pdf.Renderer
	notify Notifier
	demo   *DemoData
	now    func() time.Time
	log    *slog.Logger
}

func New(cfg Config, st *store.Store, rs *rules.Set, tx *rules.Texts, fs *files.Storage, pr *pdf.Renderer, log *slog.Logger) (*Service, error) {
	demo, err := LoadDemo(cfg.ConfigDir)
	if err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, st: st, rules: rs, texts: tx, files: fs, pdf: pr, demo: demo, now: time.Now, log: log}, nil
}

func (s *Service) SetNotifier(n Notifier) { s.notify = n }
func (s *Service) Rules() *rules.Set      { return s.rules }
func (s *Service) Texts() *rules.Texts    { return s.texts }
func (s *Service) Files() *files.Storage  { return s.files }
func (s *Service) Store() *store.Store    { return s.st }
func (s *Service) Config() Config         { return s.cfg }

// Error — ошибка сценария с HTTP-статусом и понятным пользователю текстом.
type Error struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return e.Message }

func errf(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

var (
	ErrActNotFound = errf(http.StatusNotFound, "ACT_NOT_FOUND", "Акт не найден.")
	ErrForbidden   = errf(http.StatusForbidden, "FORBIDDEN", "Нет доступа к этому акту.")
	ErrNoProfile   = errf(http.StatusConflict, "PROFILE_REQUIRED", "Сначала заполните профиль председателя в боте (/start).")
)

// AsError приводит ошибку к *Error; неизвестные ошибки — внутренние.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, store.ErrNotFound) {
		return errf(http.StatusNotFound, "NOT_FOUND", "Не найдено.")
	}
	// Нарушение ограничений БД — данные не прошли проверку, это не внутренняя ошибка.
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23514", "22003", "22007", "22008", "22P02":
			return errf(http.StatusUnprocessableEntity, "VALIDATION", "Некорректное значение поля. Проверьте введённые данные.")
		case "23505":
			return errf(http.StatusConflict, "CONFLICT", "Такая запись уже есть. Обновите данные и повторите.")
		}
	}
	return errf(http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка. Повторите действие через минуту.")
}

// RealToday — сегодняшняя дата в часовом поясе правил.
func (s *Service) RealToday() civil.Date { return civil.Of(s.now(), s.rules.Location) }

// ActToday — «сегодня» для акта: у демо-актов время может быть сдвинуто.
func (s *Service) ActToday(a store.Act) civil.Date { return s.RealToday().AddDays(a.DemoShiftDays) }

// ChairmanAct возвращает акт, если пользователь — председатель его дома.
func (s *Service) ChairmanAct(ctx context.Context, userID, actID string) (store.Act, store.House, error) {
	q := s.st.Q()
	a, err := q.ActByID(ctx, actID)
	if errors.Is(err, store.ErrNotFound) {
		return a, store.House{}, ErrActNotFound
	} else if err != nil {
		return a, store.House{}, err
	}
	h, err := q.HouseByID(ctx, a.HouseID)
	if err != nil {
		return a, h, err
	}
	if h.ChairmanUserID != userID {
		return a, h, ErrForbidden
	}
	return a, h, nil
}

// EnsureUser создаёт или обновляет пользователя по данным MAX.
func (s *Service) EnsureUser(ctx context.Context, maxID int64, first, last string) (store.User, error) {
	return s.st.Q().UpsertUser(ctx, maxID, first, last)
}

func (s *Service) chairmanMaxID(ctx context.Context, h store.House) int64 {
	u, err := s.st.Q().UserByID(ctx, h.ChairmanUserID)
	if err != nil {
		return 0
	}
	return u.MaxUserID
}

// moveStatus переводит акт по статусной машине домена; недопустимый или устаревший переход — 409.
func moveStatus(ctx context.Context, q *store.Q, actID string, from, to domain.Status, dec domain.Decision) error {
	if !from.CanTransition(to) {
		return errf(http.StatusConflict, "BAD_TRANSITION", "Действие недоступно: %s.", from.Title())
	}
	ok, err := q.SetActStatus(ctx, actID, from, to, dec)
	if err != nil {
		return err
	}
	if !ok {
		return errf(http.StatusConflict, "STATUS_CHANGED", "Статус акта только что изменился. Обновите карточку и повторите.")
	}
	return nil
}

func (s *Service) demoSuffix(a store.Act) string {
	if a.IsDemo && a.DemoShiftDays != 0 {
		return fmt.Sprintf("\n\n🧪 ДЕМО: время акта сдвинуто на %+d дн.", a.DemoShiftDays)
	}
	return ""
}
