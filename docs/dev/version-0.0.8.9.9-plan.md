# Rich markup в `#sub` replacement

## Контекст

`0.0.8.9.3` добавляет `#sub/#gag` как overlay-based UX-слой над входящим MUD-текстом: canonical log остаётся оригинальным, applied result сохраняется в `log_overlays`.

`0.0.8.9.4` добавляет HTML-like markup для локального авторского вывода (`#showme/#woutput`) и общий backend helper для генерации ANSI из safe markup.

Этот план расширяет `#sub` replacement: автор replacement может использовать markup для styling capture groups и статического текста.

## Цели

Поддержать rich substitutions вида:

```text
#sub {вы взяли (.+) из трупа (.+)} {вы взяли <fg red>%1</fg> из трупа <fg green>%2</fg>}
```

Ожидаемый результат:
- `%1/%2` подставляются из regex capture groups
- markup автора компилируется в ANSI
- `replacement_raw` сохраняется в overlay уже с ANSI
- `replacement_plain` сохраняется без ANSI
- restore воспроизводит тот же applied display result без переприменения текущих правил

## Не меняется

- `log_entries.raw_text/plain_text` остаются canonical original MUD input.
- Triggers продолжают матчиться по оригинальному plain text.
- `#highlight` остаётся отдельным dynamic presentation-only слоем.
- Входящий MUD-текст не интерпретируется как markup.

## Семантика

`#sub` replacement становится author-controlled markup template.

Порядок обработки:
1. `Pattern` раскрывает `$vars` через `substitutePatternVars`, как в `0.0.8.9.3`, с `regexp.QuoteMeta` для значений переменных
2. Regex матчится на текущем `displayPlain`
3. Replacement template парсится как safe markup/template
4. `$vars` в replacement раскрываются как literal text segments
5. `%0..%N` подставляются как literal text segments из MUD capture groups
6. Содержимое `$vars` и `%0..%N` не интерпретируется как markup
7. Итоговый template render генерирует `replacement_raw` с ANSI и `replacement_plain` без ANSI
8. `substitution` overlay сохраняет фактические `replacement_raw/replacement_plain`, `pattern_template`, `effective_pattern`, `replacement_template`

Ключевое security-правило: markup применим только к авторскому replacement template, а не к подставленным данным. Если MUD прислал capture group `<red>orc</red>`, она должна отобразиться как буквальный текст `<red>orc</red>`, а не включить цвет. Если `$target1` содержит `<red>orc</red>`, значение переменной тоже вставляется как literal text, а не как markup.

## Parser/API

Нельзя просто выполнить:

```go
renderLocalMarkup(expandedReplacement)
```

После такой операции MUD capture groups могли бы стать markup. Нужен template-aware API поверх модуля `0.0.8.9.4`, например:

```go
type MarkupSegment struct {
    Text      string
    IsMarkup  bool
}

func renderMarkupTemplate(template string, vars map[string]string, captures []string) (raw string, plain string)
```

Точная форма API не принципиальна, но renderer должен различать:
- author markup/control tokens
- literal text from `$vars`
- literal text from `%0..%N`

## Отношение к `#highlight`

Rich `#sub` частично перекрывает сценарии `#highlight`, но не заменяет его полностью.

`#highlight` лучше для:
- простого dynamic coloring без изменения текста
- правил, которые должны применяться к старым строкам при restore по текущей конфигурации
- presentation-only подсветки

Rich `#sub` лучше для:
- переписывания строки
- styling отдельных capture groups внутри replacement
- стабильного history replay через saved overlays
- сценариев, где важно сохранить именно applied result того времени

## Edge cases

- Nested markup в replacement поддерживается так же, как в `0.0.8.9.4`.
- Unknown/broken tags остаются literal text и не становятся HTML.
- `%N` без capture group заменяется пустой строкой.
- Zero-width matches остаются ignored, как в `0.0.8.9.3`.
- ANSI в capture group от MUD сохраняется как данные; renderer не должен ломать plain/raw mapping.
- ANSI/raw escape sequences, явно написанные автором в replacement, остаются допустимыми, но markup является предпочтительным синтаксисом.

## Tests

- `#sub {вы взяли (.+) из трупа (.+)} {вы взяли <fg red>%1</fg> из трупа <fg green>%2</fg>}` создаёт overlay с ANSI в `replacement_raw` и plain без ANSI.
- Capture group `<red>orc</red>` отображается как literal text, а не как markup.
- `$target1` в replacement раскрывается и может быть внутри markup: `<fg red>$target1</fg>`.
- `%1` внутри nested tags корректно наследует styles: `<red><u>%1</u></red>`.
- Restore replay даёт тот же display raw/plain, что live render.
- Canonical `log_entries.raw_text/plain_text` остаются оригинальным MUD-текстом.
- `#highlight` после rich sub применяется поверх display raw.

## Не входит в MVP этой версии

- Markup в regex patterns.
- Markup во входящем MUD-тексте.
- Display-aware FTS по rich replacement.
- UI preview с полноценным ANSI render, если это не появилось раньше.
