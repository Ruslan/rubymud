# Engine Commands

Документ описывает команды и синтаксис, которые реально поддерживаются встроенным движком RubyMUD сейчас.

Это не план и не список будущих идей: ниже только то, что уже распознаётся в `go/internal/vm`.

## Общие правила

### Префикс команд

Системные команды начинаются с `#`.

Примеры:

```text
#alias {n} {north}
#var {bag} {рюкзак}
#showme {hello}
```

### Обычный ввод

Если строка не начинается с `#`, движок обрабатывает её по statement-by-statement пайплайну.

Для каждого statement:

1. подставляет переменные вида `$name`;
2. если statement начинается с `#`, исполняет локальную системную команду;
3. иначе раскрывает alias;
4. иначе раскрывает speedwalk, если строка похожа на `2n3e`;
5. иначе отправляет statement в MUD.

Важно:

- alias могут раскрываться в локальные `#`-команды;
- тот же пайплайн используется и для trigger command'ов;
- переменные подставляются непосредственно перед выполнением конкретного statement, а не один раз на всю исходную строку.

Пример:

```text
attack $target;loot
```

### Разделитель `;`

Можно отправлять несколько команд одной строкой:

```text
stand;look;score
```

Разделение выполняется безопасно:

- `;` внутри `{...}` не режет строку;
- `;` внутри `'...'` и `"..."` тоже не режет строку.

### Переменные `$name`

В командах и alias подставляются session variables:

```text
get $loot $bag
```

### Встроенные переменные времени

Поддерживаются:

- `$DATE`
- `$TIME`
- `$HOUR`
- `$MINUTE`
- `$SECOND`
- `$TIMESTAMP`

Пример:

```text
#showme {Now: $TIME}
```

### Параметры `%0`, `%1`, `%2`...

В alias:

- `%0` — все аргументы целиком
- `%1`, `%2`, ... — отдельные аргументы

В triggers (`#action`) используются regex capture groups:

- `%0` — вся совпавшая строка;
- `%1`, `%2`, ... — захваченные группы

Примеры:

```text
#alias {gt} {get %0 $bag}
#action {^You hit (.*)$} {say target=%1}
```

### Speedwalk

Строки из `n`, `s`, `e`, `w`, `u`, `d` и цифр автоматически раскрываются в последовательность команд.

Пример:

```text
2n3e
```

будет отправлено как:

```text
n
n
e
e
e
```

### `#N` repeat

Поддерживается форма повтора команды:

```text
#3 kick rat
```

Это эквивалентно трём одинаковым командам подряд.

### `#nop`

Комментарий / no-op. Ничего не делает.

Пример:

```text
#nop This is a comment
```

## Regex в trigger'ах

`#action` использует Go regexp (RE2), а не PCRE.

Важно:

- поддерживаются обычные группы захвата `(...)`;
- не поддерживаются Perl-конструкции вроде lookahead/lookbehind: `(?!...)`, `(?=...)`, `(?<=...)` и т.п.

## Команды

## `#alias`, `#ali`

Создать, посмотреть одну alias или вывести список всех alias.

Синтаксис:

```text
#alias
#alias {name}
#alias {name} {template}
```

Примеры:

```text
#alias {n} {north}
#alias {heal} {cast 'heal' %1}
#alias
```

Замечания:

- alias сохраняются в primary profile текущей session;
- group через runtime-команду сейчас не задаётся, используется `default`.

## `#unalias`

Удалить alias по имени.

Синтаксис:

```text
#unalias {name}
```

Пример:

```text
#unalias {heal}
```

## `#variable`, `#var`

Показать список переменных, получить значение одной переменной или установить значение.

Синтаксис:

```text
#var
#var {name}
#var {name} {value}
```

Примеры:

```text
#var {bag} {рюкзак}
#var {target} {орк}
#var {bag}
#var
```

Замечания:

- это session-scoped variables;
- profile declared variables и defaults живут отдельно и настраиваются через Settings / import-export `.tt`.

## `#unvariable`, `#unvar`

Удалить session variable.

Синтаксис:

```text
#unvar {name}
```

Пример:

```text
#unvar {target}
```

## `#action`, `#act`

Создать trigger, посмотреть список trigger'ов.

Синтаксис:

```text
#action
#action {pattern} {command}
#action {pattern} {command} {group}
#action {pattern} {command} {group} {button}
```

Примеры:

```text
#action {^You are hungry\.$} {eat bread}
#action {^Leader says: "rest"$} {rest} {party}
#action {R\.I\.P\.$} {get all corpse} {loot} {button}
```

Замечания:

- `pattern` — Go regexp;
- если указан `button`, trigger создаёт кнопку вместо немедленной отправки команды;
- trigger command проходит через тот же VM pipeline, что и ручной ввод:
  работают `;`, alias, переменные, `#showme`, `#woutput`, `#tts`;
- runtime-команда `#action` сейчас не умеет задавать `buffer_action` / `target_buffer` прямо в синтаксисе.
  Для routing по буферам без `#woutput` используйте Settings UI, `.tt` import/export или API.

## `#unaction`, `#unact`

Удалить trigger по pattern.

Синтаксис:

```text
#unaction {pattern}
```

Пример:

```text
#unaction {^You are hungry\.$}
```

## `#highlight`, `#high`

Создать highlight rule или вывести список highlight'ов.

Синтаксис:

```text
#highlight
#highlight {color_spec} {pattern}
#highlight {color_spec} {pattern} {group}
```

Примеры:

```text
#highlight {256:196} {^ALARM}
#highlight {bold,underline,256:46} {^Ready$} {status}
```

Поддерживаются стили:

- `bold`
- `faint`
- `italic`
- `underline`
- `strikethrough`
- `blink`
- `reverse`
- краткая форма `b`

Цвета обычно задаются как ANSI 256, например `256:196`.

## `#unhighlight`, `#unhigh`

Удалить highlight по pattern.

Синтаксис:

```text
#unhighlight {pattern}
```

Пример:

```text
#unhighlight {^ALARM}
```

## `#hotkey`, `#hot`

Создать hotkey.

Синтаксис:

```text
#hotkey {shortcut} {command}
```

Формат `shortcut`:

- регистр не важен: `CTRL+Q` и `ctrl+q` эквивалентны;
- модификаторы: `ctrl`, `alt`, `shift`, `command`;
- обычные клавиши: `a`, `b`, `1`, `2`, ...;
- функциональные клавиши: `f1` ... `f12`;
- навигационные клавиши: `pageup`, `pagedown`, `home`, `end`, `up`, `down`, `left`, `right`;
- numpad: `num_0` ... `num_9`, а также `num_+`, `num_-`, `num_*`, `num_/`, `num_.`.

Примеры:

```text
#hotkey {f1} {score}
#hotkey {ctrl+q} {kick rat}
#hotkey {pageup} {north}
#hotkey {num_8} {n}
#hotkey {ctrl+1} {status_vars}
```

Замечания:

- hotkey сохраняется в primary profile;
- удаления runtime-командой сейчас нет, удаление/редактирование делается через Settings.
- примеры реального боевого использования можно посмотреть в `config.rb`: там есть как простые хоткеи движения, так и цепочки команд с переменными, например `f3`, `f7..f11`, `ctrl+q`, `pageup`.

## `#showme`, `#show`

Вывести текст в основной буфер `main`, не отправляя его в MUD.

Синтаксис:

```text
#showme {text}
```

Примеры:

```text
#showme {Hello from local client}
#show {Target is $target}
```

Это одна из новых команд.

## `#woutput`

Вывести текст в указанный буфер, не отправляя его в MUD.

Синтаксис:

```text
#woutput {buffer_name} {text}
```

Примеры:

```text
#woutput {chat} {Party channel connected}
#woutput {combat} {Target: $target}
```

Это одна из новых команд для multi-buffer UI.

## `#tts`, `#ts`

Озвучить текст локально на клиенте.

Синтаксис:

```text
#tts {text}
```

Примеры:

```text
#tts {ready}
#tts {$target is down}
```

Замечания:

- на macOS используется системная команда `say`;
- на других платформах команда возвращает локальное сообщение, что TTS не поддерживается;
- `#tts` можно вызывать из alias и trigger'ов.

## `#ticksize`

Установить длительность цикла тикера в секундах.

Синтаксис:

```text
#ticksize {seconds}
```

Примеры:

```text
#ticksize {60}
#ticksize {30}
```

Замечания:

- значение по умолчанию — 60 секунд;
- `#ticksize {0}` останавливает тикер;
- отрицательные значения отклоняются.

## `#tickon`

Запустить обратный отсчёт тикера, используя текущую длительность цикла.

Синтаксис:

```text
#tickon
```

Замечания:

- не принимает аргументов;
- если тикер уже запущен или цикл равен 0, ничего не делает;
- если `#ticksize` ещё не вызывался, запускает 60-секундный цикл.

## `#tickoff`

Остановить обратный отсчёт тикера.

Синтаксис:

```text
#tickoff
```

Замечания:

- не принимает аргументов.

## `#tickset`

Сбросить обратный отсчёт до текущей длительности цикла или установить новую длительность и сбросить немедленно.

Синтаксис:

```text
#tickset
#tickset {seconds}
```

Примеры:

```text
#tickset
#tickset {30}
```

Замечания:

- форма без аргументов просто возвращает отсчёт к началу текущего цикла;
- `#tickset {0}` останавливает тикер;
- часто используется в триггерах для синхронизации: `#action {^Начался день\.$} {#tickset}`;
- тикер является общим для всей сессии (session-scoped) — все клиенты видят одно и то же время;
- индикатор виден в нижней панели интерфейса и сохраняется при переподключении.

## Buffer Routing

Кроме `#woutput`, в движке поддерживается buffer routing у trigger'ов:

- `move`
- `copy`
- `echo`

Сейчас это не отдельные runtime-команды, а свойства trigger rule.

Они настраиваются через:

- Settings UI в редакторе trigger'ов;
- `.tt` import/export профилей;
- API.

То есть перекидывать лог в буферы без `#woutput` можно.

Для этого у trigger'а задаются:

- `target_buffer`
- `buffer_action`

Смысл режимов:

- `move` — строка уходит только в target buffer;
- `copy` — строка остаётся в `main` и дополнительно пишется в target buffer;
- `echo` — trigger пишет отдельную synthetic строку в target buffer.

Если нужен именно произвольный текст, собранный из `%0/%1/...`, переменных и `$TIME`, удобнее использовать `#woutput` внутри trigger command.

## Чего сейчас нет как runtime-команд

Ниже вещи, которые могут встречаться в старых TinTin/JMC/Tortilla-конфигах, но в текущем движке не реализованы как команды:

- `#group`
- `#if`
- `#wait`
- `#sub`
- `#gag`
- `#read`
- `#write`
- runtime-удаление hotkey отдельной командой
- runtime-синтаксис для trigger buffer routing

## Короткий пример

```text
#var {bag} {рюкзак}
#alias {gr} {get %0 $bag}
#action {^R\.I\.P\.$} {get all corpse} {loot} {button}
#highlight {bold,256:196} {^ALARM}
#hotkey {f1} {score}
#showme {Client ready at $TIME}
#woutput {chat} {Chat pane ready}
```
