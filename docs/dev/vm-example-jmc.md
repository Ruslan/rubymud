# JMC Reference for Built-in VM Design

## What is JMC

JMC (Java MUD Client) — основной клиент целевой аудитории adamant-mud игроков.
Написан на Java, но **синтаксис команд — TinTin++**. Не порт TinTin, а реализация
совместимого DSL поверх Java-движка.

## TinTin Compatibility

JMC использует TinTin-синтаксис с `#`-префиксом:

| JMC | TinTin++ | Совпадает? |
|-----|----------|-----------|
| `#alias` | `#alias` | Да |
| `#action` | `#action` | Да |
| `#highlight` | `#highlight` | Да |
| `#variable` | `#variable` | Да |
| `#substitute` | `#substitute` | Да |
| `#hot` | `#hot` | Да |
| `#group` | `#group` | Да |
| `%1`–`%9` | `%1`–`%9` | Да |
| `%%1` in alias | `%%1` in alias | Да |
| `$var` | `$var` | Да |

JMC — TinTin-совместимый подмножество. Пользователи знают TinTin-синтаксис наизусть.

## JMC Command Reference (extracted from a-mud.ru docs)

### `#alias`

```
#alias {name} {template} {group}
```

Examples from real player configs:

```
#alias {вар} {вар '%1 %2'} {default}
#alias {лу} {лечить раны '%1 %2'} {default}
#alias {лм} {лечить руку '%1 %2'} {default}
#alias {вар1} {#variable {cast1} {%%1}} {default}
#alias {вар2} {#variable {cast2} {%%1}} {default}
```

Key details:
- `%1`, `%2` — positional args
- `%%1`, `%%2` — args inside alias definition (to distinguish from action `%1`)
- `;` — command separator inside template
- `{group}` — group assignment (default, or custom)

### `#action`

```
#action {^pattern %1} {commands} {priority} {group}
```

Examples from real configs:

```
#action {^Вы упали!} {встать} {5} {default}
#action {^Вы хотите есть.} {#tickset} {7} {glticker}
#action {^%1 сказал%2 группе: "%3"} {#output {brown} %1 : %3} {5} {default}
#action {^<ремонт оружия> <%1> %2 [%3} {ремонт %2} {9} {glrepair}
```

Key details:
- `^` — start-of-line anchor (common in JMC practice)
- `%1`–`%9` — regex capture groups
- `{priority}` — integer, lower = higher priority (5 = normal, 9 = low)
- `{group}` — group name for enable/disable
- Multiple actions on same line: `#multiaction on`

### `#variable`

```
#variable {name} {value}
```

Usage in aliases:

```
#alias {вар1} {#variable {cast1} {%%1}} {default}
```

Usage in commands:

```
$cast1    →  variable substitution
```

### `#highlight`

```
#highlight {fg,bg} {pattern} {group}
```

Examples:

```
#highlight {yellow,b light red} {строка текста} {default}
#highlight {black,b charcoal} {^строка текста} {default}
#highlight {cyan,b black} {^другая строка} {default}
```

Colors: black, red, green, yellow, blue, magenta, cyan, white, light red, light green, etc.
`b` prefix = bold/bright.

### `#substitute` / `#subs`

```
#subs {original text} {replacement text}
```

Examples:

```
#subs {строка текста} {_строка текста_}
#subs {%1 сказал%2 группе: "%3"} {%1 сказал%2 группе: "%3" $TIME}
```

Key detail: substitute modifies the text BEFORE highlight matching.
`#presub` controls whether actions match on original or substituted text.

### `#hot`

```
#hot {key} {commands}
```

Examples:

```
#hot {F10} {лечить;#2 вар $cast1;#2 вар $cast2}
```

Note: `#2 command` = repeat command 2 times (TinTin `#number` syntax).

### `#group`

```
#group {command} {group_name}
```

Commands: `list`, `enable`, `disable`, `delete`, `info`, `global`, `local`.

