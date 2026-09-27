package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"priemka/internal/civil"
	"priemka/internal/domain"
	"priemka/internal/files"
	"priemka/internal/rules"
	"priemka/internal/store"
)

var channels = map[string]string{
	"post": "почтой", "email": "по электронной почте", "executor_portal": "через личный кабинет на сайте исполнителя",
	"in_person": "лично", "other": "иным способом",
}

// ChannelTitle — способ получения или отправки по-русски.
func ChannelTitle(c string) string { return channels[c] }

func validChannel(c string) bool { _, ok := channels[c]; return ok }

// ReceivedInput — сведения о получении акта.
type ReceivedInput struct {
	ReceivedOn     civil.Date
	Channel        string
	CopiesReceived *int
	ExecutorSigned *bool
}

// Ограничения длины текстовых полей: всё это попадает в документ.
const (
	maxField   = 500
	maxComment = 2000
)

// withAct выполняет fn в транзакции с заблокированной строкой акта: права председателя и статус
// проверяются уже по заблокированной строке, поэтому параллельные правки не теряются.
func (s *Service) withAct(ctx context.Context, userID, actID string, editable bool, fn func(q *store.Q, a store.Act, h store.House) error) error {
	return s.st.Tx(ctx, func(q *store.Q) error {
		a, err := q.ActForUpdate(ctx, actID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrActNotFound
		} else if err != nil {
			return err
		}
		h, err := q.HouseByID(ctx, a.HouseID)
		if err != nil {
			return err
		}
		if h.ChairmanUserID != userID {
			return ErrForbidden
		}
		if editable && !a.Status.Editable() {
			return errf(http.StatusConflict, "ACT_LOCKED", "Акт закрыт для изменений: %s.", a.Status.Title())
		}
		return fn(q, a, h)
	})
}

func (s *Service) validateReceived(a store.Act, d civil.Date) error {
	today := s.ActToday(a)
	if d.IsZero() || d.After(today) {
		return errf(http.StatusUnprocessableEntity, "BAD_RECEIVED_DATE", "Дата получения не может быть пустой или в будущем.")
	}
	if today.DaysSince(d) > 365 {
		return errf(http.StatusUnprocessableEntity, "BAD_RECEIVED_DATE", "Дата получения больше года назад — проверьте дату.")
	}
	return nil
}

// SetReceived фиксирует получение акта: считает сроки, ставит напоминания, переводит акт на проверку.
func (s *Service) SetReceived(ctx context.Context, userID, actID string, in ReceivedInput) (store.Act, error) {
	err := s.withAct(ctx, userID, actID, true, func(q *store.Q, a store.Act, _ store.House) error {
		if err := s.validateReceived(a, in.ReceivedOn); err != nil {
			return err
		}
		if in.Channel != "" && !validChannel(in.Channel) {
			return errf(http.StatusUnprocessableEntity, "BAD_CHANNEL", "Неизвестный способ получения.")
		}
		next := a
		next.ReceivedOn = in.ReceivedOn
		if in.Channel != "" {
			next.ReceivedChannel = in.Channel
		}
		if in.CopiesReceived != nil {
			next.CopiesReceived = in.CopiesReceived
		}
		if in.ExecutorSigned != nil {
			next.ExecutorSigned = in.ExecutorSigned
		}
		return s.saveHeader(ctx, q, a, next, userID)
	})
	if err != nil {
		return store.Act{}, err
	}
	// Если акт получен давно, сроки могли уже пройти — сразу обрабатываем, не дожидаясь тика планировщика.
	s.Tick(context.WithoutCancel(ctx))
	return s.st.Q().ActByID(ctx, actID)
}

// headerSnapshot — поля шапки акта для сравнения «изменилось ли что-то».
func headerSnapshot(a store.Act) string {
	a.Status, a.Decision, a.RulesVersion, a.IsDemo, a.DemoShiftDays = "", "", "", false, 0
	a.DeadlineResponseOn, a.DeadlineSilentOn = civil.Date{}, civil.Date{}
	a.CreatedAt, a.UpdatedAt = time.Time{}, time.Time{}
	b, _ := json.Marshal(a)
	return string(b)
}

