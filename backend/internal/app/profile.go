package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"priemka/internal/civil"
	"priemka/internal/domain"
	"priemka/internal/store"
)

// DemoData — синтетические данные из config/demo/demo-act.json.
type DemoData struct {
	House struct {
		City           string `json:"city"`
		Address        string `json:"address"`
		EntrancesCount int    `json:"entrances_count"`
	} `json:"house"`
	Chairman struct {
		FullName        string     `json:"full_name"`
		ApartmentNo     string     `json:"apartment_no"`
		AuthorityType   string     `json:"authority_type"`
		AuthorityDate   civil.Date `json:"authority_date"`
		AuthorityNumber string     `json:"authority_number"`
	} `json:"chairman"`
	Contract struct {
		Type         string     `json:"type"`
		Number       string     `json:"number"`
		Date         civil.Date `json:"date"`
		EndDate      civil.Date `json:"end_date"`
		ExecutorName string     `json:"executor_name"`
	} `json:"contract"`
	Act struct {
		Number                    string `json:"number"`
		ActDateOffsetDays         int    `json:"act_date_offset_days"`
		ExecutorSignatoryName     string `json:"executor_signatory_name"`
		ExecutorSignatoryPosition string `json:"executor_signatory_position"`
		ExecutorBasis             string `json:"executor_basis"`
		TotalAmount               string `json:"total_amount"`
		TotalAmountWords          string `json:"total_amount_words"`
	} `json:"act"`
	Lines []struct {
		WorkName        string `json:"work_name"`
		PP290Ref        string `json:"pp290_ref"`
		PeriodicityQty  string `json:"periodicity_qty"`
		Unit            string `json:"unit"`
		UnitPrice       string `json:"unit_price"`
		Amount          string `json:"amount"`
		ResidentVisible *bool  `json:"resident_visible"`
	} `json:"lines"`
}

func LoadDemo(configDir string) (*DemoData, error) {
	raw, err := os.ReadFile(filepath.Join(configDir, "demo", "demo-act.json"))
	if err != nil {
		return nil, fmt.Errorf("демо-данные: %w", err)
	}
	var d DemoData
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("демо-данные: %w", err)
	}
	return &d, nil
}

// ProfileBundle — всё, что известно о председателе.
type ProfileBundle struct {
	User     store.User      `json:"user"`
	Profile  *store.Profile  `json:"profile"`
	House    *store.House    `json:"house"`
	Contract *store.Contract `json:"contract"`
}

// Complete — профиль заполнен достаточно, чтобы принимать акты.
func (b ProfileBundle) Complete() bool {
	return b.Profile != nil && b.House != nil && b.Profile.FullName != "" && b.Profile.AuthorityType != ""
}

func (s *Service) Profile(ctx context.Context, userID string) (ProfileBundle, error) {
	q := s.st.Q()
	var b ProfileBundle
	u, err := q.UserByID(ctx, userID)
	if err != nil {
		return b, err
	}
	b.User = u
	p, err := q.Profile(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return b, nil
	} else if err != nil {
		return b, err
	}
	b.Profile = &p
	if p.HouseID == nil {
		return b, nil
	}
	h, err := q.HouseByID(ctx, *p.HouseID)
	if err != nil {
		return b, err
	}
	b.House = &h
	c, err := q.ContractForHouse(ctx, h.ID)
	if err == nil {
		b.Contract = &c
	} else if !errors.Is(err, store.ErrNotFound) {
		return b, err
	}
	return b, nil
}

// ProfileInput — данные онбординга председателя.
type ProfileInput struct {
	FullName        string     `json:"full_name"`
	ApartmentNo     string     `json:"apartment_no"`
	AuthorityType   string     `json:"authority_type"`
	AuthorityDate   civil.Date `json:"authority_date"`
	AuthorityNumber string     `json:"authority_number"`
	Applicable      *bool      `json:"applicability_confirmed"`
	City            string     `json:"city"`
	Address         string     `json:"address"`
	EntrancesCount  int        `json:"entrances_count"`
	ExecutorName    string     `json:"executor_name"`
	ContractType    string     `json:"contract_type"`
	ContractNumber  string     `json:"contract_number"`
	ContractDate    civil.Date `json:"contract_date"`
	ContractEndDate civil.Date `json:"contract_end_date"`
	Demo            bool       `json:"-"`
}

func (in ProfileInput) validate() error {
	var bad []string
	if strings.TrimSpace(in.FullName) == "" {
		bad = append(bad, "ФИО")
	}
	if in.AuthorityType != "oss_decision" && in.AuthorityType != "power_of_attorney" {
		bad = append(bad, "основание полномочий")
	}
	if strings.TrimSpace(in.Address) == "" {
		bad = append(bad, "адрес дома")
	}
	if in.EntrancesCount < 1 || in.EntrancesCount > 50 {
		bad = append(bad, "число подъездов (1–50)")
	}
	for _, f := range []struct{ v, name string }{
		{in.FullName, "ФИО"}, {in.ApartmentNo, "квартира"}, {in.AuthorityNumber, "номер документа"}, {in.City, "город"},
		{in.Address, "адрес"}, {in.ExecutorName, "исполнитель"}, {in.ContractNumber, "номер договора"},
	} {
		if len([]rune(f.v)) > maxField {
			bad = append(bad, f.name+" (длиннее "+strconv.Itoa(maxField)+" символов)")
		}
	}
	switch in.ContractType {
	case "", "management", "services", "repair":
	default:
		bad = append(bad, "вид договора")
	}
	if len(bad) > 0 {
		return &Error{Status: http.StatusUnprocessableEntity, Code: "VALIDATION", Message: "Проверьте поля: " + strings.Join(bad, ", ") + "."}
	}
	return nil
}

