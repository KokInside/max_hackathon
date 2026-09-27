package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"priemka/internal/domain"
	"priemka/internal/store"
)

var tickMu, testActMu sync.Mutex

// isPermanent — ошибка отправки, которую повтор не исправит (пользователь удалил чат с ботом и т. п.).
func isPermanent(err error) bool {
	var p interface{ Permanent() bool }
	return errors.As(err, &p) && p.Permanent()
}

// RunScheduler раз в минуту переводит просроченные акты в «принят молчанием» и рассылает напоминания.
func (s *Service) RunScheduler(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick — один проход планировщика. Идемпотентен: повторный вызов ничего не дублирует.
func (s *Service) Tick(ctx context.Context) {
	s.tickLocked(ctx)
	// Вне tickMu: ensureTestAct через SetReceived сама вызывает Tick.
	if id := s.testChairman.Load(); id != nil {
		if err := s.ensureTestAct(ctx, *id); err != nil {
			s.log.Error("планировщик: тестовая учётка", "err", err)
		}
	}
}

func (s *Service) tickLocked(ctx context.Context) {
	tickMu.Lock()
	defer tickMu.Unlock()
	if err := s.advanceStatuses(ctx); err != nil {
		s.log.Error("планировщик: статусы", "err", err)
	}
	if err := s.sendReminders(ctx); err != nil {
		s.log.Error("планировщик: напоминания", "err", err)
	}
}

func (s *Service) advanceStatuses(ctx context.Context) error {
	acts, err := s.st.Q().ActsToAdvance(ctx)
	if err != nil {
		return err
	}
	for _, a := range acts {
		if s.rules.Policy.Timing(a.ReceivedOn, s.ActToday(a)).Stage != domain.StageSilentAccepted {
			continue
		}
		err := s.st.Tx(ctx, func(q *store.Q) error {
			cur, err := q.ActForUpdate(ctx, a.ID)
			if err != nil || (cur.Status != domain.StatusInReview && cur.Status != domain.StatusDecided) {
				return err
			}
			if err := moveStatus(ctx, q, a.ID, cur.Status, domain.StatusDeemedAccepted, cur.Decision); err != nil {
				return err
			}
			// Промежуточные напоминания больше не нужны; сообщение о молчаливой приёмке — нужно.
			if err := q.ReplaceReminders(ctx, a.ID, []store.Reminder{{Kind: "d31", DueOn: s.rules.Policy.DateOfDay(a.ReceivedOn, s.rules.Policy.SilentDays+1), DueHour: *s.rules.Params.ReminderHour}}); err != nil {
				return err
			}
			return q.AddEvent(ctx, a.ID, "deemed_accepted", map[string]any{"silent_on": s.rules.Policy.ComputeDeadlines(a.ReceivedOn).SilentOn}, nil)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

type reminderData struct {
	Act                            store.Act
	ResponseOn, SilentOn, Received string
	DaysLeftResponse               int
	DaysLeftSilent                 int
	ResponseDays, SilentDays       int
	CountingNextDay                bool
	Cite, Demo                     string
}

func (s *Service) reminderData(a store.Act) reminderData {
	d := s.rules.Policy.ComputeDeadlines(a.ReceivedOn)
	t := s.rules.Policy.Timing(a.ReceivedOn, s.ActToday(a))
	return reminderData{Act: a, ResponseOn: d.ResponseOn.Russian(), SilentOn: d.SilentOn.Russian(), Received: a.ReceivedOn.Russian(),
		DaysLeftResponse: t.DaysLeftResponse, DaysLeftSilent: t.DaysLeftSilent,
		ResponseDays: s.rules.Policy.ResponseDays, SilentDays: s.rules.Policy.SilentDays, CountingNextDay: s.rules.Policy.CountingNextDay,
		Cite: s.rules.Cite(domain.RuleSilentDays), Demo: s.demoSuffix(a)}
}

// DeadlinesText — сводка сроков для бота.
func (s *Service) DeadlinesText(a store.Act) (string, error) {
	return s.texts.Msg("deadlines_summary", s.reminderData(a))
}

func (s *Service) templateFor(kind string) string {
	for _, r := range s.rules.Reminders {
		if r.Kind == kind {
			return r.Template
		}
	}
	return ""
}

func (s *Service) sendReminders(ctx context.Context) error {
	if s.notify == nil {
		return nil
	}
	for {
		var sent int
		err := s.st.Tx(ctx, func(q *store.Q) error {
			due, err := q.ClaimDueReminders(ctx, s.RealToday(), s.now().In(s.rules.Location).Hour(), 20)
			if err != nil {
				return err
			}
			// Если наступило сразу несколько напоминаний одного акта (перемотка в демо, простой сервиса),
			// отправляем только последнее: у ранних устарел текст («осталось 3 дня»).
			latest := map[string]store.Reminder{}
			for _, r := range due {
				if l, ok := latest[r.ActID]; !ok || r.DueOn.After(l.DueOn) {
					latest[r.ActID] = r
				}
			}
			for _, r := range due {
				sent++
				if latest[r.ActID].ID != r.ID {
					if err := q.MarkReminder(ctx, r.ID, nil, false); err != nil {
						return err
					}
					continue
				}
				a, err := q.ActByID(ctx, r.ActID)
				if err != nil {
					return err
				}
				// Для закрытого молчанием акта шлём только итоговое сообщение.
				if a.Status == domain.StatusDeemedAccepted && r.Kind != "d31" {
					if err := q.MarkReminder(ctx, r.ID, nil, false); err != nil {
						return err
					}
					continue
				}
				h, err := q.HouseByID(ctx, a.HouseID)
				if err != nil {
					return err
				}
				text, err := s.texts.Msg(s.templateFor(r.Kind), s.reminderData(a))
				if err == nil {
					err = s.notify.Notify(ctx, s.chairmanMaxID(ctx, h), a.ID, text)
				}
				if err != nil {
					s.log.Warn("напоминание не отправлено", "act", a.ID, "kind", r.Kind, "err", err)
				}
				if err := q.MarkReminder(ctx, r.ID, err, isPermanent(err)); err != nil {
					return err
				}
				if err == nil {
					_ = q.AddEvent(ctx, a.ID, "reminder_sent", map[string]any{"kind": r.Kind}, nil)
				}
			}
			return nil
		})
		if err != nil || sent < 20 {
			return err
		}
	}
}

// DemoShift сдвигает «сегодня» демо-акта. toDay > 0 — перейти к N-му дню срока; иначе сдвинуть на days.
func (s *Service) DemoShift(ctx context.Context, userID, actID string, days, toDay int) (store.Act, error) {
	a, _, err := s.ChairmanAct(ctx, userID, actID)
	if err != nil {
		return a, err
	}
	if !s.cfg.DemoMode || !a.IsDemo {
		return a, errf(http.StatusForbidden, "DEMO_ONLY", "Сдвиг времени доступен только для демо-актов в демо-режиме.")
	}
	if a.ReceivedOn.IsZero() {
		return a, errf(http.StatusConflict, "RECEIVED_DATE_REQUIRED", "Сначала укажите дату получения акта.")
	}
	shift := a.DemoShiftDays + days
	if toDay > 0 {
		shift = s.rules.Policy.DateOfDay(a.ReceivedOn, toDay).DaysSince(s.RealToday())
	}
	if shift < a.DemoShiftDays {
		return a, errf(http.StatusConflict, "DEMO_BACKWARDS", "Время можно только перематывать вперёд: сейчас уже %d-й день.", s.rules.Policy.DayNumber(a.ReceivedOn, s.ActToday(a)))
	}
	if shift > 400 {
		return a, errf(http.StatusUnprocessableEntity, "VALIDATION", "Слишком большой сдвиг.")
	}
	err = s.st.Tx(ctx, func(q *store.Q) error {
		if err := q.SetDemoShift(ctx, a.ID, shift); err != nil {
			return err
		}
		return q.AddEvent(ctx, a.ID, "demo_shift", map[string]any{"shift_days": shift}, &userID)
	})
	if err != nil {
		return a, err
	}
	s.Tick(ctx)
	return s.st.Q().ActByID(ctx, actID)
}

// Test accounts ---------------------------------------------------------------

// Тестовые учётки для автоматической проверки API используют отрицательные max_user_id,
// которые не пересекаются с настоящими пользователями MAX.
const (
	TestChairmanMaxID int64 = -1
	TestResidentMaxID int64 = -2
)

// EnsureTestAccounts создаёт пользователей тестовых ролей и демо-дом. После вызова планировщик следит,
// чтобы у тестового председателя был рабочий демо-акт (ensureTestAct).
func (s *Service) EnsureTestAccounts(ctx context.Context) (chairmanID, residentID string, err error) {
	ch, err := s.EnsureUser(ctx, TestChairmanMaxID, "Тестовый", "Председатель")
	if err != nil {
		return "", "", err
	}
	res, err := s.EnsureUser(ctx, TestResidentMaxID, "Тестовый", "Жилец")
	if err != nil {
		return "", "", err
	}
	if err := s.st.Q().SetConsent(ctx, ch.ID); err != nil {
		return "", "", err
	}
	if err := s.ensureTestAct(ctx, ch.ID); err != nil {
		return "", "", err
	}
	s.testChairman.Store(&ch.ID)
	return ch.ID, res.ID, nil
}

// ensureTestAct: первый в списке акт тестового председателя должен быть на проверке или с решением.
// Проверки из DATA-API.yaml берут именно его; если он ушёл в финальный статус (например, «принят молчанием»
// через 30 дней) или сверху оказался черновик, создаётся свежий — проверки повторяемы весь период проверки.
func (s *Service) ensureTestAct(ctx context.Context, chairmanID string) error {
	// Вложенный (из SetReceived → Tick) или параллельный вызов пропускаем: акт уже создаётся.
	if !testActMu.TryLock() {
		return nil
	}
	defer testActMu.Unlock()
	acts, err := s.ActsOfUser(ctx, chairmanID)
	if err != nil {
		return err
	}
	if len(acts) > 0 && (acts[0].Status == domain.StatusInReview || acts[0].Status == domain.StatusDecided) {
		return nil
	}
	a, err := s.createDemoAct(ctx, chairmanID, nil) // тестовой учётке демо-акт нужен независимо от DEMO_MODE
	if err != nil {
		return fmt.Errorf("демо-акт тестовой учётки: %w", err)
	}
	two, yes := 2, true
	if _, err := s.SetReceived(ctx, chairmanID, a.ID, ReceivedInput{ReceivedOn: s.RealToday(), Channel: "in_person", CopiesReceived: &two, ExecutorSigned: &yes}); err != nil {
		return err
	}
	if _, err := s.CreateInvite(ctx, chairmanID, a.ID); err != nil {
		return err
	}
	s.log.Info("тестовая учётка: новый демо-акт", "act", a.ID)
	return nil
}
