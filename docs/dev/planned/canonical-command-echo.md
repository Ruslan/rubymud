# Canonical Command Echo: полный план

## Статус

Карточка открыта. Последний аудит фактического кода выполнен **2026-08-22**.

Первый trace-срез решает основной browser happy path, но система пока не даёт
сквозную гарантию «показано ровно то, что действительно было принято transport
write». Остаются расхождения при ошибке записи, нескольких клиентах, отсутствии
текущей строки лога, restore, trigger/map-walk/MCP/timer-командах и в Settings
Logs/Search.

Документ является планом реализации, а не описанием уже гарантированного
поведения. Узкий завершённый срез HTML-export описан отдельно в
`../done/html-log-export-fact.md`.

## Проблема и продуктовый контракт

Пользователь вводит или запускает **исходное выражение**:

```text
#if {$fbash == 1} {сби $target1} {пну $target1}
```

После выбора ветки, раскрытия переменных и aliases реальной командой может стать:

```text
сби гоблин
```

Обычный UI, restore, Logs, search и export должны отвечать на вопрос:
«Что RubyMUD действительно передал transport-слою MUD?». Они не должны выдавать
исходный script за отправленную команду.

Нужно строго различать:

1. `command source` — текст пользователя, hotkey, trigger, timer или агента;
2. `evaluated command` — команда после VM evaluation, но ещё до network write;
3. `sent command event` — один полный command frame, успешно принятый текущим
   transport write;
4. `failed attempt` — вычисленная команда, которую transport не принял полностью;
5. `local-only resolution` — выполнение, не породившее сетевой команды.

Нормальный sent-command echo показывает только пункт 3. Source остаётся в raw
input history для recall и может позднее использоваться в отдельном debug trace.

## История решения

### До первого trace-среза

Клиент оптимистично показывал исходный текст. Сервер независимо выполнял VM и
сохранял раскрытые команды. Live UI мог показать `bash $t1`, а после refresh —
`bash orc`. Это был two-source drift между браузерной догадкой и серверной
истиной.

### 2026-05-23: постановка задачи

Карточка была выделена как отдельная проблема canonical echo. Базовое решение:
показывать финальные команды после alias/variable/`#if`/chain evaluation, а raw
source оставить только для редактирования и диагностики.

### 2026-05-30: первый command-trace срез

Коммит `9464af2` добавил:

- `client_command_id` для непустого browser input;
- серверный `command_trace` с массивом canonical `commands`;
- reconciliation optimistic hint по `client_command_id`;
- удаление pending hint для local-only результата;
- тесты aliases, переменных, `#if` и command chains.

Срез исправил видимый happy path одного browser WebSocket, но сохранил старую
модель хранения через `command_hint` overlay, привязанный к последней строке.
Такой overlay создают browser input, key/button, scheduler и MCP paths; trigger
использует отдельный legacy тип `command`, map-walk идёт в обход этого механизма,
а `source=connect` исключён из `command_hint`.

### 2026-07-07: HTML export

Коммит `c2d1a69` добавил self-contained HTML export, а `dcf2249` зафиксировал его
фактическое состояние в документации. Export объединяет MUD output с legacy
`command_hint` overlays и умеет включать вычисленные команды
input/key/button/scheduler/MCP. Поскольку эти overlays записывались до transport
write, исторические данные являются `legacy_unverified`, а не доказанным sent
stream. TXT download намеренно остался без этого контракта. Trigger overlay типа
`command` в HTML-поток не входит.

### 2026-08-15: аудит текущего состояния

Аудит подтвердил happy path и выявил системные разрывы:

- command history/overlay записываются до `conn.Write`, поэтому write failure
  способен оставить ложную запись «отправлено»;
- terminal trace не гарантирован при ошибке, optimistic hint может зависнуть;
- `command_trace` получает только origin WebSocket;
- без текущего `LogEntry` pending hint и persistent anchor отсутствуют;
- Settings backend возвращает/search-ит commands, но UI их не рисует;
- trigger и map-walk имеют отдельные write paths; scheduler и MCP уже используют
  основной `SendCommand` family. MCP теперь имеет успешный post-write live hint
  для всех браузеров и совпадающий restore overlay, но ещё не first-class event;
  scheduler не имеет полного origin/event контракта;
- HTML и TXT export имеют разные гарантии.

Проверки аудита прошли:

```text
go test ./internal/session ./internal/storage ./internal/web
cd ui && npm test -- --run render.test.ts
```

## Фактическое состояние на 2026-08-15

| Область | Состояние |
|---|---|
| Browser `client_command_id` | Реализован |
| Canonical `command_trace` при успешном browser input | Реализован |
| Alias, `$vars`, `#if`, speedwalk и chains | Реализованы в основном input path |
| Local-only empty trace | Реализован для привязанного optimistic hint |
| Raw input history для recall | Реализована |
| Terminal result при write failure | Не гарантирован |
| Истина «sent» только после transport write | Не реализована |
| Команда без существующего log anchor | Теряется как hint |
| Multi-client live parity | Не реализована |
| Occurrence-level canonical storage | Не реализована |
| MCP live/restore compatibility hint | Реализован после успешного write при наличии log anchor |
| Trigger/map-walk/timer и first-class MCP event parity | Не реализована |
| Restore/search/admin Logs parity | Частично |
| HTML export legacy `command_hint` для input/key/button/scheduler/MCP | Реализован |
| Trigger commands в restore/search/export | Не реализованы |
| TXT canonical commands | Не реализованы |

