-- Add mobile layout columns to hotkey_rules
ALTER TABLE hotkey_rules ADD COLUMN mobile_row INTEGER NOT NULL DEFAULT 0;
ALTER TABLE hotkey_rules ADD COLUMN mobile_order INTEGER NOT NULL DEFAULT 0;
