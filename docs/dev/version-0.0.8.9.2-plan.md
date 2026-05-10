# Highlights UI: сброс цвета + реестр именованных цветов

## Контекст

Сейчас цвета в highlights хранятся как TEXT в `fg`/`bg`, но без единого формата:
- `"red"` — если создан через `#highlight` команду
- `"256:196"` — если создан через UI color picker
- `"#ff0000"` — если вписан вручную или импортирован
- `""` — default/сброс цвета

Они не конвертируются друг в друга:
- UI не отображает именованные цвета, потому что не знает их палитру
- экспорт профилей может выдавать `256:196` вместо читабельного `"red"`
- нет способа сбросить `fg`/`bg` в default через UI
- multi-word цвета (`light red`, `light cyan`) ломаются в VM-команде `#highlight`

## Цели

1. **Центральный реестр цветов** — единый источник правды для Go backend и frontend API.
2. **Сброс цвета** — кнопка в UI для возврата `fg`/`bg` в `""`.
3. **Именованные цвета в UI** — select с именами рядом с color picker.
4. **Корректный экспорт/импорт** — profile script export по возможности пишет имена, а не `256:N`/hex.
5. **Без миграций БД** — существующие значения остаются валидными.

## Архитектура

Создаётся пакет `go/internal/colorreg/`.

Почему не `go/internal/vm/colorreg/`: реестр нужен сразу трём пакетам (`vm`, `storage`, `web`). Если положить его под `vm`, `storage` начнёт зависеть от VM-слоя, что неверно архитектурно.

Frontend получает тот же реестр через `GET /api/colors` и не хранит отдельную копию палитры.

---

## 1. Новый пакет `colorreg`

**Файл**: `go/internal/colorreg/colorreg.go`

```go
type NamedColor struct {
    Name    string `json:"name"`     // "red", "light cyan", ...
    Hex     string `json:"hex"`      // "#aa0000"
    AnsiFg  int    `json:"ansi_fg"`  // SGR код 31
    AnsiBg  int    `json:"ansi_bg"`  // SGR код 41
    Ansi256 int    `json:"ansi256"`  // ближайший/предпочтительный 256-индекс
}
```

### Канонические цвета для `All()` и UI select

| Имя | Hex | ANSI FG | ANSI BG | 256-idx |
|-----|-----|---------|---------|---------|
| black | `#000000` | 30 | 40 | 16 |
| red | `#aa0000` | 31 | 41 | 124 |
| green | `#00aa00` | 32 | 42 | 28 |
| brown | `#aaaa00` | 33 | 43 | 142 |
| blue | `#0000aa` | 34 | 44 | 20 |
| magenta | `#aa00aa` | 35 | 45 | 127 |
| cyan | `#00aaaa` | 36 | 46 | 37 |
| white | `#aaaaaa` | 37 | 47 | 248 |
| light red | `#ff5555` | 91 | 101 | 203 |
| light green | `#55ff55` | 92 | 102 | 83 |
| light yellow | `#ffff55` | 93 | 103 | 227 |
| light blue | `#5555ff` | 94 | 104 | 63 |
| light magenta | `#ff55ff` | 95 | 105 | 207 |
| light cyan | `#55ffff` | 96 | 106 | 87 |
| light white | `#ffffff` | 97 | 107 | 231 |

### Алиасы

Алиасы не показываются в UI select как отдельные варианты, но принимаются при парсинге и применении highlights:
- `coal` -> `black`
- `charcoal` -> `black`
- `yellow` -> `brown`
- `gray` -> `white`
- `grey` -> `white`
- `purple` -> `magenta`
- `light brown` -> `brown`

Важно: текущий `highlight_ansi.go` уже поддерживает `light brown`; не потерять это поведение.

### Reverse lookup policy

`NameFor256()` и `NameForHex()` используются для человекочитаемого экспорта. Они должны быть предсказуемыми.

Правило:
- сначала exact lookup по `Ansi256`/`Hex`
- затем explicit popular lookup для значений, которые UI часто создаёт как ближайшие к именованным цветам
- если совпадения нет, возвращать `"", false` и сохранять исходное значение

Минимальный explicit lookup:
- `256:196` -> `red`
- `256:46` -> `green`
- `256:21` -> `blue`
- `256:201` -> `magenta`
- `256:51` -> `cyan`
- `256:226` -> `brown` (каноническое имя; `yellow` остаётся alias)
- `#ff0000` -> `red`
- `#00ff00` -> `green`
- `#0000ff` -> `blue`

Если сомневаемся между ANSI dark/light variant, лучше оставить исходное `256:N`, чем делать неверное имя.

### Публичные функции

