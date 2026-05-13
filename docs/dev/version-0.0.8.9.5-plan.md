# Предкомпиляция regex для VM rules

## Контекст

Исследование latency показало, что текущий lag на массовом выводе почти не связан с SQLite.

Замер для команды `дост все`:

```text
[latency] ws_flush entries=90 lines=90 bytes=11247 oldest_to_ws=471.122104ms line_total_sum=470.93173ms parse_sum=245.936µs vm_sum=345.382992ms vm_gag_sum=78.557266ms vm_triggers_sum=162.195414ms vm_subs_sum=104.630312ms db_main_sum=38.391404ms db_extra_sum=0s db_total_sum=38.391404ms effects_sum=10.158µs highlight_sum=86.767664ms ws_queue_sum=43.323µs max_line=9.530899ms max_db=4.993161ms ws_send=121.79µs
```

Вывод:
- MUD и websocket быстрые.
- SQLite даёт около `38-40ms` на 90 строк.
- Основной тормоз: VM rule processing и highlights.
- Самые дорогие фазы: triggers, substitutions, gags, highlights.

Текущий hot path компилирует regex слишком часто:
- `MatchTriggers` компилирует trigger pattern на каждую строку и каждое правило.
- `ApplyHighlights` компилирует highlight pattern на каждую строку и каждое правило.
- `CheckGag` и `ApplySubsAndCollectOverlays` компилируют effective pattern на каждую строку и каждое правило.
- `ensureFresh()` сейчас может делать reload из storage в hot path, что маскируется внутри VM/highlight timing.

Есть ещё возможные hot spots, но их нужно отделять по уверенности:
- `ensureFresh()` сейчас вызывается из `CheckGag`, `MatchTriggers`, `ApplySubsAndCollectOverlays`, `ApplyHighlights`, то есть до 4 reload на входящую строку. Для 90 строк это до 360 reload. Это точно лишняя работа и её нужно убрать из hot path.
- `expandTriggerCommand` компилирует regex `%N` capture refs при каждом matched trigger. Это маленькая, но очевидная правка: сделать package-level compiled regex.
- `plainRangeToRawRange` и `activeANSIAt` в highlight path сканируют строку с начала для каждого match. Это потенциальный O(matches * line length) участок. Его стоит оптимизировать только если после regex/reload fixes `highlight_sum` остаётся заметным.
- String concatenation в substitutions создаёт новые строки на каждый match. Это потенциально дорого при большом числе replacements, но текущий замер не доказывает, что это главный bottleneck.

## Цель

Убрать regex compilation и storage reload из hot path обработки входящих строк.

Ожидаемый новый hot path:

```text
line -> precompiled gags -> precompiled triggers -> precompiled/cached subs -> DB append -> precompiled highlights -> websocket
```

Без изменения пользовательской семантики:
- те же regexp patterns
- тот же порядок правил
- те же enabled/group/profile semantics
- те же переменные в `#sub/#gag` patterns
- тот же behavior для invalid regex: правило пропускается

## Scope MVP

Оптимизируем:
- `#action/#act` trigger matching
- `#highlight/#high` highlighting
- `#sub/#substitute` substitutions
- `#gag` hidden substitutions
- VM freshness/reload boundary, чтобы compiled snapshots реально использовались
- repeated capture-ref regex compilation in matched triggers

Не делаем в этой версии:
- async DB writer
- `seq` identity model для live logs
- изменение storage schema
- изменение frontend rendering
- изменение regexp syntax или compatibility

## Design

### Compiled rule snapshots

Добавить compiled-представления правил внутри VM.

Примерная форма:

```go
type compiledTriggerRule struct {
    Rule storage.TriggerRule
    Re   *regexp.Regexp
    Err  error
}

type compiledHighlightRule struct {
    Rule storage.HighlightRule
    Re   *regexp.Regexp
    ANSI string
    Err  error
}

type compiledSubstituteRule struct {
    Rule                 storage.SubstituteRule
    PatternHasVars       bool
    StaticEffectivePattern string
    StaticRe             *regexp.Regexp
    Err                  error
}
```

VM fields:

```go
type VM struct {
    // existing fields...
    compiledTriggers   []compiledTriggerRule
    compiledHighlights []compiledHighlightRule
    compiledGags       []compiledSubstituteRule
    compiledSubs       []compiledSubstituteRule
    dynamicRegexCache  map[string]*regexp.Regexp
    dynamicRegexErrors map[string]error
}
```

`dynamicRegexCache` нужен для `#sub/#gag` patterns с `$variables`, потому что effective pattern зависит от текущих variable values.

