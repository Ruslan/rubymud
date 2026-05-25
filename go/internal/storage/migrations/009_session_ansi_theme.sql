ALTER TABLE sessions ADD COLUMN ansi_theme TEXT NOT NULL DEFAULT 'classic' CHECK (ansi_theme IN ('classic', 'high-contrast', 'tango-dark', 'dracula', 'gruvbox-dark'));
