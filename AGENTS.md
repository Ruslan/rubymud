# Agent Notes

This repository is `rubymud`.

- Runtime SQLite data lives in `data/`.
- Main DB file: `data/mudhost.db`.
- Prefer inspecting `mudhost.db` directly (WAL/SHM files may exist while running).

For SQLite extraction queries and debugging workflow, read:

- `docs/agents/sqlite.md`