// SaveProfile создаёт или обновляет профиль председателя, дом и договор.
func (s *Service) SaveProfile(ctx context.Context, userID string, in ProfileInput) (ProfileBundle, error) {
	if err := in.validate(); err != nil {
		return ProfileBundle{}, err
	}
	if in.ContractType == "" {
		in.ContractType = "management"
	}
	err := s.st.Tx(ctx, func(q *store.Q) error {
		p, err := q.Profile(ctx, userID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		p.UserID = userID
		p.FullName, p.ApartmentNo = strings.TrimSpace(in.FullName), strings.TrimSpace(in.ApartmentNo)
		p.AuthorityType, p.AuthorityDate, p.AuthorityNumber = in.AuthorityType, in.AuthorityDate, strings.TrimSpace(in.AuthorityNumber)
		p.ApplicabilityConfirmed = in.Applicable

		var h store.House
		if p.HouseID != nil {
			if h, err = q.HouseByID(ctx, *p.HouseID); err != nil {
				return err
			}
			h.City, h.Address, h.EntrancesCount, h.IsDemo = in.City, in.Address, in.EntrancesCount, in.Demo
			if err := q.UpdateHouse(ctx, h); err != nil {
				return err
			}
		} else {
			h, err = q.CreateHouse(ctx, store.House{ChairmanUserID: userID, City: in.City, Address: in.Address,
				EntrancesCount: in.EntrancesCount, Timezone: s.rules.Params.Timezone, IsDemo: in.Demo})
			if err != nil {
				return err
			}
			p.HouseID = &h.ID
		}
		if err := q.SaveProfile(ctx, p); err != nil {
			return err
		}
		c, err := q.ContractForHouse(ctx, h.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		c.HouseID, c.Type, c.Number, c.Date, c.EndDate, c.ExecutorName = h.ID, in.ContractType, in.ContractNumber, in.ContractDate, in.ContractEndDate, in.ExecutorName
		_, err = q.SaveContract(ctx, c)
		return err
	})
	if err != nil {
		return ProfileBundle{}, err
	}
	return s.Profile(ctx, userID)
}

// DeleteUserData удаляет пользователя и все его данные, включая файлы на диске (команда /delete).
func (s *Service) DeleteUserData(ctx context.Context, userID string) error {
	var fs []store.File
	err := s.st.Tx(ctx, func(q *store.Q) error {
		var err error
		if fs, err = q.UserFiles(ctx, userID); err != nil {
			return err
		}
		if err := q.DeleteUser(ctx, userID); err != nil {
			return err
		}
		return q.DeleteFiles(ctx, fileIDs(fs))
	})
	if err != nil {
		return err
	}
	s.removeFiles(fs)
	s.log.Info("данные пользователя удалены", "files", len(fs))
	return nil
}

// FillDemoProfile заполняет профиль синтетическими данными (только в демо-режиме).
func (s *Service) FillDemoProfile(ctx context.Context, userID string) (ProfileBundle, error) {
	if !s.cfg.DemoMode {
		return ProfileBundle{}, errf(http.StatusForbidden, "DEMO_DISABLED", "Демо-режим выключен.")
	}
	return s.fillDemoProfile(ctx, userID)
}

func (s *Service) fillDemoProfile(ctx context.Context, userID string) (ProfileBundle, error) {
	d := s.demo
	yes := true
	return s.SaveProfile(ctx, userID, ProfileInput{
		FullName: d.Chairman.FullName, ApartmentNo: d.Chairman.ApartmentNo, AuthorityType: d.Chairman.AuthorityType,
		AuthorityDate: d.Chairman.AuthorityDate, AuthorityNumber: d.Chairman.AuthorityNumber, Applicable: &yes,
		City: d.House.City, Address: d.House.Address, EntrancesCount: d.House.EntrancesCount,
		ExecutorName: d.Contract.ExecutorName, ContractType: d.Contract.Type, ContractNumber: d.Contract.Number,
		ContractDate: d.Contract.Date, ContractEndDate: d.Contract.EndDate, Demo: true,
	})
}

// headerFromProfile — шапка нового акта из профиля: председатель вводит её один раз.
func headerFromProfile(b ProfileBundle) store.Act {
	a := store.Act{
		HouseID: b.House.ID, City: b.House.City, Address: b.House.Address,
		CustomerFullName: b.Profile.FullName, CustomerApartment: b.Profile.ApartmentNo,
		CustomerAuthorityText: b.Profile.AuthorityText(), ContractType: "management",
	}
	if b.Contract != nil {
		a.ContractID = &b.Contract.ID
		a.ExecutorName, a.ContractType, a.ContractNumber = b.Contract.ExecutorName, b.Contract.Type, b.Contract.Number
		a.ContractDate, a.ContractEndDate = b.Contract.Date, b.Contract.EndDate
	}
	return a
}

// CreateAct создаёт черновик акта по полученному файлу.
func (s *Service) CreateAct(ctx context.Context, userID string, sourceFileID *string) (store.Act, error) {
	b, err := s.Profile(ctx, userID)
	if err != nil {
		return store.Act{}, err
	}
	if !b.Complete() {
		return store.Act{}, ErrNoProfile
	}
	a := headerFromProfile(b)
	a.SourceFileID = sourceFileID
	a.RulesVersion = s.rules.Version
	a.IsDemo = b.House.IsDemo
	var created store.Act
	err = s.st.Tx(ctx, func(q *store.Q) error {
		created, err = q.CreateAct(ctx, a)
		if err != nil {
			return err
		}
		return q.AddEvent(ctx, created.ID, "act_created", map[string]any{"has_file": sourceFileID != nil}, &userID)
	})
	return created, err
}

// CreateDemoAct создаёт акт с синтетическими строками. Если профиля нет — заполняет демо-профиль.
func (s *Service) CreateDemoAct(ctx context.Context, userID string, sourceFileID *string) (store.Act, error) {
	if !s.cfg.DemoMode {
		return store.Act{}, errf(http.StatusForbidden, "DEMO_DISABLED", "Демо-режим выключен.")
	}
	return s.createDemoAct(ctx, userID, sourceFileID)
}

func (s *Service) createDemoAct(ctx context.Context, userID string, sourceFileID *string) (store.Act, error) {
	b, err := s.Profile(ctx, userID)
	if err != nil {
		return store.Act{}, err
	}
	if !b.Complete() {
		if b, err = s.fillDemoProfile(ctx, userID); err != nil {
			return store.Act{}, err
		}
	}
	d := s.demo
	today := s.RealToday()
	firstOfMonth := civil.New(today.Time().Year(), today.Time().Month(), 1)
	a := headerFromProfile(b)
	a.SourceFileID = sourceFileID
	a.IsDemo = true
	a.RulesVersion = s.rules.Version
	a.Number = d.Act.Number
	a.ActDate = today.AddDays(d.Act.ActDateOffsetDays)
	a.PeriodFrom = civil.Of(firstOfMonth.Time().AddDate(0, -1, 0), s.rules.Location)
	a.PeriodTo = firstOfMonth.AddDays(-1)
	// Демо-период не должен начинаться раньше, чем порядок приёмки начал применяться.
	if from := s.rules.Policy.ApplicableFrom; !from.IsZero() && a.PeriodFrom.Before(from) {
		a.PeriodFrom, a.PeriodTo = from, a.ActDate.AddDays(-2)
	}
	a.ExecutorSignatoryName, a.ExecutorSignatoryPosition, a.ExecutorBasis = d.Act.ExecutorSignatoryName, d.Act.ExecutorSignatoryPosition, d.Act.ExecutorBasis
	if a.ExecutorName == "" || !b.House.IsDemo {
		a.ExecutorName = d.Contract.ExecutorName
	}
	total := d.Act.TotalAmount
	a.TotalAmount, a.TotalAmountWords = &total, d.Act.TotalAmountWords

	var created store.Act
	err = s.st.Tx(ctx, func(q *store.Q) error {
		created, err = q.CreateAct(ctx, a)
		if err != nil {
			return err
		}
		for _, l := range d.Lines {
			price := l.UnitPrice
			visible := l.ResidentVisible == nil || *l.ResidentVisible
			if _, err := q.InsertLine(ctx, store.Line{ActID: created.ID, WorkName: l.WorkName, PP290Ref: l.PP290Ref,
				PeriodicityQty: l.PeriodicityQty, Unit: l.Unit, UnitPrice: &price, Amount: l.Amount,
				ReviewStatus: domain.ReviewUnchecked, ResidentVisible: visible}); err != nil {
				return err
			}
		}
		return q.AddEvent(ctx, created.ID, "act_created", map[string]any{"demo": true}, &userID)
	})
	return created, err
}

// ActsOfUser — акты дома председателя, новые сверху.
func (s *Service) ActsOfUser(ctx context.Context, userID string) ([]store.Act, error) {
	b, err := s.Profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if b.House == nil {
		return []store.Act{}, nil
	}
	acts, err := s.st.Q().ActsByHouse(ctx, b.House.ID)
	if acts == nil {
		acts = []store.Act{}
	}
	return acts, err
}

// CurrentAct — последний незакрытый акт председателя (для бота).
func (s *Service) CurrentAct(ctx context.Context, userID string) (*store.Act, error) {
	acts, err := s.ActsOfUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range acts {
		if !acts[i].Status.Final() {
			return &acts[i], nil
		}
	}
	return nil, nil
}
