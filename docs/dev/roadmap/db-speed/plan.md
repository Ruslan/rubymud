# Async persistence для log/output pipeline

## Контекст

Сейчас live output ждёт SQLite перед отправкой строки в браузер:

```text
MUD -> processLine -> AppendLogEntryWithOverlays -> ApplyHighlights -> websocket -> UI
```

На быстрых MUD-ответах это превращает latency диска в latency клиента. В реальных замерах:
- MUD/server отвечает примерно за `17ms`
- на `zfs -> vm -> ext4` клиент видит строку примерно через `700ms`
- на слабом SSD с ext4 клиент видит строку примерно через `1900ms`

`WAL` и `synchronous=NORMAL` могут уменьшить стоимость записи, но не решают корневую проблему: запись в БД остаётся на critical path вывода.

## Цель

Разорвать зависимость live UI от SQLite commit latency.

Новый live pipeline:

```text
MUD -> processLine -> render/highlight -> websocket -> UI
                    \
                     -> async log writer -> SQLite
```

Команды игрока тоже не должны ждать запись history/command hints перед отправкой в MUD:

```text
input -> VM -> conn.Write(command) -> async history/hints -> SQLite
```

## Основное решение по идентификаторам

Вводим `seq` как backend-generated стабильный идентификатор live log entry.

`seq`:
- генерируется backend-сессией до записи в БД
- монотонно растёт внутри `session_id`
- уникален в рамках одной сессии
- используется frontend как основной key для live output и targeted updates
- сохраняется в `log_entries.seq`
- используется для restore ordering
- короткий в JSON/WebSocket payload

`id`:
- остаётся `INTEGER PRIMARY KEY` в SQLite
- остаётся внутренним DB/FK идентификатором
- используется таблицами `log_overlays`, FTS и legacy queries
- не должен быть нужен live UI для новых сообщений

Причина: DB `id` появляется только после INSERT. Если UI ждёт `id`, он снова ждёт SQLite. `seq` позволяет сразу отправить строку, кнопки и command hints, а DB догоняет позже.

## Семантика `seq`

`seq` является порядковым номером log entry внутри runtime session.

Правила:
- первое новое значение после старта сессии: `max(log_entries.seq) + 1`
- для старых БД после миграции: `seq = id`
- значение `0` зарезервировано как unknown/legacy и не используется для новых строк
- `seq` выдаётся только backend-ом, не БД default-ом
- `seq` не переиспользуется после ошибок записи
- если запись в БД не удалась, live UI всё равно сохраняет порядок, но restore после restart эту строку не увидит

`seq` должен быть глобальным внутри `session_id`, а не отдельным на buffer. Это упрощает targeted updates и общий порядок событий. `buffer` остаётся частью маршрутизации UI.

## Ownership and concurrency

`seq` корректен при текущей архитектуре: один backend process владеет runtime `Session`, а клиентов может быть много. Клиенты не генерируют `seq`; они только получают его от backend-а.

Правила реализации:
- у каждой active `Session` должен быть ровно один allocator `nextSeq`
- allocator инициализируется как `MaxLogSeq(sessionID) + 1`
- выдача нового значения должна быть atomic/mutex-protected, например через `atomic.Int64.Add(1)` или `s.mu`
- все live entries, включая MUD lines, local echo, copy/move/echo routing entries, получают `seq` только через этот allocator
- `(session_id, seq)` unique index остаётся DB guard-ом от багов и случайного double-owner

Не поддерживается в MVP:
- два backend process одновременно пишут live log для одного `session_id`
- две active `Session` в одном process для одного `session_id`

Если когда-нибудь понадобится multi-process ownership, потребуется leader lock/lease или DB-backed sequence allocator. Для текущей модели один backend на session — это правильный и быстрый источник истины.

## Overflow

`seq` хранится как Go `int64` и SQLite `INTEGER` (signed 64-bit). Переполнение практически недостижимо.

Оценки:
- `int64` max: `9_223_372_036_854_775_807`
- при `1_000` строк/сек это примерно `292_000_000` лет
- при `100_000` строк/сек это примерно `2_900_000` лет

Frontend получает JSON number. JavaScript точно представляет целые числа до `2^53 - 1` (`9_007_199_254_740_991`). Это меньше `int64`, но тоже практически достаточно:
- при `1_000` строк/сек это примерно `285_000` лет
- при `100_000` строк/сек это примерно `2_850` лет

