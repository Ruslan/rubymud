# 0.0.6.2 — Profile Export / Import

## Цель

Позволить делиться профайлами через `.tt` файлы на диске сервера.
Пользователь кладёт файлы в `config/`, импортирует через Settings UI.
Power user экспортирует свои профайлы туда же и передаёт папку новичку.

---

## Файловая система

Сервер работает с директорией `config/` рядом с БД (или задаётся флагом `--config-dir`).

```
config/
  Movement Base.tt
  Tanker.tt
  Healer.tt
  Hotkeys.tt
```

Файлы именуются `{profile_name}.tt`. Если имя содержит `/` или `..` — sanitize (заменить на `_`).

---

## Формат файла `.tt`

```
#nop Profile: Tanker
#nop Description: Full tank setup for RubyMUD
#nop rubymud:profile {"version":"1","exported":"2024-04-22T10:00:00Z"}

#alias {atk $1} {kill $1}
#alias {n} {north}
#alias {s} {south}

#nop rubymud:rule {"stop_after_match":true}
#action {You are stunned} {wield shield} combat
#action {$1 enters} {say hi $1} social

#nop rubymud:rule {"bold":true,"fg":"256:196","bg":"","blink":false,"reverse":false,"underline":false,"faint":false,"italic":false,"strikethrough":false}
#highlight {256:196} {You are attacked} combat
#nop rubymud:rule {"bold":false,"italic":true,"fg":"256:46","bg":""}
#highlight {256:46} {You feel better} default

#hotkey {f1} {north}
#hotkey {f2} {south}
#hotkey {f3} {look}
```

### Правила формата

**Заголовок (начало файла):**
- `#nop Profile: {name}` — имя профайла (обязательно, первая строка)
- `#nop Description: {text}` — описание (опционально)
- `#nop rubymud:profile {json}` — машиночитаемые метаданные профайла (version, exported)

**Метаданные правила:**
- `#nop rubymud:rule {json}` — применяется к **следующей** строке с правилом
- Парсер держит `pendingMeta`, сбрасывает после каждого поглощённого правила
- TinTin и другие клиенты игнорируют `#nop` — видят только стандартные строки

**Правила:**
| Строка | Формат |
|--------|--------|
| Alias | `#alias {name} {template}` |
| Trigger | `#action {pattern} {command} [{group}] [button]` |
| Highlight | `#highlight {colorspec} {pattern} [{group}]` |
| Hotkey | `#hotkey {shortcut} {command}` |

`colorspec` для highlights: `256:N` или `#RRGGBB`. Экспортируется `fg`, остальные атрибуты в `rubymud:rule`.

**Что попадает в `rubymud:rule`:**

| Тип | Поля в json |
|-----|-------------|
| trigger | `stop_after_match`, `is_button`, `name` (если ≠ pattern) |
| highlight | `bold`, `faint`, `italic`, `underline`, `strikethrough`, `blink`, `reverse`, `bg` |
| alias | — (ничего лишнего) |
| hotkey | — (ничего лишнего) |

**Разделитель профайлов** (для bulk-файла):
```
#nop ---
```
Одна пустая строка `#nop ---` между профайлами в общем файле `all-profiles.tt`.

---

## Реализация

### 1. Конфигурация сервера

**`go/internal/config/config.go`** — добавить `ConfigDir string`.

**`go/cmd/mudhost/main.go`** — флаг `--config-dir` (default: `./config` рядом с `--db`).
Создавать директорию при старте если не существует (`os.MkdirAll`).

**`go/internal/web/server.go`** — добавить `configDir string` в `Server`, передавать в `New()`.

---

### 2. Go: сериализатор и парсер

**Новый файл `go/internal/storage/profile_script.go`**

```go
type ProfileScript struct {
    Name        string
    Description string
    Aliases     []AliasRule
    Triggers    []TriggerRule
    Highlights  []HighlightRule
    Hotkeys     []HotkeyRule
}

// ExportProfileScript — читает профайл из БД, возвращает текст .tt
func (s *Store) ExportProfileScript(profileID int64) (string, error)

// ParseProfileScript — парсит текст .tt в ProfileScript
func ParseProfileScript(text string) (*ProfileScript, error)

// ImportProfileScript — транзакция: CreateProfile + вставка всех правил
func (s *Store) ImportProfileScript(ps *ProfileScript) (Profile, error)

// ExportAllProfileScripts — пишет config/{name}.tt для каждого профайла, возвращает список файлов
func (s *Store) ListProfileIDs() ([]int64, error)
```

**ExportProfileScript** — построчно:
1. `#nop Profile: {name}`
2. `#nop Description: {description}` (если не пустой)
3. `#nop rubymud:profile {"version":"1","exported":"{now}"}`
4. Пустая строка
5. Aliases по position: `#alias {name} {template}`
6. Пустая строка
7. Triggers: если trigger имеет non-default метаданные → `#nop rubymud:rule {...}` перед строкой
   `#action {pattern} {command} {group}[ button]`
8. Пустая строка
9. Highlights: всегда `#nop rubymud:rule {...}` (там всегда есть атрибуты)
   `#highlight {fg_colorspec} {pattern} {group}`