// saveHeader сохраняет изменённую шапку: пересчитывает сроки и напоминания при новой дате получения,
// переводит черновик на проверку, помечает сформированный документ устаревшим. Без изменений — ничего не делает.
func (s *Service) saveHeader(ctx context.Context, q *store.Q, prev, next store.Act, userID string) error {
	if headerSnapshot(prev) == headerSnapshot(next) {
		return nil
	}
	if err := q.SaveActHeader(ctx, next); err != nil {
		return err
	}
	if !next.ReceivedOn.IsZero() && (!prev.ReceivedOn.Equal(next.ReceivedOn) || prev.Status == domain.StatusDraft) {
		p := s.rules.Policy
		d := p.ComputeDeadlines(next.ReceivedOn)
		if err := q.SetDeadlines(ctx, next.ID, d, s.rules.Version); err != nil {
			return err
		}
		today := s.ActToday(next)
		var rs []store.Reminder
		for _, r := range s.rules.Reminders {
			due := p.DateOfDay(next.ReceivedOn, r.Day)
			// Уже прошедшие напоминания не шлём пачкой; о молчаливой приёмке сообщаем всегда.
			if due.Before(today) && r.Day <= p.SilentDays {
				continue
			}
			rs = append(rs, store.Reminder{Kind: r.Kind, DueOn: due, DueHour: *s.rules.Params.ReminderHour})
		}
		if err := q.ReplaceReminders(ctx, next.ID, rs); err != nil {
			return err
		}
		if prev.Status == domain.StatusDraft {
			if err := moveStatus(ctx, q, next.ID, prev.Status, domain.StatusInReview, ""); err != nil {
				return err
			}
		}
		if err := q.AddEvent(ctx, next.ID, "received_set", map[string]any{
			"received_on": next.ReceivedOn, "channel": next.ReceivedChannel, "response_on": d.ResponseOn, "silent_on": d.SilentOn,
		}, &userID); err != nil {
			return err
		}
	}
	return s.invalidateDecision(ctx, q, next)
}

// HeaderPatch — изменяемые поля шапки (nil — не менять).
type HeaderPatch struct {
	Number                    *string     `json:"number"`
	ActDate                   *civil.Date `json:"act_date"`
	City                      *string     `json:"city"`
	Address                   *string     `json:"address"`
	CustomerFullName          *string     `json:"customer_full_name"`
	CustomerApartment         *string     `json:"customer_apartment"`
	CustomerAuthorityText     *string     `json:"customer_authority_text"`
	ExecutorName              *string     `json:"executor_name"`
	ExecutorSignatoryName     *string     `json:"executor_signatory_name"`
	ExecutorSignatoryPosition *string     `json:"executor_signatory_position"`
	ExecutorBasis             *string     `json:"executor_basis"`
	ContractType              *string     `json:"contract_type"`
	ContractNumber            *string     `json:"contract_number"`
	ContractDate              *civil.Date `json:"contract_date"`
	ContractEndDate           *civil.Date `json:"contract_end_date"`
	PeriodFrom                *civil.Date `json:"period_from"`
	PeriodTo                  *civil.Date `json:"period_to"`
	TotalAmount               *string     `json:"total_amount"`
	TotalAmountWords          *string     `json:"total_amount_words"`
	CopiesReceived            *int        `json:"copies_received"`
	ExecutorSigned            *bool       `json:"executor_signed"`
	ReceivedOn                *civil.Date `json:"received_on"`
	ReceivedChannel           *string     `json:"received_channel"`
	ExecutorSentOn            *civil.Date `json:"executor_sent_on"`
}

// setStr записывает обрезанное значение, если поле передано, и проверяет длину.
func setStr(dst *string, v *string, field string, max int) error {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	if len([]rune(s)) > max {
		return errf(http.StatusUnprocessableEntity, "VALIDATION", "%s: не больше %d символов.", field, max)
	}
	*dst = s
	return nil
}

func setDate(dst *civil.Date, v *civil.Date) {
	if v != nil {
		*dst = *v
	}
}

