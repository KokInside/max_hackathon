package store

import (
	"context"
	"strconv"
	"time"
)

type Invite struct {
	ID        string     `json:"-"`
	ActID     string     `json:"act_id"`
	Token     string     `json:"token"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"-"`
}

func (q *Q) CreateInvite(ctx context.Context, actID, token string, expires time.Time) (Invite, error) {
	inv := Invite{ActID: actID, Token: token, ExpiresAt: expires}
	err := q.q.QueryRow(ctx, `INSERT INTO resident_invites (act_id, token, expires_at) VALUES ($1, $2, $3) RETURNING id`,
		actID, token, expires).Scan(&inv.ID)
	return inv, err
}

// ActiveInvite — действующее приглашение акта, если оно уже создано.
func (q *Q) ActiveInvite(ctx context.Context, actID string) (Invite, error) {
	var inv Invite
	err := q.q.QueryRow(ctx, `
		SELECT id, act_id, token, expires_at, revoked_at FROM resident_invites
		 WHERE act_id = $1 AND revoked_at IS NULL AND expires_at > now() ORDER BY created_at DESC LIMIT 1`, actID).
		Scan(&inv.ID, &inv.ActID, &inv.Token, &inv.ExpiresAt, &inv.RevokedAt)
	return inv, notFound(err)
}

func (q *Q) InviteByToken(ctx context.Context, token string) (Invite, error) {
	var inv Invite
	err := q.q.QueryRow(ctx, `SELECT id, act_id, token, expires_at, revoked_at FROM resident_invites WHERE token = $1`, token).
		Scan(&inv.ID, &inv.ActID, &inv.Token, &inv.ExpiresAt, &inv.RevokedAt)
	return inv, notFound(err)
}

type Vote struct {
	LineID     string `json:"line_id"`
	EntranceNo int    `json:"entrance_no"`
	Answer     string `json:"answer"`
	Comment    string `json:"comment"`
}

func (q *Q) UpsertVote(ctx context.Context, userID string, v Vote) error {
	_, err := q.q.Exec(ctx, `
		INSERT INTO resident_votes (line_id, user_id, entrance_no, answer, comment) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (line_id, user_id) DO UPDATE SET entrance_no = $3, answer = $4, comment = $5, updated_at = now()`,
		v.LineID, userID, v.EntranceNo, v.Answer, v.Comment)
	return err
}

func (q *Q) VotesOfUser(ctx context.Context, actID, userID string) ([]Vote, error) {
	rows, err := q.q.Query(ctx, `
		SELECT v.line_id, v.entrance_no, v.answer, v.comment FROM resident_votes v
		  JOIN act_lines l ON l.id = v.line_id WHERE l.act_id = $1 AND v.user_id = $2`, actID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Vote{}
	for rows.Next() {
		var v Vote
		if err := rows.Scan(&v.LineID, &v.EntranceNo, &v.Answer, &v.Comment); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// VoteSummary — агрегат ответов жильцов по строке. Имён нет: председатель видит только числа и тексты.
type VoteSummary struct {
	Yes      int      `json:"yes"`
	No       int      `json:"no"`
	Unknown  int      `json:"unknown"`
	Comments []string `json:"comments"`
}

func (q *Q) VoteSummaries(ctx context.Context, actID string) (map[string]*VoteSummary, error) {
	rows, err := q.q.Query(ctx, `
		SELECT v.line_id, v.answer, v.comment, v.entrance_no FROM resident_votes v
		  JOIN act_lines l ON l.id = v.line_id WHERE l.act_id = $1 ORDER BY v.created_at`, actID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*VoteSummary{}
	for rows.Next() {
		var lineID, answer, comment string
		var entrance int
		if err := rows.Scan(&lineID, &answer, &comment, &entrance); err != nil {
			return nil, err
		}
		s := out[lineID]
		if s == nil {
			s = &VoteSummary{Comments: []string{}}
			out[lineID] = s
		}
		switch answer {
		case "yes":
			s.Yes++
		case "no":
			s.No++
		default:
			s.Unknown++
		}
		if comment != "" {
			s.Comments = append(s.Comments, "Подъезд "+strconv.Itoa(entrance)+": "+comment)
		}
	}
	return out, rows.Err()
}

// CountRespondents — сколько разных жильцов ответили по акту.
func (q *Q) CountRespondents(ctx context.Context, actID string) (int, error) {
	var n int
	err := q.q.QueryRow(ctx, `
		SELECT count(DISTINCT v.user_id) FROM resident_votes v JOIN act_lines l ON l.id = v.line_id WHERE l.act_id = $1`, actID).Scan(&n)
	return n, err
}