10. Пустая строка
11. Hotkeys: `#hotkey {shortcut} {command}`

**ParseProfileScript** — построчный парсер:
- Переиспользует `splitBraceArg` из `go/internal/vm/parse.go`
- Накапливает `pendingMeta map[string]any` при виде `#nop rubymud:rule {...}`
- При встрече правила: применяет pendingMeta, сбрасывает
- Позиции: счётчик per-type, начиная с 1

---

### 3. API endpoints

Добавить в `go/internal/web/server.go`:

```
GET  /api/profiles/{id}/export    → exportProfileToFile
POST /api/profiles/export/all     → exportAllProfilesToFiles  (один файл per профайл)
POST /api/profiles/import         → importProfileFromFile  (body: {"filename": "Tanker.tt"})
POST /api/profiles/import/all     → importAllProfilesFromFiles  (все .tt из config/)
GET  /api/profiles/files          → listProfileFiles  (список .tt файлов в config/)
```

**`exportProfileToFile`** — пишет `config/{name}.tt` на диск, возвращает `{"filename": "Tanker.tt"}`.

**`exportAllProfilesToFiles`** — для каждого профайла в БД пишет отдельный `config/{name}.tt`.
Возвращает список записанных файлов.

**`listProfileFiles`** — читает `config/*.tt`, возвращает `[{"filename": "Tanker.tt"}, ...]`.

**`importProfileFromFile`** — читает `config/{filename}`, парсит, создаёт профайл в БД.
Body: `{"filename": "Tanker.tt"}` (только имя файла, без path traversal — `filepath.Base`).

**`importAllProfilesFromFiles`** — читает все `config/*.tt`, импортирует каждый отдельно.

---

### 4. VM: команда `#hotkey`

**`go/internal/vm/commands_alias_variable.go`**

```go
func (v *VM) cmdHotkey(rest string) []Result {
    shortcut, rest := splitBraceArg(strings.TrimSpace(rest))
    command, _ := splitBraceArg(strings.TrimSpace(rest))
    if shortcut == "" || command == "" {
        return echoResults([]string{"#hotkey: usage: #hotkey {shortcut} {command}"})
    }
    pid := v.primaryProfileID()
    if pid == 0 {
        return echoResults([]string{"#hotkey: no primary profile"})
    }
    if _, err := v.store.CreateHotkey(pid, shortcut, command); err != nil {
        return echoResults([]string{fmt.Sprintf("#hotkey: error: %v", err)})
    }
    return echoResults([]string{fmt.Sprintf("#hotkey: %s -> %s", shortcut, command)})
}
```

Зарегистрировать в `go/internal/vm/commands.go`: `case "hotkey": return v.cmdHotkey(rest)`.

---

### 5. Settings UI

**Per-profile кнопки** (в списке профайлов):
- `Export` — `POST /api/profiles/{id}/export` → показывает путь к файлу
- `Delete` — уже есть

**Bulk секция** (вверху таба Profiles):
```
[ Export All to config/ ]   [ Import All from config/ ]
```

**Секция "Files on disk"** под списком профайлов (файлы из `GET /api/profiles/files`):
```
Files in config/
─────────────────────────────────
Tanker.tt          [ Import ]
Movement Base.tt   [ Import ]
Hotkeys.tt         [ Import ]
```

Каждый файл — одна кнопка Import. После импорта — `fetchData()` обновляет список профайлов.
Файлы в `config/` не удаляются автоматически — пользователь управляет ими вручную (гит, rsync, scp).

---

## Критические файлы

| Файл | Что меняется |
|------|-------------|
| `go/internal/storage/profile_script.go` | **Новый** — сериализатор + парсер |
| `go/internal/web/server.go` | 6 новых handlers + configDir field |
| `go/cmd/mudhost/main.go` | флаг `--config-dir`, os.MkdirAll, передача в web.New() |
| `go/internal/config/config.go` | поле ConfigDir (опционально) |
| `go/internal/vm/commands.go` | регистрация `#hotkey` |
| `go/internal/vm/commands_alias_variable.go` | cmdHotkey |
| `ui/src/SettingsApp.svelte` | Export/Import buttons, Files list секция |

Переиспользовать: `go/internal/vm/parse.go:splitBraceArg`.

---

## Verification

1. `go build ./...` без ошибок
2. Создать профайл с алиасами, триггерами (stop_after_match=true), хайлайтами (bold=true), хоткеями
3. Export → появляется `config/ProfileName.tt`; открыть в редакторе — читаемо, `rubymud:rule` есть
4. Удалить профайл из БД
5. Settings → Files → Import `ProfileName.tt` → профайл восстановился с полными атрибутами (bold=true сохранился)
6. Export All → `config/*.tt` для каждого профайла
7. Import All → все профайлы восстановились
8. `#hotkey {f5} {north}` в game UI → появляется в профайле
9. Экспортировать профайл с `#hotkey` → строки `#hotkey` есть в файле, после Import All работает
10. TinTin совместимость: загрузить `.tt` файл в TinTin++ — `#alias` и `#action` подхватились, `#nop` проигнорированы
