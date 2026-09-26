-- +goose Up
-- Повтор неудачной отправки напоминания с растущей паузой: сбой MAX на несколько минут не должен терять напоминание.
ALTER TABLE reminders ADD COLUMN next_attempt_at timestamptz;

-- +goose Down
ALTER TABLE reminders DROP COLUMN next_attempt_at;
