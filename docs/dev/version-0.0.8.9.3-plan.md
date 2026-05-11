# `#sub` — подстановка текста

## Контекст

`#sub` (полная форма `#substitute`) — визуальная замена текста от MUD-сервера до вывода на экран. Позволяет: сокращать длинные сообщения, исправлять опечатки, скрывать строки (gag), добавлять акценты.

Ключевое решение: `#sub` не меняет canonical log. В `log_entries.raw_text/plain_text` хранится оригинальная строка от MUD после `normalizeLine/stripANSI`, а результат замен хранится как overlay в `log_overlays` и применяется при отображении.

Стандарт де-факто (JMC, TinTin++): триггеры работают на оригинальном тексте, замена происходит после триггеров.

## Pipeline

### Текущий

```
MUD → normalizeLine → stripANSI → MatchTriggers → AppendLogEntry → ApplyHighlights → broadcast
         raw            plain       на оригинале      raw+plain в БД      на raw, transient
```

### Новый (с `#sub`)

```
MUD → normalizeLine → stripANSI → CheckGags → MatchTriggers → ApplySubsAndCollectOverlays → AppendLogEntryWithOverlays → ApplyEffects → ApplyHighlights → broadcast
         raw            plain        |          на оригинале       display text + overlays       raw+plain + overlays DB    buttons/sends   на display raw
                                     |
                               gag-правила
                               (is_gag=true)

Если gag сработал:

MUD → normalizeLine → stripANSI → CheckGags → AppendLogEntryWithOverlays(original+gag) → return
                                      скрыто       raw+plain + hidden marker в БД       no triggers, no broadcast
```

**Порядок обоснован**:
- **Canonical log не мутируется**: оригинальный текст доступен для отладки, экспорта, поиска и будущих режимов отображения
- **Gag до triggers**: скрытая строка не должна триггерить логику
- **Gag сохраняется как hidden overlay**: строка исчезает из обычного UI, но не теряется из журнала
- **Triggers на оригинале**: замена не ломает автоматизацию (JMC/TinTin++ совместимость)
- **Subs после triggers**: визуальная замена для отображения, сохранённая как overlay
- **AppendLogEntryWithOverlays до ApplyEffects**: effects/buttons получают стабильный `log_entry_id`; sub/gag overlays пишутся атомарно с log entry
- **Highlights после subs**: красят уже заменённый текст

## Модель данных

### Правила

Таблица `substitute_rules` (по аналогии с `highlight_rules`):

```sql
CREATE TABLE IF NOT EXISTS substitute_rules (
  id INTEGER PRIMARY KEY,
  profile_id INTEGER NOT NULL,
  position INTEGER NOT NULL DEFAULT 0,
  pattern TEXT NOT NULL,          -- regex-template, поддерживает $vars как literal-фрагменты
  replacement TEXT NOT NULL,      -- template с $vars и %0..%N для capture groups
  is_gag INTEGER NOT NULL DEFAULT 0,  -- 1 = gag (скрытие строки), применяется до triggers
  enabled INTEGER NOT NULL DEFAULT 1,
  group_name TEXT DEFAULT 'default',
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
);
```

`is_gag` — ключевое решение: gag и sub хранятся в одной таблице, но флаг определяет, на каком этапе пайплайна применяется правило (gag — до triggers, sub — после).

`pattern/replacement` хранятся как шаблоны, а не как уже раскрытый текст. Это нужно для правил вида:

```text
#substitute {$target1} {[F1] $target1}
#substitute {$tank1} {[Shift-F5] $tank1}
```

Такие правила должны использовать актуальные значения переменных при обработке новых входящих строк. При этом уже обработанная история не пересчитывается: конкретный результат каждой замены сохраняется в `log_overlays`.

В `pattern` переменные подставляются как literal-текст через `regexp.QuoteMeta`, чтобы значение переменной не становилось неожиданным regex-фрагментом. Например, если `$target1 = King\dark`, effective regex получает `King\\dark` и матчится на буквальный `King\dark`.

