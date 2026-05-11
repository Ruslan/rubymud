# HTML-like разметка для локального вывода

## Контекст

`#showme` и `#woutput` сейчас выводят текст локально, не отправляя его в MUD. Цветной разовый вывод возможен только через raw ANSI escape sequences, что неудобно для ручного ввода и плохо читается в profile scripts.

`#highlight` решает другую задачу: это постоянное regexp-правило подсветки входящих и отображаемых строк. Для сообщений вроде `#showme {No target}` нужен разовый, авторский формат прямо внутри текста.

У конкурентов есть похожие решения:
- TinTin++ использует inline-коды вида `<125>`, `<acf>`, `<F000000>` в `#show`/`#echo`.
- Tortilla/JMC имеют цветовой параметр у `#output`/`#woutput`.
- Mudlet использует читаемые Lua helpers `cecho("<red>text")`, `decho("<255,0,0>text")`, `hecho("#ff0000text")`.

Для RubyMUD наиболее удобный вариант: безопасная HTML-like разметка, которая компилируется в ANSI перед записью в log/output.

## Цели

- Сделать разовый цветной вывод читаемым:

```text
#showme {<red>Ошибка:</red> <b>нет цели</b>}
#woutput {combat} {<yellow><u>Target:</u></yellow> $target}
```

- Переиспользовать возможности `#highlight`: именованные цвета, `256:N`, hex, RGB, bold/faint/italic/underline/blink/reverse/strikethrough.
- Не исполнять HTML в браузере и не вводить XSS-риск.
- Не применять новую разметку к входящим строкам от MUD-сервера.

## Scope MVP

Разметка применяется только к локальному авторскому выводу:

```text
#showme {text}
#show {text}
#woutput {buffer} {text}
```

Не применяется к:
- входящему тексту от MUD-сервера;
- `#highlight` pattern/color spec;
- `#sub` replacement в рамках MVP;
- обычным командам, отправляемым в MUD.

Причина: локальный вывод находится под контролем автора скрипта, а входящий MUD-текст должен оставаться данными, не клиентской разметкой.

Важно: `#sub` replacement в `0.0.8.9.3` по-прежнему принимает plain/raw ANSI, но не HTML-like markup. Это сознательное разделение MVP, чтобы `#sub` и локальная разметка не сцеплялись в одну большую миграцию.

## Синтаксис

### Styles

```text
<b>...</b>
<bold>...</bold>
<faint>...</faint>
<i>...</i>
<italic>...</italic>
<u>...</u>
<underline>...</underline>
<s>...</s>
<strike>...</strike>
<strikethrough>...</strikethrough>
<blink>...</blink>
<reverse>...</reverse>
<reset>
```

### Named colors

Теги цветов используют `colorreg` и те же canonical/alias имена, что highlights. Multi-word имена в tag-name форме пишутся через `-`:

```text
<red>red</red>
<light-red>light red</light-red>
<cyan>cyan</cyan>
<bg-blue>blue background</bg-blue>
<bg-light-blue>light blue background</bg-light-blue>
```

Дополнительно поддержать explicit-форму, чтобы не плодить спецслучаи для custom values:

```text
<fg red>red</fg>
<fg light red>light red</fg>
<bg blue>blue background</bg>
```

### 256 / hex / RGB

```text
<fg 256:196>ansi 256 red</fg>
<bg 256:21>ansi 256 blue background</bg>
<fg #ff8800>hex foreground</fg>
<bg #202020>hex background</bg>
<fg rgb255,128,0>rgb foreground</fg>
<bg rgb20,20,20>rgb background</bg>
```

`rgb` формат должен совпадать с текущим backend-парсингом highlights: `rgbR,G,B` без пробела между `rgb` и числами.

## Семантика

Разметка транслируется в ANSI SGR sequences до записи в log:

```text
#showme {<red>Ошибка:</red> ok}
```

становится raw text:

```text
\x1b[31mОшибка:\x1b[0m ok
```

`PlainText` в БД остаётся без ANSI благодаря существующему `stripANSI()`.

