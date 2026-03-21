CREATE TABLE IF NOT EXISTS messages (
    id           TEXT     PRIMARY KEY,
    from_player  TEXT     NOT NULL,
    to_player    TEXT     NOT NULL,
    type         TEXT     NOT NULL CHECK(type IN ('Assignment','Done','Blocked','Background','Lifecycle')),
    priority     TEXT     NOT NULL DEFAULT 'Normal' CHECK(priority IN ('High','Normal')),
    payload      TEXT     NOT NULL DEFAULT '',
    wait_for_ack INTEGER  NOT NULL DEFAULT 0,
    created_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    delivered_at DATETIME
);

CREATE TABLE IF NOT EXISTS players (
    id           TEXT     PRIMARY KEY,
    name         TEXT     NOT NULL,
    status       TEXT     NOT NULL DEFAULT 'Idle' CHECK(status IN ('Idle','Running','Dead')),
    is_conductor INTEGER  NOT NULL DEFAULT 0,
    profile      TEXT,
    created_at   DATETIME NOT NULL DEFAULT (datetime('now')),
    last_seen_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS jobs (
    id                TEXT     PRIMARY KEY,
    message_id        TEXT     NOT NULL REFERENCES messages(id),
    player_id         TEXT     NOT NULL REFERENCES players(id),
    player_name       TEXT     NOT NULL,
    payload           TEXT     NOT NULL DEFAULT '',
    scratchpad_path   TEXT     NOT NULL,
    status            TEXT     NOT NULL DEFAULT 'InProgress' CHECK(status IN ('InProgress','Backgrounded','Complete','DeadLetter')),
    approval_metadata TEXT,
    created_at        DATETIME NOT NULL DEFAULT (datetime('now')),
    completed_at      DATETIME
);

CREATE TABLE IF NOT EXISTS notifications (
    id         TEXT     PRIMARY KEY,
    message_id TEXT     REFERENCES messages(id),
    job_id     TEXT     REFERENCES jobs(id),
    type       TEXT     NOT NULL,
    summary    TEXT     NOT NULL,
    read_at    DATETIME,
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS approvals (
    id         TEXT     PRIMARY KEY,
    job_id     TEXT     NOT NULL REFERENCES jobs(id),
    message_id TEXT     NOT NULL REFERENCES messages(id),
    scorecard  TEXT     NOT NULL DEFAULT '{}',
    decision   TEXT     CHECK(decision IN ('Autonomous','Human')),
    decided_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
