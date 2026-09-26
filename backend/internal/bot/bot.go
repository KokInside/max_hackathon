// Package bot — диалог с председателем в чате MAX. Короткие шаги и кнопки здесь,
// работа со строками акта — в мини-приложении.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/max-messenger/max-bot-api-client-go/v2/model"

	"priemka/internal/app"
	"priemka/internal/maxbot"
	"priemka/internal/store"
)

type Bot struct {
	c     *maxbot.Client
	svc   *app.Service
	log   *slog.Logger
	locks sync.Map // max_user_id → *sync.Mutex: события одного пользователя обрабатываются по очереди
}

func New(c *maxbot.Client, svc *app.Service, log *slog.Logger) *Bot {
	return &Bot{c: c, svc: svc, log: log}
}

// Handle — точка входа для вебхука и long polling.
func (b *Bot) Handle(ctx context.Context, u model.Update) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("bot: panic", "update", u.UpdateType, "panic", r)
		}
	}()

	var sender model.User
	switch u.UpdateType {
	case model.UpdateMessageCreated:
		msg := u.GetMessage()
		if msg.Recipient.ChatType != model.ChatTypeDialog {
			return // в групповых чатах бот не ведёт диалог
		}
		sender = model.User{UserID: msg.Sender.UserID, FirstName: msg.Sender.FirstName, LastName: msg.Sender.LastName}
	case model.UpdateMessageCallback:
		sender = u.GetCallback().User
	case model.UpdateBotStarted:
		sender = u.GetUser()
	default:
		return
	}
	if sender.UserID == 0 || sender.IsBot {
		return
	}
	mu, _ := b.locks.LoadOrStore(sender.UserID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	user, err := b.svc.EnsureUser(ctx, sender.UserID, sender.FirstName, sender.LastName)
	if err != nil {
		b.log.Error("bot: пользователь", "err", err)
		return
	}
	s := &session{b: b, ctx: ctx, user: user, to: maxbot.Recipient{UserID: sender.UserID}}
	if s.sess, err = b.svc.Store().Q().Session(ctx, user.ID); err != nil {
		b.log.Error("bot: сессия", "err", err)
		return
	}

	switch u.UpdateType {
	case model.UpdateBotStarted:
		err = s.start()
	case model.UpdateMessageCallback:
		cb := u.GetCallback()
		_ = b.c.AnswerCallback(ctx, cb.CallbackID, "")
		err = s.callback(cb.Payload)
	case model.UpdateMessageCreated:
		err = s.message(u.GetMessage())
	}
	if err != nil {
		s.fail(err)
	}
}

// session — обработка одного события одного пользователя.
type session struct {
	b    *Bot
	ctx  context.Context
	user store.User
	sess store.Session
	to   maxbot.Recipient
}

func (s *session) send(text string, kb *model.Keyboard) error {
	_, err := s.b.c.Send(s.ctx, maxbot.Outgoing{To: s.to, Text: text, Keyboard: kb})
	return err
}

func (s *session) setState(state string, data map[string]string) error {
	s.sess.State, s.sess.Data = state, data
	return s.b.svc.Store().Q().SaveSession(s.ctx, s.user.ID, s.sess)
}

func (s *session) clearState() error { return s.setState("", nil) }

// fail сообщает об ошибке так, чтобы можно было продолжить без перезапуска.
func (s *session) fail(err error) {
	var ae *app.Error
	text := "Что-то пошло не так. Повторите действие или откройте /menu."
	if errors.As(err, &ae) && ae.Status < 500 {
		text = ae.Message
	} else {
		s.b.log.Error("bot", "user", s.user.ID, "err", err)
	}
	if e := s.send("⚠️ "+text, menuKeyboard(s.b.svc.Config().DemoMode)); e != nil {
		s.b.log.Error("bot: не удалось отправить ошибку", "err", e)
	}
}

