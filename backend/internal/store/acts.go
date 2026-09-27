package store

import (
	"context"
	"encoding/json"
	"time"

	"priemka/internal/civil"
	"priemka/internal/domain"
)

// Act — карточка акта. Поля шапки повторяют форму приказа Минстроя № 761/пр.
type Act struct {
	ID           string  `json:"id"`
	HouseID      string  `json:"house_id"`
	ContractID   *string `json:"-"`
	ParentActID  *string `json:"parent_act_id"`
	SourceFileID *string `json:"source_file_id"`

	Number                    string     `json:"number"`
	ActDate                   civil.Date `json:"act_date"`
	City                      string     `json:"city"`
	Address                   string     `json:"address"`
	CustomerFullName          string     `json:"customer_full_name"`
	CustomerApartment         string     `json:"customer_apartment"`
	CustomerAuthorityText     string     `json:"customer_authority_text"`
	ExecutorName              string     `json:"executor_name"`
	ExecutorSignatoryName     string     `json:"executor_signatory_name"`
	ExecutorSignatoryPosition string     `json:"executor_signatory_position"`
	ExecutorBasis             string     `json:"executor_basis"`
	ContractType              string     `json:"contract_type"`
	ContractNumber            string     `json:"contract_number"`
	ContractDate              civil.Date `json:"contract_date"`
	ContractEndDate           civil.Date `json:"contract_end_date"`
	PeriodFrom                civil.Date `json:"period_from"`
	PeriodTo                  civil.Date `json:"period_to"`
	TotalAmount               *string    `json:"total_amount"`
	TotalAmountWords          string     `json:"total_amount_words"`
	CopiesReceived            *int       `json:"copies_received"`
	ExecutorSigned            *bool      `json:"executor_signed"`

	ReceivedOn         civil.Date      `json:"received_on"`
	ReceivedChannel    string          `json:"received_channel"`
	ExecutorSentOn     civil.Date      `json:"executor_sent_on"`
	Status             domain.Status   `json:"status"`
	Decision           domain.Decision `json:"decision"`
	DeadlineResponseOn civil.Date      `json:"-"`
	DeadlineSilentOn   civil.Date      `json:"-"`
	RulesVersion       string          `json:"rules_version"`
	IsDemo             bool            `json:"is_demo"`
	DemoShiftDays      int             `json:"demo_shift_days"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

// Facts — данные для проверок сроков исполнителя.
func (a Act) Facts() domain.ActFacts {
	return domain.ActFacts{
		ActDate: a.ActDate, PeriodFrom: a.PeriodFrom, PeriodTo: a.PeriodTo, ContractEndDate: a.ContractEndDate,
		ReceivedOn: a.ReceivedOn, ExecutorSentOn: a.ExecutorSentOn,
		CopiesReceived: a.CopiesReceived, ExecutorSigned: a.ExecutorSigned,
	}
}

// Total — итог п. 2 акта в копейках (nil, если не указан).
func (a Act) Total() *domain.Kopecks {
	if a.TotalAmount == nil {
		return nil
	}
	k, err := domain.ParseMoney(*a.TotalAmount)
	if err != nil {
		return nil
	}
	return &k
}

const actCols = `id, house_id, contract_id, parent_act_id, source_file_id,
	number, act_date, city, address, customer_full_name, customer_apartment, customer_authority_text,
	executor_name, executor_signatory_name, executor_signatory_position, executor_basis,
	contract_type, contract_number, contract_date, contract_end_date, period_from, period_to,
	total_amount::text, total_amount_words, copies_received, executor_signed,
	received_on, received_channel, executor_sent_on, status, decision, deadline_response_on, deadline_silent_on,
	rules_version, is_demo, demo_shift_days, created_at, updated_at`

func scanAct(row interface{ Scan(...any) error }) (Act, error) {
	var a Act
	err := row.Scan(&a.ID, &a.HouseID, &a.ContractID, &a.ParentActID, &a.SourceFileID,
		&a.Number, &a.ActDate, &a.City, &a.Address, &a.CustomerFullName, &a.CustomerApartment, &a.CustomerAuthorityText,
		&a.ExecutorName, &a.ExecutorSignatoryName, &a.ExecutorSignatoryPosition, &a.ExecutorBasis,
		&a.ContractType, &a.ContractNumber, &a.ContractDate, &a.ContractEndDate, &a.PeriodFrom, &a.PeriodTo,
		&a.TotalAmount, &a.TotalAmountWords, &a.CopiesReceived, &a.ExecutorSigned,
		&a.ReceivedOn, &a.ReceivedChannel, &a.ExecutorSentOn, &a.Status, &a.Decision, &a.DeadlineResponseOn, &a.DeadlineSilentOn,
		&a.RulesVersion, &a.IsDemo, &a.DemoShiftDays, &a.CreatedAt, &a.UpdatedAt)
	return a, notFound(err)
}

func (q *Q) CreateAct(ctx context.Context, a Act) (Act, error) {
	return scanAct(q.q.QueryRow(ctx, `
		INSERT INTO acts (house_id, contract_id, parent_act_id, source_file_id, number, act_date, city, address,
		    customer_full_name, customer_apartment, customer_authority_text, executor_name, executor_signatory_name,
		    executor_signatory_position, executor_basis, contract_type, contract_number, contract_date, contract_end_date,
		    period_from, period_to, total_amount, total_amount_words, rules_version, is_demo)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22::numeric, $23, $24, $25)
		RETURNING `+actCols,
		a.HouseID, a.ContractID, a.ParentActID, a.SourceFileID, a.Number, a.ActDate, a.City, a.Address,
		a.CustomerFullName, a.CustomerApartment, a.CustomerAuthorityText, a.ExecutorName, a.ExecutorSignatoryName,
		a.ExecutorSignatoryPosition, a.ExecutorBasis, a.ContractType, a.ContractNumber, a.ContractDate, a.ContractEndDate,
		a.PeriodFrom, a.PeriodTo, a.TotalAmount, a.TotalAmountWords, a.RulesVersion, a.IsDemo))
}

func (q *Q) ActByID(ctx context.Context, id string) (Act, error) {
	return scanAct(q.q.QueryRow(ctx, `SELECT `+actCols+` FROM acts WHERE id = $1`, id))
}

// ActForUpdate блокирует строку акта до конца транзакции.
// DeleteAct удаляет акт; строки, доказательства, документы, отправки, напоминания и история удаляются каскадом,
// у следующего акта цепочки ссылка на прежний обнуляется. Записи файлов удаляет вызывающий (DeleteFiles).
func (q *Q) DeleteAct(ctx context.Context, id string) error {
	_, err := q.q.Exec(ctx, `DELETE FROM acts WHERE id = $1`, id)
	return err
}

func (q *Q) ActForUpdate(ctx context.Context, id string) (Act, error) {
	return scanAct(q.q.QueryRow(ctx, `SELECT `+actCols+` FROM acts WHERE id = $1 FOR UPDATE`, id))
}

func (q *Q) ActsByHouse(ctx context.Context, houseID string) ([]Act, error) {
	rows, err := q.q.Query(ctx, `SELECT `+actCols+` FROM acts WHERE house_id = $1 ORDER BY created_at DESC`, houseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Act
	for rows.Next() {
		a, err := scanAct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActsToAdvance — незакрытые акты с датой получения, для планировщика.
func (q *Q) ActsToAdvance(ctx context.Context) ([]Act, error) {
	rows, err := q.q.Query(ctx, `SELECT `+actCols+` FROM acts WHERE status IN ('in_review', 'decided') AND received_on IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Act
	for rows.Next() {
		a, err := scanAct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SaveActHeader сохраняет поля шапки и данные о получении.
func (q *Q) SaveActHeader(ctx context.Context, a Act) error {
	_, err := q.q.Exec(ctx, `
		UPDATE acts SET number = $2, act_date = $3, city = $4, address = $5, customer_full_name = $6,
		    customer_apartment = $7, customer_authority_text = $8, executor_name = $9, executor_signatory_name = $10,
		    executor_signatory_position = $11, executor_basis = $12, contract_type = $13, contract_number = $14,
		    contract_date = $15, contract_end_date = $16, period_from = $17, period_to = $18, total_amount = $19::numeric,
		    total_amount_words = $20, copies_received = $21, executor_signed = $22, received_on = $23,
		    received_channel = $24, executor_sent_on = $25, updated_at = now()
		 WHERE id = $1`,
		a.ID, a.Number, a.ActDate, a.City, a.Address, a.CustomerFullName, a.CustomerApartment, a.CustomerAuthorityText,
		a.ExecutorName, a.ExecutorSignatoryName, a.ExecutorSignatoryPosition, a.ExecutorBasis, a.ContractType,
		a.ContractNumber, a.ContractDate, a.ContractEndDate, a.PeriodFrom, a.PeriodTo, a.TotalAmount, a.TotalAmountWords,
		a.CopiesReceived, a.ExecutorSigned, a.ReceivedOn, a.ReceivedChannel, a.ExecutorSentOn)
	return err
}

// SetActStatus меняет статус, только если он всё ещё равен from; false — статус успели изменить.
func (q *Q) SetActStatus(ctx context.Context, id string, from, to domain.Status, dec domain.Decision) (bool, error) {
	tag, err := q.q.Exec(ctx, `UPDATE acts SET status = $3, decision = $4, updated_at = now() WHERE id = $1 AND status = $2`, id, from, to, dec)
	return tag.RowsAffected() == 1, err
}

func (q *Q) SetDeadlines(ctx context.Context, id string, d domain.Deadlines, rulesVersion string) error {
	_, err := q.q.Exec(ctx, `UPDATE acts SET deadline_response_on = $2, deadline_silent_on = $3, rules_version = $4, updated_at = now() WHERE id = $1`,
		id, d.ResponseOn, d.SilentOn, rulesVersion)
	return err
}

func (q *Q) SetDemoShift(ctx context.Context, id string, days int) error {
	_, err := q.q.Exec(ctx, `UPDATE acts SET demo_shift_days = $2, updated_at = now() WHERE id = $1`, id, days)
	return err
}

// Event — запись истории акта.
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

func (q *Q) AddEvent(ctx context.Context, actID, typ string, payload any, actorUserID *string) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if payload == nil {
		raw = []byte("{}")
	}
	_, err = q.q.Exec(ctx, `INSERT INTO act_events (act_id, type, payload, actor_user_id) VALUES ($1, $2, $3, $4)`, actID, typ, raw, actorUserID)
	return err
}

func (q *Q) Events(ctx context.Context, actID string) ([]Event, error) {
	rows, err := q.q.Query(ctx, `SELECT id, type, payload, created_at FROM act_events WHERE act_id = $1 ORDER BY created_at, id`, actID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