## Архитектурные решения

### 1. Session владеет отправкой, VM — вычислением

Эта карточка не делает всю существующую VM pure: сегодня VM держит Store и
некоторые local commands меняют runtime/SQLite. Граница уже: VM не владеет
transport write, canonical command-event persistence или WebSocket fanout.

Session-owned execution queue принимает source job целиком. Для обычного
browser/trigger/timer/MCP source она выполняет VM ровно один раз внутри очереди,
получает упорядоченную chain и сразу передаёт её dispatcher. Это сохраняет
admission order: два concurrent source не могут вычислиться A→B, а отправиться
B→A. Trigger job дополнительно несёт immutable copy исходных `Captures` и
trusted target buffer; очередь вызывает именно `ProcessInputWithCaptures`, а не
делает предварительную текстовую подстановку. Иначе capture с `;` меняет момент
command splitting и семантику alias/`#if`. Trusted map-walk job может содержать
уже подготовленные physical frames, поскольку его parser/tracker gate находится
выше VM; повторная evaluation запрещена.

Session-owned dispatcher является единственной точкой, которая:

1. сериализует admission и однократную evaluation source;
2. получает всю evaluated outgoing chain одним job;
3. выполняет полные transport writes;
4. после каждого успеха создаёт canonical sent event;
5. передаёт events общему timeline sequencer.

Отдельный VM effect `send_to_mud` допустим как внутреннее представление, но не
является целью сам по себе. Инвариант важнее формы API: ни один source path не
пишет в `conn` в обход общего dispatcher.

### 2. Определение `sent`

`sent` означает: все байты одного command frame, включая требуемый delimiter,
полностью приняты локальным transport write без ошибки.

Это не утверждение, что удалённый MUD обработал команду: TCP/Telnet не даёт такой
application-level гарантии. Документация и UI не должны обещать remote ACK.

Вся execution chain записывается под одним command write gate, чтобы другой
browser/trigger/timer/MCP job не вклинился между её элементами. `writeFull`
обрабатывает результаты `Write` формально:

| Результат | Действие |
|---|---|
| `n == len && err == nil` | frame `sent`, можно продолжить chain |
| `0 < n < len && err == nil` | продолжить тот же frame под тем же gate |
| `n == 0 && err == nil` | `io.ErrNoProgress`, connection poisoned |
| `n == 0 && err != nil` | frame не sent; закрыть connection |
| `0 < n < len && err != nil` | frame indeterminate; закрыть connection |
| `n == len && err != nil` | frame indeterminate по строгому контракту; закрыть connection |

После indeterminate frame запрещены любые следующие command frames: оборванный
`kill\n` нельзя склеить с будущим `look\n`. Успешные полные frames до отказа
остаются sent events. `partial` означает «есть успешный prefix chain, следующий
frame точно не начал записываться»; `indeterminate` означает «текущий frame мог
уйти частично или полностью». Для каждого write задаётся bounded deadline.

Сервер обязан **создать и попытаться доставить** terminal resolution. Получение
его браузером после разрыва WebSocket гарантировать невозможно; UI обрабатывает
такой reconnect как unknown и никогда автоматически не повторяет команду.

### 3. Raw history не является журналом отправки

`history_entries(kind=input)` хранит source для Arrow/Ctrl+R recall. Normal
non-sensitive browser source сохраняется только после authoritative server
echo-state/redaction decision и завершения execution; сеть могла отказать, но
source всё равно полезен для recall. После terminal non-redacted resolution тот
же raw source разрешено добавить в отдельный browser recall store. Echo-off и
connect source не сохраняются ни на server, ни в browser recall.

Он не доказывает отправку. Запрет persistence для `RawSource` ниже относится к
event table, broadcast и operational logs, но не к этой явно разрешённой recall
history после redaction gate. Новые `expanded` history rows не используются как
canonical audit trail. Settings History явно подписывается как source/recall;
normal sent view живёт в Logs. Для failed/local-only сохраняется только normal
raw source в recall; вычисленный/частично отправленный frame не сохраняется как
canonical command и pending marker удаляется terminal resolution.

### 4. Canonical event не зависит от MUD output row

Команда может быть отправлена до первой строки MUD, после очистки pane или между
двумя ответами. Поэтому «последний видимый log entry» не может быть её identity.

Новый sent event является самостоятельной occurrence-level записью. Опциональный
legacy anchor допускается только внутри compatibility adapter старого UI и не
попадает в новую таблицу, identity, restore cursor, search или export ordering.

### 5. Все command sources используют один контракт

В область задачи входят:

- browser input, hotkeys и buttons;
- aliases и VM command chains;
- triggers/actions;
- timers, ticker subscriptions и delayed commands;
- map-walk;
- MCP `mud_send_command` и другие разрешённые agent sends.

`source=connect` и echo-off password input требуют отдельной security policy:
секретный текст нельзя broadcast/persist/export. Детальная Telnet ECHO работа
остаётся в `password-echo-off-guard.md`, и является prerequisite до включения
`command_events` для browser input.

