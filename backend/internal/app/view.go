package app

import (
	"context"
	"errors"

	"priemka/internal/civil"
	"priemka/internal/domain"
	"priemka/internal/rules"
	"priemka/internal/store"
)

// Claim — утверждение для интерфейса: значение, происхождение (норма/расчёт/рекомендация/допущение) и основания.
type Claim struct {
	Code     string          `json:"code"`
	Kind     domain.Kind     `json:"kind"`
	Severity domain.Severity `json:"severity"`
	Text     string          `json:"text"`
	Basis    []rules.Basis   `json:"basis"`
}

type SumsView struct {
	LinesTotal     string  `json:"lines_total"`
	Disputed       string  `json:"disputed"`
	DisputedCount  int     `json:"disputed_count"`
	UncheckedCount int     `json:"unchecked_count"`
	ActTotal       *string `json:"act_total"`
	Mismatch       bool    `json:"mismatch"`
}

type EvidenceView struct {
	store.Evidence
	URL string `json:"url"`
}

type LineView struct {
	store.Line
	Evidence  []EvidenceView     `json:"evidence"`
	Residents *store.VoteSummary `json:"residents"`
}

type DocumentView struct {
	store.Document
	URL string `json:"url"`
}

type InviteView struct {
	Token string `json:"token"`
	Link  string `json:"link"`
}

type DeadlinesView struct {
	ResponseOn civil.Date    `json:"response_on"`
	SilentOn   civil.Date    `json:"silent_on"`
	Timing     domain.Timing `json:"timing"`
	Basis      []rules.Basis `json:"basis"`
}

// ActView — карточка акта для мини-приложения.
type ActView struct {
	Act         store.Act        `json:"act"`
	StatusTitle string           `json:"status_title"`
	House       store.House      `json:"house"`
	Today       civil.Date       `json:"today"`
	Editable    bool             `json:"editable"`
	Deletable   bool             `json:"deletable"`
	Deadlines   *DeadlinesView   `json:"deadlines"`
	Checks      []Claim          `json:"checks"`
	Sums        SumsView         `json:"sums"`
	Lines       []LineView       `json:"lines"`
	Documents   []DocumentView   `json:"documents"`
	Dispatches  []store.Dispatch `json:"dispatches"`
	Events      []store.Event    `json:"events"`
	Invite      *InviteView      `json:"invite"`
	Respondents int              `json:"respondents"`
	SourceFile  *string          `json:"source_file_url"`
	Notes       []Claim          `json:"notes"`
}

func sumsView(s domain.Sums) SumsView {
	v := SumsView{LinesTotal: s.LinesTotal.String(), Disputed: s.Disputed.String(), DisputedCount: s.DisputedCount,
		UncheckedCount: s.UncheckedCount, Mismatch: s.Mismatch}
	if s.ActTotal != nil {
		t := s.ActTotal.String()
		v.ActTotal = &t
	}
	return v
}

func (s *Service) claims(fs []domain.Finding) []Claim {
	out := make([]Claim, 0, len(fs))
	for _, f := range fs {
		out = append(out, Claim{Code: f.Code, Kind: f.Kind, Severity: f.Severity, Text: f.Text, Basis: s.rules.Basis(f.Basis...)})
	}
	return out
}

