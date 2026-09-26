package store

import (
	"context"
	"time"

	"priemka/internal/civil"
)

type File struct {
	ID           string `json:"id"`
	StorageKey   string `json:"-"`
	Mime         string `json:"mime"`
	Size         int64  `json:"size"`
	SHA256       string `json:"-"`
	OriginalName string `json:"original_name"`
	UploadedBy   *string
}

func (q *Q) CreateFile(ctx context.Context, f File) (File, error) {
	err := q.q.QueryRow(ctx, `
		INSERT INTO files (storage_key, mime, size, sha256, original_name, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		f.StorageKey, f.Mime, f.Size, f.SHA256, f.OriginalName, f.UploadedBy).Scan(&f.ID)
	return f, err
}

// UserFiles — файлы пользователя: загруженные им и относящиеся к актам его домов.
func (q *Q) UserFiles(ctx context.Context, userID string) ([]File, error) {
	rows, err := q.q.Query(ctx, `
		WITH my_acts AS (
		    SELECT a.id, a.source_file_id FROM acts a JOIN houses h ON h.id = a.house_id WHERE h.chairman_user_id = $1
		)
		SELECT id, storage_key FROM files
		 WHERE uploaded_by = $1
		    OR id IN (SELECT source_file_id FROM my_acts)
		    OR id IN (SELECT e.file_id FROM evidence e WHERE e.act_id IN (SELECT id FROM my_acts))
		    OR id IN (SELECT d.file_id FROM documents d WHERE d.act_id IN (SELECT id FROM my_acts))
		    OR id IN (SELECT v.file_id FROM resident_votes v WHERE v.user_id = $1)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.StorageKey); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteFiles удаляет записи о файлах (сами файлы на диске удаляет files.Storage).
func (q *Q) DeleteFiles(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.q.Exec(ctx, `DELETE FROM files WHERE id = ANY($1::uuid[])`, ids)
	return err
}

func (q *Q) FileByID(ctx context.Context, id string) (File, error) {
	var f File
	err := q.q.QueryRow(ctx, `SELECT id, storage_key, mime, size, sha256, original_name, uploaded_by FROM files WHERE id = $1`, id).
		Scan(&f.ID, &f.StorageKey, &f.Mime, &f.Size, &f.SHA256, &f.OriginalName, &f.UploadedBy)
	return f, notFound(err)
}

type Document struct {
	ID           string    `json:"id"`
	ActID        string    `json:"-"`
	Kind         string    `json:"kind"`
	FileID       string    `json:"file_id"`
	Version      int       `json:"version"`
	RulesVersion string    `json:"rules_version"`
	MaxMessageID string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

func (q *Q) CreateDocument(ctx context.Context, d Document) (Document, error) {
	err := q.q.QueryRow(ctx, `
		INSERT INTO documents (act_id, kind, file_id, version, rules_version)
		VALUES ($1, $2, $3, COALESCE((SELECT max(version) FROM documents WHERE act_id = $1), 0) + 1, $4)
		RETURNING id, version, created_at`,
		d.ActID, d.Kind, d.FileID, d.RulesVersion).Scan(&d.ID, &d.Version, &d.CreatedAt)
	return d, err
}

func (q *Q) SetDocumentMessage(ctx context.Context, id, mid string) error {
	_, err := q.q.Exec(ctx, `UPDATE documents SET max_message_id = $2 WHERE id = $1`, id, mid)
	return err
}

func (q *Q) Documents(ctx context.Context, actID string) ([]Document, error) {
	rows, err := q.q.Query(ctx, `SELECT id, act_id, kind, file_id, version, rules_version, max_message_id, created_at
		FROM documents WHERE act_id = $1 ORDER BY version DESC`, actID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Document{}
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.ActID, &d.Kind, &d.FileID, &d.Version, &d.RulesVersion, &d.MaxMessageID, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (q *Q) DocumentByID(ctx context.Context, id string) (Document, error) {
	var d Document
	err := q.q.QueryRow(ctx, `SELECT id, act_id, kind, file_id, version, rules_version, max_message_id, created_at FROM documents WHERE id = $1`, id).
		Scan(&d.ID, &d.ActID, &d.Kind, &d.FileID, &d.Version, &d.RulesVersion, &d.MaxMessageID, &d.CreatedAt)
	return d, notFound(err)
}

type Dispatch struct {
	ID          string     `json:"id"`
	ActID       string     `json:"-"`
	DocumentID  *string    `json:"document_id"`
	Channel     string     `json:"channel"`
	ChannelNote string     `json:"channel_note"`
	SentOn      civil.Date `json:"sent_on"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (q *Q) CreateDispatch(ctx context.Context, d Dispatch) (Dispatch, error) {
	err := q.q.QueryRow(ctx, `
		INSERT INTO dispatches (act_id, document_id, channel, channel_note, sent_on) VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`, d.ActID, d.DocumentID, d.Channel, d.ChannelNote, d.SentOn).Scan(&d.ID, &d.CreatedAt)
	return d, err
}

func (q *Q) Dispatches(ctx context.Context, actID string) ([]Dispatch, error) {
	rows, err := q.q.Query(ctx, `SELECT id, act_id, document_id, channel, channel_note, sent_on, created_at
		FROM dispatches WHERE act_id = $1 ORDER BY created_at`, actID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dispatch{}
	for rows.Next() {
		var d Dispatch
		if err := rows.Scan(&d.ID, &d.ActID, &d.DocumentID, &d.Channel, &d.ChannelNote, &d.SentOn, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Reminder — запланированное напоминание по акту.
type Reminder struct {
	ID       string
	ActID    string
	Kind     string
	DueOn    civil.Date
	DueHour  int
	Attempts int
}

// ReplaceReminders пересоздаёт неотправленные напоминания акта (при смене даты получения).
func (q *Q) ReplaceReminders(ctx context.Context, actID string, rs []Reminder) error {
	if _, err := q.q.Exec(ctx, `DELETE FROM reminders WHERE act_id = $1 AND sent_at IS NULL`, actID); err != nil {
		return err
	}
	for _, r := range rs {
		if _, err := q.q.Exec(ctx, `
			INSERT INTO reminders (act_id, kind, due_on, due_hour) VALUES ($1, $2, $3, $4)
			ON CONFLICT (act_id, kind) DO NOTHING`, actID, r.Kind, r.DueOn, r.DueHour); err != nil {
			return err
		}
	}
	return nil
}

// ClaimDueReminders блокирует наступившие напоминания: день срока прошёл или наступил и настал час отправки.
// Для демо-актов «сегодня» сдвинуто на demo_shift_days, а после перемотки час не ждём — иначе демо не показать.
// Вызывается внутри транзакции: строки остаются заблокированными до commit.
func (q *Q) ClaimDueReminders(ctx context.Context, today civil.Date, hour, limit int) ([]Reminder, error) {
	rows, err := q.q.Query(ctx, `
		SELECT r.id, r.act_id, r.kind, r.due_on, r.due_hour, r.attempts
		  FROM reminders r JOIN acts a ON a.id = r.act_id
		 WHERE r.sent_at IS NULL AND r.attempts < $4
		   AND (r.next_attempt_at IS NULL OR r.next_attempt_at <= now())
		   AND (r.due_on < $1::date + a.demo_shift_days
		        OR (r.due_on = $1::date + a.demo_shift_days AND ($2 >= r.due_hour OR a.demo_shift_days > 0)))
		   AND a.status IN ('in_review', 'decided', 'deemed_accepted')
		 ORDER BY r.due_on
		 LIMIT $3
		 FOR UPDATE OF r SKIP LOCKED`, today, hour, limit, MaxReminderAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reminder
	for rows.Next() {
		var r Reminder
		if err := rows.Scan(&r.ID, &r.ActID, &r.Kind, &r.DueOn, &r.DueHour, &r.Attempts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MaxReminderAttempts — сколько раз пытаемся отправить напоминание. С паузой 1, 2, 4 … 60 минут
// это покрывает около 6 часов недоступности MAX.
const MaxReminderAttempts = 12

// MarkReminder фиксирует отправку. При ошибке следующая попытка — через 2^попытка минут, но не больше часа;
// при постоянной ошибке (giveUp) повторов больше не будет.
func (q *Q) MarkReminder(ctx context.Context, id string, sendErr error, giveUp bool) error {
	if sendErr != nil && giveUp {
		_, err := q.q.Exec(ctx, `UPDATE reminders SET attempts = $3, last_error = $2, next_attempt_at = NULL WHERE id = $1`, id, sendErr.Error(), MaxReminderAttempts)
		return err
	}
	if sendErr == nil {
		_, err := q.q.Exec(ctx, `UPDATE reminders SET sent_at = now(), attempts = attempts + 1, last_error = '', next_attempt_at = NULL WHERE id = $1`, id)
		return err
	}
	_, err := q.q.Exec(ctx, `
		UPDATE reminders SET attempts = attempts + 1, last_error = $2,
		       next_attempt_at = now() + least(power(2, attempts), 60) * interval '1 minute'
		 WHERE id = $1`, id, sendErr.Error())
	return err
}

// SkipReminders помечает напоминания акта неактуальными (акт закрыт).
func (q *Q) SkipReminders(ctx context.Context, actID string) error {
	_, err := q.q.Exec(ctx, `UPDATE reminders SET sent_at = now(), last_error = 'skipped' WHERE act_id = $1 AND sent_at IS NULL`, actID)
	return err
}