Поэтому `seq` можно передавать как JSON number. Если в будущем потребуется абсолютная точность за пределами JS safe integer, можно будет добавить строковое поле `seq_s`, но для RubyMUD это лишний overhead.

## Schema migration

Добавить миграцию `008_log_seq.sql`:

```sql
ALTER TABLE log_entries ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;

UPDATE log_entries
SET seq = id
WHERE seq = 0;

CREATE UNIQUE INDEX IF NOT EXISTS log_entries_session_seq_uidx
ON log_entries(session_id, seq);

CREATE INDEX IF NOT EXISTS log_entries_session_seq_idx
ON log_entries(session_id, seq);

CREATE INDEX IF NOT EXISTS log_entries_session_window_seq_idx
ON log_entries(session_id, window_name, seq);
```

Notes:
- unique index создаётся после backfill, иначе старые строки с default `0` конфликтуют
- `seq` не должен заменять `id` в FK
- `log_overlays.log_entry_id` не меняем в MVP, writer сам резолвит `seq -> id` при записи

Go models:

```go
type LogRecord struct {
    ID        int64 `gorm:"primaryKey"`
    SessionID int64 `gorm:"index" json:"session_id"`
    Seq       int64 `gorm:"index" json:"seq"`
    // existing fields...
}

type LogEntry struct {
    ID       int64
    Seq      int64
    Buffer   string
    // existing fields...
}

type ClientLogEntry struct {
    ID       int64 `json:"id,omitempty"`
    Seq      int64 `json:"seq,omitempty"`
    Text     string `json:"text"`
    Buffer   string `json:"buffer,omitempty"`
    Commands []string `json:"commands,omitempty"`
    Buttons  []ButtonOverlay `json:"buttons,omitempty"`
}
```

Frontend rule:
- use `entry.seq` as the primary DOM/data key when present
- fallback to `entry.id` only for legacy restored rows if needed

Server messages:

```go
type ServerMsg struct {
    EntryID       int64 `json:"entry_id,omitempty"`
    EntrySeq      int64 `json:"entry_seq,omitempty"`
    // existing fields...
}
```

`command_hint` should target `entry_seq` for new live messages. `entry_id` can remain for compatibility during migration.

## Async writer architecture

Добавить session-owned async writer. Он живёт вместе с `Session`, потому что seq allocation, live pending cache и command hints являются runtime-состоянием сессии.

Основные структуры:

```go
type pendingLogEntry struct {
    Seq          int64
    SessionID    int64
    Buffer       string
    Stream       string
    RawText      string
    PlainText    string
    DisplayRaw   string
    DisplayPlain string
    SourceType   string
    SourceID     string
    ReceivedAt   storage.SQLiteTime
    CreatedAt    storage.SQLiteTime
    Overlays     []storage.LogOverlay
    Buttons      []storage.ButtonOverlay
    Commands     []string
}

type pendingOverlay struct {
    Seq     int64
    Overlay  storage.LogOverlay
}

type pendingHistoryEntry struct {
    SessionID int64
    Kind      string
    Line      string
    CreatedAt storage.SQLiteTime
}

type writeJob struct {
    LogEntry *pendingLogEntry
    Overlay  *pendingOverlay
    History  *pendingHistoryEntry
}
```

Минимальная реализация может использовать один `chan writeJob` и один goroutine writer.

Batch policy:
- flush каждые `10-25ms`
- или когда накоплено `N` jobs, например `128`
- один SQLite transaction на batch
- в transaction сначала insert `log_entries`, затем связанные overlays/buttons/command hints/history

Queue policy:
- bounded queue, например `8192` jobs
- метрики: queue length, dropped jobs, last write error, write latency p50/p95
- на `Session.Close()` writer должен flush-ить очередь с timeout
- если очередь заполнена, первый MVP может блокировать только writer enqueue на короткий timeout, затем логировать drop; более строгий режим можно сделать настройкой

## Storage API

Нужны batch-oriented методы в storage:

```go
func (s *Store) MaxLogSeq(sessionID int64) (int64, error)

func (s *Store) AppendLogEntriesBatch(entries []PendingLogRecord) (map[int64]int64, error)

func (s *Store) AppendOverlaysForSeqs(sessionID int64, overlays []PendingOverlay) error

func (s *Store) AppendHistoryEntriesBatch(entries []PendingHistoryRecord) error
```