func (s *session) message(m model.MessageUpdate) error {
	text := strings.TrimSpace(m.Body.Text)
	for _, at := range m.Body.Attachments {
		if at.Type == model.AttachFile || at.Type == model.AttachImage {
			return s.onFile(at)
		}
	}
	if strings.HasPrefix(text, "/") {
		cmd, _, _ := strings.Cut(strings.ToLower(strings.Fields(text)[0]), "@")
		switch cmd {
		case "/start":
			return s.start()
		case "/menu":
			if err := s.clearState(); err != nil {
				return err
			}
			return s.menu()
		case "/help":
			return s.help()
		case "/demo":
			return s.demoMenu()
		case "/delete":
			return s.send("Удалить все ваши данные в сервисе: профиль, дом, акты, документы и ответы? Это необратимо.",
				kb(row(cbBtn("Удалить навсегда", "delete_confirm")), row(cbBtn("Отмена", "menu"))))
		}
		return s.send("Не знаю такой команды. Доступно: /menu, /help, /demo, /delete.", nil)
	}
	if text == "" {
		return s.send("Пришлите акт файлом (PDF) или фото, либо откройте /menu.", nil)
	}
	return s.onText(text)
}

func (s *session) start() error {
	if s.user.ConsentAt == nil {
		return s.send("Здравствуйте! Я «Приёмка» — помощник председателя совета многоквартирного дома.\n\n"+
			"С 01.09.2026 у совета есть 10 дней, чтобы подписать акт выполненных работ управляющей компании или направить обоснованный отказ. "+
			"Если молчать 30 дней, акт считается принятым.\n\n"+
			"Я считаю сроки и напоминаю о них, проверяю сроки самой УК, помогаю проверить акт по строкам вместе с жильцами и готовлю PDF: письмо к подписанному акту или мотивированный отказ.\n\n"+
			"Для работы я храню ваше имя, данные дома и акты. Жильцы, которые отвечают по ссылке, в документы по имени не попадают. Удалить данные — /delete.",
			kb(row(cbBtn("✅ Согласен на обработку данных", "consent")), row(cbBtn("Подробнее", "help"))))
	}
	return s.menu()
}

func (s *session) help() error {
	return s.send("Как это работает:\n\n"+
		"1. Заполните профиль: ФИО, дом, основание полномочий, управляющая компания.\n"+
		"2. Получили акт от УК — пришлите его сюда файлом или фото и укажите дату получения. Я посчитаю 10-й и 30-й день и напомню о них.\n"+
		"3. В мини-приложении отметьте по каждой строке: подтверждено, под сомнением или не выполнено, добавьте комментарий и фото.\n"+
		"4. Отправьте ссылку жильцам — они отметят, что видели работы в своём подъезде.\n"+
		"5. Сформируйте PDF: письмо к подписанному акту или обоснованный отказ. Отправьте его в УК сами и отметьте здесь способ и дату.\n\n"+
		"Сроки считаются в календарных днях со следующего дня после получения — это допущение, в приказе сказано просто «дней».\n"+
		"Документы — шаблоны; правовые формулировки не проверены юристом.", menuKeyboard(s.b.svc.Config().DemoMode))
}

func (s *session) menu() error {
	prof, err := s.b.svc.Profile(s.ctx, s.user.ID)
	if err != nil {
		return err
	}
	if !prof.Complete() {
		return s.startOnboarding()
	}
	cur, err := s.b.svc.CurrentAct(s.ctx, s.user.ID)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("Дом: %s\nПредседатель совета: %s", prof.House.Address, prof.Profile.FullName)
	k := menuKeyboard(s.b.svc.Config().DemoMode)
	if cur != nil {
		text += "\n\n" + s.b.actLine(*cur)
		k = actKeyboard(s.b, *cur, s.b.svc.Config().DemoMode)
	} else {
		text += "\n\nАктов на проверке нет. Когда получите акт от УК — пришлите его сюда."
	}
	return s.send(text, k)
}

// actLine — одна строка о состоянии акта.
func (b *Bot) actLine(a store.Act) string {
	num := a.Number
	if num == "" {
		num = "без номера"
	}
	s := fmt.Sprintf("Акт № %s — %s.", num, a.Status.Title())
	if !a.ReceivedOn.IsZero() && a.Status.Editable() {
		t := b.svc.Rules().Policy.Timing(a.ReceivedOn, b.svc.ActToday(a))
		d := b.svc.Rules().Policy.ComputeDeadlines(a.ReceivedOn)
		switch {
		case t.DaysLeftResponse >= 0:
			s += fmt.Sprintf(" Ответить до %s (осталось %d дн.).", d.ResponseOn.Russian(), t.DaysLeftResponse)
		case t.DaysLeftSilent >= 0:
			s += fmt.Sprintf(" Срок ответа прошёл; молчаливая приёмка после %s.", d.SilentOn.Russian())
		}
	}
	if a.IsDemo {
		s += " [ДЕМО]"
	}
	return s
}