Telnet echo-state transitions и command admission проходят через один session
actor. На admission execution получает immutable `RedactionMode` и `EchoEpoch`;
режим не может ослабнуть до terminal result всей chain, даже если сразу после
password write приходит `WONT ECHO`. При echo-off успешный transport write даёт private resolution
`sent_redacted`: это не `local_only`, потому что bytes действительно ушли, но
public event и command text не создаются. Server публикует упорядоченный
`echo_state {masked, epoch}`; browser использует его для маскировки, но не считает
локальное состояние доказательством безопасности. Raw source вообще не попадает
в DOM/console/`localStorage` до terminal non-redacted resolution: optimistic UI
показывает только opaque pending marker. Server остаётся authoritative guard и
независимо не пишет secret в history, operational logs, event table или trace
payload. Connect commands подчиняются той же redaction policy и не дублируются в
`history_entries`.

### 6. Multi-client semantics

Протокол разделяет два сообщения:

- public `command_event` с canonical text, `event_id` и stream position — всем
  attached clients данной session;
- private `command_resolution` со status, `client_command_id` и event IDs —
  только transient sink, приславшему request.

Persistent `client_uid` для MVP не нужен. Текущий integer client sink достаточен
на время WebSocket connection; после reconnect старый pending честно становится
`unknown`. `client_command_id` является только UI correlation ID, не idempotency
key: неизвестный результат нельзя автоматически resend-ить.

Observer clients никогда не получают raw source или origin correlation. Origin
может увидеть public event до private resolution или наоборот, поэтому оба path
используют stable `event_id`: public row и reconciliation взаимно дедуплицируются.

## Целевая runtime-модель

Одна обработка source получает backend-generated `execution_id`. Рекомендуемый
формат — случайный 128-bit hex из `crypto/rand`; он не требует SQLite round-trip и
остаётся уникальным после restart.

```go
type CommandExecution struct {
    ExecutionID    string
    SessionID      int64
    ClientCommandID string
    Source         string
    SourceSubtype  string
    SourceRef      string
    RawSource      string // runtime only; recall history allowed after redaction gate
    Captures       []string // runtime-only immutable copy; trigger jobs only
    TargetBuffer   string   // trusted server-selected buffer
    RedactionMode  string   // immutable: normal|sensitive
    EchoEpoch      uint64   // latched at admission
}

type SentCommandEvent struct {
    EventID      string // execution_id + ordinal
    ExecutionID  string
    Ordinal      int
    StreamSeq    int64
    SessionID    int64
    Source       string
    SourceSubtype string
    SourceRef    string
    Buffer       string
    Command      string
    OccurredAt   time.Time
}

type DispatchResult struct { // internal session result
    ExecutionID    string
    Status         string // sent|sent_redacted|partial|indeterminate|failed|local_only|rejected
    Events         []SentCommandEvent
    UncertainFrame bool
    RecallAllowed  bool // derived from latched redaction/source policy
    ErrorCode      string
}

type CommandResolution struct { // private wire DTO
    ExecutionID     string
    ClientCommandID string
    Status          string
    EventIDs        []string
    UncertainFrame  bool
    RecallAllowed   bool // authoritative; false for sensitive/connect/unknown
    ErrorCode       string
}
```

Явный mapper преобразует internal `DispatchResult.Events` в public event DTOs и
`EventIDs` private resolution. `RawSource` не входит в event/broadcast/operational
persistence; единственное разрешённое сохранение — normal recall history после
redaction gate. Тексты ошибок не включают пароль или полный frame при echo-off.

`rejected` в первом срезе относится только к server-boundary validation или
отказу bounded dispatcher queue. Текущий `ProcessInputDetailed` не возвращает
structured VM errors: его diagnostic echoes считаются `local_only`, пока VM не
получит отдельный typed outcome. План не предполагает распознавать ошибку по
тексту echo.

## Целевая persistence-модель

### Общий порядок stream

`occurred_at` и независимые integer PK не могут гарантировать порядок между MUD
output и commands, особенно при async persistence. Поэтому `seq` из
`db-async-persistence.md` расширяется до общего `stream_seq`:

- один session-owned timeline sequencer обслуживает `log_entries` и
  `command_events`;
- до активации session allocator синхронно получает из SQLite durable lease
  диапазона `stream_seq`; crash может оставить gap, но никогда не разрешает
  повторно выдать уже показанный live ID;
- output после transform и command после полного successful write подают
  immutable candidate в sequencer;
- sequencer одним сериализованным actor-step назначает `stream_seq`, вставляет
  row в recent ring, принимает решение persistence enqueue и передаёт событие
  единственной ordered fanout stage;
- SQLite transaction и фактические WebSocket writes выполняются вне sequencer
  step; ordered fanout stage принимает события только из sequencer и начинает
  их рассылку строго по `stream_seq`. Текущий synchronous broadcast может быть
  этой stage до появления отдельных per-client queues;
- `(session_id,stream_seq)` уникален в каждом источнике, а allocator обеспечивает
  отсутствие совпадений между таблицами;
- startup audit проверяет отсутствие cross-table collisions и монотонность
  durable high-watermark; такую целостность нельзя выразить одним SQLite UNIQUE
  index над двумя таблицами;
- MUD response, пришедший сразу после command write, конкурирует только за место
  в sequencer queue; порядок его приёма actor-ом становится canonical live order;
- restore, catch-up, browse, context и search pagination используют composite
  cursor `(stream_seq,sub_order,stable_id)`;
- старые `log_entries` backfill-ятся `stream_seq=id`; legacy overlays наследуют
  `stream_seq` anchor row и получают deterministic sub-order после output.