Example:

```
#group enable krit
#group disable krit
```

### `#status`

```
#status {position} {text} {color}
```

Status bar line in JMC. Position 1–5.

```
#status 3 {Слепой !!!} {light red}
#status 1 {$cast1} {light blue}
```

### `#output`

```
#output {color} {text}
```

Echo text to output window with color.

### `#multiaction` / `#multihighlight`

```
#multiaction on
#multihighlight on
```

Allow multiple actions/highlights to fire on same line.

### `#log`

```
#log $DATE.log append
#log
```

### `#tickset` / `#tickoff`

Tick timer management.

### `#unsubs` / `#unaction` / `#unalias`

Remove definitions.

## Differences From Full TinTin++

JMC is a subset. Notable things JMC omits or simplifies:

1. No `#forall`, `#foreach`, `#while` — no loops
2. No `#math` — no arithmetic
3. No `#if`/`#else` — no conditionals
4. No `#regexp` — only simple `#action` patterns
5. No `#buffer` — no scrollback API
6. No `#class` — uses `#group` instead
7. No `#send` — commands go directly
8. No `#map` / `#path` — no mapping

What JMC adds on top of TinTin base:

1. `#status` — status bar
2. `#presub` — substitute-before-action toggle
3. `%%1` — alias arg notation inside alias definitions
4. `#N command` — repeat command N times (e.g. `#2 вар $cast1`)

## Implications For Our Built-in VM

### Priority 1: TinTin-compatible input commands

These should work as native client commands:

```
#alias {name} {template}
#unalias {name}
#action {pattern} {commands}
#unaction {pattern}
#variable {name} {value}
#variable {name}             → show value
#unvariable {name}
#highlight {colors} {pattern}
#subs {original} {replacement}
#hot {key} {commands}
#group enable {name}
#group disable {name}
#group list
```

### Priority 2: Variable/arg syntax

Already compatible with our v1:
- `$varname` — variable substitution (✓ done)
- `%1`–`%9` — positional args in aliases (✓ done)
- `%%1` — alias args inside alias def (needs implementation)
- `;` — command separator (✓ done)

### Priority 3: TinTin repeat syntax

```
#3 команда    →  отправить "команда" 3 раза
```

This is heavily used in JMC configs. Easy to implement as a built-in command.

### Pattern matching modes

Three modes observed in real configs:

1. **Wildcard** (default): `%0`–`%9` wildcards, `^` start-of-line
2. **PCRE regex with slash delimiters**: `/regex/` — JMC syntax from real ArcticMUD configs
3. **PCRE regex with `$` prefix**: `$regex` — Tortilla extension

### `#nop`

```
#nop This is a comment
```

Standard TinTin comment. Lines starting with `#nop` are ignored.

### `#read`

```
#read filename.txt
```

Load and execute a config file. Used for modular config organization:
```
#read Arctic.set
#read JMC_Arctic_Actions.txt
```

### `#tickset`

```
#tickset {seconds}
```

Reset the tick timer to the given period. Used inside triggers that detect tick events.

### `#broadcast`

```
#broadcast send {keyword} {message}
```

Inter-window communication in JMC. Used for dual-box play:
- Window 1 sends: `#broadcast send $secretkw %0`
- Window 2 receives: `#action TEXT {^$secretkw %0} {%0}`

### `#connect`

```
#connect {host} {port}
```

Connect to MUD server. Used in auto-reconnect triggers:
```
#action {^#Connection lost.} {#connect mud.arctic.org 2700}
```

### Not in v1 but noted

- `#multiaction` — for triggers v2
- `#presub` — for substitute + action ordering
- `#status` — needs UI status bar
- `#output` — needs echo effect
- `#group` — needs group model

## Summary

JMC = TinTin++ subset in Java. Our built-in VM should use TinTin `#command` syntax
as the native command language. This is what users already know.