### Static vs dynamic patterns

Static pattern:
- не содержит `$var`
- компилируется один раз при `VM.Reload()` или runtime rule mutation
- используется напрямую в hot path

Dynamic pattern:
- содержит `$var`
- effective pattern строится через `substitutePatternVars`
- compiled regex берётся из cache по ключу effective pattern
- если variable value изменился, effective pattern изменится и получит новый cache entry
- cache очищается при reload и при variable mutations, чтобы не держать бесконечные старые значения

Ключ cache может быть:

```text
sub:<rule_id>:<effective_pattern>
gag:<rule_id>:<effective_pattern>
```

Для runtime rules без stable DB id можно включать pattern/group/index, но проще использовать сам effective pattern как часть ключа. Одинаковые effective patterns могут шарить compiled regex.

### Highlight optimization

`highlightToANSI(h)` тоже сейчас пересчитывается при каждом match. Для highlight compiled snapshot надо сохранить:
- compiled regex
- ANSI prefix string

Hot path `ApplyHighlights` должен только:
- пройти по compiled highlights
- найти matches
- вставить заранее подготовленный ANSI

### Trigger optimization

`MatchTriggers` должен итерироваться по `compiledTriggers`.

Invalid trigger regex:
- логировать при compile/reload, а не на каждую строку
- пропускать правило в hot path

### Gag/sub optimization

`CheckGag` и `ApplySubsAndCollectOverlays` должны итерироваться по `compiledGags`/`compiledSubs`.

Для static rules:
- использовать `StaticRe`
- `EffectivePattern` в overlay payload равен `StaticEffectivePattern`

Для dynamic rules:
- вычислить effective pattern
- получить compiled regex через `cachedEffectiveRegex`
- сохранить actual `EffectivePattern` в overlay payload, как сейчас

Replacement в `#sub` остаётся dynamic:
- `$vars` в replacement раскрываются при применении правила
- `%0..%N` capture refs раскрываются при match
- это не часть regex compilation optimization

## Freshness model

Compiled regex не даст полного эффекта, если `ensureFresh()` продолжит reload из storage на каждую строку.

Это не speculative optimization. Сейчас reload happens inside the measured `vm_*`/`highlight` buckets, потому что все rule-processing functions начинают с `ensureFresh()`. Даже если SQLite отвечает быстро, повторная загрузка и merge rules/variables 4 раза на строку напрямую конкурируют с regex work.

Нужно заменить hot-path reload на explicit freshness:
- `VM.Reload()` вызывается при старте session
- `VM.Reload()` вызывается после runtime command mutations, которые меняют rules/profiles/settings
- `VM.Reload()` вызывается по `settings.changed`/store notification, если правило изменилось через UI
- `MatchTriggers`, `ApplyHighlights`, `CheckGag`, `ApplySubsAndCollectOverlays` не должны делать storage reload на каждую строку

Варианты реализации:
- убрать `ensureFresh()` из hot path и вызывать reload в точках mutation
- или сделать `ensureFresh()` cheap через dirty/version flag

MVP recommendation: dirty/version flag.

Пример:

```go
type VM struct {
    loaded bool
    dirty  atomic.Bool
}

func (v *VM) MarkDirty()
func (v *VM) ensureFresh() {
    if !v.loaded || v.dirty.Swap(false) {
        _ = v.Reload()
    }
}
```

Но `ensureFresh()` не должен обращаться к DB, если VM не dirty.

## Cache invalidation

Rebuild compiled snapshots on:
- `VM.Reload()`
- rule add/delete/update via runtime commands
- profile order changes
- settings changed for triggers/highlights/substitutes/gags

Clear `dynamicRegexCache` on:
- `VM.Reload()`
- `#variable/#var`
- `#unvariable/#unvar`
- any operation that replaces `v.variables`

Why clear on variable change:
- dynamic effective patterns may include variable values
- cache could otherwise grow with every changing target/tank variable
- clearing is cheap compared with per-line compile

## Confidence-ranked fixes

### P0: required fixes

- Make `ensureFresh()` cheap or remove it from hot path. This is required for compiled snapshots to matter.
- Precompile static trigger/highlight/sub/gag regex rules.
- Cache compiled effective regex for dynamic `$variable` sub/gag patterns.
- Precompute highlight ANSI strings.
- Move trigger capture-ref regex (`%(\d+)`) to a package-level compiled variable.

These are high-confidence because the current code repeats the work per line/per rule and the measured slow buckets are exactly the functions containing that work.

### P1: verify after P0

