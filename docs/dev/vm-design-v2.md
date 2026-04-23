# Built-in VM Design — RubyMUD Go Client

## Motivation

Русскоязычные MUD-игроки — это консервативная аудитория с устоявшимися привычками.
Они играют годами, иногда десятилетиями, на одних и тех же персонажах.
Их конфиги — это живые организмы, которые растут вместе с персонажем.

**Текущая ситуация:**

| Клиент | Язык | Платформа | Статус |
|--------|------|-----------|--------|
| JMC 3.x | C++ (DLL, Win32, MFC/ATL) | Windows | Мёртв (последний релиз ~2015) |
| Tortilla | C++ (MFC) | Windows | Поддерживается, но Windows-only |
| TinTin++ | C | Кроссплатформенный | Жив, но англоязычный, слабая кириллица |
| Mudlet | C++ | Кроссплатформенный | Жив, но другой UX, Lua-скриптинг |

"Jaba" в JMC — это никнейм автора, не Java. JMC = C++/Win32 DLL,
происходит от TinTin++ (credit в исходниках).

Игроки сидят на JMC/Tortilla потому что:

1. Их конфиги работают. Они их писали годами.
2. Они знают синтаксис наизусть. Это мышечная память.
3. Альтернативы требуют переписывания всего.
4. Переезд = неделя простоя + потеря боевых триггеров = смерть персонажа.

**Новый клиент не станет популярным, если переезд стоит дороже, чем остаться.**

## Core Thesis

Наша VM должна быть **TinTin-совместимой** на уровне синтаксиса команд.

Это значит: игрок открывает наш клиент, вставляет свой JMC/Tortilla конфиг,
и он работает. Не на 100% — но на 90%+ из коробки.

Остальные 10% — это Tortilla-расширения (`$`-regex, `%(...)` ops, `#antisub`),
которые мы добавляем итеративно.

**Продуктовый приоритет: совместимость > инновация.**

## JMC Source Code Findings

Изучены исходники JMC (`/Users/ru/Games/jmc`). Ключевые находки:

### Processing Pipeline (реальный порядок)

```
MUD data → telnet → charset conversion → line splitting
    → для каждой строки:
        1. COM "Incoming" event (ActiveX scripts)
        2. IF presub=ON:  check_all_actions (триггеры ДО subs)
        3. IF togglesubs=OFF: antisub → sub
        4. IF presub=OFF (default): check_all_actions (триггеры ПОСЛЕ subs)
        5. do_one_high (highlight, ВСЕГДА последним)
    → display/log
```

**По умолчанию:** antisub → sub → action → highlight.

### Gag = sub с заменой "."

`#gag {pattern}` — это буквально `#substitute {{pattern} .}`.
Строки равные "." не отображаются. Это маркер suppression, не пустая строка.

### Два режима alias

1. **Simple** (`#alias {kk} {kick}`) — lookup по точному совпадению первого слова.
2. **Regex** (`#alias {/pattern/} {expansion}`) — PCRE матч по всей input строке.
   Опциональные флаги: `i` (case-insensitive).

Порядок проверки: сначала regex aliases, потом simple lookup.

### Три типа action

1. `Action_TEXT` (default) — ANSI-stripped текст
2. `Action_RAW` — с ANSI-кодами
3. `Action_COLOR` — ANSI конвертированы в `&X` notation

PCRE actions: `/pattern/` с флагами `i`, `m`, `g`.

### Variable substitution: сначала % потом $

В `prepare_actionalias()`:
1. `substitute_vars()` — заменяет `%0`–`%9`
2. `substitute_myvars()` — заменяет `$varname`

Это позволяет динамические имена переменных через `%N`.

### 27 built-in variables в JMC

`$DATE`, `$YEAR`, `$MONTH`, `$DAY`, `$TIME`, `$HOUR`, `$MINUTE`, `$SECOND`,
`$MILLISECOND`, `$TIMESTAMP`, `$CLOCK`, `$CLOCKMS`, `$INPUT`, `$NOCOLOR`,
`$RANDOM`, `$HOSTNAME`, `$HOSTIP`, `$HOSTPORT`, `$EOP`, `$EOL`, `$ESC`,
`$PING`, `$PINGPROXY`, `$PRODUCTNAME`, `$PRODUCTVERSION`, `$COMMAND`,
`$FILENAME`.

### #if — полноценный expression evaluator

Не просто string compare. Stack-based evaluator с операторами:
`||`, `&&`, `==`, `!=`, `>`, `>=`, `<`, `<=`, `+`, `-`, `*`, `/`, `!`, `( )`.
Результат integer (boolean = 0/1). Литералы `T`/`F` для true/false.