Остальная часть `pattern` остаётся regex. Capture groups поддерживаются:

```text
#sub {Вы сбили (.+) своим} {%1 ->BASH}
```

Если входящая строка `Вы сбили орка своим ударом`, replacement получит `%1 = орка`.

Go struct:
```go
type SubstituteRule struct {
    ID          int64      `gorm:"primaryKey" json:"id"`
    ProfileID   int64      `json:"profile_id"`
    Position    int        `json:"position"`
    Pattern     string     `json:"pattern"`
    Replacement string     `json:"replacement"`
    IsGag       bool       `json:"is_gag"`
    Enabled     bool       `json:"enabled"`
    GroupName   string     `json:"group_name"`
    UpdatedAt   SQLiteTime `json:"updated_at"`
}
```

### Применённые замены

Для строк журнала используем существующую таблицу `log_overlays`. Новые поля в `log_entries` не добавляем.

`log_entries.raw_text/plain_text` остаются оригиналом. `log_overlays` описывает, какие UX-слои были применены к конкретной строке в момент получения.

В SQL `log_overlays` уже содержит нужные колонки:

```sql
CREATE TABLE IF NOT EXISTS log_overlays (
  id INTEGER PRIMARY KEY,
  log_entry_id INTEGER NOT NULL,
  overlay_type TEXT NOT NULL,
  layer INTEGER NOT NULL DEFAULT 0,
  start_offset INTEGER,
  end_offset INTEGER,
  payload_json TEXT NOT NULL,
  source_type TEXT NOT NULL,
  source_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (log_entry_id) REFERENCES log_entries(id) ON DELETE CASCADE
);
```

Go struct нужно расширить до фактической SQL-схемы:

```go
type LogOverlay struct {
    ID          int64      `gorm:"primaryKey"`
    LogEntryID  int64      `gorm:"index" json:"log_entry_id"`
    OverlayType string     `gorm:"index" json:"overlay_type"`
    Layer       int        `json:"layer"`
    StartOffset *int       `json:"start_offset"`
    EndOffset   *int       `json:"end_offset"`
    PayloadJSON string     `json:"payload_json"`
    SourceType  string     `json:"source_type"`
    SourceID    string     `json:"source_id"`
    CreatedAt   SQLiteTime `json:"created_at"`
}
```

Overlay для sub:

```json
{
  "overlay_type": "substitution",
  "source_type": "substitute_rule",
  "source_id": "123",
  "layer": 10,
  "start_offset": 12,
  "end_offset": 31,
  "payload_json": {
    "replacement_raw": "short text",
    "replacement_plain": "short text",
    "rule_id": 123,
    "pattern_template": "long (.*) text",
    "effective_pattern": "long (.*) text"
  }
}
```

`start_offset/end_offset` — byte offsets в текущем `plain`-тексте на момент применения конкретного overlay. Это совпадает с индексами Go `regexp` и с текущим `plainRangeToRawRange()`.

Overlay для gag:

```json
{
  "overlay_type": "gag",
  "source_type": "substitute_rule",
  "source_id": "123",
  "layer": 0,
  "payload_json": {
    "rule_id": 123,
    "pattern_template": "$spamPattern",
    "effective_pattern": "spam line"
  }
}
```

`gag` не нуждается в offsets: он скрывает всю строку.

Для overlays, созданных `#sub/#gag`, используем `source_type = 'substitute_rule'`, `source_id = rule.ID`. `rule_id` в `payload_json` дублируется для удобства debug/export tooling.

## Алгоритм `ApplySubsAndCollectOverlays`

Принимает оригинальные `raw` и `plain`. Возвращает display-версию `(displayRaw, displayPlain)` и список overlay-записей, которые позволяют воспроизвести тот же результат при restore.

Оригинальные `raw/plain` не мутируются и пишутся в `log_entries` как есть.

