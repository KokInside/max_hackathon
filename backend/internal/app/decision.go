package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"priemka/internal/civil"
	"priemka/internal/domain"
	"priemka/internal/files"
	"priemka/internal/pdf"
	"priemka/internal/store"
)

// DecisionResult — сформированный документ и итог доставки в чат.
type DecisionResult struct {
	Document  store.Document `json:"document"`
	URL       string         `json:"url"`
	Delivered bool           `json:"delivered"`
	Warning   string         `json:"warning,omitempty"`
}

// Decide проверяет решение, формирует PDF и отправляет его председателю ботом.
// Документ собирается под блокировкой акта: параллельная правка не попадёт между чтением строк и сменой статуса.
func (s *Service) Decide(ctx context.Context, userID, actID string, d domain.Decision, confirmDisputed bool) (DecisionResult, error) {
	var (
		doc   store.Document
		saved files.Saved
		act   store.Act
		house store.House
		kind  string
	)
	err := s.withAct(ctx, userID, actID, false, func(q *store.Q, a store.Act, h store.House) error {
		act, house = a, h
		lines, err := q.Lines(ctx, a.ID)
		if err != nil {
			return err
		}
		dl := make([]domain.Line, len(lines))
		for i, l := range lines {
			dl[i] = l.Domain()
		}
		if err := domain.ValidateDecision(a.Status, d, dl, confirmDisputed); err != nil {
			var de *domain.DecisionError
			if errors.As(err, &de) {
				st := http.StatusUnprocessableEntity
				if de.Code == "ACT_LOCKED" {
					st = http.StatusConflict
				}
				return &Error{Status: st, Code: de.Code, Message: de.Message}
			}
			return err
		}
		in, err := s.documentInput(ctx, q, a, d, lines, dl)
		if err != nil {
			return err
		}
		raw, err := s.pdf.Render(in)
		if err != nil {
			return fmt.Errorf("PDF: %w", err)
		}
		if saved, err = s.files.SaveBytes(raw, ".pdf"); err != nil {
			return err
		}
		kind = "refusal"
		if d == domain.DecisionSign {
			kind = "cover_letter"
		}
		f, err := q.CreateFile(ctx, store.File{StorageKey: saved.Key, Mime: "application/pdf", Size: saved.Size, SHA256: saved.SHA256, OriginalName: docFileName(kind, a, 0)})
		if err != nil {
			return err
		}
		if doc, err = q.CreateDocument(ctx, store.Document{ActID: a.ID, Kind: kind, FileID: f.ID, RulesVersion: s.rules.Version}); err != nil {
			return err
		}
		if err := moveStatus(ctx, q, a.ID, a.Status, domain.StatusDecided, d); err != nil {
			return err
		}
		return q.AddEvent(ctx, a.ID, "decided", map[string]any{"decision": d, "document_id": doc.ID, "version": doc.Version}, &userID)
	})
	if err != nil {
		if saved.Key != "" {
			s.removeFiles([]store.File{{StorageKey: saved.Key}})
		}
		return DecisionResult{}, err
	}

	res := DecisionResult{Document: doc, URL: s.FileURL(doc.FileID)}
	if s.notify == nil {
		res.Warning = "Бот не подключён: скачайте документ здесь."
		return res, nil
	}
	act.Status, act.Decision = domain.StatusDecided, d
	mid, err := s.notify.SendDocument(ctx, s.chairmanMaxID(ctx, house), act, doc, s.files.Path(saved.Key), docFileName(kind, act, doc.Version))
	if err != nil {
		s.log.Error("отправка документа в чат", "act", act.ID, "err", err)
		res.Warning = "Документ сформирован, но не отправлен в чат с ботом. Скачайте его здесь."
		return res, nil
	}
	res.Delivered = true
	if err := s.st.Q().SetDocumentMessage(ctx, doc.ID, mid); err != nil {
		s.log.Warn("сохранение id сообщения с документом", "err", err)
	}
	return res, nil
}

