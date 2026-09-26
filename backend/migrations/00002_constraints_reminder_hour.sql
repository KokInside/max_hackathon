-- +goose Up

-- Час отправки напоминания (в часовом поясе правил): без него напоминания уходили в полночь.
ALTER TABLE reminders ADD COLUMN due_hour smallint NOT NULL DEFAULT 10 CHECK (due_hour BETWEEN 0 AND 23);

-- Проверки на уровне БД дублируют проверки сервиса: некорректные данные не попадут в документ даже при ошибке в коде.
ALTER TABLE acts
    ADD CONSTRAINT acts_total_amount_nonneg CHECK (total_amount >= 0),
    ADD CONSTRAINT acts_copies_range CHECK (copies_received BETWEEN 0 AND 10),
    ADD CONSTRAINT acts_demo_shift_nonneg CHECK (demo_shift_days >= 0),
    ADD CONSTRAINT acts_period_order CHECK (period_from IS NULL OR period_to IS NULL OR period_from <= period_to);

ALTER TABLE act_lines
    ADD CONSTRAINT act_lines_amount_nonneg CHECK (amount >= 0),
    ADD CONSTRAINT act_lines_unit_price_nonneg CHECK (unit_price >= 0),
    ADD CONSTRAINT act_lines_disputed_nonneg CHECK (disputed_amount >= 0);

ALTER TABLE resident_votes ADD CONSTRAINT resident_votes_entrance_pos CHECK (entrance_no >= 1);

-- Индексы под запросы сервиса и под внешние ключи, по которым идут каскады.
CREATE INDEX resident_invites_act_idx ON resident_invites (act_id);
CREATE INDEX dispatches_act_idx ON dispatches (act_id);
CREATE INDEX contracts_house_idx ON contracts (house_id, created_at DESC);
CREATE INDEX chairman_profiles_house_idx ON chairman_profiles (house_id);
CREATE INDEX files_uploaded_by_idx ON files (uploaded_by);
CREATE INDEX evidence_line_idx ON evidence (line_id);
CREATE INDEX evidence_file_idx ON evidence (file_id);
CREATE INDEX documents_file_idx ON documents (file_id);
CREATE INDEX resident_votes_user_idx ON resident_votes (user_id);
CREATE INDEX acts_open_idx ON acts (status) WHERE status IN ('in_review', 'decided');

-- +goose Down
DROP INDEX acts_open_idx, resident_votes_user_idx, documents_file_idx, evidence_file_idx, evidence_line_idx,
    files_uploaded_by_idx, chairman_profiles_house_idx, contracts_house_idx, dispatches_act_idx, resident_invites_act_idx;
ALTER TABLE resident_votes DROP CONSTRAINT resident_votes_entrance_pos;
ALTER TABLE act_lines DROP CONSTRAINT act_lines_disputed_nonneg, DROP CONSTRAINT act_lines_unit_price_nonneg, DROP CONSTRAINT act_lines_amount_nonneg;
ALTER TABLE acts DROP CONSTRAINT acts_period_order, DROP CONSTRAINT acts_demo_shift_nonneg, DROP CONSTRAINT acts_copies_range, DROP CONSTRAINT acts_total_amount_nonneg;
ALTER TABLE reminders DROP COLUMN due_hour;
