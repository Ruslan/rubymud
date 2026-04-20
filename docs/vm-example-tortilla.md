# Tortilla MUD Client — Reference for Built-in VM Design

## What is Tortilla

Tortilla (Черепаха) — нативный C++/MFC Windows MUD-клиент, второй по популярности
в Рунете после JMC. Сама документация говорит:

> "Прообразом Черепахи является другой мад-клиент: Jaba Mud Client 3, самый
> популярный клиент в Рунете... Тортилла очень похожа на JMC3"

Поставляется с плагином `jmc3import.dll` для импорта JMC-настроек.

## Relationship to JMC/TinTin

Tortilla = JMC3 + расширения. Базовый синтаксис идентичен:

| Общее с JMC | В Tortilla добавлено |
|-------------|---------------------|
| `#alias`, `#action`, `#sub`, `#gag` | `#antisub` — защита строк от sub/gag |
| `#highlight`, `#hotkey`, `#timer` | `$`-префикс = PCRE regex |
| `#var`, `#group`, `%0`–`%9` | `%(...)` substring ops |
| `;` separator, `#N` repeat | Variable separators `$.`, `$_`, `$!`, `$$` |
| `$varname` substitution | `#if`/`#else`, `#math`, `#strop` |
| `^` start-of-line anchor | `#wait` (delayed commands) |
| `{}`, `'`, `"` delimiters | `#output`/`#woutput` with color |
| `%%` wildcard (no capture) | `#stop` (stop triggers, keep line) |
| Abbreviable to 3 chars | Lua + C++ plugin system |
| | Subs with color in replacement |
| | RGB/256-color highlights |
| | `$var*` multi-variable matching |
| | Output windows (1-6) |

## Command Syntax

### General

- Prefix: `#`
- Separator: `;`
- Delimiters: `{}`, `'`, `"` — эквивалентны
- Abbreviation: минимум 3 символа (`#act` = `#action`)
- Repeat: `#3 {команда}` — повторить 3 раза (1-100, без вложенности)
- TAB на пустой строке вставляет `#`

### `#alias`

```
#alias {name} {commands} [group]
```

- `%0` — все аргументы одной строкой
- `%1`–`%9` — позиционные параметры (слова)

```
#alias {pn} {punch %1}
#alias {вар1} {#variable {cast1} {%%1}}
```

`%%1` внутри алиаса = аргумент алиаса (не `%1` триггера).

### `#action`

```
#action {pattern} {commands} [group]
```

Два режима паттернов:

1. **Wildcard** (JMC-стиль, по умолчанию):
   - `%0`–`%9` = wildcard capture → внутренне конвертируется в PCRE `(.*?)`
   - `%%` = любой текст без захвата → `.*?`
   - `^` = начало строки
   - `$` в конце = матчить неполные строки (prompt)

2. **PCRE regex** (префикс `$`):
   - `#action {$^Ваш регекс$} {команда} {group}`
   - Первый `$` — маркер режима, не часть regex
   - `%0`–`%9` НЕ используются, вместо них capture groups `()`

**Variable interpolation в паттернах:**
- `$varname` — подставляет значение переменной
- `$varname*` — multi-match: проверяет все переменные с таким префиксом

**Priority/порядок:** subs → antisubs → gags → actions

**Управление потоком триггеров:**
- `#drop` — удалить строку целиком
- `#stop` — остановить триггеры, строку оставить

### `#sub` / `#subs`

```
#sub {source} {replacement} [group]
```

Замена текста ДО триггеров. В replacement можно вставлять цвет:
```
#sub {привет} {{rgb120,100,80} ПРИВЕТ!}
```

### `#gag`

```
#gag {pattern} [group]
```

Удалить строку (sub с пустой заменой).

### `#antisub`

```
#antisub {pattern} [group]
```

Защитить строку от sub/gag. Если матчит antisub, sub/gag не сработают.

### `#highlight`

```
#highlight {pattern} {color_spec} [group]
```

Color spec: `{text_color [b background] [italic] [line] [border]}`

Цвета: `black, red, green, brown, blue, magenta, cyan, gray, coal, light red, light green, yellow, light blue, purple, light cyan, white, light magenta, light brown, grey, charcoal`

RGB: `{rgb120,100,80}`, `{b rgb123,16,48}`

Атрибуты: `italic`, `line` (underline), `border` (outline)

### `#variable`

```
#variable                — список всех
#variable {substring}    — поиск
#variable {name} {value} — установить
```

Reference: `$varname`

Variable separators:
- `$.` — точка после значения
- `$_` — подчеркивание после значения
- `$!` — ничего после значения (пустой разделитель)
- `$$` — литеральный `$`