Запись `log_entries` и generated `substitution/gag` overlays должна идти через единый storage helper, например `AppendLogEntryWithOverlays`, внутри одной DB-транзакции. Нельзя допускать состояние, где live display был изменён, но overlay не сохранился: restore/search/debug начнут расходиться с тем, что видел пользователь.

По каждому sub-правилу в порядке `position`:
1. Раскрываем переменные в `Pattern`: `effectivePattern = substitutePatternVars(rule.Pattern)`, где значения переменных проходят через `regexp.QuoteMeta`
2. Компилируем regex из `effectivePattern`
3. Находим все совпадения в текущем `displayPlain` (`FindAllStringSubmatchIndex`)
4. Отбрасываем zero-length matches (`start == end`)
5. Обрабатываем совпадения **с конца** (как в `ApplyHighlights`) — чтобы индексы не плыли
6. Для каждого совпадения:
   - `plainRangeToRawRange(displayRaw, start, end)` — маппинг текущих plain-индексов на текущий raw, пропуская ANSI
   - Раскрываем переменные в `Replacement`: `replacementTemplate = substituteVars(rule.Replacement)`
   - Строим фактический replacement из `replacementTemplate`: `%0` → весь match, `%1` → capture group 1, `%2` → capture group 2, и т.д.
   - `replacementRaw` — фактическая строка replacement как raw; `replacementPlain = stripANSI(replacementRaw)`
   - Добавляем overlay `substitution` со `start_offset/end_offset` в координатах текущего `displayPlain`, `layer` монотонно увеличивается
   - В payload сохраняем `replacement_raw/replacement_plain`, `rule_id`, `pattern_template`, `effective_pattern`
   - Заменяем segment в `displayRaw` на `replacementRaw`
   - Заменяем segment в `displayPlain` на `replacementPlain`

Порядок раскрытия replacement важен: сначала `$vars`, потом `%0..%N`. Так текст, пришедший от MUD и попавший в capture group, не будет случайно интерпретирован как переменная VM.

`%0..%N` берутся из regex capture groups. `%0` — весь match, `%1` — первая capture group, `%2` — вторая и т.д.; отсутствующая группа заменяется на пустую строку.

При записи копий в дополнительные буферы (`BufferAction=copy`) sub-overlays нужно дублировать для каждой фактической `log_entry`, потому что `log_overlays.log_entry_id` указывает на конкретную строку журнала.

### Replay overlays

Restore не переприменяет текущие sub-правила. Он берёт `log_entries.raw_text/plain_text` и последовательно применяет сохранённые overlays:
1. Если есть overlay `gag` — строка пропускается в обычном restore/UI
2. `substitution` overlays сортируются по `layer ASC`, при одинаковом layer — по `id ASC`
3. Для каждого overlay маппим `start_offset/end_offset` из текущего `displayPlain` в текущий `displayRaw`
4. Вставляем сохранённые `replacement_raw/replacement_plain`
5. После replay применяем текущие highlights как presentation-only слой

### Gag

Gag — это sub-правило с `is_gag = true`. Применяется **до** triggers в отдельном проходе (`CheckGags`).
`CheckGags` раскрывает переменные в `Pattern` так же, как `ApplySubsAndCollectOverlays`: `effectivePattern = substitutePatternVars(rule.Pattern)`, с `regexp.QuoteMeta` для значений переменных. В `gag` overlay сохраняются `pattern_template` и `effective_pattern`.

При срабатывании gag-правила на строке:
1. Оригинальная строка пишется в `log_entries`
2. К ней добавляется `log_overlays.overlay_type = 'gag'`
3. Triggers не запускаются
4. Строка не отправляется клиентам и не попадает в обычный restore/UI

Так как gag применяется до triggers/routing, скрытая строка сохраняется в buffer `main`. Для обычного UI это не видно из-за `gag` overlay; debug/export режимы смогут показать, что строка была скрыта.

Причины:
- Пользователь ожидает, что gag-строка полностью исчезнет из сессии.
- Но для отладки, экспорта и аудита важно не терять оригинальный MUD-поток.
- Скрытие через overlay сохраняет транспортную модель: клиент по-прежнему получает только отображаемые строки.

