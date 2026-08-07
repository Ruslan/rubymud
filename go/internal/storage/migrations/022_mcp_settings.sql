-- Singleton configuration for the optional MCP endpoint. The bearer secret is
-- never stored here: token_digest is the lowercase SHA-256 hexadecimal digest.
CREATE TABLE IF NOT EXISTS mcp_settings (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    token_digest TEXT NOT NULL DEFAULT '' CHECK (
        token_digest = '' OR (
            length(token_digest) = 64 AND
            token_digest NOT GLOB '*[^0-9a-f]*'
        )
    ),
    revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    updated_at TEXT NOT NULL
);

INSERT OR IGNORE INTO mcp_settings (singleton, enabled, token_digest, updated_at)
VALUES (1, 0, '', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