Точные имена не принципиальны. Важно:
- batch insert entries возвращает map `seq -> db id`
- overlays продолжают сохраняться через `log_entry_id`
- если overlay пришёл после entry, writer резолвит `seq -> id` через in-memory map или SELECT
- если overlay пришёл до entry, writer держит его в pending map до появления entry в том же writer loop

## Live processing pipeline

### Входящая MUD строка

Новый порядок:

```text
normalizeLine
stripANSI
CheckGags
MatchTriggers
ApplySubsAndCollectOverlays
allocate seq
build ClientLogEntry
ApplyHighlights
broadcast
enqueue log entry + overlays/buttons
ApplyEffects
```

Для gag:
- seq всё равно выделяется
- запись с gag overlay enqueue-ится
- broadcast не происходит
- triggers не выполняются, как сейчас

Для обычной строки:
- broadcast происходит до SQLite
- DB write происходит в фоне
- `ClientLogEntry.seq` заполнен сразу

### Buttons

Сейчас `vm.ApplyEffects` пишет `AppendButtonOverlay` напрямую через `v.store`. Это надо убрать из live path.

Новое правило:
- VM возвращает effects/buttons как данные
- Session добавляет buttons в `ClientLogEntry` до broadcast
- Session enqueue-ит button overlays вместе с pending log entry
- restore загружает buttons из `log_overlays`, как раньше

VM не должен писать в storage при обработке live line. Это важно для async persistence и тестируемости.

### Trigger send command hints

Сейчас trigger send вызывает `sendTriggerCommand(cmd, entryID, buffer)` и пишет command overlay по DB id.

Нужно заменить на `seq`:

```go
sendTriggerCommand(cmd string, entrySeq int64, buffer string) error
```

Порядок:
- отправить command в MUD без ожидания history/overlay writes
- enqueue history entry
- enqueue command overlay targeting `entrySeq`
- broadcast `command_hint` with `entry_seq`

### User input history/hints

Для пользовательского input:
- `conn.Write(command)` должен быть раньше `AppendHistoryEntry`
- history пишется async
- command hint для последней видимой строки должен target-ить last known `seq`, а не делать DB query `AppendCommandHintToLatestLogEntry`

Session должна хранить:

```go
lastVisibleSeq int64
```

При broadcast новой видимой строки `lastVisibleSeq = seq`. Для input command hint enqueue-ится на этот seq, если он есть. UI уже добавляет локальный hint оптимистично, но server-side overlay нужен для restore.

## Restore and reconnect

Restore должен использовать `seq`, а не только DB `id`:

```text
SELECT ... FROM log_entries
WHERE session_id = ? AND window_name = ?
ORDER BY seq DESC
LIMIT ?
```

Затем reverse для chronological order.

Проблема: если browser reconnect произошёл до flush, строки есть в памяти, но ещё нет в БД.

Решение MVP:
- Session держит in-memory recent ring buffer per buffer, например последние `1000` live `ClientLogEntry`
- `RecentLogsPerBuffer` для live session merge-ит DB restore и pending/in-memory entries by `seq`
- после успешного flush entry остаётся в ring до вытеснения, но не дублируется при restore

Без этого reconnect во время backlog может временно потерять последние live строки до следующего restore после flush.

## Ordering guarantees

Гарантируем:
- live UI получает строки в порядке `seq`
- DB restore возвращает строки в порядке `seq`
- command hints/buttons target-ят `seq`, поэтому не зависят от DB id
- copy/move/echo entries получают отдельные seq values, потому что это отдельные log entries

Не гарантируем:
- последние unflushed строки переживают crash процесса
- DB `id` совпадает с live order при async/batch writes

## SQLite configuration

Оставить WAL/perf настройки, но применить надёжно для каждого соединения.

Текущий вариант через `db.Exec("PRAGMA ...")` не гарантирует connection-scoped PRAGMA на новых соединениях из pool.

Предпочтительно:

```text
file:path.db?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)
```

или эквивалентный DSN builder с URL escaping.

Также рассмотреть:
- `busy_timeout(5000)` уже задаётся драйвером, но можно оставить явно
- `SetMaxOpenConns(1)` даёт простую предсказуемость для SQLite writer, но может ухудшить concurrent reads
- async writer с WAL обычно лучше, чем принудительный single connection для всего приложения

