package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"priemka/internal/civil"
	"priemka/internal/files"
	"priemka/internal/store"
)

// InvitePrefix — префикс start_param для приглашения жильца.
const InvitePrefix = "inv_"

// InviteLink — диплинк на мини-приложение бота. Текстовая ссылка, а не кнопка: при пересылке кнопки теряются.
func (s *Service) InviteLink(token string) string {
	return fmt.Sprintf("https://max.ru/%s?startapp=%s%s", s.cfg.BotUsername, InvitePrefix, token)
}

// InviteText — готовый текст для пересылки в чат дома.
func (s *Service) InviteText(a store.Act, link string) string {
	period := ""
	if !a.PeriodFrom.IsZero() && !a.PeriodTo.IsZero() {
		period = fmt.Sprintf(" за период %s — %s", a.PeriodFrom.Russian(), a.PeriodTo.Russian())
	}
	return fmt.Sprintf("Соседи! Управляющая компания отчиталась о работах по дому%s. "+
		"Помогите совету дома проверить, что работы действительно выполнены: откройте ссылку, выберите свой подъезд и отметьте «было» или «не было». "+
		"Это займёт 2 минуты, ваши имена в документы не попадают.\n\n%s", period, link)
}

// CreateInvite возвращает действующее приглашение акта или создаёт новое.
func (s *Service) CreateInvite(ctx context.Context, userID, actID string) (store.Invite, error) {
	a, _, err := s.ChairmanAct(ctx, userID, actID)
	if err != nil {
		return store.Invite{}, err
	}
	if a.Status.Final() {
		return store.Invite{}, errf(http.StatusConflict, "ACT_LOCKED", "Акт закрыт: %s.", a.Status.Title())
	}
	q := s.st.Q()
	inv, err := q.ActiveInvite(ctx, a.ID)
	if err == nil {
		return inv, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return inv, err
	}
	inv, err = q.CreateInvite(ctx, a.ID, files.RandomToken(16), s.now().Add(s.cfg.InviteTTL))
	if err != nil {
		return inv, err
	}
	return inv, q.AddEvent(ctx, a.ID, "invite_created", nil, &userID)
}

// ResidentLine — строка акта глазами жильца: без цен.
type ResidentLine struct {
	ID             string  `json:"id"`
	Position       int     `json:"position"`
	WorkName       string  `json:"work_name"`
	PeriodicityQty string  `json:"periodicity_qty"`
	Entrances      []int32 `json:"entrances"`
}

type ResidentView struct {
	ActNumber      string         `json:"act_number"`
	Address        string         `json:"address"`
	PeriodFrom     civil.Date     `json:"period_from"`
	PeriodTo       civil.Date     `json:"period_to"`
	EntrancesCount int            `json:"entrances_count"`
	Open           bool           `json:"open"`
	IsDemo         bool           `json:"is_demo"`
	Consented      bool           `json:"consented"`
	Lines          []ResidentLine `json:"lines"`
	MyVotes        []store.Vote   `json:"my_votes"`
}

func (s *Service) inviteAct(ctx context.Context, token string) (store.Invite, store.Act, store.House, error) {
	q := s.st.Q()
	inv, err := q.InviteByToken(ctx, token)
	if errors.Is(err, store.ErrNotFound) {
		return inv, store.Act{}, store.House{}, errf(http.StatusNotFound, "INVITE_NOT_FOUND", "Ссылка недействительна. Попросите председателя прислать новую.")
	} else if err != nil {
		return inv, store.Act{}, store.House{}, err
	}
	if inv.RevokedAt != nil || s.now().After(inv.ExpiresAt) {
		return inv, store.Act{}, store.House{}, errf(http.StatusGone, "INVITE_EXPIRED", "Срок действия ссылки истёк. Попросите председателя прислать новую.")
	}
	a, err := q.ActByID(ctx, inv.ActID)
	if err != nil {
		return inv, a, store.House{}, err
	}
	h, err := q.HouseByID(ctx, a.HouseID)
	return inv, a, h, err
}

