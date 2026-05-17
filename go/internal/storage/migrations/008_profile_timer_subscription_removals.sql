ALTER TABLE profile_timer_subscriptions ADD COLUMN is_removal INTEGER NOT NULL DEFAULT 0;
ALTER TABLE profile_timer_subscriptions ADD COLUMN is_bulk INTEGER NOT NULL DEFAULT 0;