// apply переносит правку в копию акта и проверяет значения.
func (p HeaderPatch) apply(s *Service, a *store.Act) error {
	for _, f := range []struct {
		dst   *string
		v     *string
		field string
	}{
		{&a.Number, p.Number, "Номер акта"}, {&a.City, p.City, "Город"}, {&a.Address, p.Address, "Адрес"},
		{&a.CustomerFullName, p.CustomerFullName, "ФИО председателя"}, {&a.CustomerApartment, p.CustomerApartment, "Квартира"},
		{&a.CustomerAuthorityText, p.CustomerAuthorityText, "Основание полномочий"}, {&a.ExecutorName, p.ExecutorName, "Исполнитель"},
		{&a.ExecutorSignatoryName, p.ExecutorSignatoryName, "Подписант исполнителя"},
		{&a.ExecutorSignatoryPosition, p.ExecutorSignatoryPosition, "Должность подписанта"},
		{&a.ExecutorBasis, p.ExecutorBasis, "Основание подписанта"}, {&a.ContractNumber, p.ContractNumber, "Номер договора"},
		{&a.TotalAmountWords, p.TotalAmountWords, "Сумма прописью"},
	} {
		if err := setStr(f.dst, f.v, f.field, maxField); err != nil {
			return err
		}
	}
	for _, f := range []struct{ dst, v *civil.Date }{
		{&a.ActDate, p.ActDate}, {&a.ContractDate, p.ContractDate}, {&a.ContractEndDate, p.ContractEndDate},
		{&a.PeriodFrom, p.PeriodFrom}, {&a.PeriodTo, p.PeriodTo}, {&a.ExecutorSentOn, p.ExecutorSentOn},
	} {
		setDate(f.dst, f.v)
	}
	if p.ContractType != nil {
		switch *p.ContractType {
		case "management", "services", "repair":
			a.ContractType = *p.ContractType
		default:
			return errf(http.StatusUnprocessableEntity, "VALIDATION", "Вид договора: management, services или repair.")
		}
	}
	if err := money(p.TotalAmount, "Итоговая сумма", &a.TotalAmount); err != nil {
		return err
	}
	if p.CopiesReceived != nil {
		if *p.CopiesReceived < 0 || *p.CopiesReceived > 10 {
			return errf(http.StatusUnprocessableEntity, "VALIDATION", "Число экземпляров: от 0 до 10.")
		}
		a.CopiesReceived = p.CopiesReceived
	}
	if p.ExecutorSigned != nil {
		a.ExecutorSigned = p.ExecutorSigned
	}
	if p.ReceivedChannel != nil {
		if *p.ReceivedChannel != "" && !validChannel(*p.ReceivedChannel) {
			return errf(http.StatusUnprocessableEntity, "BAD_CHANNEL", "Неизвестный способ получения.")
		}
		a.ReceivedChannel = *p.ReceivedChannel
	}
	if p.ReceivedOn != nil {
		if err := s.validateReceived(*a, *p.ReceivedOn); err != nil {
			return err
		}
		a.ReceivedOn = *p.ReceivedOn
	}
	if !a.PeriodFrom.IsZero() && !a.PeriodTo.IsZero() && a.PeriodTo.Before(a.PeriodFrom) {
		return errf(http.StatusUnprocessableEntity, "VALIDATION", "Период: дата окончания раньше даты начала.")
	}
	return nil
}

// UpdateHeader применяет правку шапки акта из мини-приложения или бота.
func (s *Service) UpdateHeader(ctx context.Context, userID, actID string, p HeaderPatch) error {
	return s.withAct(ctx, userID, actID, true, func(q *store.Q, a store.Act, _ store.House) error {
		next := a
		if err := p.apply(s, &next); err != nil {
			return err
		}
		return s.saveHeader(ctx, q, a, next, userID)
	})
}

// invalidateDecision — после правки акта сформированный документ устаревает.
func (s *Service) invalidateDecision(ctx context.Context, q *store.Q, a store.Act) error {
	cur, err := q.ActByID(ctx, a.ID)
	if err != nil {
		return err
	}
	if cur.Status != domain.StatusDecided {
		return nil
	}
	if err := moveStatus(ctx, q, a.ID, cur.Status, domain.StatusInReview, ""); err != nil {
		return err
	}
	return q.AddEvent(ctx, a.ID, "decision_outdated", nil, nil)
}

// LineInput — поля строки акта.
type LineInput struct {
	WorkName        *string              `json:"work_name"`
	PP290Ref        *string              `json:"pp290_ref"`
	PeriodicityQty  *string              `json:"periodicity_qty"`
	Unit            *string              `json:"unit"`
	UnitPrice       *string              `json:"unit_price"`
	Amount          *string              `json:"amount"`
	ReviewStatus    *domain.ReviewStatus `json:"review_status"`
	Comment         *string              `json:"comment"`
	DisputedAmount  *string              `json:"disputed_amount"`
	ResidentVisible *bool                `json:"resident_visible"`
	Entrances       *[]int32             `json:"entrances"`
}