### Edge cases

- **Оригинальные логи**: `log_entries.raw_text/plain_text` никогда не содержат результат `#sub`; это canonical input от MUD.
- **ANSI внутри заменяемого текста**: `plainRangeToRawRange` корректно пропускает ANSI-коды. Replay overlay "hello"→"hi" в `\x1b[31mhello\x1b[0m` даст `\x1b[31mhi\x1b[0m` — окружающее форматирование сохраняется.
- **Пустой replacement**: допустим для не-gag правил — удаляет match, но строка остаётся (в отличие от gag, который скрывает строку целиком).
- **ANSI в replacement**: можно вставлять raw `\x1b[...m` — рендерятся через `ansi_up` на фронте.
- **Пересекающиеся sub-правила**: последовательная обработка по `position`. Sub A → Sub B: если A изменил текст, B видит изменённый.
- **Несколько matches одного правила**: обрабатываются с конца; каждый match получает отдельный `substitution` overlay.
- **Переменные в pattern/replacement**: `$target1` и другие VM-переменные раскрываются при обработке новой входящей строки, не при restore. Applied overlay фиксирует уже раскрытый `effective_pattern` и фактический `replacement_raw/plain`.
- **Неизвестная переменная в pattern**: остаётся literal-текстом `$name`, экранированным для regex.
- **Неизвестная переменная в replacement**: остаётся литералом `$name`, как в текущем `substituteVars`.
- **Переменная в regex**: значение вставляется как literal-фрагмент через `regexp.QuoteMeta`. Если позже понадобится подставлять переменную как raw regex, нужен отдельный явный синтаксис, не MVP.
- **Zero-width matches**: совпадения с `start == end` игнорируются в MVP. Вставки через `^`, `$`, `\b`, lookahead можно добавить позже отдельной семантикой.
- **`%0` без capture groups**: возвращает весь match (удобно для оборачивания: `#sub {text} {[%0]}`).
- **`%N` без capture group**: заменяется на пустую строку.
- **Sub/gag применяются ТОЛЬКО к входящим строкам от MUD-сервера**, не к локальным echo (`#showme`, `#woutput`, trigger-echo). Sub — UX-слой над MUD-потоком; локальный вывод должен оставаться под контролем автора.
- **Sub/gag не применяются к уже сохранённым строкам при изменении правил**: старые строки отображаются по сохранённым overlays, новые строки — по текущим правилам.
- **Command hints не должны цепляться к gag-строкам**: `AppendCommandHintToLatestLogEntry` нужно заменить/обновить до latest visible log entry, исключая записи с overlay `gag`.

## Команды VM

```
#sub {regex} {replacement} [group]
#substitute {regex} {replacement} [group]    (алиас)
#unsub {regex}
#gag {regex}                                  (сокращение: sub с is_gag=true, replacement="")
```

Парсинг:
- `#sub {pattern} {replacement} [group_name]` → создаёт/обновляет `SubstituteRule` с `is_gag = false`
- `#unsub {pattern}` → удаляет по pattern (любой, и gag и sub)
- `#gag {pattern}` → создаёт sub с `is_gag = true`, `replacement = ""`

`#sub/#substitute/#gag/#unsub` должны быть code-bearing для парсера VM: нельзя делать глобальный `substituteVars` до dispatch, иначе шаблон `$target1` будет потерян при сохранении правила. Переменные раскрываются только при применении правила к входящей строке.

**Экранирование фигурных скобок**: парсинг с учётом вложенности скобок — `#sub {foo} {{bar}}` интерпретирует replacement как `{bar}`. Несбалансированные литералы `{`/`}` — через `\{`/`\}` (узкое расширение, не для MVP).

## REST API

```
GET    /api/profiles/{pid}/subs        — список всех sub-правил профиля
POST   /api/profiles/{pid}/subs        — создать правило
PUT    /api/profiles/{pid}/subs/{id}   — обновить правило
DELETE /api/profiles/{pid}/subs/{id}   — удалить правило
```