- `All() []NamedColor` — только канонические цвета для API/UI.
- `CanonicalName(input string) (string, bool)` — trim/lowercase + alias resolution, `"COAL" -> "black"`.
- `LookupName(input string) (NamedColor, bool)` — canonical/alias name -> color.
- `NameToHex(input string) (string, bool)`.
- `NameToAnsiFg(input string) (string, bool)`.
- `NameToAnsiBg(input string) (string, bool)`.
- `NameFor256(index int) (string, bool)` — reverse lookup для экспорта.
- `NameForHex(hex string) (string, bool)` — reverse lookup для экспорта.
- `NormalizeExportColor(value string) string` — `"256:196" -> "red"`, `"#ff0000" -> "red"`, неизвестное значение возвращается как есть.
- `NormalizeStoredColor(value string) string` — trim/lowercase, `default -> ""`, known aliases -> canonical name, unknown `256:N`/hex/rgb остаются как есть.

**Тесты**: `go/internal/colorreg/colorreg_test.go`

Покрыть:
- canonical aliases: `coal`, `charcoal`, `grey`, `gray`, `purple`, `light brown`
- `default` не является цветом, но `NormalizeStoredColor("default") == ""`
- `NameFor256(196) == "red"`
- `NameForHex("#ff0000") == "red"`
- unknown значения не превращаются в случайные имена

---

## 2. Изменения в VM

### `go/internal/vm/highlight_format.go`

**`parseColorSpec()`**:
- Нормализовать через `colorreg.NormalizeStoredColor()`.
- Поддержать `default` как сброс FG: `#highlight {default} {pattern}` -> `FG=""`.
- Исправить multi-word имена для FG: `light red`, `light cyan`, `light brown`.
- Исправить multi-word имена для BG: `b light red`, `b light blue`.
- Проверять multi-word только если комбинация `currentToken + " " + nextToken` является известным именем/алиасом.

**`formatColorSpec()`**:
- Для VM-вывода `#highlight` использовать `colorreg.NormalizeExportColor()` для `FG` и `BG`.
- Если `FG == ""`, не добавлять `default` в VM list output, чтобы не менять текущий смысл пустого цвета внутри сложного color spec.

### `go/internal/vm/highlight_ansi.go`

- Убрать inline `fgMap`/`bgMap`.
- Использовать `colorreg.NameToAnsiFg()` и `colorreg.NameToAnsiBg()`.
- Сохранить поддержку hex, `rgb...` и `256:N` как fallback.

### Тесты VM

Добавить/обновить тесты в `go/internal/vm/highlight_test.go` или отдельном файле:
- `parseColorSpec("light red bold")` -> `FG="light red"`, `Bold=true`
- `parseColorSpec("light red b light blue")` -> `FG="light red"`, `BG="light blue"`
- `parseColorSpec("default b default")` -> `FG=""`, `BG=""`
- `highlightToANSI()` применяет alias `light brown` без регрессии

---

## 3. Изменения в storage export/import

Важно: экспорт/импорт `.tt` профилей сейчас не использует `vm/formatColorSpec()` и `vm/parseColorSpec()`.

### `go/internal/storage/profile_script.go`

**ExportProfileScript()**:
- При записи `#highlight` нормализовать FG через `colorreg.NormalizeExportColor()`.
- Если FG пустой, писать `default`, как сейчас.
- При записи meta поля `"bg"` нормализовать BG через `colorreg.NormalizeExportColor()`.
- Не менять unknown/custom цвета: `#123456`, `256:17`, `rgb...` остаются как есть, если нет уверенного reverse lookup.

**ParseProfileScript()**:
- После чтения FG из `#highlight {fg} {pattern}` применять `colorreg.NormalizeStoredColor()`.
- После чтения meta `"bg"` применять `colorreg.NormalizeStoredColor()`.
- `default` должен продолжать импортироваться как `""`.
- Legacy import `#highlight {red} {pattern}` должен сохранять `FG="red"`.
- Multi-word имена в profile scripts уже безопасны, потому что читаются через brace args: `#highlight {light red} {pattern}`.

### Тесты storage

Добавить/обновить `go/internal/storage/profile_script_test.go`:
- экспорт `FG="256:196"` пишет `#highlight {red}`
- экспорт `BG="256:21"` в meta пишет имя, если reverse lookup настроен
- импорт `#highlight {default}` сохраняет `FG=""`
- импорт `#highlight {COAL}` сохраняет canonical `FG="black"`
- round-trip не ломает unknown custom цвета

---

## 4. Изменения в web API

### `go/internal/web/server.go`

Добавить route внутри `/api`:

```go
r.Get("/colors", s.listColors)
```

Handler:

```go
func (s *Server) listColors(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(colorreg.All())
}
```

Контракт ответа:

```json
[
  {"name":"red","hex":"#aa0000","ansi_fg":31,"ansi_bg":41,"ansi256":124}
]
```

Важно: endpoint находится под текущей `/api` token middleware. Frontend должен вызывать его с `X-Session-Token`, как остальные API-запросы.

### Тесты web

Добавить тест в `go/internal/web/api_test.go`:
- `GET /api/colors` без token -> `401`
- `GET /api/colors` с token -> `200`
- JSON содержит lowercase поля `name`, `hex`, `ansi_fg`, `ansi_bg`, `ansi256`
- список содержит `red` и `light cyan`

---

## 5. Изменения во frontend

**Файл**: `ui/src/SettingsApp.svelte`