func docFileName(kind string, a store.Act, version int) string {
	num := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' {
			return r
		}
		return '-'
	}, a.Number)
	if num == "" {
		num = "b-n"
	}
	name := fmt.Sprintf("%s_act-%s", kind, num)
	if !a.ActDate.IsZero() {
		name += "_" + a.ActDate.String()
	}
	if version > 0 {
		name += fmt.Sprintf("_v%d", version)
	}
	return name + ".pdf"
}

type docData struct {
	Number, ActDate, PeriodFrom, PeriodTo, Total, Received, Channel string
	CiteShort, Address, CustomerName, Apartment, Authority          string
	ExecutorName, Generated, RulesVersion                           string
	Disputed, LinesTotal                                            string
	DisputedCount, Respondents                                      int
	HasViolations                                                   bool
	RequestsNo                                                      int
}

// cellLimit — предел текста возражения в ячейке таблицы: длиннее строка не помещается на страницу A4.
const cellLimit = 250

var authorRoles = map[string]string{"chairman": "председатель совета", "council": "член совета", "resident": "жилец"}

func placeLine(city string) string {
	city = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(city), "г."))
	if city == "" {
		return ""
	}
	return "г. " + city
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func (s *Service) documentInput(ctx context.Context, q *store.Q, a store.Act, d domain.Decision, lines []store.Line, dl []domain.Line) (pdf.Input, error) {
	sums := domain.Summarize(dl, a.Total())
	findings := s.rules.Policy.CheckExecutor(a.Facts())
	var violations []string
	for _, f := range findings {
		if f.Severity != domain.SevViolation {
			continue
		}
		text := f.Text
		for _, id := range f.Basis {
			if cite := s.rules.Cite(id); cite != "" && s.rules.Rules[id].Kind == domain.KindNorm {
				text += " Основание: " + cite + "."
				break
			}
		}
		violations = append(violations, text)
	}
	respondents, err := q.CountRespondents(ctx, a.ID)
	if err != nil {
		return pdf.Input{}, err
	}
	total := "—"
	if t := a.Total(); t != nil {
		total = t.Rubles()
	}
	data := docData{
		Number: orDash(a.Number), ActDate: orDash(a.ActDate.Russian()), PeriodFrom: orDash(a.PeriodFrom.Russian()), PeriodTo: orDash(a.PeriodTo.Russian()),
		Total: total, Received: a.ReceivedOn.Russian(), Channel: ChannelTitle(a.ReceivedChannel),
		CiteShort: s.rules.Sources["minstroy_318"].Cite, Address: a.Address,
		CustomerName: a.CustomerFullName, Apartment: a.CustomerApartment, Authority: a.CustomerAuthorityText,
		ExecutorName: orDash(a.ExecutorName), Generated: s.RealToday().Russian(), RulesVersion: s.rules.Version,
		Disputed: sums.Disputed.Rubles(), LinesTotal: sums.LinesTotal.Rubles(), DisputedCount: sums.DisputedCount,
		Respondents: respondents, HasViolations: len(violations) > 0,
	}
	data.RequestsNo = 2
	if data.HasViolations {
		data.RequestsNo = 3
	}
	t := s.texts
	must := func(key string) string {
		if err != nil {
			return ""
		}
		var v string
		v, err = t.Doc(key, data)
		return v
	}
	list := func(key string) []string {
		if err != nil {
			return nil
		}
		var v []string
		v, err = t.DocList(key, data)
		return v
	}

	in := pdf.Input{
		To:        []string{must("common.to")},
		From:      strings.Split(must("common.from"), "\n"),
		Place:     placeLine(a.City),
		Date:      s.ActToday(a).Russian(),
		Signature: must("common.signature"),
		SignName:  a.CustomerFullName,
		Footer:    must("common.footer"),
		Created:   s.now(),
	}
	if a.IsDemo {
		in.Watermark = must("common.demo_watermark")
	}

	switch d {
	case domain.DecisionSign:
		in.Title = must("cover_letter.title")
		in.Heading = in.Title
		in.Subheading = must("cover_letter.subject")
		in.Paragraphs = list("cover_letter.body")
		in.Closing = []string{must("cover_letter.attachments")}
	case domain.DecisionRefuse:
		in.Title = must("refusal.title")
		in.Heading = in.Title
		in.Subheading = must("refusal.subject")
		in.Paragraphs = list("refusal.intro")
		if len(violations) > 0 {
			in.Sections = append(in.Sections, pdf.Section{Heading: must("refusal.violations_heading"), Bullets: violations})
		}
		evs, e := q.EvidenceForAct(ctx, a.ID)
		if e != nil {
			return in, e
		}
		votes, e := q.VoteSummaries(ctx, a.ID)
		if e != nil {
			return in, e
		}
		codes := map[string][]string{}
		for _, ev := range evs {
			if ev.LineID != nil {
				codes[*ev.LineID] = append(codes[*ev.LineID], ev.Code)
			}
		}
		tbl := &pdf.Table{Widths: []int{1, 5, 2, 3, 3, 5, 2, 3}}
		var full []string
		if tbl.Columns, err = t.DocList("refusal.objections_columns", data); err != nil {
			return in, err
		}
		for i, l := range lines {
			if !l.ReviewStatus.Disputed() {
				continue
			}
			price := dl[i].Amount.Rubles()
			if dl[i].DisputedAmount != nil && *dl[i].DisputedAmount != dl[i].Amount {
				price += "; спорно " + dl[i].DisputedAmount.Rubles()
			}
			res := "—"
			if v := votes[l.ID]; v != nil {
				res = fmt.Sprintf("%d / %d / %d", v.Yes, v.No, v.Unknown)
			}
			comment := orDash(l.Comment)
			if r := []rune(l.Comment); len(r) > cellLimit {
				comment = string(r[:cellLimit-50]) + "… (полностью — ниже)"
				full = append(full, fmt.Sprintf("Строка %d. %s", l.Position, l.Comment))
			}
			tbl.Rows = append(tbl.Rows, []string{
				fmt.Sprint(l.Position), l.WorkName, orDash(l.Unit), price, l.ReviewStatus.Title(), comment,
				orDash(strings.Join(codes[l.ID], ", ")), res,
			})
		}
		objections := pdf.Section{Heading: must("refusal.objections_heading"), Table: tbl, Paragraphs: nil}
		objections.Paragraphs = append(objections.Paragraphs, must("refusal.disputed_total"))
		if sums.Mismatch {
			objections.Paragraphs = append(objections.Paragraphs, must("refusal.mismatch"))
		}
		if respondents > 0 {
			objections.Paragraphs = append(objections.Paragraphs, must("refusal.residents_note"))
		}
		in.Sections = append(in.Sections, objections)
		if len(full) > 0 {
			in.Sections = append(in.Sections, pdf.Section{Heading: must("refusal.full_objections_heading"), Paragraphs: full})
		}
		in.Sections = append(in.Sections,
			pdf.Section{Heading: must("refusal.requests_heading"), Bullets: list("refusal.requests")})

		ev := pdf.Section{Heading: must("refusal.evidence_heading")}
		if len(evs) == 0 {
			ev.Paragraphs = []string{must("refusal.evidence_empty")}
		} else {
			pos := map[string]int{}
			for _, l := range lines {
				pos[l.ID] = l.Position
			}
			for _, e := range evs {
				kind := "фото"
				if e.Mime == "application/pdf" {
					kind = "документ"
				}
				line := ""
				if e.LineID != nil {
					line = fmt.Sprintf(", к строке %d", pos[*e.LineID])
				}
				note := ""
				if e.Note != "" {
					note = ": " + e.Note
				}
				who := authorRoles[e.AuthorRole]
				if who == "" {
					who = e.AuthorRole
				}
				ev.Bullets = append(ev.Bullets, fmt.Sprintf("%s — %s%s; предоставил %s, добавлено %s%s", e.Code, kind, line, who, civil.Of(e.CreatedAt, s.rules.Location).Russian(), note))
			}
		}
		in.Sections = append(in.Sections, ev)
	}
	return in, err
}