func money(v *string, field string, dst **string) error {
	if v == nil {
		return nil
	}
	if strings.TrimSpace(*v) == "" {
		*dst = nil
		return nil
	}
	k, err := domain.ParseMoney(*v)
	if err != nil || k < 0 {
		return errf(http.StatusUnprocessableEntity, "VALIDATION", "%s: неотрицательное число, например 1250.50.", field)
	}
	s := k.String()
	*dst = &s
	return nil
}

func (in LineInput) apply(l *store.Line, entrances int) error {
	if in.WorkName != nil {
		name := strings.TrimSpace(*in.WorkName)
		if name == "" || len([]rune(name)) > 500 {
			return errf(http.StatusUnprocessableEntity, "VALIDATION", "Наименование работы: от 1 до 500 символов.")
		}
		l.WorkName = name
	}
	if err := setStr(&l.PP290Ref, in.PP290Ref, "Пункт ПП № 290", 100); err != nil {
		return err
	}
	if err := setStr(&l.PeriodicityQty, in.PeriodicityQty, "Периодичность", 200); err != nil {
		return err
	}
	if err := setStr(&l.Unit, in.Unit, "Единица измерения", 50); err != nil {
		return err
	}
	if err := money(in.UnitPrice, "Стоимость за единицу", &l.UnitPrice); err != nil {
		return err
	}
	if in.Amount != nil {
		var amt *string
		if err := money(in.Amount, "Цена", &amt); err != nil {
			return err
		}
		if amt == nil {
			return errf(http.StatusUnprocessableEntity, "VALIDATION", "Цена строки обязательна.")
		}
		l.Amount = *amt
	}
	if in.ReviewStatus != nil {
		if !in.ReviewStatus.Valid() {
			return errf(http.StatusUnprocessableEntity, "VALIDATION", "Статус строки: unchecked, confirmed, doubtful или not_done.")
		}
		l.ReviewStatus = *in.ReviewStatus
	}
	if in.Comment != nil {
		if err := setStr(&l.Comment, in.Comment, "Комментарий", maxComment); err != nil {
			return err
		}
	}
	if err := money(in.DisputedAmount, "Оспариваемая сумма", &l.DisputedAmount); err != nil {
		return err
	}
	if in.ResidentVisible != nil {
		l.ResidentVisible = *in.ResidentVisible
	}
	if in.Entrances != nil {
		for _, e := range *in.Entrances {
			if e < 1 || int(e) > entrances {
				return errf(http.StatusUnprocessableEntity, "VALIDATION", "Подъезд должен быть от 1 до %d.", entrances)
			}
		}
		l.Entrances = *in.Entrances
		if len(l.Entrances) == 0 {
			l.Entrances = nil
		}
	}
	// Оспорить больше, чем стоит работа по акту, нельзя.
	if l.DisputedAmount != nil {
		d, _ := domain.ParseMoney(*l.DisputedAmount)
		amt, _ := domain.ParseMoney(l.Amount)
		if d > amt {
			return errf(http.StatusUnprocessableEntity, "VALIDATION", "Оспариваемая сумма (%s ₽) больше цены строки (%s ₽).", d.Rubles(), amt.Rubles())
		}
	}
	return nil
}

func (s *Service) AddLine(ctx context.Context, userID, actID string, in LineInput) (store.Line, error) {
	var l store.Line
	err := s.withAct(ctx, userID, actID, true, func(q *store.Q, a store.Act, h store.House) error {
		l = store.Line{ActID: a.ID, ReviewStatus: domain.ReviewUnchecked, ResidentVisible: true, Amount: "0.00"}
		if in.WorkName == nil {
			return errf(http.StatusUnprocessableEntity, "VALIDATION", "Наименование работы обязательно.")
		}
		if err := in.apply(&l, h.EntrancesCount); err != nil {
			return err
		}
		var err error
		if l, err = q.InsertLine(ctx, l); err != nil {
			return err
		}
		return s.invalidateDecision(ctx, q, a)
	})
	return l, err
}

// withLine — как withAct, но для операции над строкой: строка перечитывается под блокировкой акта.
func (s *Service) withLine(ctx context.Context, userID, lineID string, fn func(q *store.Q, l store.Line, a store.Act, h store.House) error) error {
	l, err := s.st.Q().LineByID(ctx, lineID)
	if err != nil {
		return err
	}
	return s.withAct(ctx, userID, l.ActID, true, func(q *store.Q, a store.Act, h store.House) error {
		l, err := q.LineByID(ctx, lineID)
		if err != nil {
			return err
		}
		return fn(q, l, a, h)
	})
}

