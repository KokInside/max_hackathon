package store

import (
	"context"
	"strconv"
	"time"

	"priemka/internal/domain"
)

// Line — строка таблицы акта (столбцы формы 761/пр) и результат её проверки.
type Line struct {
	ID              string              `json:"id"`
	ActID           string              `json:"-"`
	Position        int                 `json:"position"`
	WorkName        string              `json:"work_name"`
	PP290Ref        string              `json:"pp290_ref"`
	PeriodicityQty  string              `json:"periodicity_qty"`
	Unit            string              `json:"unit"`
	UnitPrice       *string             `json:"unit_price"`
	Amount          string              `json:"amount"`
	ReviewStatus    domain.ReviewStatus `json:"review_status"`
	Comment         string              `json:"comment"`
	DisputedAmount  *string             `json:"disputed_amount"`
	ResidentVisible bool                `json:"resident_visible"`
	Entrances       []int32             `json:"entrances"`
	PrevDisputed    bool                `json:"prev_disputed"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

// Domain — строка для расчётов domain.Summarize.
func (l Line) Domain() domain.Line {
	amt, _ := domain.ParseMoney(l.Amount)
	dl := domain.Line{Amount: amt, Status: l.ReviewStatus, Comment: l.Comment}
	if l.DisputedAmount != nil {
		if k, err := domain.ParseMoney(*l.DisputedAmount); err == nil {
			dl.DisputedAmount = &k
		}
	}
	return dl
}

const lineCols = `id, act_id, position, work_name, pp290_ref, periodicity_qty, unit, unit_price::text, amount::text,
	review_status, comment, disputed_amount::text, resident_visible, entrances, prev_disputed, updated_at`

func scanLine(row interface{ Scan(...any) error }) (Line, error) {
	var l Line
	err := row.Scan(&l.ID, &l.ActID, &l.Position, &l.WorkName, &l.PP290Ref, &l.PeriodicityQty, &l.Unit, &l.UnitPrice, &l.Amount,
		&l.ReviewStatus, &l.Comment, &l.DisputedAmount, &l.ResidentVisible, &l.Entrances, &l.PrevDisputed, &l.UpdatedAt)
	return l, notFound(err)
}

func (q *Q) Lines(ctx context.Context, actID string) ([]Line, error) {
	rows, err := q.q.Query(ctx, `SELECT `+lineCols+` FROM act_lines WHERE act_id = $1 ORDER BY position, created_at`, actID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Line{}
	for rows.Next() {
		l, err := scanLine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (q *Q) LineByID(ctx context.Context, id string) (Line, error) {
	return scanLine(q.q.QueryRow(ctx, `SELECT `+lineCols+` FROM act_lines WHERE id = $1`, id))
}

// InsertLine добавляет строку в конец таблицы акта.
func (q *Q) InsertLine(ctx context.Context, l Line) (Line, error) {
	return scanLine(q.q.QueryRow(ctx, `
		INSERT INTO act_lines (act_id, position, work_name, pp290_ref, periodicity_qty, unit, unit_price, amount,
		    review_status, comment, disputed_amount, resident_visible, entrances, prev_disputed)
		VALUES ($1, COALESCE((SELECT max(position) FROM act_lines WHERE act_id = $1), 0) + 1,
		    $2, $3, $4, $5, $6::numeric, $7::numeric, $8, $9, $10::numeric, $11, $12, $13)
		RETURNING `+lineCols,
		l.ActID, l.WorkName, l.PP290Ref, l.PeriodicityQty, l.Unit, l.UnitPrice, l.Amount,
		l.ReviewStatus, l.Comment, l.DisputedAmount, l.ResidentVisible, l.Entrances, l.PrevDisputed))
}

func (q *Q) UpdateLine(ctx context.Context, l Line) (Line, error) {
	return scanLine(q.q.QueryRow(ctx, `
		UPDATE act_lines SET work_name = $2, pp290_ref = $3, periodicity_qty = $4, unit = $5, unit_price = $6::numeric,
		    amount = $7::numeric, review_status = $8, comment = $9, disputed_amount = $10::numeric, resident_visible = $11,
		    entrances = $12, updated_at = now()
		 WHERE id = $1 RETURNING `+lineCols,
		l.ID, l.WorkName, l.PP290Ref, l.PeriodicityQty, l.Unit, l.UnitPrice, l.Amount, l.ReviewStatus, l.Comment,
		l.DisputedAmount, l.ResidentVisible, l.Entrances))
}

func (q *Q) DeleteLine(ctx context.Context, id string) error {
	_, err := q.q.Exec(ctx, `DELETE FROM act_lines WHERE id = $1`, id)
	return err
}

// CopyLinesForSuccessor копирует строки акта в новый акт; оспоренные ранее строки помечаются.
func (q *Q) CopyLinesForSuccessor(ctx context.Context, fromActID, toActID string) error {
	_, err := q.q.Exec(ctx, `
		INSERT INTO act_lines (act_id, position, work_name, pp290_ref, periodicity_qty, unit, unit_price, amount,
		    resident_visible, entrances, prev_line_id, prev_disputed)
		SELECT $2, position, work_name, pp290_ref, periodicity_qty, unit, unit_price, amount,
		    resident_visible, entrances, id, review_status IN ('doubtful', 'not_done')
		  FROM act_lines WHERE act_id = $1`, fromActID, toActID)
	return err
}

// Evidence — доказательство по строке (фото или документ).
type Evidence struct {
	ID         string    `json:"id"`
	ActID      string    `json:"-"`
	LineID     *string   `json:"line_id"`
	FileID     *string   `json:"file_id"`
	Seq        int       `json:"seq"`
	Code       string    `json:"code"`
	Note       string    `json:"note"`
	AuthorRole string    `json:"author_role"`
	Mime       string    `json:"mime"`
	CreatedAt  time.Time `json:"created_at"`
}

// AddEvidence добавляет доказательство со следующим номером Д-N. Строка акта блокируется,
// чтобы параллельные загрузки не получили одинаковый номер. Вызывать внутри транзакции.
func (q *Q) AddEvidence(ctx context.Context, e Evidence) (Evidence, error) {
	if _, err := q.q.Exec(ctx, `SELECT 1 FROM acts WHERE id = $1 FOR UPDATE`, e.ActID); err != nil {
		return e, err
	}
	err := q.q.QueryRow(ctx, `
		INSERT INTO evidence (act_id, line_id, file_id, seq, note, author_role)
		VALUES ($1, $2, $3, COALESCE((SELECT max(seq) FROM evidence WHERE act_id = $1), 0) + 1, $4, $5)
		RETURNING id, seq, created_at`,
		e.ActID, e.LineID, e.FileID, e.Note, e.AuthorRole).Scan(&e.ID, &e.Seq, &e.CreatedAt)
	e.Code = evidenceCode(e.Seq)
	return e, err
}

func evidenceCode(seq int) string { return "Д-" + strconv.Itoa(seq) }

func (q *Q) EvidenceForAct(ctx context.Context, actID string) ([]Evidence, error) {
	rows, err := q.q.Query(ctx, `
		SELECT e.id, e.act_id, e.line_id, e.file_id, e.seq, e.note, e.author_role, COALESCE(f.mime, ''), e.created_at
		  FROM evidence e LEFT JOIN files f ON f.id = e.file_id
		 WHERE e.act_id = $1 ORDER BY e.seq`, actID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Evidence{}
	for rows.Next() {
		var e Evidence
		if err := rows.Scan(&e.ID, &e.ActID, &e.LineID, &e.FileID, &e.Seq, &e.Note, &e.AuthorRole, &e.Mime, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Code = evidenceCode(e.Seq)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (q *Q) EvidenceByID(ctx context.Context, id string) (Evidence, error) {
	var e Evidence
	err := q.q.QueryRow(ctx, `SELECT id, act_id, line_id, file_id, seq, note, author_role, created_at FROM evidence WHERE id = $1`, id).
		Scan(&e.ID, &e.ActID, &e.LineID, &e.FileID, &e.Seq, &e.Note, &e.AuthorRole, &e.CreatedAt)
	e.Code = evidenceCode(e.Seq)
	return e, notFound(err)
}

// LineFiles — файлы доказательств строки (удаляются вместе со строкой).
func (q *Q) LineFiles(ctx context.Context, lineID string) ([]File, error) {
	return q.files(ctx, `SELECT f.id, f.storage_key FROM evidence e JOIN files f ON f.id = e.file_id WHERE e.line_id = $1`, lineID)
}

func (q *Q) DeleteEvidence(ctx context.Context, id string) error {
	_, err := q.q.Exec(ctx, `DELETE FROM evidence WHERE id = $1`, id)
	return err
}
