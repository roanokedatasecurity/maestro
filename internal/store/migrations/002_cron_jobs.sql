CREATE TABLE IF NOT EXISTS cron_jobs (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    script_path     TEXT NOT NULL,
    schedule        TEXT NOT NULL,
    scratchpad_path TEXT NOT NULL,
    owner_player_id TEXT,
    last_fired_at   DATETIME,
    next_fire_at    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
