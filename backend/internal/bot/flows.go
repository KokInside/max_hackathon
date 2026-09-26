package bot

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/max-messenger/max-bot-api-client-go/v2/model"

	"priemka/internal/app"
	"priemka/internal/civil"
	"priemka/internal/domain"
	"priemka/internal/store"
)

const (
	stAwaitFile     = "await_act_file"
	stAwaitRecvDate = "await_received_date"
	stAwaitDispDate = "await_dispatch_date"
)

// maxActFile — лимит на файл акта от УК.
const maxActFile = 20 << 20

func (s *session) callback(payload string) error {
	cmd, arg, _ := strings.Cut(payload, ":")
	arg2 := ""
	if i := strings.LastIndex(arg, ":"); i >= 0 {
		arg, arg2 = arg[:i], arg[i+1:]
	}
	switch cmd {
	case "consent":
		if err := s.b.svc.Store().Q().SetConsent(s.ctx, s.user.ID); err != nil {
			return err
		}
		u, err := s.b.svc.Store().Q().UserByID(s.ctx, s.user.ID)
		if err != nil {
			return err
		}
		s.user = u
		return s.menu()
	case "help":
		return s.help()
	case "menu":
		if err := s.clearState(); err != nil {
			return err
		}
		return s.menu()
	case "profile":
		return s.showProfile()
	case "profile_edit":
		return s.startOnboarding()
	case "demo_profile":
		if _, err := s.b.svc.FillDemoProfile(s.ctx, s.user.ID); err != nil {
			return err
		}
		if err := s.clearState(); err != nil {
			return err
		}
		if err := s.send("Профиль заполнен синтетическими данными (ДЕМО): вымышленный дом и управляющая компания.", nil); err != nil {
			return err
		}
		return s.menu()
	case "auth", "skip", "applic":
		return s.onboardingCallback(cmd, arg)
	case "delete_confirm":
		if err := s.b.svc.DeleteUserData(s.ctx, s.user.ID); err != nil {
			return err
		}
		return s.send("Ваши данные удалены. Чтобы начать заново, отправьте /start.", nil)
	}

	if s.user.ConsentAt == nil {
		return s.start()
	}
	switch cmd {
	case "act_new":
		prof, err := s.b.svc.Profile(s.ctx, s.user.ID)
		if err != nil {
			return err
		}
		if !prof.Complete() {
			return s.startOnboarding()
		}
		if err := s.setState(stAwaitFile, map[string]string{}); err != nil {
			return err
		}
		return s.send("Пришлите акт, полученный от УК: PDF или фото (до 20 МБ). Если страниц несколько — пришлите первую, остальные можно добавить позже в мини-приложении как доказательства.",
			kb(row(cbBtn("Отмена", "menu"))))
	case "act_demo":
		a, err := s.b.svc.CreateDemoAct(s.ctx, s.user.ID, nil)
		if err != nil {
			return err
		}
		if err := s.send(fmt.Sprintf("🧪 Создан демо-акт № %s с 8 строками (синтетические данные, вымышленная УК). Дата акта — %s, период — %s–%s.",
			a.Number, a.ActDate.Russian(), a.PeriodFrom.Russian(), a.PeriodTo.Russian()), nil); err != nil {
			return err
		}
		return s.askReceived(a.ID)
	case "recv":
		return s.askReceived(arg)
	case "rd": // дата получения: today / yesterday / other
		return s.receivedDate(arg, arg2)
	case "ch": // способ получения
		a, err := s.act(arg)
		if err != nil {
			return err
		}
		patch := app.HeaderPatch{ReceivedChannel: &arg2}
		if err := s.b.svc.UpdateHeader(s.ctx, s.user.ID, a.ID, patch); err != nil {
			return err
		}
		return s.send("Получили два экземпляра акта, подписанные исполнителем?", kb(
			row(cbBtn("Да, два подписанных", "cp:"+a.ID+":2")),
			row(cbBtn("Только один", "cp:"+a.ID+":1"), cbBtn("Не подписаны УК", "cp:"+a.ID+":unsigned"))))
	case "cp":
		a, err := s.act(arg)
		if err != nil {
			return err
		}
		copies, signed := 2, true
		switch arg2 {
		case "1":
			copies = 1
		case "unsigned":
			signed = false
		}
		if err := s.b.svc.UpdateHeader(s.ctx, s.user.ID, a.ID, app.HeaderPatch{CopiesReceived: &copies, ExecutorSigned: &signed}); err != nil {
			return err
		}
		return s.summary(a.ID)
	case "inv":
		return s.invite(arg)
	case "disp":
		a, err := s.act(arg)
		if err != nil {
			return err
		}
		if a.Status != domain.StatusDecided {
			return s.send("Сначала сформируйте документ в мини-приложении: подписание или отказ.", actKeyboard(s.b, a, s.b.svc.Config().DemoMode))
		}
		return s.send("Как вы направили документ исполнителю?", kb(
			row(cbBtn("Лично", "dc:"+a.ID+":in_person"), cbBtn("Почтой", "dc:"+a.ID+":post")),
			row(cbBtn("E-mail", "dc:"+a.ID+":email"), cbBtn("Личный кабинет УК", "dc:"+a.ID+":executor_portal")),
			row(cbBtn("Иначе", "dc:"+a.ID+":other"))))
	case "dc":
		if err := s.setState(stAwaitDispDate, map[string]string{"act": arg, "channel": arg2}); err != nil {
			return err
		}
		return s.send("Когда отправили?", kb(row(cbBtn("Сегодня", "dd:"+arg+":today"), cbBtn("Вчера", "dd:"+arg+":yesterday")),
			row(cbBtn("Другая дата", "dd:"+arg+":other"))))
	case "dd":
		if arg2 == "other" {
			return s.send("Напишите дату отправки, например 25.09.2026.", nil)
		}
		a, err := s.act(arg)
		if err != nil {
			return err
		}
		d := s.b.svc.ActToday(a)
		if arg2 == "yesterday" {
			d = d.AddDays(-1)
		}
		return s.dispatch(a.ID, s.sess.Data["channel"], d)
	case "next":
		if err := s.setState(stAwaitFile, map[string]string{"parent": arg}); err != nil {
			return err
		}
		return s.send("Пришлите новый акт от УК (PDF или фото). Строки прежнего акта скопируются, ранее оспоренные будут отмечены.", kb(row(cbBtn("Отмена", "menu"))))
	case "demo":
		if arg2 != "" {
			return s.demoShift(arg, arg2)
		}
		return s.demoActions(arg)
	case "open":
		a, err := s.act(arg)
		if err != nil {
			return err
		}
		return s.send(s.b.actLine(a), actKeyboard(s.b, a, s.b.svc.Config().DemoMode))
	}
	return s.menu()
}

