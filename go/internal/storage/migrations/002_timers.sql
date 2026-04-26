CREATE TABLE IF NOT EXISTS timers (
    session_id   INTEGER NOT NULL,
    name         TEXT NOT NULL,
    cycle_ms     INTEGER NOT NULL DEFAULT 60000,
    next_tick_at TEXT,      -- ISO8601, NULL when disabled or uninitialized
    remaining_ms INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 0,
    icon         TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, name),
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS timer_subscriptions (
    session_id INTEGER NOT NULL,
    timer_name TEXT NOT NULL,
    second     INTEGER NOT NULL,
    sort_order INTEGER NOT NULL,
    command    TEXT NOT NULL,
    PRIMARY KEY (session_id, timer_name, second, sort_order),
    FOREIGN KEY (session_id, timer_name)
        REFERENCES timers(session_id, name)
        ON DELETE CASCADE
);