func (s *Service) UpdateLine(ctx context.Context, userID, lineID string, in LineInput) (store.Line, error) {
	var out store.Line
	err := s.withLine(ctx, userID, lineID, func(q *store.Q, l store.Line, a store.Act, h store.House) error {
		if err := in.apply(&l, h.EntrancesCount); err != nil {
			return err
		}
		var err error
		if out, err = q.UpdateLine(ctx, l); err != nil {
			return err
		}
		return s.invalidateDecision(ctx, q, a)
	})
	return out, err
}

func (s *Service) DeleteLine(ctx context.Context, userID, lineID string) error {
	var fs []store.File
	err := s.withLine(ctx, userID, lineID, func(q *store.Q, l store.Line, a store.Act, _ store.House) error {
		var err error
		if fs, err = q.LineFiles(ctx, l.ID); err != nil {
			return err
		}
		if err := q.DeleteLine(ctx, l.ID); err != nil {
			return err
		}
		if err := q.DeleteFiles(ctx, fileIDs(fs)); err != nil {
			return err
		}
		return s.invalidateDecision(ctx, q, a)
	})
	if err == nil {
		s.removeFiles(fs)
	}
	return err
}

// AddEvidence сохраняет фото или PDF как доказательство по строке. Файл пишется на диск до транзакции
// (чтение загрузки может быть долгим — акт при этом не блокируется) и удаляется, если транзакция не прошла.
func (s *Service) AddEvidence(ctx context.Context, userID, lineID string, r io.Reader, name, note string) (store.Evidence, error) {
	if len([]rune(note)) > maxField {
		return store.Evidence{}, errf(http.StatusUnprocessableEntity, "VALIDATION", "Подпись к фото: не больше %d символов.", maxField)
	}
	if _, _, err := s.lineAccess(ctx, userID, lineID); err != nil {
		return store.Evidence{}, err
	}
	saved, err := s.files.Save(r, s.cfg.MaxPhoto, files.ImagesAndPDF)
	if err != nil {
		return store.Evidence{}, fileError(err, s.cfg.MaxPhoto)
	}
	var ev store.Evidence
	err = s.withLine(ctx, userID, lineID, func(q *store.Q, l store.Line, a store.Act, _ store.House) error {
		f, err := q.CreateFile(ctx, store.File{StorageKey: saved.Key, Mime: saved.Mime, Size: saved.Size, SHA256: saved.SHA256, OriginalName: name, UploadedBy: &userID})
		if err != nil {
			return err
		}
		if ev, err = q.AddEvidence(ctx, store.Evidence{ActID: a.ID, LineID: &l.ID, FileID: &f.ID, Note: strings.TrimSpace(note), AuthorRole: "chairman"}); err != nil {
			return err
		}
		ev.Mime = saved.Mime
		return s.invalidateDecision(ctx, q, a)
	})
	if err != nil {
		s.removeFiles([]store.File{{StorageKey: saved.Key}})
	}
	return ev, err
}

// lineAccess — быстрая проверка прав и статуса до чтения загружаемого файла.
func (s *Service) lineAccess(ctx context.Context, userID, lineID string) (store.Line, store.Act, error) {
	l, err := s.st.Q().LineByID(ctx, lineID)
	if err != nil {
		return l, store.Act{}, err
	}
	a, _, err := s.ChairmanAct(ctx, userID, l.ActID)
	if err == nil && !a.Status.Editable() {
		err = errf(http.StatusConflict, "ACT_LOCKED", "Акт закрыт для изменений: %s.", a.Status.Title())
	}
	return l, a, err
}

func (s *Service) DeleteEvidence(ctx context.Context, userID, evidenceID string) error {
	ev, err := s.st.Q().EvidenceByID(ctx, evidenceID)
	if err != nil {
		return err
	}
	var fs []store.File
	err = s.withAct(ctx, userID, ev.ActID, true, func(q *store.Q, a store.Act, _ store.House) error {
		ev, err := q.EvidenceByID(ctx, evidenceID)
		if err != nil {
			return err
		}
		if err := q.DeleteEvidence(ctx, ev.ID); err != nil {
			return err
		}
		if ev.FileID != nil {
			f, err := q.FileByID(ctx, *ev.FileID)
			if err != nil {
				return err
			}
			fs = append(fs, f)
			if err := q.DeleteFiles(ctx, []string{f.ID}); err != nil {
				return err
			}
		}
		return s.invalidateDecision(ctx, q, a)
	})
	if err == nil {
		s.removeFiles(fs)
	}
	return err
}