func (s *session) act(id string) (store.Act, error) {
	a, _, err := s.b.svc.ChairmanAct(s.ctx, s.user.ID, id)
	return a, err
}

func (s *session) onText(text string) error {
	if s.user.ConsentAt == nil {
		return s.start()
	}
	switch s.sess.State {
	case stName, stApartment, stAuthType, stAuthDoc, stAddress, stEntrances, stExecutor, stContract, stApplic:
		return s.onboardingText(text)
	case stAwaitRecvDate:
		d, err := civil.Parse(text)
		if err != nil {
			return s.send("Не понял дату. Напишите в формате ДД.ММ.ГГГГ, например 25.09.2026.", nil)
		}
		return s.setReceived(s.sess.Data["act"], d)
	case stAwaitDispDate:
		d, err := civil.Parse(text)
		if err != nil {
			return s.send("Не понял дату. Напишите в формате ДД.ММ.ГГГГ, например 25.09.2026.", nil)
		}
		return s.dispatch(s.sess.Data["act"], s.sess.Data["channel"], d)
	case stAwaitFile:
		return s.send("Жду файл акта: PDF или фото. Или нажмите «Отмена».", kb(row(cbBtn("Отмена", "menu"))))
	}
	return s.send("Я понимаю кнопки и файлы. Пришлите акт от УК файлом или откройте /menu.", menuKeyboard(s.b.svc.Config().DemoMode))
}

