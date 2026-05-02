CREATE TABLE IF NOT EXISTS profile_timers (
    profile_id   INTEGER NOT NULL,
    name         TEXT NOT NULL,
    icon         TEXT NOT NULL DEFAULT '',
    cycle_ms     INTEGER NOT NULL DEFAULT 60000,
    repeat_mode  TEXT NOT NULL DEFAULT 'repeating',
    PRIMARY KEY (profile_id, name),
    FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS profile_timer_subscriptions (
    profile_id INTEGER NOT NULL,
    timer_name TEXT NOT NULL,
    second     INTEGER NOT NULL,
    sort_order INTEGER NOT NULL,
    command    TEXT NOT NULL,
    PRIMARY KEY (profile_id, timer_name, second, sort_order),
    FOREIGN KEY (profile_id, timer_name)
        REFERENCES profile_timers(profile_id, name)
        ON DELETE CASCADE
);
