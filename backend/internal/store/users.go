package store

import (
	"context"
	"encoding/json"
	"time"

	"priemka/internal/civil"
)

type User struct {
	ID        string     `json:"id"`
	MaxUserID int64      `json:"max_user_id"`
	FirstName string     `json:"first_name"`
	LastName  string     `json:"last_name"`
	ConsentAt *time.Time `json:"consent_at"`
}

func (q *Q) UpsertUser(ctx context.Context, maxID int64, first, last string) (User, error) {
	var u User
	err := q.q.QueryRow(ctx, `
		INSERT INTO users (max_user_id, first_name, last_name) VALUES ($1, $2, $3)
		ON CONFLICT (max_user_id) DO UPDATE
		   SET first_name = CASE WHEN EXCLUDED.first_name <> '' THEN EXCLUDED.first_name ELSE users.first_name END,
		       last_name  = CASE WHEN EXCLUDED.first_name <> '' THEN EXCLUDED.last_name  ELSE users.last_name END
		RETURNING id, max_user_id, first_name, last_name, consent_at`,
		maxID, first, last).Scan(&u.ID, &u.MaxUserID, &u.FirstName, &u.LastName, &u.ConsentAt)
	return u, err
}

func (q *Q) UserByID(ctx context.Context, id string) (User, error) {
	var u User
	err := q.q.QueryRow(ctx, `SELECT id, max_user_id, first_name, last_name, consent_at FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.MaxUserID, &u.FirstName, &u.LastName, &u.ConsentAt)
	return u, notFound(err)
}

func (q *Q) SetConsent(ctx context.Context, userID string) error {
	_, err := q.q.Exec(ctx, `UPDATE users SET consent_at = COALESCE(consent_at, now()) WHERE id = $1`, userID)
	return err
}

// DeleteUser удаляет пользователя и всё, что ему принадлежит (каскадом): дома, акты, строки,
// доказательства, документы, ответы, историю. Строки таблицы files удаляются отдельно — DeleteFiles.
func (q *Q) DeleteUser(ctx context.Context, userID string) error {
	_, err := q.q.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	return err
}

type House struct {
	ID             string `json:"id"`
	ChairmanUserID string `json:"-"`
	City           string `json:"city"`
	Address        string `json:"address"`
	EntrancesCount int    `json:"entrances_count"`
	Timezone       string `json:"timezone"`
	CouncilChatID  *int64 `json:"council_chat_id"`
	IsDemo         bool   `json:"is_demo"`
}

const houseCols = `id, chairman_user_id, city, address, entrances_count, timezone, council_chat_id, is_demo`

func scanHouse(row interface{ Scan(...any) error }) (House, error) {
	var h House
	err := row.Scan(&h.ID, &h.ChairmanUserID, &h.City, &h.Address, &h.EntrancesCount, &h.Timezone, &h.CouncilChatID, &h.IsDemo)
	return h, notFound(err)
}

func (q *Q) HouseByID(ctx context.Context, id string) (House, error) {
	return scanHouse(q.q.QueryRow(ctx, `SELECT `+houseCols+` FROM houses WHERE id = $1`, id))
}

func (q *Q) CreateHouse(ctx context.Context, h House) (House, error) {
	return scanHouse(q.q.QueryRow(ctx, `
		INSERT INTO houses (chairman_user_id, city, address, entrances_count, timezone, is_demo)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+houseCols,
		h.ChairmanUserID, h.City, h.Address, h.EntrancesCount, h.Timezone, h.IsDemo))
}

func (q *Q) UpdateHouse(ctx context.Context, h House) error {
	_, err := q.q.Exec(ctx, `UPDATE houses SET city = $2, address = $3, entrances_count = $4, is_demo = $5 WHERE id = $1`,
		h.ID, h.City, h.Address, h.EntrancesCount, h.IsDemo)
	return err
}

type Profile struct {
	UserID                 string     `json:"-"`
	HouseID                *string    `json:"house_id"`
	FullName               string     `json:"full_name"`
	ApartmentNo            string     `json:"apartment_no"`
	AuthorityType          string     `json:"authority_type"`
	AuthorityDate          civil.Date `json:"authority_date"`
	AuthorityNumber        string     `json:"authority_number"`
	ApplicabilityConfirmed *bool      `json:"applicability_confirmed"`
}