// onFile принимает акт от УК. Файл без предварительной кнопки тоже считается новым актом.
func (s *session) onFile(at model.Attachment) error {
	if s.user.ConsentAt == nil {
		return s.start()
	}
	prof, err := s.b.svc.Profile(s.ctx, s.user.ID)
	if err != nil {
		return err
	}
	if !prof.Complete() {
		return s.startOnboarding()
	}
	if at.Payload.URL == "" {
		return s.send("Не удалось получить файл. Пришлите его ещё раз.", nil)
	}
	if at.Size > maxActFile {
		return s.send("Файл больше 20 МБ — пришлите сжатую копию или фото первой страницы.", nil)
	}
	body, err := s.b.c.Download(s.ctx, at.Payload.URL)
	if err != nil {
		s.b.log.Warn("bot: скачивание", "err", err)
		return s.send("Не удалось скачать файл из MAX. Пришлите его ещё раз через минуту.", nil)
	}
	defer body.Close()
	name := at.FileName
	if name == "" {
		name = path.Base(at.Payload.URL)
		if at.Type == model.AttachImage {
			name = "photo.jpg"
		}
	}
	parent := ""
	if s.sess.State == stAwaitFile {
		parent = s.sess.Data["parent"]
	}
	a, f, err := s.b.svc.ReceiveActFile(s.ctx, s.user.ID, body, name, parent)
	if err != nil {
		return err
	}
	if err := s.send(fmt.Sprintf("Файл принят (%d КБ).", (f.Size+1023)/1024), nil); err != nil {
		return err
	}
	return s.askReceived(a.ID)
}

func (s *session) askReceived(actID string) error {
	if err := s.setState(stAwaitRecvDate, map[string]string{"act": actID}); err != nil {
		return err
	}
	return s.send("Когда вы получили акт? От этой даты считаются 10 и 30 дней.",
		kb(row(cbBtn("Сегодня", "rd:"+actID+":today"), cbBtn("Вчера", "rd:"+actID+":yesterday")),
			row(cbBtn("Другая дата", "rd:"+actID+":other"))))
}

func (s *session) receivedDate(actID, which string) error {
	a, err := s.act(actID)
	if err != nil {
		return err
	}
	switch which {
	case "other":
		if err := s.setState(stAwaitRecvDate, map[string]string{"act": actID}); err != nil {
			return err
		}
		return s.send("Напишите дату получения, например 25.09.2026.", nil)
	case "yesterday":
		return s.setReceived(actID, s.b.svc.ActToday(a).AddDays(-1))
	default:
		return s.setReceived(actID, s.b.svc.ActToday(a))
	}
}

func (s *session) setReceived(actID string, d civil.Date) error {
	if _, err := s.b.svc.SetReceived(s.ctx, s.user.ID, actID, app.ReceivedInput{ReceivedOn: d}); err != nil {
		return err
	}
	if err := s.clearState(); err != nil {
		return err
	}
	return s.send("Как акт поступил?", kb(
		row(cbBtn("Лично", "ch:"+actID+":in_person"), cbBtn("Почтой", "ch:"+actID+":post")),
		row(cbBtn("E-mail", "ch:"+actID+":email"), cbBtn("Личный кабинет УК", "ch:"+actID+":executor_portal")),
		row(cbBtn("Иначе", "ch:"+actID+":other"))))
}

// summary — сроки, проверки исполнителя и следующий шаг.
func (s *session) summary(actID string) error {
	a, err := s.act(actID)
	if err != nil {
		return err
	}
	text, err := s.b.svc.DeadlinesText(a)
	if err != nil {
		return err
	}
	var checks []string
	for _, f := range s.b.svc.Rules().Policy.CheckExecutor(a.Facts()) {
		switch f.Severity {
		case domain.SevViolation:
			checks = append(checks, "❗ "+f.Text+" (расчёт сервиса)")
		case domain.SevWarning:
			checks = append(checks, "⚠️ "+f.Text)
		}
	}
	if len(checks) > 0 {
		text += "\n\nПроверка сроков исполнителя:\n" + strings.Join(checks, "\n") + "\nНарушения попадут в текст отказа."
	} else if !a.ActDate.IsZero() {
		text += "\n\nСроки оформления и направления акта исполнителем соблюдены."
	} else {
		text += "\n\nУкажите в мини-приложении дату акта и период — я проверю сроки самой УК."
	}
	if a.IsDemo {
		text += "\n\n🧪 Это демо-акт. Чтобы увидеть напоминания 9-го дня и молчаливую приёмку, нажмите «Демо: перемотать время»."
	}
	text += "\n\nДальше: откройте проверку и отметьте каждую строку акта."
	return s.send(text, actKeyboard(s.b, a, s.b.svc.Config().DemoMode))
}

func (s *session) invite(actID string) error {
	inv, err := s.b.svc.CreateInvite(s.ctx, s.user.ID, actID)
	if err != nil {
		return err
	}
	a, err := s.act(actID)
	if err != nil {
		return err
	}
	link := s.b.svc.InviteLink(inv.Token)
	if err := s.send("Перешлите следующее сообщение в чат дома или подъезда. Ссылка текстовая — она сохранится при пересылке. Ответы жильцов увидите в карточке акта (только числа и комментарии, без имён).", nil); err != nil {
		return err
	}
	return s.send(s.b.svc.InviteText(a, link), nil)
}