Built-in variables: `$DATE`, `$TIME`, `$DAY`, `$MONTH`, `$YEAR`, `$HOUR`, `$MINUTE`, `$SECOND`, `$MILLISECOND`, `$TIMESTAMP`, `$BUFFER`

### `#hotkey`

```
#hotkey {key_combo} {commands} [group]
```

Format: `[shift+][ctrl+][alt+]keyname`
Keys: `F1`–`F12`, `INS`, `DEL`, `HOME`, `END`, `PGUP`, `PGDN`, `ENTER`, `SPACE`, `UP`, `DOWN`, `LEFT`, `RIGHT`, `NUM1`–`NUM9`, `DIV`, `MUL`, `MIN`, `ADD`, `RETURN`, `ESC`, `BACK`, `TAB`

### `#timer`

```
#timer {N} [period] [commands] [group]
```

1-10 таймеров, period в секундах (дробные: `1.5`), max 9999.9.
`#wait {seconds} {command}` — отложенная команда.

### `#group`

```
#group                    — список
#group {name}             — инфо
#group {op} {name}        — on/off/info
```

Operations: `on`/`enable`/вкл, `off`/`disable`/выкл, `info`/`list`/инф/список

### `#if`

```
#if {condition} {then_commands} [else_commands]
```

Operators: `=`, `==`, `!=`, `~=`, `<>`, `<`, `>`, `<=`, `>=`

### `#math`

```
#math {var} {expression}
```

Integer: `+`, `-`, `*`, `/`, `%` (digit grouping)

### `#strop`

```
#strop {result_var} {source} {op} [source2]
```

Operations: `replace`, `trim`, `concat`

### `#output` / `#woutput`

```
#output [color] {text}        — в главное окно, обрабатывается триггерами
#woutput {win} [color] {text} — в окно win (0=главное, 1-6), БЕЗ обработки триггерами
```

### Parameter Substring Operations

```
%(length)id          — обрезать до length символов
%(-count)id          — убрать count символов с конца
%(start,length)id    — взять length символов с позиции start
%(start,-count)id    — с позиции start, убрать count с конца
```

Example: `%(-3)1` — убрать 3 символа с конца `%1`.

## Processing Order

1. Raw MUD text
2. Lua plugin triggers (PRE-sub)
3. Substitute / antisub / gag
4. Action (classic triggers)
5. Display

## Configuration Storage

XML profiles:
```
gamedata/<MudWorld>/profiles/<Character>.xml
```

Contains: actions, aliases, hotkeys, subs, asubs, gags, highlights, timers, groups, variables, tabwords, colors, plugins, messages.

Plugins: Lua tables via `loadTable`/`saveTable`.

## Implications For Our Built-in VM

Tortilla — надмножество JMC. Наш VM должен быть совместим с общим ядром:

### Must have (общее ядро JMC + Tortilla)

| Команда | Уже в нашем v1? |
|---------|-----------------|
| `#alias {name} {template} [group]` | Частично (нет `#alias` команды, нет group) |
| `#unalias {name}` | Нет |
| `#action {pattern} {cmds} [group]` | Нет (следующая итерация) |
| `#var {name} {value}` | Нет (нет runtime-команды) |
| `$varname` substitution | Да |
| `%1`–`%9` args | Да |
| `;` separator | Да |
| `#N {commands}` repeat | Нет |
| `^` start-of-line | N/A (для triggers) |
| `#group enable/disable` | Нет |

### Nice to have (Tortilla extensions)

| Feature | Priority |
|---------|----------|
| `$`-prefix PCRE regex | Medium |
| `#sub` with color | Medium |
| `#gag` | Low (= sub с пустой заменой) |
| `#antisub` | Low |
| `%(...)` substring ops | Low |
| Variable separators `$.`, `$_` | Low |
| `$var*` multi-match | Low |
| `#if`/`#else` | Medium |
| `#math` | Low |
| `#wait` | Medium |
| `#output`/`#woutput` | Medium (echo + window) |
| `#stop`/`#drop` | Medium (для triggers) |

### Not needed (Tortilla-specific)

- `#strop` — лучше через plugin
- `#component`, `#debug` — debug/development tools
- `#password`, `#hide` — Windows-specific
- `#savelog`, `#clog` — logging layer handles this
- `#cr` — trivial
- Plugin system — separate concern

## Summary

Tortilla = JMC3 + PCRE + `#if` + `#math` + `#sub` с цветом + `#wait` + `#antisub` + `%(...)` ops.
Оба клиента используют TinTin `#command` синтаксис.
Наш VM должен реализовать общее ядро JMC/Tortilla как native commands.