func (s *Service) ResidentView(ctx context.Context, userID, token string) (ResidentView, error) {
	_, a, h, err := s.inviteAct(ctx, token)
	if err != nil {
		return ResidentView{}, err
	}
	q := s.st.Q()
	u, err := q.UserByID(ctx, userID)
	if err != nil {
		return ResidentView{}, err
	}
	lines, err := q.Lines(ctx, a.ID)
	if err != nil {
		return ResidentView{}, err
	}
	v := ResidentView{ActNumber: a.Number, Address: a.Address, PeriodFrom: a.PeriodFrom, PeriodTo: a.PeriodTo,
		EntrancesCount: h.EntrancesCount, Open: a.Status.Editable(), IsDemo: a.IsDemo, Consented: u.ConsentAt != nil, Lines: []ResidentLine{}}
	for _, l := range lines {
		if l.ResidentVisible {
			v.Lines = append(v.Lines, ResidentLine{ID: l.ID, Position: l.Position, WorkName: l.WorkName, PeriodicityQty: l.PeriodicityQty, Entrances: l.Entrances})
		}
	}
	if v.MyVotes, err = q.VotesOfUser(ctx, a.ID, userID); err != nil {
		return v, err
	}
	return v, nil
}

// VotesInput — ответы жильца.
type VotesInput struct {
	Consent    bool         `json:"consent"`
	EntranceNo int          `json:"entrance_no"`
	Votes      []store.Vote `json:"votes"`
}

func (s *Service) SubmitVotes(ctx context.Context, userID, token string, in VotesInput) (ResidentView, error) {
	_, a, h, err := s.inviteAct(ctx, token)
	if err != nil {
		return ResidentView{}, err
	}
	if !a.Status.Editable() {
		return ResidentView{}, errf(http.StatusConflict, "ACT_LOCKED", "Проверка этого акта уже завершена. Спасибо!")
	}
	q := s.st.Q()
	u, err := q.UserByID(ctx, userID)
	if err != nil {
		return ResidentView{}, err
	}
	if u.ConsentAt == nil && !in.Consent {
		return ResidentView{}, errf(http.StatusUnprocessableEntity, "CONSENT_REQUIRED", "Нужно согласие на обработку ответов, чтобы учесть их.")
	}
	if in.EntranceNo < 1 || in.EntranceNo > h.EntrancesCount {
		return ResidentView{}, errf(http.StatusUnprocessableEntity, "VALIDATION", "Выберите подъезд от 1 до %d.", h.EntrancesCount)
	}
	if len(in.Votes) == 0 {
		return ResidentView{}, errf(http.StatusUnprocessableEntity, "VALIDATION", "Отметьте хотя бы одну работу.")
	}
	lines, err := q.Lines(ctx, a.ID)
	if err != nil {
		return ResidentView{}, err
	}
	allowed := map[string]store.Line{}
	for _, l := range lines {
		if l.ResidentVisible {
			allowed[l.ID] = l
		}
	}
	for i, v := range in.Votes {
		l, ok := allowed[v.LineID]
		if !ok {
			return ResidentView{}, errf(http.StatusUnprocessableEntity, "VALIDATION", "Строка %s недоступна для ответа.", v.LineID)
		}
		if len(l.Entrances) > 0 && !slices.Contains(l.Entrances, int32(in.EntranceNo)) {
			return ResidentView{}, errf(http.StatusUnprocessableEntity, "VALIDATION", "Работа «%s» не относится к подъезду %d.", l.WorkName, in.EntranceNo)
		}
		if v.Answer != "yes" && v.Answer != "no" && v.Answer != "unknown" {
			return ResidentView{}, errf(http.StatusUnprocessableEntity, "VALIDATION", "Ответ: yes, no или unknown.")
		}
		if len([]rune(v.Comment)) > 500 {
			return ResidentView{}, errf(http.StatusUnprocessableEntity, "VALIDATION", "Комментарий: не больше 500 символов.")
		}
		in.Votes[i].EntranceNo = in.EntranceNo
		in.Votes[i].Comment = strings.TrimSpace(v.Comment)
	}
	err = s.st.Tx(ctx, func(q *store.Q) error {
		if in.Consent {
			if err := q.SetConsent(ctx, userID); err != nil {
				return err
			}
		}
		for _, v := range in.Votes {
			if err := q.UpsertVote(ctx, userID, v); err != nil {
				return err
			}
		}
		return q.AddEvent(ctx, a.ID, "resident_votes", map[string]any{"entrance": in.EntranceNo, "count": len(in.Votes)}, nil)
	})
	if err != nil {
		return ResidentView{}, err
	}
	return s.ResidentView(ctx, userID, token)
}

// inviteTTL по умолчанию — до окончания срока молчаливой приёмки с запасом.
func DefaultInviteTTL() time.Duration { return 45 * 24 * time.Hour }