- Optimize highlight raw/plain offset mapping if `highlight_sum` remains high.
- Current implementation calls `plainRangeToRawRange` and `activeANSIAt` per match, and both scan from the start of the string.
- Better approach if needed: build a per-line plain-to-raw offset map and ANSI state checkpoints once, then apply all highlight inserts using those maps.

This is likely useful for lines with many highlight matches or lots of ANSI, but less certain than P0 because the current measurement does not split highlight compile time from offset mapping time.

### P2: only if benchmarks show it

- Rewrite substitution replacement application to avoid repeated full-string copies.
- This matters only when many substitution matches happen on the same line.
- Current `vm_subs_sum` is more likely dominated by reload + regex compile, so defer this until after P0 measurement.

## Implementation steps

1. Add temporary counters/timing around `ensureFresh()`/`Reload()` if needed to confirm reload share inside `vm_*` buckets.
2. Make `ensureFresh()` cheap; no unconditional DB reload per line.
3. Add compiled rule types and cache fields to `go/internal/vm`.
4. Add `rebuildCompiledRules()` called from `VM.Reload()` and runtime rule mutation paths.
5. Add `cachedEffectiveRegex(kind, ruleID, effectivePattern)` helper.
6. Update `MatchTriggers` to use compiled triggers.
7. Move trigger capture-ref regex to package-level compiled variable.
8. Update `ApplyHighlights` to use compiled highlights and precomputed ANSI.
9. Update `CheckGag` to use compiled/static or cached dynamic regex.
10. Update `ApplySubsAndCollectOverlays` to use compiled/static or cached dynamic regex.
11. Re-measure the same workload.
12. Only if `highlight_sum` remains high, optimize highlight offset/ANSI-state mapping.
13. Only if `vm_subs_sum` remains high with many actual substitutions, optimize substitution string rebuilding.
14. Keep existing latency instrumentation until the optimization is validated.
15. Remove or gate diagnostic latency logs after validation if they become noisy.

## Tests

### Correctness

- Trigger still fires for static pattern.
- Trigger invalid regex is skipped and does not spam logs per line.
- Highlight still applies static regex.
- `#gag` still hides matching lines.
- `#sub` still creates substitution overlays and preserves original canonical log.
- Rule order remains unchanged.
- Disabled rules remain ignored.

### Dynamic variables

- `#sub {$target} {...}` uses current `$target` value.
- Changing `$target` invalidates dynamic regex cache or produces a new effective compiled regex.
- `#gag {$spam}` reacts to variable changes.
- Missing variables preserve current behavior through `substitutePatternVars`.

### Reload/freshness

- Adding trigger through runtime command makes it active without restart.
- Deleting/updating highlight through storage/UI makes old compiled regex disappear after settings change.
- `ensureFresh()` does not hit storage for every line when VM is not dirty.

### Performance

- Add benchmark for 90 lines with representative trigger/sub/gag/highlight rule counts.
- Compare before/after for:
  - `vm_gag_sum`
  - `vm_triggers_sum`
  - `vm_subs_sum`
  - `highlight_sum`
- If possible, split one validation run into:
  - `reload_sum` / reload count
  - regex compile count/time
  - highlight match application time after compiled regex
- Target: reduce VM+highlight time from `~432ms` for 90 lines to below `100ms` on the measured workload.

## Observability

Keep split latency logs while validating:

```text
vm_gag_sum=...
vm_triggers_sum=...
vm_subs_sum=...
highlight_sum=...
```

Success criteria:
- `db_total_sum` may remain around `40ms/90 lines`
- `vm_triggers_sum`, `vm_subs_sum`, `vm_gag_sum`, and `highlight_sum` should drop substantially
- `ws_send` should remain near zero

## Risks

- Stale compiled rules if dirty/reload hooks miss a mutation path.
- Dynamic variable patterns can be wrong if cache key ignores effective pattern.
- Invalid regex errors may become less visible if logged only on reload; tests and UI listing should still expose bad rules eventually.
- Removing unconditional `ensureFresh()` may reveal places that relied on reload-by-accident.
- Optimizing highlight offset mapping before P0 measurement could add complexity without visible benefit.
- Rewriting substitution string construction before P0 measurement could add risk without addressing the current dominant bottleneck.

## Rollout

1. Land compiled regex snapshots with tests.
2. Run the same `дост все` benchmark and compare split latency logs.
3. If `highlight_sum` remains high, optimize highlight offset mapping.
4. If VM/highlight drops but oldest line still waits for a large batch, add batch flush threshold by line count/time in a follow-up.
5. Keep async DB roadmap deferred unless DB time becomes dominant in other workloads.
