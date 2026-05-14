# Динамические подсветки с поддержкой переменных

## Описание
В текущей реализации подсветки (Highlights) компилируются один раз при загрузке профиля. Если в шаблоне подсветки используется переменная (например, `$target`), она не раскрывается в значение переменной, а воспринимается как часть регулярного выражения (`$` становится regex-якорем конца строки), из-за чего такое правило фактически не работает как шаблон с переменной.

Для сравнения, замены (Substitutions) уже поддерживают динамическое раскрытие переменных в шаблонах. Цель данной задачи — перевести подсветки на ту же динамическую модель.

## Предложенные изменения

### [Component] Go Runtime (VM)

#### [MODIFY] [types.go](file:///home/ru/rubymud/go/internal/vm/types.go)
- Убрать поля `re *regexp.Regexp` и `rule storage.HighlightRule` из структуры `compiledHighlight` — регулярки больше не компилируются на этапе `rebuildCaches`, а само правило берётся из `v.highlights` по тому же индексу.
- Оставить в `compiledHighlight` только поле `ansi string` (кэш ANSI-последовательности для правила).
- `compiledHighlights` остаётся срезом `[]compiledHighlight`, индексируется параллельно с `v.highlights`.

Почему не `map[int64]string`: при `store == nil` все подсветки имеют `ID = 0`, что приводит к коллизиям. Параллельная индексация по позиции в срезе надёжнее.

Инвариант: после `ensureFresh()` длины `v.highlights` и `v.compiledHighlights` должны совпадать. В `ApplyHighlights` стоит либо опираться на этот инвариант, либо добавить защиту `if i >= len(v.compiledHighlights) { continue }`, чтобы ручная мутация `v.highlights` без увеличения `rulesVersion` не приводила к panic.

#### [MODIFY] [runtime.go](file:///home/ru/rubymud/go/internal/vm/runtime.go)
- В `rebuildCaches`:
  - Убрать компиляцию регулярных выражений для подсветок (строки 178-180).
  - Оставить только вычисление ANSI через `highlightToANSI(h)` и заполнение `compiledHighlights` параллельно с `v.highlights`:
    ```go
    v.compiledHighlights = make([]compiledHighlight, len(v.highlights))
    for i := range v.highlights {
        v.compiledHighlights[i] = compiledHighlight{
            ansi: highlightToANSI(&v.highlights[i]),
        }
    }
    ```
  - Строка `v.effectivePatternCache = make(map[string]*regexp.Regexp)` остаётся без изменений — очистка кэша при перестроении правил уже работает корректно.

#### [MODIFY] [highlight_apply.go](file:///home/ru/rubymud/go/internal/vm/highlight_apply.go)
- Переработать функцию `ApplyHighlights` для поддержки динамических шаблонов:
  - Итерироваться по `v.highlights` с индексом `i` (вместо `v.compiledHighlights`).
  - Внутри цикла для каждого включенного правила:
    - Пропускать правила с `!rule.Enabled`.
    - Получать ANSI из кэша: `ansi := v.compiledHighlights[i].ansi`, пропускать если `ansi == ""`.
    - Вызывать `effectivePattern := v.substitutePatternVars(rule.Pattern)` для раскрытия переменных.
    - Вызывать `re := v.compileEffectivePattern(rule.Pattern, effectivePattern)` для получения скомпилированной регулярки из `effectivePatternCache`.
    - Пропускать если `re == nil` (ошибка компиляции).
    - Остальная логика (поиск совпадений, вставка ANSI, восстановление внешнего контекста) остаётся без изменений.
- `plainText := stripANSIFromVM(text)` можно оставить вычисленным один раз перед циклом: подсветки вставляют только ANSI-последовательности и не меняют plain-текст, поэтому plain offsets остаются валидными для следующих правил.
- См. образец: `ApplySubsAndCollectOverlays` в `substitution_apply.go:76-138`.

#### [MODIFY] [runtime_cache_test.go](file:///home/ru/rubymud/go/internal/vm/runtime_cache_test.go)
- Тест `TestHighlightCachePrecomputesANSI` (строка 80-94) сейчас не обращается к `compiledHighlight.re`, поэтому может пройти почти без изменений. Его лучше усилить: проверять, что `compiledHighlights` содержит одну запись, `compiledHighlights[0].ansi != ""`, текст подсвечивается корректно, а после первого `ApplyHighlights` в `effectivePatternCache` появляется скомпилированный шаблон.

#### [NEW] [highlight_dynamic_test.go](file:///home/ru/rubymud/go/internal/vm/highlight_dynamic_test.go)
- Добавить тесты, проверяющие:
  1. **Подсветка через переменную**: задать `v.variables["enemy"] = "Крыса"`, highlight с pattern `$enemy`. Вызвать `ApplyHighlights("Атакует Крыса!")` — проверить что слово «Крыса» обёрнуто в ANSI.
  2. **Literal-экранирование значения переменной**: задать значение с regex-метасимволами, например `v.variables["enemy"] = "a.b"` или `v.variables["enemy"] = "King\\dark"`. Проверить, что подсвечивается только literal-строка (`a.b`), а не regex-совпадение (`axb`). Это должно повторять поведение `substitutePatternVars`, где значение проходит через `regexp.QuoteMeta`.
  3. **Автообновление при смене переменной**: после первого apply менять переменную через `v.ProcessInputDetailed("#variable {enemy} {Волк}")`, а не прямой записью в map. Это проверяет реальное поведение `cmdVariable`: переменная обновляется, `effectivePatternCache` очищается, затем «Волк» подсвечен, а «Крыса» — нет. Если тест меняет `v.variables` напрямую, тогда cache нужно очищать явно, потому что прямой доступ обходит runtime-команду.
  4. **Корректность кэша**: вызвать `ApplyHighlights` дважды без изменения переменных. Проверить `len(v.effectivePatternCache)` до и после вызовов — размер не должен расти при повторном вызове (регулярка берётся из кэша, а не перекомпилируется).
  5. **Несколько подсветок на одной строке**: две подсветки с разными переменными (например `$enemy` и `$weapon`), обе находят совпадение в одной строке — проверить что оба ANSI-кода применены.
  6. **Неизвестная переменная остаётся literal**: pattern `$unknown` должен подсветить literal-текст `$unknown`, а не воспринимать `$` как regex-якорь.

## План верификации

### Автоматические тесты
- Запуск всех тестов VM из модуля Go: `cd go && go test -v ./internal/vm/...`
- Убедиться что существующие тесты подсветок (`highlight_test.go`) проходят без изменений.

### Ручная проверка
1. Создать подсветку на переменную `$enemy`: `#highlight {red} {$enemy}`
2. Установить переменную: `#variable {enemy} {Крыса}`
3. Убедиться, что в выводе слово «Крыса» подсвечено красным.
4. Изменить переменную: `#variable {enemy} {Волк}`
5. Убедиться, что теперь подсвечивается «Волк», а «Крыса» — нет.
6. Создать вторую подсветку на `$weapon` с другим цветом.
7. Отправить строку содержащую оба слова — убедиться что оба подсвечены разными цветами.