Это заменяет отдельный log-only `seq` из DB async плана; конкурирующего allocator
быть не должно. Live и pending recent ring также индексируются `stream_seq`.
Lease обновляется заранее вне timeline actor. Если следующий диапазон не удалось
зарезервировать до исчерпания текущего, session прекращает admission, закрывает
transport и не публикует candidate без ID; переиспользование sequence запрещено.
Production lease большой, а тестовый allocator использует маленькие диапазоны
для детерминированной проверки renew/failure/crash gaps.

### Таблицы и миграция

Следующая свободная миграция заменяет неисполненный draft `008_log_seq.sql` из
DB async плана и явно меняет обе стороны stream:

```sql
ALTER TABLE log_entries ADD COLUMN stream_seq INTEGER NOT NULL DEFAULT 0;
UPDATE log_entries SET stream_seq = id WHERE stream_seq = 0;
CREATE UNIQUE INDEX log_entries_session_stream_seq_uidx
    ON log_entries(session_id, stream_seq);
CREATE INDEX log_entries_session_window_stream_seq_idx
    ON log_entries(session_id, window_name, stream_seq);
```

Фактический номер migration выбирается по текущему latest schema; имя `008` не
переиспользуется. Затем добавить occurrence-level таблицу `command_events`:

```text
id           INTEGER PRIMARY KEY
session_id   INTEGER NOT NULL
stream_seq   INTEGER NOT NULL
event_id     TEXT NOT NULL
execution_id TEXT NOT NULL
ordinal      INTEGER NOT NULL CHECK (ordinal >= 0)
source       TEXT NOT NULL
source_subtype TEXT NOT NULL DEFAULT ''
source_ref   TEXT NOT NULL DEFAULT ''
window_name  TEXT NOT NULL DEFAULT 'main'
command      TEXT NOT NULL
occurred_at  TEXT NOT NULL -- UTC RFC3339Nano
```

Та же migration добавляет durable allocator state:

```text
stream_sequence_heads
session_id        INTEGER PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE
reserved_through  INTEGER NOT NULL CHECK (reserved_through >= 0)
```

Migration инициализирует `reserved_through` текущим максимумом `stream_seq` из
обеих таблиц. Перед запуском session короткая `BEGIN IMMEDIATE` transaction
атомарно сдвигает high-watermark на размер lease и возвращает диапазон
`old+1..new`; background renew делает то же заранее. Эта служебная запись —
единственный синхронный SQLite prerequisite на activation/редкое пополнение, а
не per-event write. Любой best-effort persistence drop уже находится внутри
durably reserved диапазона и потому не ведёт к повтору после restart.

Инварианты и индексы:

- UNIQUE `(session_id,stream_seq)`;
- UNIQUE `(session_id,event_id)`; `event_id=execution_id:ordinal`, поэтому второй
  дублирующий UNIQUE для execution/ordinal не нужен;
- index `(session_id,window_name,stream_seq)` только после проверки query plan;
- FK `session_id -> sessions(id) ON DELETE CASCADE`, при этом migration/open tests
  обязаны проверить `PRAGMA foreign_keys` на реальном pooled connection path.

Anchor в MVP отсутствует: legacy presentation anchor не является identity и не
оправдывает межтабличный FK. `source_ref` формируется trusted server adapter:
decimal trigger rule ID, timer/delay name или ID, map-walk job ID, MCP request/tool
ID; пустая строка означает отсутствие устойчивого source object. Пустой
`command` допустим и означает реально отправленный bare newline (empty Enter).

Таблица содержит только успешные sent events. `failed`, `rejected` и `local_only`
являются terminal runtime resolutions, но не нормальным sent log. Счётчики и
структурированные server logs дают диагностику без сохранения потенциального
секрета как будто это команда.

`command_events` не заменяет `history_entries`. Substring search сначала получает
измеренный scan на текущем corpus; B-tree по `command` для `*query*` не добавляется.
Отдельный FTS вводится только после benchmark/query-plan evidence.

### Выбранный rollout persistence

Срез transport-truth может временно сохранить legacy overlay/history синхронно,
но только **после** network write и вне command write gate. Это не задерживает
байты для MUD, хотя до среза 2 ещё может задержать terminal resolution. DB failure
после successful write не меняет transport outcome: private resolution остаётся
sent, потому что сеть необратима. Деградация persistence не является частью
terminal command resolution и публикуется отдельным session-health event. Raw
input сохраняется после execution, включая failed/local-only source, как recall
data, но только если authoritative redaction gate разрешил текст.

Новая `command_events` таблица не включается до общего session async writer из
`db-async-persistence.md`. Canonical card активирует этот общий foundation:
`stream_seq`, bounded queue, retry/degraded metrics и recent ring для reconnect.
Частный command writer запрещён. Sequencer после successful write делает:

1. allocate `stream_seq` + insert recent ring;
2. пытаться enqueue persistence job не дольше `10ms`;
3. schedule public fanout в том же sequence order;
4. вернуть event IDs dispatcher-у для private resolution.

Persistence queue имеет capacity `8192`. При timeout item остаётся только в
recent ring до eviction, session health становится degraded, счётчик
`persistence_enqueue_dropped` растёт; gameplay не блокируется дальше. При ring
eviction неперсистированного item пишется отдельный loss counter/log. Late batch
failure retry-ится с bounded backoff и отражается только в health. На Close общий
writer получает до `2s` на flush. Это сознательный `interactive-first`,
best-effort durability contract.

Без recent ring async rollout не принимается: reconnect до flush должен merge-ить
DB rows и pending live rows по `stream_seq`.

## WebSocket-протокол

### Browser request