Для локального авторского вывода canonical log — это уже отрендеренный ANSI text. Исходная markup-строка (`<red>...`) не сохраняется отдельно и не replay-ится как source markup. Это отличается от `#sub`, где оригинальный MUD-текст остаётся canonical input, а display-изменения сохраняются как overlays.

Закрывающие теги должны восстанавливать предыдущий стиль, а не всегда делать полный reset без restore. Пример:

```text
<red>red <b>bold red</b> red again</red>
```

После `</b>` foreground должен остаться red.

`<reset>` сбрасывает весь стек форматирования и возвращает default style.

## Parser

Добавить отдельный маленький backend-модуль для parser/render логики, например:

```text
go/internal/vm/markup.go
go/internal/vm/markup_test.go
```

Публичная точка входа внутри VM:

```go
func renderLocalMarkup(input string) string
```

Модуль должен быть независим от command dispatch: он принимает строку и возвращает строку с ANSI. Команды `#showme/#show/#woutput` только вызывают helper на своих display-text аргументах.

Требования:
- whitelist тегов, никаких произвольных HTML-тегов;
- неизвестные/битые теги оставлять как plain text;
- текст между тегами не HTML-decoded и не исполняется;
- вложенность поддерживается стеком active styles;
- закрытие несуществующего тега оставлять как visible plain text, не игнорировать и не паниковать;
- несбалансированные открытые теги в конце строки не должны ломать вывод;
- escape literal `<` не нужен в MVP, потому что неизвестные теги остаются visible plain text.

## ANSI generation

Вынести или переиспользовать логику из `highlightToANSI()` как общий backend helper, чтобы `#highlight` и local markup не разошлись в цветовой семантике:

```go
type TextStyle struct {
    FG string
    BG string
    Bold bool
    Faint bool
    Italic bool
    Underline bool
    Blink bool
    Reverse bool
    Strikethrough bool
}
```

Нужна функция генерации ANSI из style без зависимости от `storage.HighlightRule`, либо адаптер из `TextStyle` в `HighlightRule`.

Цвета должны поддерживать те же форматы:
- named colors через `colorreg.NameToAnsiFg/NameToAnsiBg`;
- `256:N`;
- `#rgb` / `#rrggbb`;
- `rgbR,G,B`.

## Интеграция

В `go/internal/vm/commands.go`:

```go
case "showme", "show":
    text, _ := splitBraceArg(rest)
    if text == "" { text = rest }
    text = renderLocalMarkup(text)
    return []Result{{Text: text, Kind: ResultEcho, TargetBuffer: "main", ...}}

case "woutput":
    buffer, afterBuffer := splitBraceArg(rest)
    text, _ := splitBraceArg(afterBuffer)
    if text == "" { text = strings.TrimSpace(afterBuffer) }
    text = renderLocalMarkup(text)
    return []Result{{Text: text, Kind: ResultEcho, TargetBuffer: buffer, ...}}
```

Frontend менять не нужно: `ui/src/render.ts` уже использует `ansi_up` для `entry.text`.

## Security

- Не передавать HTML из markup напрямую во frontend.
- Не добавлять `dangerouslySetInnerHTML`-подобную обработку пользовательских тегов.
- Все supported tags превращаются только в ANSI, а потом уже существующий `ansi_up` конвертирует ANSI в safe HTML spans.
- Unknown tags остаются обычным текстом, чтобы `<script>` не стал HTML.

## Tests

Parser unit tests (`go/internal/vm/markup_test.go`), дешёвые и без storage/session:
- simple tag: `<red>x</red>` генерирует ANSI red, plain после stripANSI равен `x`.
- nested restore: `<red>a <u>b</u> c</red>` восстанавливает red после `</u>`.
- multiple nested attrs: `<red><u><i>x</i></u></red>` корректно снимает italic, потом underline, потом red.
- `<reset>` сбрасывает stack и дальнейший текст идёт default style.
- unknown tags: `<foo>x</foo>` остаются текстом или как минимум не превращаются в HTML/ANSI.
- broken/unbalanced tags do not panic: `<red>x`, `</red>x`, `<fg>bad</fg>`.
- named colors: `<light-red>`, `<bg-blue>`, aliases через `colorreg`.
- custom colors: `<fg 256:196>`, `<fg #ff0000>`, `<bg rgb0,51,102>`.
- generated ANSI contains expected SGR fragments for representative cases.