func fileIDs(fs []store.File) []string {
	ids := make([]string, 0, len(fs))
	for _, f := range fs {
		ids = append(ids, f.ID)
	}
	return ids
}

// removeFiles удаляет файлы с диска после коммита; сбой только логируется — запись в БД уже удалена.
func (s *Service) removeFiles(fs []store.File) {
	for _, f := range fs {
		if err := s.files.Remove(f.StorageKey); err != nil {
			s.log.Warn("удаление файла", "key", f.StorageKey, "err", err)
		}
	}
}

func fileError(err error, max int64) error {
	switch {
	case errors.Is(err, files.ErrTooLarge):
		return errf(http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "Файл больше %d МБ — пришлите сжатую копию.", max>>20)
	case errors.Is(err, files.ErrBadType):
		return errf(http.StatusUnsupportedMediaType, "BAD_FILE_TYPE", "Поддерживаются фото (JPEG, PNG, WebP) и PDF.")
	}
	return err
}

// ReceiveActFile сохраняет файл акта от УК, полученный ботом, и создаёт акт: новый или (parentActID)
// следующий после отказа. При ошибке файл и его запись удаляются.
// ActDeletable — можно ли удалить акт: демо — всегда, настоящий — пока документ не отмечен отправленным в УК
// (после отправки акт — история переписки с исполнителем; стереть всё можно через удаление данных пользователя).
func ActDeletable(a store.Act) bool {
	return a.IsDemo || !a.Status.Sent()
}

// DeleteAct удаляет акт со строками, доказательствами, документами, ответами жильцов, напоминаниями и файлами.
func (s *Service) DeleteAct(ctx context.Context, userID, actID string) error {
	var fs []store.File
	err := s.withAct(ctx, userID, actID, false, func(q *store.Q, a store.Act, _ store.House) error {
		if !ActDeletable(a) {
			return errf(http.StatusConflict, "ACT_SENT", "Документ по акту уже отправлен в УК — акт хранится как история переписки с исполнителем. Удалить все свои данные можно командой /delete в боте.")
		}
		var err error
		if fs, err = q.ActFiles(ctx, a.ID); err != nil {
			return err
		}
		if err := q.DeleteAct(ctx, a.ID); err != nil {
			return err
		}
		return q.DeleteFiles(ctx, fileIDs(fs))
	})
	if err != nil {
		return err
	}
	s.removeFiles(fs)
	s.log.Info("акт удалён", "act", actID, "files", len(fs))
	return nil
}

func (s *Service) ReceiveActFile(ctx context.Context, userID string, r io.Reader, name, parentActID string) (store.Act, store.File, error) {
	saved, err := s.files.Save(r, s.cfg.MaxActFile, files.ImagesAndPDF)
	if err != nil {
		return store.Act{}, store.File{}, fileError(err, s.cfg.MaxActFile)
	}
	f, err := s.st.Q().CreateFile(ctx, store.File{StorageKey: saved.Key, Mime: saved.Mime, Size: saved.Size, SHA256: saved.SHA256, OriginalName: name, UploadedBy: &userID})
	if err != nil {
		s.removeFiles([]store.File{{StorageKey: saved.Key}})
		return store.Act{}, f, err
	}
	var a store.Act
	if parentActID != "" {
		a, err = s.Successor(ctx, userID, parentActID, &f.ID)
	} else {
		a, err = s.CreateAct(ctx, userID, &f.ID)
	}
	if err != nil {
		if derr := s.st.Q().DeleteFiles(ctx, []string{f.ID}); derr != nil {
			s.log.Warn("удаление записи файла", "err", derr)
		}
		s.removeFiles([]store.File{f})
	}
	return a, f, err
}

// FileURL — подписанная ссылка на файл для мини-приложения.
func (s *Service) FileURL(fileID string) string {
	exp, sig := s.files.Sign(fileID, 30*time.Minute)
	return fmt.Sprintf("/api/v1/files/%s?exp=%d&sig=%s", fileID, exp, sig)
}

// Basis — основания для ответа API.
func (s *Service) Basis(ids ...string) []rules.Basis { return s.rules.Basis(ids...) }