## UI

Новый раздел «Substitutions» в Svelte admin/settings UI (`SettingsApp.svelte`):
- Форма: `pattern` (regex), `replacement` (текст с `%0`..`%N`), `group_name`, чекбокс `is_gag`
- Таблица: порядок, enabled, is_gag (иконка), pattern → replacement, group, кнопки Edit/Delete
- Preview: показывает как выглядит замена на примере (pattern матчится на sample text, показывается результат)
- Gag-правила визуально отличаются в списке (иконка/цвет)

## Import/export

`#sub/#substitute/#gag` входят в MVP для profile script import/export наравне с постоянными командами `#alias`, `#action`, `#highlight`, `#hotkey`, timers и profile variables.

Экспорт должен сохранять именно шаблоны `pattern/replacement`, а не раскрытые `effective_pattern/replacement_raw` из applied overlays.

При импорте:
- `#sub {pattern} {replacement} [group]` создаёт/обновляет обычное sub-правило
- `#substitute` является алиасом `#sub`
- `#gag {pattern} [group]` создаёт/обновляет gag-правило с пустым replacement
- metadata `group_name`, `enabled`, `position` должны поддерживаться тем же способом, что у существующих правил профиля

## Transport и поиск

Транспортный контракт менять не нужно: клиент по-прежнему получает `ClientLogEntry.Text` как уже подготовленный display text. Отличие только на сервере: перед отправкой live/restore нужно вызвать render-функцию, которая применяет сохранённые overlays к оригинальному `RawText`, а затем highlights.

Поиск в MVP остаётся по `log_entries.plain_text`, то есть по оригинальному MUD-тексту. Gag-строки должны исключаться из обычного search/MCP по умолчанию через `NOT EXISTS` для `log_overlays.overlay_type = 'gag'`.

Все обычные log-readers должны использовать тот же visible-фильтр: `RecentLogs`, `RecentLogsPerBuffer`, `LogsSinceID`, `LogRangeDetailed`, `SearchLogsDetailed`, а также привязка `command_hint` к последней строке. Скрытые gag-записи остаются доступны только для специальных debug/export режимов.

Display-aware search по заменённому тексту возможен позже, но это отдельная задача: либо overlay-aware scan, либо отдельный FTS по материализованному display text.

## Restore при переподключении

Sub/gag-правила **не переприменяются** при restore. Restore воспроизводит то, что пользователь видел в момент получения строки, через сохранённые `log_overlays`.

Это особенно важно для правил с переменными: если `$target1` сегодня указывает на другого персонажа, старые строки всё равно должны показывать старую фактически применённую замену, а не результат текущего значения `$target1`.

Алгоритм restore для строки:
1. Загружаем оригинальные `raw_text/plain_text`
2. Загружаем overlays `gag/substitution/button/command_hint`
3. Если есть `gag`, строка не включается в обычный restore
4. Применяем `substitution` overlays к оригиналу и получаем display raw/plain
5. Применяем текущие highlights к display raw
6. Отправляем клиенту обычный `ClientLogEntry.Text`

Highlights остаются presentation-only слоем и не сохраняются как overlay в рамках этого плана.

## Тесты

### VM parsing и storage правил

- `#sub {foo} {bar}` создаёт `SubstituteRule` с `is_gag=false`, `pattern=foo`, `replacement=bar`, `enabled=true`, `group_name=default`.
- `#substitute` работает как алиас `#sub`.
- `#gag {spam}` создаёт `SubstituteRule` с `is_gag=true`, `replacement=""`.
- `#unsub {foo}` удаляет и обычные sub-правила, и gag-правила по pattern.
- `#sub {$target1} {[F1] $target1}` сохраняет шаблоны с `$target1` как есть; переменная не раскрывается при создании правила.
- `#sub/#substitute/#gag/#unsub` проходят через code-bearing path VM и не попадают под глобальный `substituteVars` до dispatch.

### ApplySubsAndCollectOverlays