// DispatchInput — отметка об отправке документа исполнителю.
type DispatchInput struct {
	Channel     string     `json:"channel"`
	ChannelNote string     `json:"channel_note"`
	SentOn      civil.Date `json:"sent_on"`
}

// Dispatch фиксирует отправку. Подписание закрывает акт, отказ — переводит в ожидание нового акта.
func (s *Service) Dispatch(ctx context.Context, userID, actID string, in DispatchInput) (store.Act, error) {
	if !validChannel(in.Channel) {
		return store.Act{}, errf(http.StatusUnprocessableEntity, "BAD_CHANNEL", "Укажите способ отправки: почта, e-mail, личный кабинет, лично или иное.")
	}
	if len([]rune(in.ChannelNote)) > maxField {
		return store.Act{}, errf(http.StatusUnprocessableEntity, "VALIDATION", "Комментарий к отправке: не больше %d символов.", maxField)
	}
	err := s.withAct(ctx, userID, actID, false, func(q *store.Q, a store.Act, _ store.House) error {
		if a.Status != domain.StatusDecided {
			return errf(http.StatusConflict, "NO_DOCUMENT", "Сначала сформируйте документ: подписание или отказ. Сейчас: %s.", a.Status.Title())
		}
		today := s.ActToday(a)
		if in.SentOn.IsZero() {
			in.SentOn = today
		}
		if in.SentOn.After(today) || in.SentOn.Before(a.ReceivedOn) {
			return errf(http.StatusUnprocessableEntity, "BAD_DATE", "Дата отправки должна быть между датой получения акта и сегодняшним днём.")
		}
		next := domain.StatusClosedSigned
		if a.Decision == domain.DecisionRefuse {
			next = domain.StatusRefusedSent
		}
		docs, err := q.Documents(ctx, a.ID)
		if err != nil {
			return err
		}
		var docID *string
		if len(docs) > 0 {
			docID = &docs[0].ID
		}
		if _, err := q.CreateDispatch(ctx, store.Dispatch{ActID: a.ID, DocumentID: docID, Channel: in.Channel, ChannelNote: strings.TrimSpace(in.ChannelNote), SentOn: in.SentOn}); err != nil {
			return err
		}
		if err := moveStatus(ctx, q, a.ID, a.Status, next, a.Decision); err != nil {
			return err
		}
		if err := q.SkipReminders(ctx, a.ID); err != nil {
			return err
		}
		late := in.SentOn.After(s.rules.Policy.ComputeDeadlines(a.ReceivedOn).ResponseOn)
		return q.AddEvent(ctx, a.ID, "dispatched", map[string]any{"channel": in.Channel, "sent_on": in.SentOn, "after_response_deadline": late}, &userID)
	})
	if err != nil {
		return store.Act{}, err
	}
	return s.st.Q().ActByID(ctx, actID)
}