Backend VM command tests:
- `#showme {<red>x</red>}` возвращает ANSI red и plain после stripANSI равен `x`.
- Вложенность: `<red>a <b>b</b> c</red>` восстанавливает red после `</b>`.
- `<reset>` сбрасывает stack.
- Named colors: `<light-red>`, `<bg-blue>`, aliases через `colorreg`.
- Custom colors: `<fg 256:196>`, `<fg #ff0000>`, `<bg rgb0,51,102>`.
- Unknown tags: `<foo>x</foo>` остаются текстом или как минимум не превращаются в HTML/ANSI.
- `#woutput {combat} {<green>x</green>}` пишет в нужный buffer.

Session/storage tests:
- `RawText` содержит ANSI, `PlainText` не содержит ANSI и не содержит служебных тегов для supported markup.
- Restore/recent logs отдают raw ANSI как и обычные highlighted строки.

Frontend smoke:
- существующий `ansi_up` рендерит generated ANSI без изменений UI-кода.

## Docs

Обновить `docs/engine-commands.md`:
- В раздел `#showme`, `#show` добавить подраздел `Локальная разметка`.
- В раздел `#woutput` добавить тот же синтаксис.
- Явно написать отличие от `#highlight`: `#highlight` — постоянное правило, markup в `#showme` — разовый вывод.

Примеры для docs:

```text
#showme {<red>Ошибка:</red> нет цели}
#showme {<yellow><b>Tick</b></yellow> через 10 секунд}
#woutput {combat} {<fg 256:196>CRIT</fg>: $target}
```

## Не входит в MVP

- TinTin++ numeric tags `<125>`, `<acf>`, `<F000000>`.
- Markdown syntax.
- Реальный HTML/CSS.
- Применение markup к входящему MUD-тексту.
- Markup в `#sub` replacement — вынесено в `0.0.8.9.5`.
- UI editor/preview для markup.

## Future: markup в других командах

Разметку можно расширять после MVP, но не как глобальный препроцессор всех команд. Правильная модель: каждая команда явно помечает аргументы, которые являются author-controlled display text и допускают markup.

Кандидаты после MVP:
- `#sub` replacement — следующий отдельный план `0.0.8.9.5`
- trigger echo/button labels, если они являются локальным UI-текстом
- timer/local notification text
- будущие toast/status/UI-команды

Не должны автоматически парсить markup:
- входящий MUD-текст
- команды, отправляемые в MUD
- regex patterns
- имена переменных, alias, triggers, timers, buffers

Для `#sub` replacement нужен template-aware renderer, а не простой `renderLocalMarkup` после `%0..%N`. Capture groups приходят из MUD и должны оставаться данными, а не markup. Без этого MUD-строка с `<red>` внутри capture group смогла бы изменить стиль вывода.

Будущая безопасная последовательность для rich `#sub`:
1. Парсим author replacement template как markup/template
2. Раскрываем `$vars` как author-controlled template values
3. Подставляем `%0..%N` как literal text segments, не интерпретируя их содержимое как markup
4. Генерируем `replacement_raw` с ANSI и `replacement_plain` без ANSI
5. Сохраняем фактический результат в `log_overlays`, как в `0.0.8.9.3`

Тогда rich sub сможет делать capture-specific styling:

```text
#sub {вы взяли (.+) из трупа (.+)} {вы взяли <fg red>%1</fg> из трупа <fg green>%2</fg>}
```

Это функционально пересекается с `#highlight`, но не заменяет его полностью: `#highlight` остаётся динамическим presentation-only правилом, а `#sub` сохраняет applied display результат в history overlays.

## Решения по MVP

- JMC/Tortilla-style `#output [color] text` не входит в MVP; если понадобится, это отдельная compatibility-команда поверх `#showme`.
- Unknown opening/closing tags остаются visible plain text. Это проще для отладки и безопаснее, чем молча терять пользовательский текст.
- Escape literal `<` не входит в MVP. Для literal HTML-like текста достаточно того, что неизвестные/битые теги не исполняются и остаются текстом.