### a) Типы и загрузка реестра

Добавить тип:

```ts
interface NamedColor {
  name: string;
  hex: string;
  ansi_fg: number;
  ansi_bg: number;
  ansi256: number;
}
```

Добавить state:

```ts
let namedColorList: NamedColor[] = [];
```

В `fetchData()` грузить `/api/colors` с тем же `headers`, желательно в первом `Promise.all` вместе с sessions/profiles:

```ts
const [sessRes, profRes, colorsRes] = await Promise.all([
  fetch('/api/sessions', { headers }),
  fetch('/api/profiles', { headers }),
  fetch('/api/colors', { headers })
]);
```

Если `/api/colors` не загрузился, не ломать страницу: оставить `namedColorList = []` и продолжить старый fallback.

### b) Отображение цвета

`colorValueToHex()`:
- trim/lowercase
- если пусто, вернуть `#cccccc`
- сначала искать именованный цвет/alias среди `namedColorList` или через простой local lookup по `name`
- затем parse hex
- затем `256:N`
- затем fallback `#cccccc`

На frontend alias lookup можно не дублировать полностью, если backend отдаёт только canonical names. Тогда UI корректно покажет canonical значения из новых данных, а legacy aliases будут fallback до следующего сохранения. Если хотим красивое отображение старых alias без backend roundtrip, добавить минимальную alias map: `grey/gray -> white`, `purple -> magenta`, `coal/charcoal -> black`, `yellow/light brown -> brown`.

### c) Select с именами

Рядом с `<input type="color">` добавить `<select>` для FG и BG.

Требования:
- `<option value="">default</option>`
- options из `namedColorList`
- при выборе имени писать строку напрямую в `highlightEditor.fg/bg`
- color picker продолжает писать `256:N`
- если текущее значение custom (`256:N`, `#hex`, unknown), select не должен визуально притворяться default

Для custom значения использовать computed value:

```ts
function namedColorSelectValue(value: string): string {
  const normalized = value.trim().toLowerCase();
  if (!normalized) return '';
  return namedColorList.some((c) => c.name === normalized) ? normalized : '__custom__';
}
```

И option:

```svelte
{#if namedColorSelectValue(highlightEditor.fg) === '__custom__'}
  <option value="__custom__">custom</option>
{/if}
```

При `change` игнорировать `__custom__`, остальные значения писать в field.

### d) Кнопка сброса

Рядом с select/picker добавить кнопку:

```svelte
<button type="button" class="btn-icon" aria-label="Reset foreground color" on:click={() => highlightEditor = { ...highlightEditor, fg: '' }}>Reset</button>
```

Для BG аналогично.

### e) CSS и mobile

- Расширить `.color-field`, чтобы select, picker, reset и `<code>` не ломали строку.
- Добавить стиль для color select.
- На mobile разрешить `.form-row`/`.color-field` wrap, чтобы controls не вылезали за экран.

---

## 6. Сводка файлов

| Файл | Действие |
|---|---|
| `go/internal/colorreg/colorreg.go` | **Новый** — реестр цветов, aliases, normalize/reverse lookup |
| `go/internal/colorreg/colorreg_test.go` | **Новый** — тесты реестра |
| `go/internal/vm/highlight_format.go` | `parseColorSpec` multi-word/default/canonical; `formatColorSpec` export normalization |
| `go/internal/vm/highlight_ansi.go` | заменить inline maps на `colorreg` |
| `go/internal/vm/highlight_test.go` | тесты multi-word/default/aliases |
| `go/internal/storage/profile_script.go` | export/import normalization для FG/BG |
| `go/internal/storage/profile_script_test.go` | тесты export/import цветов |
| `go/internal/web/server.go` | `GET /api/colors` + handler |
| `go/internal/web/api_test.go` | тест API контракта `/api/colors` |
| `ui/src/SettingsApp.svelte` | fetch реестра, select, reset button, `colorValueToHex`, mobile CSS |

Миграций БД не требуется. Старые записи с `fg: "256:196"` остаются валидными, в UI отображаются через `colorValueToHex`, а при экспорте по возможности превращаются в именованные цвета.

---

## 7. Acceptance checklist

- `#highlight {light red} {danger}` сохраняет `FG="light red"`, а не `"light"`.
- `#highlight {light red b light blue bold} {danger}` сохраняет `FG="light red"`, `BG="light blue"`, `Bold=true`.
- `#highlight {default} {danger}` сбрасывает FG.
- UI позволяет выбрать `red` и сохранить именно `"red"`, не `"256:N"`.
- UI позволяет сбросить FG/BG в `""`.
- UI корректно показывает существующие `red`, `light cyan`, `256:196`, `#ff0000`.
- Export profile пишет `#highlight {red}` для `FG="256:196"`, если reverse lookup настроен.
- Unknown custom colors не теряются и не превращаются в неверные имена.
- `GET /api/colors` работает только с token и отдаёт JSON в lowercase field format.

## 8. Проверка

Запустить:

```sh
make test
```

```sh
npm run build
```

`npm run build` запускать из `ui/`.