// AuthorityText — строка «на основании …» для формы акта.
func (p Profile) AuthorityText() string {
	var s string
	switch p.AuthorityType {
	case "oss_decision":
		s = "решения общего собрания собственников помещений"
	case "power_of_attorney":
		s = "доверенности"
	default:
		return ""
	}
	if !p.AuthorityDate.IsZero() {
		s += " от " + p.AuthorityDate.Russian()
	}
	if p.AuthorityNumber != "" {
		s += " № " + p.AuthorityNumber
	}
	return s
}

func (q *Q) Profile(ctx context.Context, userID string) (Profile, error) {
	var p Profile
	err := q.q.QueryRow(ctx, `
		SELECT user_id, house_id, full_name, apartment_no, authority_type, authority_date, authority_number, applicability_confirmed
		  FROM chairman_profiles WHERE user_id = $1`, userID).
		Scan(&p.UserID, &p.HouseID, &p.FullName, &p.ApartmentNo, &p.AuthorityType, &p.AuthorityDate, &p.AuthorityNumber, &p.ApplicabilityConfirmed)
	return p, notFound(err)
}

func (q *Q) SaveProfile(ctx context.Context, p Profile) error {
	_, err := q.q.Exec(ctx, `
		INSERT INTO chairman_profiles (user_id, house_id, full_name, apartment_no, authority_type, authority_date, authority_number, applicability_confirmed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id) DO UPDATE SET house_id = $2, full_name = $3, apartment_no = $4, authority_type = $5,
		       authority_date = $6, authority_number = $7, applicability_confirmed = $8, updated_at = now()`,
		p.UserID, p.HouseID, p.FullName, p.ApartmentNo, p.AuthorityType, p.AuthorityDate, p.AuthorityNumber, p.ApplicabilityConfirmed)
	return err
}

type Contract struct {
	ID           string     `json:"id"`
	HouseID      string     `json:"-"`
	Type         string     `json:"type"`
	Number       string     `json:"number"`
	Date         civil.Date `json:"date"`
	EndDate      civil.Date `json:"end_date"`
	ExecutorName string     `json:"executor_name"`
}

// ContractForHouse возвращает последний договор дома.
func (q *Q) ContractForHouse(ctx context.Context, houseID string) (Contract, error) {
	var c Contract
	err := q.q.QueryRow(ctx, `
		SELECT id, house_id, type, number, date, end_date, executor_name FROM contracts
		 WHERE house_id = $1 ORDER BY created_at DESC LIMIT 1`, houseID).
		Scan(&c.ID, &c.HouseID, &c.Type, &c.Number, &c.Date, &c.EndDate, &c.ExecutorName)
	return c, notFound(err)
}

func (q *Q) SaveContract(ctx context.Context, c Contract) (Contract, error) {
	if c.ID == "" {
		err := q.q.QueryRow(ctx, `
			INSERT INTO contracts (house_id, type, number, date, end_date, executor_name)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			c.HouseID, c.Type, c.Number, c.Date, c.EndDate, c.ExecutorName).Scan(&c.ID)
		return c, err
	}
	_, err := q.q.Exec(ctx, `UPDATE contracts SET type = $2, number = $3, date = $4, end_date = $5, executor_name = $6 WHERE id = $1`,
		c.ID, c.Type, c.Number, c.Date, c.EndDate, c.ExecutorName)
	return c, err
}

// Session — состояние диалога с ботом.
type Session struct {
	State string
	Data  map[string]string
}

func (q *Q) Session(ctx context.Context, userID string) (Session, error) {
	var s Session
	var raw []byte
	err := q.q.QueryRow(ctx, `SELECT state, data FROM bot_sessions WHERE user_id = $1`, userID).Scan(&s.State, &raw)
	if err = notFound(err); err == ErrNotFound {
		return Session{Data: map[string]string{}}, nil
	} else if err != nil {
		return s, err
	}
	s.Data = map[string]string{}
	_ = json.Unmarshal(raw, &s.Data)
	return s, nil
}

func (q *Q) SaveSession(ctx context.Context, userID string, s Session) error {
	if s.Data == nil {
		s.Data = map[string]string{}
	}
	raw, _ := json.Marshal(s.Data)
	_, err := q.q.Exec(ctx, `
		INSERT INTO bot_sessions (user_id, state, data) VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET state = $2, data = $3, updated_at = now()`, userID, s.State, raw)
	return err
}