```json
{
  "type": "command",
  "command": "bash $target",
  "ui_source": "input",
  "client_command_id": "browser-local-id"
}
```

`ui_source` допускает только `input|key|button`; server сохраняет это как
`source_subtype` при trusted `source=browser`. Empty Enter тоже получает
`client_command_id` и создаёт command event с пустым `command` после успешной
записи newline. Повтор активного `client_command_id` в одном transient sink
отклоняется как `rejected`; это защита correlation map, не idempotency/resend.

### Public canonical event

```json
{
  "type": "command_event",
  "event_id": "server-id:0",
  "execution_id": "server-id",
  "ordinal": 0,
  "stream_seq": 1234,
  "source": "browser",
  "source_subtype": "input",
  "buffer": "main",
  "command": "bash orc",
  "occurred_at": "2026-08-15T12:34:56.123456789Z"
}
```

Сообщение broadcast-ится всем attached clients и не содержит raw source или
`client_command_id`.

### Private terminal resolution

Для каждого принятого request, включая empty Enter, сервер создаёт и пытается
отправить origin sink terminal resolution даже при validation/transport error:

```json
{
  "type": "command_resolution",
  "execution_id": "server-id",
  "client_command_id": "browser-local-id",
  "status": "sent",
  "event_ids": ["server-id:0"],
  "uncertain_frame": false,
  "recall_allowed": true,
  "error_code": ""
}
```

Browser держит submitted source только в transient request state. Он может
перенести его в отдельный recall store лишь при `recall_allowed=true`; при false
или потерянном resolution source очищается. Это поле вычисляется из latched
server redaction mode, а не из текущего client `echo_state`.

Совместимость: на переходном этапе origin decoder принимает старый
`command_trace.commands: string[]` и новый pair event/resolution. Сервер не должен
поддерживать два transport write path; legacy shape является только wire adapter.
Stable event IDs делают безопасной любую очередность public event и private
resolution.

Execution queue использует bounded submit-and-wait: WebSocket handler ждёт
terminal result, поэтому отдельный `accepted` event в MVP не нужен. Если submit
переведут в fire-and-forget, `accepted` ACK станет обязательным; terminal
resolution всё равно создаётся для каждого принятого ID.

## Live UI и reconciliation

Pending command хранится независимо от существующих log entries:

- map key: локальный `client_command_id` внутри текущего WebSocket lifecycle;
- собственная компактная pending row в активном buffer;
- row содержит только opaque pending marker и не показывает raw source до
  terminal non-redacted resolution; sensitive source не превращается в text row
  вообще;
- отсутствие MUD output не мешает создать pending row.

Terminal behavior:

- `sent`: pending заменяется canonical events;
- `sent_redacted`: pending удаляется без text/event row;
- `partial`: показываются только успешно sent events, отдельно — безопасная
  ошибка оставшейся части chain;
- `indeterminate`: показывается только подтверждённый prefix и предупреждение,
  что последний frame мог уйти частично; resend не предлагается;
- `failed/rejected`: pending удаляется, показывается status error;
- `local_only`: pending удаляется без sent row.

Дополнительные инварианты:

- `clearOutput` очищает pending registry и отсоединяет DOM references;
- pruning не удаляет bookkeeping без явного terminal/disconnect handling;
- stable `event_id` дедуплицирует live, catch-up и restore;
- при reconnect старый unresolved pending становится `disconnected/unknown`, а
  не остаётся вечной «отправленной» строкой;
- live rows и restored rows используют один renderer данных, а не повторную VM
  evaluation.

## Storage, restore, search и export

### Новый read contract

Logs browse/context/search получают canonical command events как отдельный тип
строки рядом с MUD output. Общий DTO:

```go
type StreamRow struct {
    Kind       string // output|command
    StableID   string // stream:<seq>:0|legacy:<overlay_id>
    EventID    string // command only; empty for output/legacy
    DBID       int64  // optional after persistence; never live identity
    StreamSeq  int64
    SubOrder   int
    Buffer     string
    OccurredAt string // RFC3339Nano
    Text       string
    Source     string
    SourceSubtype string
    Provenance string // confirmed|legacy_unverified
}
```

Opaque cursor кодирует `(stream_seq,sub_order,stable_id)`; отдельного
`before_seq/after_seq` недостаточно, потому что несколько legacy overlays делят
sequence anchor. Новый context endpoint принимает `stable_id` или полный cursor,
поэтому command-only row может быть anchor и page boundary не разрывает legacy
group. Browse/search limit считается по совокупным rows, restore выбирает
последние N combined rows per buffer, а catch-up продолжает с последнего cursor.

Новые live output и commands получают `StableID` из `stream_seq` до async INSERT;
SQLite `id` является только optional metadata. Старые numeric `log_entry_id`
endpoints остаются compatibility adapters на время миграции.

UI обязан отображать:

- `source=command`;
- canonical text;
- timestamp и buffer;
- визуальное отличие, например `> command`;
- видимый `legacy/unverified` badge для transport-unconfirmed adapter rows;
- command-only search match, даже если соседний MUD output не содержит query.

Первый search implementation объединяет текущий output query с bounded scan
`command_events.command`; corpus benchmark и `EXPLAIN QUERY PLAN` определяют,
нужен ли отдельный FTS. Обычный B-tree не ускоряет substring `*query*`. Нельзя
помещать command в `log_entries_fts` через фиктивный output row без измерения:
это меняет FTS и retention semantics.

### Backward compatibility

Старые данные остаются читаемыми:

1. после cutover новые sends пишутся только в `command_events`;
2. restore/search/export временно объединяют `command_events` со старыми overlays
   `command_hint` и trigger `command`;
3. legacy rows маркируются `legacy_unverified`, потому что создавались до
   transport write; synthetic stable ID строится из overlay PK;
4. adapter выбирает ровно одно представление legacy command — standalone row,
   а старое вложенное `ClientLogEntry.Commands` на этом read path отключается;
5. исторический backfill не обязателен: overlay timestamp/anchor не всегда
   позволяет восстановить точную occurrence semantics;
6. после одного стабильного release legacy read adapter можно оставить read-only
   либо удалить отдельной миграцией с явным release note.

### Export policy

- нейтральный HTML label `Include commands` читает новую таблицу и legacy adapter;
- HTML сохраняет текущий default `commands=1`;
- TXT получает отдельный `Include commands in TXT` toggle, default `off`,
  и простой prefix `> `; это сохраняет прежний download без скрытой смены default;
- confirmed rows экспортируются как `> command`, legacy как `? > command`; HTML
  добавляет `data-provenance="confirmed|legacy_unverified"` и видимую legend;
- trigger/map-walk/MCP/timer events включаются по тем же правилам, что browser
  commands;
- `source=connect`, echo-off/redacted и failed attempts не экспортируются.

## Источники команд и единая точка отправки

Переводить paths нужно по одному, с source-specific regression tests:

| Source | Canonical source label | Особые проверки |
|---|---|---|
| Browser/hotkey/button | `browser` + subtype | origin correlation, multi-client |
| Trigger/action | `trigger` | matched entry только в `source_ref` metadata |
| Timer/ticker/delay | `timer` | ordering, stop/reload races |
| Map walk | `map_walk` | pause/stop, door/open command chain |
| MCP | `mcp` | caller result и browser broadcast |
| Initial commands | `connect` | не показывать и не экспортировать секреты |

Source provenance назначает server boundary. Browser не может прислать trusted
`connect`, `trigger` или `mcp`: значения `input/key/button` нормализуются в
`source=browser` и allowlisted subtype. Buffer также назначается сервером:
browser/MCP/timer/map-walk по умолчанию `main`, trigger наследует matched buffer,
а другой buffer разрешается только конкретным trusted adapter.

Execution queue принимает raw source и внутри одного serialized job ровно один
раз запускает VM, после чего dispatcher отправляет всю evaluated chain. Для
trigger выбирается capture-aware entrypoint с immutable `Captures` из job.
Map-walk является явным trusted исключением: он передаёт уже подготовленные
physical frames после tracker/parser gate; door/open/move commands одного
разрешённого шага идут одним serialized job и не проходят VM повторно.

Telnet negotiation frames не создают command events, но transport пишет их под
тем же raw write mutex, чтобы IAC bytes не перемежались с chunks `writeFull`.
Следовательно forbidden pattern — не любой низкоуровневый write, а command frame
в обход dispatcher или control frame в обход transport write gate.

Фактически MCP и scheduler уже входят в основной `SendCommand` family; для них
нужно исправить trusted provenance, origin resolution и scheduling metadata, а
не создавать ещё один evaluation path. Trigger и map-walk остаются отдельными
реальными write paths до миграции.

## План реализации: repro-first срезы

Каждый срез начинается одним узким failing repro, подтверждает red, реализует
только соответствующее поведение и заканчивается полностью green suite. Матрица
ниже — inventory, а не предложение коммитить девять красных тестов заранее.

### Срез 1. Security redaction и transport truth в существующей модели

Цель — закрыть ложное «sent» и secret leak до новой схемы.

Последовательные repro/fix пары:

1. `WILL ECHO` включает authoritative echo-off state; browser маскирует input и
   не пишет secret в console/optimistic row/`localStorage`, server не пишет его в
   history/operational log/overlay. Command admission лочит redaction/epoch до
   конца chain; race `WILL -> submit/write -> WONT до history enqueue` не
   сохраняет password. `WONT ECHO` возвращает обычный режим только для следующих
   admissions.
2. immediate write error не создаёт canonical overlay; расширенный legacy
   origin-only `command_trace` всегда завершает и удаляет pending.
3. short nil write дописывается под тем же gate; zero-progress превращается в
   bounded error и закрывает connection.
4. partial+error и full-length+error дают `indeterminate`, закрывают connection
   и не создают event для uncertain frame.
5. chain с successful prefix и последующим zero-byte failure сохраняет только
   prefix и возвращает `partial`.
6. concurrent executions и Telnet control frame не перемежают command bytes.
7. raw input сохраняется после execution как recall data; local-only source
   остаётся recallable, но не становится sent. Browser получает raw recall право
   только вместе с terminal non-redacted resolution; до него DOM хранит opaque
   marker, а не source text.
8. source от browser нормализуется и не может spoof-ить `connect/trigger/mcp`.
9. empty Enter получает correlation ID и после successful newline write даёт
   empty-text sent result; duplicate active ID получает rejected result.

До first-class events legacy presentation anchor захватывается один раз до
execution и не переопределяется между chain items. Срез ещё не решает отсутствие
anchor, exact stream ordering и multi-client restore.

### Срез 2. Общий `stream_seq`, async writer и recent ring

Это совместный foundation с `db-async-persistence.md`, а не частный command
writer:

1. repro migration/backfill и timeline sequencing concurrent candidates;
2. одной migration добавить `log_entries.stream_seq`, пустую `command_events`
   table, `stream_sequence_heads` и индексы; backfill high-watermark максимумом
   обеих stream-таблиц;
3. заменить planned log-only `seq` на общий sequencer/`stream_seq` и durable
   range leases с crash-gap/no-reuse тестами;
4. добавить bounded session writer, batch transaction, retry/health state;
5. перенести raw history enqueue из command critical path;
6. добавить recent ring и DB+memory merge при reconnect/catch-up;
7. проверить exact fanout order, queue timeout/drop, ring eviction, close flush и
   ordering при out-of-order DB batches;
8. измерить latency и throughput на текущем `mudhost.db`.

Срез принимается только с green migration, concurrency и reconnect tests.

### Срез 3. First-class `command_events` и unified reads

1. repro occurrence duplicates, exact output/command ordering и command context;
2. добавить storage model и batch/list helpers поверх dormant таблицы;
3. генерировать `execution_id/event_id/stream_seq` до DB;
4. записывать только successful non-redacted events after-write;
5. ввести `StreamRow` и composite cursor для restore/browse/context;
6. добавить legacy standalone adapter с `legacy_unverified` и synthetic IDs;
7. до любого source cutover перевести **все уже существующие consumers** на
   dual-read new+legacy: restore/catch-up, storage search/context и HTML export;
   добавить contract tests, что dormant new rows уже видны каждому consumer;
8. оставить legacy writes включёнными: новый read/write path пока dormant до
   атомарного browser UI cutover;
9. benchmark substring scan; FTS добавлять только при доказанной необходимости.

### Срез 4. Live UI и multi-client

1. repro pending command до первой MUD line;
2. вынести pending из привязки к последнему `LogEntry`;
3. внедрить public `command_event` и private `command_resolution` с legacy
   decoder fallback;
4. проверить обе очередности event/resolution и stable-ID dedupe;
5. broadcast successful events двум session clients без source leakage;
6. закрыть clear/prune/sleep-wake/reconnect состояния;
7. запретить automatic retry для unknown/indeterminate result;
8. в одном deploy включить `source=browser` во внутреннем cutover allowlist:
   писать только `command_events`, отключить browser `command_hint`, включить
   public/private protocol; gate разрешает switch только после green dual-read
   consumer contract из среза 3, до этого deploy старый path остаётся целым;
9. добавить WebSocket integration и focused renderer tests без full-pane rerender.

### Срез 5. Перевести остальные command sources

В порядке риска:

1. triggers/actions;
2. timer/ticker/delay;
3. MCP;
4. map-walk вместе с door/open/phrase steps;
5. connect path через уже готовую redaction policy.

После каждого переноса запретить прямой write статическим поиском/test helper и
проверить, что routing/tracker semantics не изменились.

Cutover выполняется per source: пока adapter не включён в allowlist, он пишет
только legacy representation; после включения — только `command_events`. Dual-read
остаётся для истории, dual-write одной execution запрещён во избежание duplicates.
Каждый feature switch атомарно проверяет, что deployed restore/catch-up,
search/context и HTML consumer versions уже понимают новый stream; иначе source
остаётся на legacy write. Поэтому перенос trigger/MCP/scheduler не создаёт окно,
в котором команды исчезают из существующего export или поиска.

Каждый adapter получает trusted source/buffer/source_ref и enqueue-ит исходный
source job. Trigger также передаёт immutable captures; evaluation происходит
ровно один раз в session queue. Только map-walk enqueue-ит trusted prepared
physical chain, сохраняя room-confirmation gate. Для timer/MCP проверяется уже
существующая `SendCommand` интеграция, а не создаётся второй VM path.

### Срез 6. Полный Settings UI и TXT export

1. расширить UI types полем canonical command rows;
2. показать commands в browse, context и search;
3. выделять command-only match;
4. добавить pagination tests без загрузки всего corpus;
5. проверить уже переключённый в срезе 3 HTML new+legacy export в UI;
6. добавить отдельный default-off TXT commands toggle;
7. сверить timestamps/timezone и buffer filters на всех поверхностях.

### Срез 7. Cleanup и закрытие карточки

1. перестать писать legacy expanded history/command overlays;
2. обновить `log-browsing.md`, `html-log-export-fact.md`, engine/MCP docs;
3. удалить только действительно неиспользуемый write code после полного `rg` и
   migration compatibility review;
4. прогнать full Go/UI suites, race tests, production build и smoke reconnect;
5. перенести карточку в `done` с фактическим контрактом и известными non-goals.

## Матрица обязательных тестов

### Session/transport

- alias, `$var`, `#if`, speedwalk и multi-command chain;
- local-only execution;
- full write, short nil write, zero-progress, immediate error, partial+error,
  full-length+error и disconnect between chain items;
- whole-chain serialization и отсутствие byte interleaving с другим execution;
- отсутствие interleaving между Telnet control frame и command write;
- raw input остаётся recallable, failed evaluated command не становится sent;
- immutable redaction latch переживает race `WILL ECHO -> submit/write -> WONT
  ECHO -> history enqueue`; secret отсутствует во всех server/browser stores;
- trusted source normalization и запрет browser provenance spoofing;
- empty Enter создаёт sent event с пустым text, а duplicate active
  `client_command_id` получает rejected resolution;
- для каждого принятого browser ID создаётся terminal resolution; доставка
  проверяется пока origin WebSocket остаётся доступен.

### Storage

