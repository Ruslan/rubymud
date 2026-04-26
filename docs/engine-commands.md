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
#ticksize {name} {seconds}
```

Примеры:

```text
#ticksize {60}
#ticksize {herb} {30}
```

Замечания:

- форма без `name` устанавливает размер для default `ticker`;
- значение по умолчанию для основного тикера — 60 секунд;
- `#ticksize {0}` останавливает тикер;
- отрицательные значения отклоняются;
- имя таймера не должно быть числом и не должно начинаться с `+` или `-`.

## `#tickon`

Запустить обратный отсчёт тикера, используя текущую длительность цикла.

Синтаксис:

```text
#tickon
#tickon {name}
```

Замечания:

- форма без `name` работает с default `ticker`;
- если тикер уже запущен или цикл равен 0, ничего не делает;
- если `#ticksize` ещё не вызывался для этого имени, запускает 60-секундный цикл.

## `#tickoff`

Остановить обратный отсчёт тикера.

Синтаксис:

```text
#tickoff
#tickoff {name}
```

Замечания:

- форма без `name` работает с default `ticker`;
- если таймер уже остановлен, команда просто ничего не делает.

## `#tickset`

Сбросить обратный отсчёт до текущей длительности цикла, установить новую длительность цикла с немедленным reset или скорректировать текущую фазу относительно текущего времени.

Синтаксис:

```text
#tickset
#tickset {seconds}
#tickset {+seconds}
#tickset {-seconds}
#tickset {name}
#tickset {name} {seconds}
#tickset {name} {+seconds}
#tickset {name} {-seconds}
```

Примеры:

```text
#tickset
#tickset {30}
#tickset {+2}
#tickset {-1.5}
#tickset {herb}
#tickset {herb} {30}
#tickset {herb} {+5}
```

Замечания:

- форма без аргументов просто возвращает отсчёт к началу текущего цикла;
- формы без `name` работают с default `ticker`;
- `#tickset {seconds}` устанавливает **новую длительность цикла** и немедленно сбрасывает фазу (эквивалент `#ticksize {seconds}; #tickset`);
- `#tickset {+seconds}` и `#tickset {-seconds}` (относительное значение, дельта) корректируют текущую фазу **без изменения длины цикла**:
  - `+seconds` увеличивает оставшееся время (отодвигает тик);
  - `-seconds` уменьшает оставшееся время (приближает тик);
  - дельта-коррекция зажимается (clamp) между `0` и текущей длительностью цикла;
- `#tickset {0}` останавливает тикер;
- часто используется в триггерах для синхронизации: `#action {^Начался день\.$} {#tickset}`;
- тикеры являются общими для всей сессии (session-scoped) — все клиенты видят одно и то же время;
- основной тикер виден в нижней панели, дополнительные активные тикеры отображаются рядом в виде компактных плашек;
- **персистентность**: состояние повторяющихся таймеров (цикл, фаза, иконка, подписки) сохраняется в базе данных и восстанавливается при перезагрузке сервера;
- при восстановлении включенного таймера фаза пересчитывается с учётом времени, прошедшего пока сервер был выключен (на основе wall clock), чтобы сохранить ритм тика;
- разовые отложенные команды (`#delay`) не сохраняются и не восстанавливаются.

## `#tickicon`

Установить иконку для отображения таймера в интерфейсе.

Синтаксис:

```text
#tickicon {icon}
#tickicon {name} {icon}
```

Примеры:

```text
#tickicon {🕒}
#tickicon {herb} {🪴}
#tickicon {herb} {}
```

Замечания:

- форма без `name` устанавливает иконку для default `ticker`;
- `icon` — короткая строка, обычно один эмодзи;
- иконка отображается перед временем таймера в статусной строке;
- пустой аргумент `{}` очищает иконку таймера.

## `#tickat`

Подписать команду на выполнение в определённый момент цикла тикера, когда до следующего тика остаётся указанное число секунд.

Синтаксис:

```text
#tickat {second} {command}
#tickat {name} {second} {command}
```

Примеры:

```text
#tickat {3} {stand;wear shield}
#tickat {herb} {0} {use herb}
#tickat {0} {#delay {ready} {2} {report ready}}
```

Замечания:

- `second` должен быть целым числом от `0` до текущей длины цикла;
- для default ticker верхняя граница по умолчанию равна `60`, даже если `#tickon` ещё не вызывался;
- если названный таймер ещё не существует, он будет создан с циклом по умолчанию (60с);
- подписки привязаны к конкретному таймеру;
- команда срабатывает на каждой итерации цикла, пока подписка существует;
- scheduled command проходит через обычный VM pipeline: работают `;`, alias, переменные, `#showme`, `#woutput`, `#tts`;
- повторный вызов `#tickat` для того же `second` добавляет ещё одну команду в этот слот.

## `#untickat`

Удалить все подписки, привязанные к указанной секунде обратного отсчёта.

Синтаксис:

```text
#untickat {second}
#untickat {name} {second}
```

Примеры:

```text
#untickat {3}
#untickat {herb} {0}
```

Замечания:

- удаляет все команды, зарегистрированные для этого `second`;
- если подписок на этой секунде нет, команда просто ничего не делает (no-op).

## `#delay`

Запланировать одноразовую отложенную команду.

Синтаксис:

```text
#delay {seconds} {command}
#delay {id} {seconds} {command}
```

Примеры:

```text
#delay {2} {stand}
#delay {ready} {1.5} {report ready}
```

Замечания:

- форма без `id` создаёт анонимную задержку;
- форма с `id` позволяет потом отменить задержку через `#undelay`;
- **важно**: повторный `#delay` с тем же `id` silently перезаписывает предыдущую задержку — старый таймер отменяется, новый стартует заново; это удобно для debouncing (например, «сделай действие через 5 с после *последней* атаки»);
- `seconds` может быть дробным неотрицательным числом;
- слишком маленькие задержки автоматически зажимаются до безопасного минимума runtime;
- delayed command тоже проходит через обычный VM pipeline;
- одновременно можно держать ограниченное число pending delays, при переполнении команда вернёт диагностическое сообщение;
- **важно**: в отличие от повторяющихся таймеров, `#delay` является волатильным и **не сохраняется** при перезагрузке сервера.

## `#undelay`

Отменить ранее поставленную отложенную команду по её идентификатору.

Синтаксис:

```text
#undelay {id}
```

Примеры:

```text
#undelay {ready}
```

Замечания:

- работает только для задержек, созданных через именованную форму `#delay {id} {seconds} {command}`;
- если delay с таким `id` уже выполнен или не существует, команда просто ничего не делает.

## `#ticker`

Быстрое создание повторяющегося таймера одной командой — шорткат, эквивалентный вызову `#tickset {name} {seconds}; #tickat {name} {0} {command}; #tickon {name}`.

Синтаксис:

```text
#ticker {name} {seconds} {command}
```

Примеры:

```text
#ticker {herb} {58} {stand}
#ticker {spell} {4} {cast 'magic missile'}
```

Замечания:

- создаёт именованный таймер (или обновляет его cycle/reset state, если он уже существует), задаёт длину цикла, подписывает команду на second `0` (конец каждого цикла) и сразу включает таймер;
- при повторном вызове с тем же `name` цикл таймера перезапускается с новыми параметрами, но existing subscriptions не очищаются автоматически;
- если таймер уже имеет другие подписки, `#ticker` добавит ещё одну на second `0` — для полного сброса таймера используйте ручную комбинацию команд;
- эта команда создана для быстрого перехода пользователей TinTin++ (`#ticker`) и Tortilla (repeating timer), которым нужен простой независимый таймер.

## Migration From Other Clients

Если вы переносите timer-скрипты из JMC, Tortilla или TinTin++, важно учитывать, что у RubyMUD модель таймеров гибридная:

- есть default world-tick timer `ticker`, как в старых tick-oriented клиентах;
- есть named timers, ближе к generic timers в более гибких клиентах;
- есть `#tickat`, который привязывает действия к remaining-second slot внутри цикла, а не просто создаёт независимый interval timer.

### JMC

Наиболее близкий по базовой логике клиент.

Привычные соответствия:

- `#tickon` -> `#tickon`
- `#tickoff` -> `#tickoff`
- `#ticksize {60}` -> `#tickset {60}`
- `#tickset` -> `#tickset`

Ключевое отличие:

- в JMC `#ticksize {seconds}` меняет длину цикла и немедленно сбрасывает фазу;
- в RubyMUD `#ticksize` меняет только длину цикла (без сброса фазы);
- RubyMUD `#tickset {seconds}` делает и то, и другое — это точный 1:1 эквивалент JMC `#ticksize`.

Практическая адаптация:

- JMC `#ticksize {60}` → RubyMUD `#tickset {60}` (меняет размер и сбрасывает фазу, ровно как в JMC)
- если нужно только сменить размер без сброса (редкий случай), используется `#ticksize {60}`

### Tortilla

В Tortilla чаще встречается модель "независимый timer по id" плюс отдельный `#wait` для разовых задержек.

Полезные соответствия:

- Tortilla `#wait` -> RubyMUD `#delay`
- Tortilla named/repeating timer use case -> RubyMUD named timer через `#ticksize {name} {seconds}` + `#tickon {name}` (или шорткат `#ticker {name} {seconds} {command}` для single-action timer)
- Tortilla reset/update timer use case -> RubyMUD `#tickset {name}` или `#tickset {name} {seconds}`

Ключевое отличие:

- вместо набора независимых таймеров для действий внутри одного большого цикла в RubyMUD часто удобнее использовать один timer и несколько `#tickat` подписок.

Пример адаптации:

Вместо набора отдельных таймеров вокруг одного 60-секундного цикла:

```text
#ticksize {60}
#tickon
#tickat {58} {stand}
#tickat {3} {wear shield}
#tickat {0} {bash $target}
```

То есть RubyMUD чаще описывает "действия внутри цикла", а не "много таймеров, стартованных в разное время".

Если же нужен именно простой независимый таймер (как Tortilla repeating timer или TinTin++ `#ticker`), удобнее использовать шорткат:

```text
#ticker {herb} {58} {stand}
```

### TinTin++

Наиболее близок по `#delay` и по идее named timers, но отличается по tick lifecycle.

Полезные соответствия:

- TinTin++ `#delay {seconds} {command}` -> RubyMUD `#delay {seconds} {command}`
- TinTin++ named delay -> RubyMUD `#delay {id} {seconds} {command}` + `#undelay {id}`
- TinTin++ generic ticker use case -> RubyMUD named timer через `#ticker {name} {seconds} {command}` (простейший 1:1 вариант) или `#ticksize {name} {seconds}` + `#tickon {name}`

Ключевые отличия:

- TinTin++ обычно мыслит тикеры как create/delete objects;
- RubyMUD разделяет жизненный цикл часов и жизненный цикл подписок:
  - выключение timer (пауза цикла, подписки остаются): `#tickoff`
  - удаление подписок цикла (цикл продолжается, действия убраны): `#untickat`
  - у RubyMUD пока нет одной команды, которая одновременно останавливает цикл и чистит все подписки (full delete);
  - если нужно полностью убрать timer по образу TinTin++ `#untick`, комбинируйте `#tickoff` + `#untickat`;
- у RubyMUD есть отдельная world-tick модель через default `ticker`;
- у RubyMUD есть `#tickicon`, это UI-расширение, а не совместимая с TinTin++ runtime-команда.

Практическая адаптация:

- если в TinTin++ вы удаляли ticker, чтобы он больше не работал, в RubyMUD обычно сначала решают, что именно нужно:
  - остановить timer -> `#tickoff`
  - убрать действия на конкретной секунде -> `#untickat`
  - полное удаление (часы + подписки) -> `#tickoff` плюс `#untickat` на все задействованные секунды
- если вам нужен one-shot follow-up, перенос чаще всего прямой:

```text
#delay {2} {report ready}
```

### Quick Mapping

```text
JMC #tickon                        -> #tickon
JMC #tickoff                       -> #tickoff
JMC #ticksize {seconds}            -> #tickset {seconds}  (1:1, меняет размер + сбрасывает фазу)
JMC #tickset                       -> #tickset
Tortilla #wait                     -> #delay
Tortilla repeating timer by id     -> #ticksize {name} {seconds}; #tickon {name}
                                      (or #ticker {name} {seconds} {command} for single-action timers)
TinTin++ #delay                    -> #delay
TinTin++ delete-style ticker       -> #tickoff (pause cycle) or #untickat (remove actions);
                                      no single "full delete" command yet
```

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
