-- +goose Up

CREATE TABLE users (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    max_user_id  bigint NOT NULL UNIQUE,
    first_name   text NOT NULL DEFAULT '',
    last_name    text NOT NULL DEFAULT '',
    consent_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE houses (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    chairman_user_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    city              text NOT NULL,
    address           text NOT NULL,
    entrances_count   int  NOT NULL DEFAULT 1 CHECK (entrances_count BETWEEN 1 AND 50),
    timezone          text NOT NULL DEFAULT 'Europe/Moscow',
    council_chat_id   bigint,
    is_demo           boolean NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX houses_chairman_idx ON houses (chairman_user_id);

CREATE TABLE chairman_profiles (
    user_id                  uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    house_id                 uuid REFERENCES houses(id) ON DELETE SET NULL,
    full_name                text NOT NULL DEFAULT '',
    apartment_no             text NOT NULL DEFAULT '',
    authority_type           text NOT NULL DEFAULT '' CHECK (authority_type IN ('', 'oss_decision', 'power_of_attorney')),
    authority_date           date,
    authority_number         text NOT NULL DEFAULT '',
    applicability_confirmed  boolean,
    updated_at               timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE contracts (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    house_id       uuid NOT NULL REFERENCES houses(id) ON DELETE CASCADE,
    type           text NOT NULL DEFAULT 'management' CHECK (type IN ('management', 'services', 'repair')),
    number         text NOT NULL DEFAULT '',
    date           date,
    end_date       date,
    executor_name  text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE files (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    storage_key    text NOT NULL UNIQUE,
    mime           text NOT NULL,
    size           bigint NOT NULL,
    sha256         text NOT NULL,
    original_name  text NOT NULL DEFAULT '',
    uploaded_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE acts (
    id                           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    house_id                     uuid NOT NULL REFERENCES houses(id) ON DELETE CASCADE,
    contract_id                  uuid REFERENCES contracts(id) ON DELETE SET NULL,
    parent_act_id                uuid REFERENCES acts(id) ON DELETE SET NULL,
    source_file_id               uuid REFERENCES files(id) ON DELETE SET NULL,

    -- Поля формы акта (приказ Минстроя № 761/пр)
    number                       text NOT NULL DEFAULT '',
    act_date                     date,
    city                         text NOT NULL DEFAULT '',
    address                      text NOT NULL DEFAULT '',
    customer_full_name           text NOT NULL DEFAULT '',
    customer_apartment           text NOT NULL DEFAULT '',
    customer_authority_text      text NOT NULL DEFAULT '',
    executor_name                text NOT NULL DEFAULT '',
    executor_signatory_name      text NOT NULL DEFAULT '',
    executor_signatory_position  text NOT NULL DEFAULT '',
    executor_basis               text NOT NULL DEFAULT '',
    contract_type                text NOT NULL DEFAULT 'management' CHECK (contract_type IN ('management', 'services', 'repair')),
    contract_number              text NOT NULL DEFAULT '',
    contract_date                date,
    contract_end_date            date,
    period_from                  date,
    period_to                    date,
    total_amount                 numeric(14,2),
    total_amount_words           text NOT NULL DEFAULT '',
    copies_received              int,
    executor_signed              boolean,

    -- Приёмка
    received_on                  date,
    received_channel             text NOT NULL DEFAULT '' CHECK (received_channel IN ('', 'post', 'email', 'executor_portal', 'in_person', 'other')),
    executor_sent_on             date,
    status                       text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'in_review', 'decided', 'closed_signed', 'refused_sent', 'deemed_accepted', 'replaced')),
    decision                     text NOT NULL DEFAULT '' CHECK (decision IN ('', 'sign', 'refuse')),
    deadline_response_on         date,
    deadline_silent_on           date,
    rules_version                text NOT NULL DEFAULT '',
    is_demo                      boolean NOT NULL DEFAULT false,
    demo_shift_days              int NOT NULL DEFAULT 0,
    created_at                   timestamptz NOT NULL DEFAULT now(),
    updated_at                   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX acts_house_idx ON acts (house_id, created_at DESC);

CREATE TABLE act_lines (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    act_id            uuid NOT NULL REFERENCES acts(id) ON DELETE CASCADE,
    position          int NOT NULL,
    work_name         text NOT NULL,
    pp290_ref         text NOT NULL DEFAULT '',
    periodicity_qty   text NOT NULL DEFAULT '',
    unit              text NOT NULL DEFAULT '',
    unit_price        numeric(14,2),
    amount            numeric(14,2) NOT NULL DEFAULT 0,
    review_status     text NOT NULL DEFAULT 'unchecked' CHECK (review_status IN ('unchecked', 'confirmed', 'doubtful', 'not_done')),
    comment           text NOT NULL DEFAULT '',
    disputed_amount   numeric(14,2),
    resident_visible  boolean NOT NULL DEFAULT true,
    entrances         int[],
    prev_line_id      uuid REFERENCES act_lines(id) ON DELETE SET NULL,
    prev_disputed     boolean NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX act_lines_act_idx ON act_lines (act_id, position);

CREATE TABLE evidence (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    act_id       uuid NOT NULL REFERENCES acts(id) ON DELETE CASCADE,
    line_id      uuid REFERENCES act_lines(id) ON DELETE CASCADE,
    file_id      uuid REFERENCES files(id) ON DELETE SET NULL,
    seq          int NOT NULL,
    note         text NOT NULL DEFAULT '',
    author_role  text NOT NULL DEFAULT 'chairman' CHECK (author_role IN ('chairman', 'council', 'resident')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (act_id, seq)
);

CREATE TABLE resident_invites (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    act_id      uuid NOT NULL REFERENCES acts(id) ON DELETE CASCADE,
    token       text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE resident_votes (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    line_id      uuid NOT NULL REFERENCES act_lines(id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entrance_no  int NOT NULL,
    answer       text NOT NULL CHECK (answer IN ('yes', 'no', 'unknown')),
    comment      text NOT NULL DEFAULT '',
    file_id      uuid REFERENCES files(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (line_id, user_id)
);

CREATE TABLE documents (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    act_id          uuid NOT NULL REFERENCES acts(id) ON DELETE CASCADE,
    kind            text NOT NULL CHECK (kind IN ('cover_letter', 'refusal')),
    file_id         uuid NOT NULL REFERENCES files(id),
    version         int NOT NULL,
    rules_version   text NOT NULL,
    max_message_id  text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (act_id, version)
);

CREATE TABLE dispatches (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    act_id        uuid NOT NULL REFERENCES acts(id) ON DELETE CASCADE,
    document_id   uuid REFERENCES documents(id) ON DELETE SET NULL,
    channel       text NOT NULL CHECK (channel IN ('post', 'email', 'executor_portal', 'in_person', 'other')),
    channel_note  text NOT NULL DEFAULT '',
    sent_on       date NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE reminders (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    act_id      uuid NOT NULL REFERENCES acts(id) ON DELETE CASCADE,
    kind        text NOT NULL,
    due_on      date NOT NULL,
    sent_at     timestamptz,
    attempts    int NOT NULL DEFAULT 0,
    last_error  text NOT NULL DEFAULT '',
    UNIQUE (act_id, kind)
);
CREATE INDEX reminders_pending_idx ON reminders (due_on) WHERE sent_at IS NULL;

CREATE TABLE act_events (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    act_id         uuid NOT NULL REFERENCES acts(id) ON DELETE CASCADE,
    type           text NOT NULL,
    payload        jsonb NOT NULL DEFAULT '{}'::jsonb,
    actor_user_id  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX act_events_act_idx ON act_events (act_id, created_at);

CREATE TABLE bot_sessions (
    user_id     uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    state       text NOT NULL DEFAULT '',
    data        jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE bot_sessions, act_events, reminders, dispatches, documents, resident_votes,
    resident_invites, evidence, act_lines, acts, files, contracts, chairman_profiles, houses, users;