- Простая замена: `foo bar` + `#sub {foo} {baz}` даёт display `baz bar`, а `log_entries.raw_text/plain_text` остаются `foo bar`.
- Regex capture: `Вы сбили орка своим` + `#sub {Вы сбили (.+) своим} {%1 ->BASH}` даёт display `орка ->BASH`; overlay содержит `replacement_raw="орка ->BASH"` и не меняет canonical log.
- `%0` возвращает весь match: `#sub {foo} {[%0]}` превращает `foo` в `[foo]`.
- Отсутствующая capture group заменяется пустой строкой: `%9` не оставляет literal `%9`.
- Несколько matches одного правила обрабатываются с конца и создают несколько `substitution` overlays.
- Последовательные правила работают по текущему display text: Sub A меняет текст, Sub B видит результат A.
- Empty replacement для non-gag удаляет match, но строка остаётся visible.
- ANSI вокруг match сохраняется через `plainRangeToRawRange`.
- ANSI в replacement попадает в `replacement_raw`, а `replacement_plain` хранится без ANSI.
- Zero-width matches (`^`, `$`, `\b`, lookahead с `start == end`) игнорируются.

### Переменные

- Переменная в pattern раскрывается на apply-time и экранируется как literal regex-фрагмент: `$target1 = King\dark` матчится на буквальный `King\dark`, а не на raw regex.
- Переменная в replacement раскрывается на apply-time: `#sub {$target1} {[F1] $target1}` при `$target1=Orc` даёт `[F1] Orc`.
- Смена `$target1` после записи строки не меняет restore старой строки, потому что replay использует сохранённый overlay.
- Неизвестная переменная в pattern остаётся literal `$name` и экранируется для regex.
- Неизвестная переменная в replacement остаётся literal `$name`.

### Gag

- `#gag {spam}` сохраняет оригинальную строку в `log_entries` buffer `main`, добавляет overlay `gag`, не запускает triggers и не broadcast-ит строку.
- Gag-строка не попадает в обычный restore/UI/search/MCP.
- Gag-строка остаётся доступной для debug/export режима.
- `AppendCommandHintToLatestLogEntry` привязывает команду к latest visible log entry, игнорируя hidden gag-записи.

### Overlays, restore и readers

- `AppendLogEntry + substitution/gag overlays` выполняются атомарно в транзакции.
- Replay overlays восстанавливает тот же display text, что был отправлен live.
- `substitution` overlays применяются в `layer ASC, id ASC` и дают детерминированный результат.
- `RecentLogs`, `RecentLogsPerBuffer`, `LogsSinceID`, `LogRangeDetailed`, `SearchLogsDetailed` применяют visible-фильтр до `LIMIT`.
- При `BufferAction=copy` sub-overlays дублируются для каждой созданной `log_entry`.
- Button overlays и command hints сохраняются и отображаются вместе с rendered display text.

### Import/export и API/UI

- Profile export пишет `#sub/#substitute/#gag` как шаблоны `pattern/replacement`, не как applied overlays.
- Profile import восстанавливает `pattern`, `replacement`, `is_gag`, `enabled`, `position`, `group_name`.
- REST CRUD `/api/profiles/{pid}/subs` создаёт, обновляет, удаляет и возвращает правила.
- Svelte admin/settings раздел «Substitutions» показывает обычные sub и gag, позволяет редактировать `pattern`, `replacement`, `group_name`, `enabled`, `is_gag`.

## Не входит в план (пока)

- **Приоритет как в TinTin++** (третий аргумент) — пользователи могут управлять через position/drag-and-drop
- **Цветовой синтаксис в replacement** (типа `<red>`, `<110>`) — только raw ANSI
- **Multi-line matching** — только построчная обработка
- **Применение sub к локальным echo** — только к входящим от MUD-сервера
- **Display-aware full-text search** — MVP ищет по оригинальному `plain_text`
- **Переприменение новых/изменённых sub-правил к старым логам** — старые строки используют сохранённые overlays