### #N repeat с таймером

`#5 command` — повторить 5 раз немедленно.
`#5:3 command` — цикл от 1 до 5 с задержкой 3 deciseconds.
`%0` внутри = счётчик итерации.

### #wait — cooperative delay

`#wait {N}` (deciseconds) — ставит `iWaitState`, последующий input
уходит в очередь. Когда таймер доходит до 0, очередь сбрасывается
в `parse_input()`. Клиент не блокируется.

### PCRE variable dependency tracking

Когда `$variable` в PCRE-паттерне, JMC трекит зависимость в `VarPcreDeps`.
При изменении переменной — все зависимые PCRE паттерны перекомпилируются.

### .set file = просто JMC-команды

Формат `.set` файла — это просто последовательность `#command` строк.
`#write` сохраняет, `#read` загружает и выполняет построчно.
После чтения указанного файла автоматически читается `global.set`.
Поддержка brace-continuation (многострочные определения внутри `{}`).

### Delayed action deletion

Actions помечаются `m_bDeleted = TRUE` во время выполнения,
реально удаляются в следующем MUD read cycle. Защита от crash
при модификации списка во время итерации.

### ActiveX Scripting

JMC встроен Windows Active Script engine (JScript, VBScript, Perl).
События: `Incoming`, `Input`, `Connected`, `ConnectLost`, `Timer`,
`PreTimer`, `Prompt`, `Unload`. Это то, что мы заменим plugin-протоколом.

### `#loopback {text}`

Прогоняет текст через весь output pipeline (subs, actions, highlights).
Полезно для скриптов. Аналог в нашем контексте — отправить текст
в output pipeline как `source_type = 'local'`.

### Comment character — настраиваемый

`#comment {char}` — меняет символ комментария (default `#`).
`#nop` — стандартный TinTin no-op, но общий comment char тоже настраивается.

JMC и Tortilla оба реализуют подмножество TinTin++ DSL.
Мы реализуем общее ядро + наиболее используемые расширения.

### Общее ядро (JMC ∩ Tortilla) — Must Have

| Команда | Синтаксис | Наш статус |
|---------|-----------|------------|
| `#alias` | `#alias {name} {template} [group]` | Частично (нет `#alias` runtime-команды, нет group) |
| `#unalias` | `#unalias {name}` | Нет |
| `#action` | `#action {pattern} {commands} [group]` | Нет (следующая итерация) |
| `#unaction` | `#unaction {pattern}` | Нет |
| `#variable` | `#variable {name} [value]` | Нет (нет runtime-команды) |
| `#unvariable` | `#unvariable {name}` | Нет |
| `#highlight` | `#highlight {pattern} {colors} [group]` | Нет |
| `#sub` | `#sub {pattern} {replacement} [group]` | Нет |
| `#gag` | `#gag {pattern} [group]` | Нет |
| `#hotkey` | `#hotkey {key} {commands} [group]` | Нет (runtime) |
| `#group` | `#group {on\|off} {name}` | Нет |
| `#nop` | `#nop comment` | Нет |
| `#read` | `#read {filename}` | Нет |
| `#output` | `#output [color] {text}` | Нет |
| `#N repeat` | `#3 {команда}` | Нет |
| `;` separator | `cmd1;cmd2;cmd3` | Да |
| `$var` substitution | `$varname` в командах | Да |
| `%1`–`%9` args | В alias и action | Да (alias) |
| `%%1` alias args | Внутри `#alias` определения | Нет |
| `%0` full match | Весь захваченный текст / все аргументы | Нет |
| `^` start-of-line | Anchor в patterns | N/A (для triggers) |
| `%%` wildcard | Любой текст без захвата | N/A |
| `/regex/` PCRE aliases | `#alias {/pattern/} {template}` | Нет |
| `/regex/` PCRE actions | `#action {/pattern/i} {cmds}` | Нет |

### Tortilla Extensions — Should Have

| Feature | Приоритет | Обоснование |
|---------|-----------|-------------|
| `$`-prefix PCRE regex | Medium | Tortilla-юзеры используют активно |
| `/regex/` slash regex | Medium | JMC-юзеры используют (Arctic configs) |
| `#if`/`#else` | Medium | Критично для боевых триггеров |
| `#wait` | Medium | Отложенные команды — типичный паттерн |
| `#stop`/`#drop` | Medium | Управление цепочкой триггеров |
| `#sub` с цветом | Low | Nice to have |
| `#antisub` | Low | Нишевый feature |
| `%(...)` substring ops | Low | Редко используется |
| Variable separators `$.`, `$_` | Low | Edge case |
| `$var*` multi-match | Low | Продвинутый feature |
| `#math` | Low | Простая арифметика |
| `#strop` | Low | Через plugin |
| `#tickset`/`#timer` | Medium | Tick timer — базовый MUD паттерн |
| `#woutput` | Medium | Вывод в окна (у нас есть window_name) |