func (s *session) dispatch(actID, channel string, d civil.Date) error {
	a, err := s.b.svc.Dispatch(s.ctx, s.user.ID, actID, app.DispatchInput{Channel: channel, SentOn: d})
	if err != nil {
		return err
	}
	if err := s.clearState(); err != nil {
		return err
	}
	text := fmt.Sprintf("Отмечено: документ направлен %s %s.", app.ChannelTitle(channel), d.Russian())
	if a.Status == domain.StatusRefusedSent {
		text += "\n\nПосле отказа исполнитель оформляет новый акт по тем же правилам; до этого возможны согласительное совещание и осмотр. Когда получите новый акт — нажмите «Получил новый акт»."
	} else {
		text += "\n\nАкт закрыт. Напоминания по нему больше не придут."
	}
	return s.send(text, actKeyboard(s.b, a, s.b.svc.Config().DemoMode))
}

func (s *session) showProfile() error {
	p, err := s.b.svc.Profile(s.ctx, s.user.ID)
	if err != nil {
		return err
	}
	if !p.Complete() {
		return s.startOnboarding()
	}
	text := fmt.Sprintf("Профиль:\nФИО: %s\nКвартира: %s\nОснование: %s\nДом: %s (подъездов: %d)",
		p.Profile.FullName, p.Profile.ApartmentNo, p.Profile.AuthorityText(), p.House.Address, p.House.EntrancesCount)
	if p.Contract != nil {
		text += fmt.Sprintf("\nИсполнитель: %s\nДоговор: %s %s", p.Contract.ExecutorName, p.Contract.Number, p.Contract.Date.Russian())
	}
	if p.House.IsDemo {
		text += "\n\n🧪 ДЕМО-профиль (синтетические данные)"
	}
	return s.send(text, kb(row(cbBtn("Изменить", "profile_edit"), cbBtn("Меню", "menu"))))
}

// Демо-режим ------------------------------------------------------------------

func (s *session) demoMenu() error {
	if !s.b.svc.Config().DemoMode {
		return s.send("Демо-режим выключен.", nil)
	}
	cur, err := s.b.svc.CurrentAct(s.ctx, s.user.ID)
	if err != nil {
		return err
	}
	if cur == nil || !cur.IsDemo {
		return s.send("🧪 Демо-режим: создайте демо-акт, укажите дату получения — и сможете перематывать время акта, чтобы увидеть напоминания и молчаливую приёмку.",
			kb(row(cbBtn("🧪 Взять демо-акт", "act_demo"))))
	}
	return s.demoActions(cur.ID)
}

func (s *session) demoActions(actID string) error {
	a, err := s.act(actID)
	if err != nil {
		return err
	}
	if !a.IsDemo || a.ReceivedOn.IsZero() {
		return s.send("Перемотка времени доступна для демо-акта с указанной датой получения.", nil)
	}
	day := s.b.svc.Rules().Policy.DayNumber(a.ReceivedOn, s.b.svc.ActToday(a))
	return s.send(fmt.Sprintf("🧪 Время демо-акта: %d-й день после получения (сдвиг %+d дн.). Напоминания придут в течение минуты после перемотки.", day, a.DemoShiftDays),
		kb(row(cbBtn("+1 день", "demo:"+a.ID+":+1"), cbBtn("К 9-му дню", "demo:"+a.ID+":9")),
			row(cbBtn("К 11-му дню", "demo:"+a.ID+":11"), cbBtn("К 31-му дню", "demo:"+a.ID+":31"))))
}

func (s *session) demoShift(actID, arg string) error {
	var days, to int
	if strings.HasPrefix(arg, "+") {
		days, _ = strconv.Atoi(arg[1:])
	} else {
		to, _ = strconv.Atoi(arg)
	}
	a, err := s.b.svc.DemoShift(s.ctx, s.user.ID, actID, days, to)
	var ae *app.Error
	if errors.As(err, &ae) && ae.Code == "DEMO_BACKWARDS" {
		return s.send(ae.Message, nil)
	} else if err != nil {
		return err
	}
	day := s.b.svc.Rules().Policy.DayNumber(a.ReceivedOn, s.b.svc.ActToday(a))
	return s.send(fmt.Sprintf("🧪 Перемотано: сейчас %d-й день после получения акта.", day), actKeyboard(s.b, a, true))
}