## Backpressure and durability modes

Нужно явно выбрать режим.

MVP recommendation: `interactive-first`.

Поведение:
- live output никогда не ждёт обычный SQLite commit
- queue full больше не должна блокировать read loop бесконечно
- при queue overflow логируем warning и увеличиваем metric
- для критичных данных можно блокировать на короткий timeout, например `50ms`, затем drop

Будущий режим: `durable-first`.

Поведение:
- queue full блокирует producer до освобождения места
- latency может снова расти, но потерь меньше
- полезно для debugging/log archival mode

## Error handling

Если batch write failed:
- writer логирует ошибку
- retry с backoff
- jobs остаются в памяти до лимита retry/queue
- UI не получает rollback, потому что строка уже показана

Если retry limit превышен:
- jobs можно drop-нуть с warning
- session status/debug metrics должны показывать `log_persistence_degraded`

На `Session.Close()`:
- остановить чтение/команды
- flush writer queue
- дождаться flush с timeout, например `2s`
- если timeout, закрыть session и залогировать количество unflushed jobs

## Tests

### Unit storage

- migration backfills `seq = id` for old rows
- unique `(session_id, seq)` rejects duplicates
- batch insert returns `seq -> id`
- overlays by seq resolve to correct `log_entry_id`
- restore orders by `seq`, not `id` or `created_at`

### Session pipeline

- incoming line is broadcast before slow storage write completes
- MUD command is written to conn before history write completes
- trigger button appears in live output without waiting for DB id
- trigger send command hint targets `entry_seq`
- user command hint persists against `lastVisibleSeq`
- gag line is persisted async but not broadcast
- copy buffer entries get separate seq values and restore correctly
- reconnect during pending write includes in-memory pending entries without duplicates

### Frontend

- `LogEntry.seq` is used as DOM key/data attribute when present
- `command_hint.entry_seq` updates the correct line
- legacy `entry_id` restore still works if `seq` is absent

### Performance

- artificial `AppendLogEntriesBatch` delay of `1s` must not delay websocket output by `1s`
- benchmark read loop with synchronous storage vs async queue
- verify queue flush batches multiple log entries into one transaction

## Rollout steps

1. Add `seq` migration and model fields.
2. Update restore queries and ClientLogEntry JSON to include `seq`.
3. Update frontend to use `seq` for line identity and targeted command hints.
4. Refactor VM effects so live trigger buttons do not write storage directly.
5. Add session async writer and batch storage APIs.
6. Switch incoming output path to broadcast-before-persist.
7. Switch command history/hints to async persistence and send-before-history.
8. Add pending recent ring buffer and merge it into restore for live sessions.
9. Tighten SQLite PRAGMA configuration through DSN or per-connection setup.
10. Add tests and perf regression checks.

## Не входит в MVP

- Crash-proof disk spool outside SQLite.
- Exactly-once persistence after process crash.
- Distributed/multi-process writers for the same SQLite file.
- Replacing `log_overlays.log_entry_id` with `log_seq`.
- Full rewrite of search/FTS around `seq`.

## Open questions

- Queue overflow policy: block briefly, drop oldest non-critical, or switch session to degraded mode?
- Ring buffer size for live restore: `1000`, `5000`, or configurable?
- Should local `#showme/#woutput` echo entries also use the async writer immediately in MVP? Recommendation: yes, because they are visible output and should share the same `seq` model.
- Should history have its own backend-generated `seq` later? Not needed for this fix; log output seq is the latency-critical identity.

## Status: deferred

Follow-up latency instrumentation showed that SQLite is not the primary bottleneck for the current hot case.

Representative measurement for `дост все`:

```text
entries=90 lines=90 bytes=11247 oldest_to_ws=471.122104ms
vm_sum=345.382992ms
vm_gag_sum=78.557266ms
vm_triggers_sum=162.195414ms
vm_subs_sum=104.630312ms
highlight_sum=86.767664ms
db_total_sum=38.391404ms
ws_send=121.79µs
```

Only about `40ms` of the `~471ms` backend delay was SQLite for 90 lines. Async DB persistence remains architecturally useful, but it is deferred until VM/highlight regex hot paths are optimized.