- occurrence duplicates не схлопываются;
- unique event IDs, общий `stream_seq` и deterministic chain order;
- timeline actor сохраняет одинаковый concurrent output/command порядок в
  fanout, ring, persistence и restore/catch-up;
- durable lease не повторяет live `stream_seq` после dropped enqueue и restart;
  renew failure с маленьким test lease закрывает session до повторного ID;
- legacy synthetic-ID adapter не создаёт duplicates и помечает unverified;
- command-only search, unified cursor и paged context;
- dormant confirmed row до source cutover виден restore, catch-up, search,
  context и HTML через new+legacy consumer adapter;
- echo-off/connect/failed exclusions.

### WebSocket/UI

- pending без MUD line;
- public event содержит timestamp/subtype, private resolution — только event IDs;
- canonical replace для одного и нескольких commands;
- sent-redacted/partial/indeterminate/failed/local-only states;
- два clients, origin и observer;
- catch-up/restore dedupe;
- clear, pruning, sleep/wake и reconnect;
- echo-off не попадает в console, optimistic DOM или `localStorage`;
- no full-pane rerender regression.

### Source parity

- trigger, timer, MCP и map-walk создают тот же event shape;
- trigger `%1`, содержащий `;`, проходит capture-aware VM ровно один раз и
  сохраняет прежнюю alias/`#if`/command-splitting семантику;
- map-walk pause/stop не публикуют unsent remaining steps;
- MCP ответ и browser event согласованы;
- direct connect credentials не появляются в normal surfaces.

### Admin/export

- browse/context/search показывают commands;
- command-only search визуально содержит match;
- HTML default-on и отдельный TXT default-off toggle, prefix, buffer и timezone;
- legacy rows имеют badge/prefix и machine-readable provenance;
- highlights/substitutions/gags не меняют canonical command text;
- большая streaming export выборка не буферизуется целиком.

## Observability и отказоустойчивость

Добавить метрики/structured logs:

- executions accepted/resolved by status;
- successful command events by source;
- transport write errors и partial writes;
- persistence queue depth/retries/drops;
- unmatched terminal resolutions на client;
- live/restore duplicate suppression count.

Если transport write успешен, а persistence временно недоступна, live event
остаётся истинным, а session помечает `command_persistence_degraded`. Уже
поставленная в writer job retry-ится с bounded backoff. Если enqueue превысил
лимит `10ms`, отдельного неограниченного retry нет: event остаётся в recent ring
до eviction, drop учитывается метрикой и health-сигналом. Нельзя откатывать уже
показанный sent event: сеть необратима.

При переполнении действует выбранная выше политика: максимум `10ms` ожидания,
затем best-effort loss с сохранением в recent ring до eviction и отдельным
session-health сигналом. Потеря persistence наблюдаема; MUD loop не зависает
безгранично.

## Риски и ограничения

1. TCP write success не равен выполнению команды MUD — UI говорит `sent`, не
   `executed`.
2. Dual-read legacy/new способен дать duplicates — нужны provenance и fixtures.
3. Persisting raw source расширяет поверхность утечки секретов — в normal event
   table source text не хранится.
4. Async persistence может временно расходиться с reconnect restore — общий
   recent ring/queue из DB async плана должен закрыть окно.
5. Перевод map-walk нельзя менять отдельно от tracker confirmation semantics.
6. Новый поток не должен добавлять DB/network ожидание в game read loop.

## Не входит в задачу

- доказательство, что MUD исполнил команду;
- полноценный VM debugger с исходным expression trace;
- шифрование всей базы и credential vault;
- изменение alias syntax или matcher semantics;
- глобальный cross-session command search;
- durable multi-process ownership одной session.

## Критерии закрытия

Карточка переносится в `done`, когда одновременно выполнено следующее:

1. Normal echo содержит только полные commands, успешно принятые transport write.
2. Failure/local-only/sent-redacted/partial/indeterminate честно завершают origin
   pending state, пока WebSocket доступен; reconnect оставляет unknown без retry.
3. Alias, variable, `#if`, speedwalk и chain source не выдаются за sent text.
4. Browser, trigger, timer, MCP и map-walk используют единый dispatcher.
5. Session execution queue сохраняет admission/evaluation/whole-chain order, а
   timeline sequencer одинаково упорядочивает ring, persistence и fanout.
6. Два клиента видят один и тот же canonical sent stream без raw source leakage.
7. Команда не зависит от наличия MUD output anchor.
8. Общий `stream_seq` и composite cursor обеспечивают одинаковый порядок live,
   catch-up и restore
   без duplicates, включая reconnect до async flush через recent ring.
9. Settings browse/context/search отображают confirmed command rows,
   command-only matches и видимую маркировку legacy-unverified rows.
10. HTML и отдельный default-off TXT toggle читают тот же canonical stream и
    явно маркируют legacy-unverified commands.
11. Echo-off/connect secrets не попадают ни в console, `localStorage`, DOM,
    normal storage, broadcast или export. Для non-sensitive failed/local-only
    execution вычисленные frames не попадают в canonical surfaces, pending marker
    удаляется, но raw source после terminal non-redacted resolution может жить
    только в явно отделённом server/browser recall history.
12. Empty Enter, partial/indeterminate writes и duplicate correlation IDs имеют
    явную проверенную семантику.
13. Repro-first, race, full Go/UI, production build и reconnect smoke проходят.
14. Документация описывает фактический контракт и legacy compatibility, после
    чего этот файл заменяется кратким fact-документом в `docs/dev/done/`.