### Наше расширение — Can Have

Мы не должны изобретать новый синтаксис. Но можем добавить минимальные вещи,
которые TinTin не покрывает и которые полезны для нашего UX:

| Feature | Обоснование |
|---------|-------------|
| Button overlays на action match | Наша overlay-модель позволяет. Не ломает TinTin совместимость |
| Window routing в action | `#action {pattern} {commands} {group}` — добавим опциональный `window:` |
| Persistent variables в SQLite | Уже сделано. TinTin/JMC теряют переменные при рестарте |

## Input Processing Path

Совпадает с JMC/Tortilla:

```
1. Пользователь вводит текст
2. Если начинается с # → системная команда (#alias, #var, #action, ...)
3. Иначе:
   a. Split по ; (учитывая вложенные {})
   b. Variable substitution: $varname → value
   c. Alias expansion: name %1 %2 → template
   d. #N repeat expansion
   e. Каждая результирующая команда отправляется в MUD
   f. Команда логируется в history
```

## Output Processing Path

Совпадает с JMC/Tortilla processing order:

```
1. Raw MUD text → transport normalization
2. Split на visible lines
3. Каждая строка → canonical log_entry
4. Substitute (замена текста)
5. Antisub check (защита от gag/sub)
6. Gag (удаление строки)
7. Action/trigger matching (с побочными эффектами)
8. Highlight (раскраска)
9. Button overlay (наше расширение)
10. Window routing (наше расширение)
11. Display в UI
```

## Syntax Details

### Command Prefix

`#` — префикс системных команд. Совместим с TinTin/JMC/Tortilla.

Аббревиатуры: минимум 3 символа (`#ali` = `#alias`, `#act` = `#action`).

### Parameter Delimiters

`{}`, `'`, `"` — эквивалентны. Вложенные `{}` разрешены.

```
#alias {lootall} {get all corpse;get all 2.corpse}
#alias 'mm' 'cast magic missile'
```

### Command Separator

`;` — разделяет несколько команд. Внутри `{}/'/` — литерал.

### Variable Syntax

```
$varname              — подстановка значения
$varname.             — значение + точка (Tortilla separator)
$varname_             — значение + подчеркивание (Tortilla separator)
$$                    — литеральный $
```

Имена переменных: Unicode letters + digits + `_` + `.`. Должны начинаться с буквы или `_`.

Built-in variables (будут добавлены):

| Variable | Описание |
|----------|----------|
| `$DATE` | Текущая дата |
| `$TIME` | Текущее время |
| `$HOUR`, `$MINUTE`, `$SECOND` | Компоненты времени |
| `$TIMESTAMP` | Unix timestamp |

### Positional Parameters

| Контекст | Синтаксис | Значение |
|----------|-----------|----------|
| Alias template | `%0` | Все аргументы одной строкой |
| Alias template | `%1`–`%9` | Позиционные слова |
| Alias definition (nested) | `%%1`–`%%9` | Аргументы outer alias |
| Action pattern | `%0`–`%9` | Wildcard capture (или regex capture) |
| Action pattern | `%%` | Любой текст без захвата |
| Action result | `%0`–`%9` | Захваченные группы из pattern |
| Sub replacement | `%0`–`%9` | Захваченные группы |
| Substring ops | `%(start,len)1` | Подстрока от позиции (Tortilla) |

### Pattern Matching

Три режима, как в JMC + Tortilla:

1. **Wildcard** (по умолчанию):
   - `%0`–`%9` = wildcard → конвертируется в regex `(.*?)`
   - `%%` = любой текст без захвата → `.*?`
   - `^` = начало строки
   - Без `^` = матч в любом месте строки

2. **PCRE regex со слэшами** (JMC):
   - `#action {/regex/} {commands}`

3. **PCRE regex с `$`-префиксом** (Tortilla):
   - `#action {$pattern} {commands}`
   - Первый `$` = маркер режима
   - `%0`–`%9` НЕ используются, вместо них capture groups `()`

### Groups

Каждый элемент (alias, action, sub, highlight, hotkey, timer, gag) принадлежит группе.

```
#group on krit         — включить группу
#group off krit        — выключить группу
#group                 — список групп
```

Default группы: `default` (enabled), `off` (disabled).

При создании элемента без указания группы → `default`.

### Repeat Syntax