// ActView собирает карточку акта. Доступ проверяется по председателю дома.
func (s *Service) ActView(ctx context.Context, userID, actID string) (ActView, error) {
	a, h, err := s.ChairmanAct(ctx, userID, actID)
	if err != nil {
		return ActView{}, err
	}
	q := s.st.Q()
	v := ActView{Act: a, StatusTitle: a.Status.Title(), House: h, Today: s.ActToday(a), Editable: a.Status.Editable(), Deletable: ActDeletable(a)}

	if !a.ReceivedOn.IsZero() {
		d := s.rules.Policy.ComputeDeadlines(a.ReceivedOn)
		v.Deadlines = &DeadlinesView{ResponseOn: d.ResponseOn, SilentOn: d.SilentOn,
			Timing: s.rules.Policy.Timing(a.ReceivedOn, v.Today), Basis: s.rules.Basis(d.Basis...)}
	}
	v.Checks = s.claims(s.rules.Policy.CheckExecutor(a.Facts()))

	lines, err := q.Lines(ctx, a.ID)
	if err != nil {
		return v, err
	}
	evs, err := q.EvidenceForAct(ctx, a.ID)
	if err != nil {
		return v, err
	}
	votes, err := q.VoteSummaries(ctx, a.ID)
	if err != nil {
		return v, err
	}
	byLine := map[string][]EvidenceView{}
	for _, e := range evs {
		ev := EvidenceView{Evidence: e}
		if e.FileID != nil {
			ev.URL = s.FileURL(*e.FileID)
		}
		if e.LineID != nil {
			byLine[*e.LineID] = append(byLine[*e.LineID], ev)
		}
	}
	dl := make([]domain.Line, 0, len(lines))
	v.Lines = make([]LineView, 0, len(lines))
	for _, l := range lines {
		lv := LineView{Line: l, Evidence: byLine[l.ID], Residents: votes[l.ID]}
		if lv.Evidence == nil {
			lv.Evidence = []EvidenceView{}
		}
		v.Lines = append(v.Lines, lv)
		dl = append(dl, l.Domain())
	}
	v.Sums = sumsView(domain.Summarize(dl, a.Total()))

	docs, err := q.Documents(ctx, a.ID)
	if err != nil {
		return v, err
	}
	for _, d := range docs {
		v.Documents = append(v.Documents, DocumentView{Document: d, URL: s.FileURL(d.FileID)})
	}
	if v.Documents == nil {
		v.Documents = []DocumentView{}
	}
	if v.Dispatches, err = q.Dispatches(ctx, a.ID); err != nil {
		return v, err
	}
	if v.Events, err = q.Events(ctx, a.ID); err != nil {
		return v, err
	}
	inv, err := q.ActiveInvite(ctx, a.ID)
	if err == nil {
		v.Invite = &InviteView{Token: inv.Token, Link: s.InviteLink(inv.Token)}
	} else if !errors.Is(err, store.ErrNotFound) {
		return v, err
	}
	if v.Respondents, err = q.CountRespondents(ctx, a.ID); err != nil {
		return v, err
	}
	if a.SourceFileID != nil {
		u := s.FileURL(*a.SourceFileID)
		v.SourceFile = &u
	}
	v.Notes = s.claims([]domain.Finding{{
		Code: "no_remarks_option", Kind: domain.KindNorm, Severity: domain.SevInfo, Basis: []string{domain.RuleNoRemarksInForm},
		Text: "Подписать акт «с замечаниями» нельзя: либо подписание (работы приняты полностью), либо обоснованный отказ.",
	}})
	return v, nil
}

// ActSummary — строка списка актов.
type ActSummary struct {
	store.Act
	StatusTitle string         `json:"status_title"`
	Deadlines   *DeadlinesView `json:"deadlines"`
}

func (s *Service) ActList(ctx context.Context, userID string) ([]ActSummary, error) {
	acts, err := s.ActsOfUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]ActSummary, 0, len(acts))
	for _, a := range acts {
		sm := ActSummary{Act: a, StatusTitle: a.Status.Title()}
		if !a.ReceivedOn.IsZero() {
			d := s.rules.Policy.ComputeDeadlines(a.ReceivedOn)
			sm.Deadlines = &DeadlinesView{ResponseOn: d.ResponseOn, SilentOn: d.SilentOn, Timing: s.rules.Policy.Timing(a.ReceivedOn, s.ActToday(a)), Basis: []rules.Basis{}}
		}
		out = append(out, sm)
	}
	return out, nil
}