// Successor создаёт новый акт после отказа: шапка и строки копируются, ранее оспоренные строки помечаются.
func (s *Service) Successor(ctx context.Context, userID, actID string, sourceFileID *string) (store.Act, error) {
	var created store.Act
	err := s.withAct(ctx, userID, actID, false, func(q *store.Q, a store.Act, _ store.House) error {
		if a.Status != domain.StatusRefusedSent {
			return errf(http.StatusConflict, "NOT_REFUSED", "Новый акт создаётся после отправленного отказа. Сейчас: %s.", a.Status.Title())
		}
		n := a
		n.ParentActID = &a.ID
		n.SourceFileID = sourceFileID
		n.Number, n.ActDate, n.TotalAmount, n.TotalAmountWords = "", civil.Date{}, nil, ""
		n.RulesVersion = s.rules.Version
		var err error
		if created, err = q.CreateAct(ctx, n); err != nil {
			return err
		}
		if err := q.CopyLinesForSuccessor(ctx, a.ID, created.ID); err != nil {
			return err
		}
		if err := moveStatus(ctx, q, a.ID, a.Status, domain.StatusReplaced, a.Decision); err != nil {
			return err
		}
		if err := q.AddEvent(ctx, a.ID, "replaced", map[string]any{"successor_id": created.ID}, &userID); err != nil {
			return err
		}
		return q.AddEvent(ctx, created.ID, "act_created", map[string]any{"parent_id": a.ID}, &userID)
	})
	return created, err
}