```
#3 {команда}           — повторить команду 3 раза
#2 {вар $cast1}        — повторить 2 раза
```

Число от 1 до 100. Без вложенности.

### Conditional

```
#if {condition} {then} [else]
```

Operators: `=`, `==`, `!=`, `~=`, `<>`, `<`, `>`, `<=`, `>=`

Примеры:
```
#if {$лид = 1} {взя все тру}
#if {$пари = 1} {пари} {#nop}
```

## Implementation Priority

### Iteration 1: Alias + Variables (текущая)

- [x] `alias_rules` таблица
- [x] `$var` substitution (Unicode-aware)
- [x] `%1`–`%9` positional args
- [x] `;` separator
- [x] Alias expansion с рекурсией (max depth 10)
- [x] SQLite persistence для aliases и variables
- [x] Seed из config.rb при старте
- [ ] `#alias` runtime-команда
- [ ] `#variable` runtime-команда
- [ ] `#unalias`, `#unvariable`
- [ ] `%0` (все аргументы)
- [ ] `%%1` (nested alias args)
- [ ] `#N` repeat
- [ ] `#nop`
- [ ] Встроенные переменные `$DATE`, `$TIME`

### Iteration 2: Triggers

- [ ] `trigger_rules` таблица
- [ ] `#action` runtime-команда
- [ ] Wildcard pattern matching (`%0`–`%9`, `%%`, `^`)
- [ ] Action effects: `send_command`, `echo`
- [ ] `#unaction`
- [ ] `#stop`, `#drop`
- [ ] Action groups
- [ ] Variable interpolation в patterns (`$var`)
- [ ] PCRE regex режим (`/regex/` и `$pattern`)

### Iteration 3: Highlight + Sub + Gag

- [ ] `#highlight` runtime-команда
- [ ] `#sub` runtime-команда
- [ ] `#gag` runtime-команда
- [ ] ANSI color specs
- [ ] Processing order: sub → gag → action → highlight
- [ ] `#unhighlight`, `#unsub`, `#ungag`
- [ ] Sub с цветом в replacement (Tortilla)

### Iteration 4: Groups + Control Flow

- [ ] `#group` runtime-команда
- [ ] Group model в SQLite
- [ ] `#if`/`#else`
- [ ] `#read` (загрузка TinTin-совместимых конфигов)
- [ ] `#wait` (delayed commands)
- [ ] `#output`

### Iteration 5: Timers + Windows

- [ ] `#timer` runtime-команда
- [ ] `#tickset`
- [ ] `timer_state` таблица
- [ ] `#woutput` (window routing)
- [ ] Button overlays на action match

### Iteration 6: Migration UX

- [ ] JMC `.set` file importer
- [ ] Tortilla XML profile importer
- [ ] `#read` для TinTin-совместимых скриптов
- [ ] Мастер миграции в UI
- [ ] Отчёт о совместимости (что не поддерживается)

## Migration Strategy

### Phase 1: Manual migration

Пользователь копирует свои `#alias`, `#action`, `#var` команды
в наш клиент через `#read` или вставку в input.

Это уже работает для простых алиасов.

### Phase 2: Semi-automatic migration

JMC `.set` файл парсится нашим импортёром.
Tortilla XML профиль парсится нашим импортёрром.

Неподдерживаемые фичи подсвечиваются с предупреждениями.

### Phase 3: Transparent migration

Пользователь указывает путь к JMC/Tortilla профилю.
Клиент импортирует всё автоматически при первом запуске.
В UI показывается что импортировано, что нет, и почему.

## What We Explicitly Do NOT Port

1. **Ruby blocks как core scripting** — `instance_exec`, `method_missing`, `load config.rb`
   Это красиво в Ruby, но это тоже lock-in. TinTin-синтаксис — наш стандарт.

2. **JMC-specific Windows UI** — status bar, clickpad, menu
   Наша архитектура — browser UI, не Windows forms.

3. **Tortilla Lua plugins** — свой plugin bridge, не совместимость с Tortilla DLL.

4. **`#broadcast`** — межоконная коммуникация JMC.
   У нас одна shared session + multiple browser clients. Другая модель.

5. **`#presub`** — toggle sub-before-action.
   Мы фиксируем порядок sub → gag → action → highlight. Это правильнее,
   чем toggle, который все всё равно ставят в `on`.

## Summary

Наша VM = TinTin++ common core + JMC/Tortilla совместимость + overlay-расширения.

Приоритет: **игрок должен иметь возможность перенести свой конфиг
из JMC/Tortilla в наш клиент и продолжить играть в тот же день.**

Без этого продукт не станет популярным. Точка.
