# Profiles Feature Plan

## Context

Rules (aliases, triggers, highlights) и hotkeys сейчас привязаны к session_id.
Это не позволяет переиспользовать одни и те же правила в разных сессиях.

**Профайл** — именованный набор aliases + triggers + highlights + hotkeys.
Сессия имеет упорядоченный список активных профайлов. VM при загрузке мёрджит правила из всех профайлов сессии в порядке приоритета (меньший order_index = выше приоритет).

Export/Import профайлов на диск — **следующий минорный релиз**.

---

## Schema

### Новые таблицы (types.go + open.go AutoMigrate)

```go
type Profile struct {
    ID          int64      `gorm:"primaryKey" json:"id"`
    Name        string     `json:"name"`
    Description string     `json:"description"`
    CreatedAt   SQLiteTime `json:"created_at"`
}

type SessionProfile struct {
    ID         int64 `gorm:"primaryKey"`
    SessionID  int64 `json:"session_id"`
    ProfileID  int64 `json:"profile_id"`
    OrderIndex int   `json:"order_index"`
    // UNIQUE(session_id, profile_id) — добавить через uniqueIndex тег
}

type HotkeyRule struct {
    ID        int64  `gorm:"primaryKey" json:"id"`
    ProfileID int64  `json:"profile_id"`
    Shortcut  string `json:"shortcut"`
    Command   string `json:"command"`
}
```

### Изменения существующих таблиц

Добавить поле `ProfileID int64 json:"profile_id"` к:
- `AliasRule` (alias.go)
- `TriggerRule` (trigger.go)
- `HighlightRule` (highlight.go)

`SessionID` в этих таблицах **оставить** (SQLite + AutoMigrate не умеют drop column).
После миграции `session_id` не используется для lookups — только `profile_id`.

---

## Миграция данных (open.go)

После AutoMigrate — запустить `MigrateToProfiles(db, hotkeys []config.Hotkey)`:

```
Если profiles таблица пуста:
  Для каждой session:
    1. Создать Profile{Name: session.Name}
    2. UPDATE alias_rules SET profile_id=? WHERE session_id=? AND profile_id=0
    3. UPDATE trigger_rules SET profile_id=? WHERE session_id=? AND profile_id=0
    4. UPDATE highlight_rules SET profile_id=? WHERE session_id=? AND profile_id=0
    5. INSERT INTO session_profiles(session_id, profile_id, order_index=0)
  Если hotkeys не пусты:
    Создать Profile{Name: "Hotkeys"}
    INSERT hotkey_rules для каждого hotkey
    INSERT session_profiles для каждой session (order_index=1)
```

---

## Storage Layer

### Новые файлы

**go/internal/storage/profile.go**
```go
ListProfiles() ([]Profile, error)
CreateProfile(name, description string) (Profile, error)
GetProfile(id int64) (*Profile, error)
UpdateProfile(p Profile) error
DeleteProfile(id int64) error

GetSessionProfiles(sessionID int64) ([]SessionProfileEntry, error) // с именем профайла
GetOrderedProfileIDs(sessionID int64) ([]int64, error)
GetPrimaryProfileID(sessionID int64) (int64, error)  // order_index=0, для VM #-команд
AddProfileToSession(sessionID, profileID int64, orderIndex int) error
RemoveProfileFromSession(sessionID, profileID int64) error
ReorderSessionProfiles(sessionID int64, ordered []int64) error
GetSessionIDsForProfile(profileID int64) ([]int64, error)  // для NotifySettingsChanged
```

**go/internal/storage/hotkey.go**
```go
ListHotkeys(profileID int64) ([]HotkeyRule, error)
CreateHotkey(profileID int64, shortcut, command string) (HotkeyRule, error)
UpdateHotkey(h HotkeyRule) error           // WHERE id AND profile_id
DeleteHotkeyByID(id, profileID int64) error
LoadHotkeysForProfiles(profileIDs []int64) ([]HotkeyRule, error)
```

### Изменения существующих файлов

**alias.go, trigger.go, highlight.go** — добавить:
```go
LoadAliasesForProfiles(profileIDs []int64) ([]AliasRule, error)
LoadTriggersForProfiles(profileIDs []int64) ([]TriggerRule, error)
LoadHighlightsForProfiles(profileIDs []int64) ([]HighlightRule, error)
```

Эти функции: `WHERE profile_id IN (?) AND enabled = true`, затем сортировка в Go
по позиции profileID в переданном слайсе (порядок приоритета).

Существующие `ListAliases(sessionID)` и др. — переключить на profileID:
```go
func (s *Store) ListAliases(profileID int64) ([]AliasRule, error) {
    // WHERE profile_id = ?
}
```

**group.go** — `ListRuleGroups(profileID)` и `SetGroupEnabled(profileID, ...)`.

---

## VM (go/internal/vm/runtime.go)

`Reload()` меняется:
```go
func (v *VM) Reload() error {
    profileIDs, err := v.store.GetOrderedProfileIDs(v.sessionID)
    // ...
    v.aliases, _ = v.store.LoadAliasesForProfiles(profileIDs)
    v.triggers, _ = v.store.LoadTriggersForProfiles(profileIDs)
    v.highlights, _ = v.store.LoadHighlightsForProfiles(profileIDs)
    v.variables, _ = v.store.LoadVariables(v.sessionID)  // без изменений
}
```

Семантика мёржа:
- **Aliases**: первое совпадение по имени побеждает (профайл с меньшим order_index = выше приоритет)
- **Triggers**: все срабатывают в порядке профайлов, внутри профайла по id
- **Highlights**: все применяются в порядке профайлов

VM-команды `#alias`, `#action`, `#highlight` и др. пишут в primary profile сессии
(order_index=0). Storage: `GetPrimaryProfileID(sessionID int64) (int64, error)`.

---

## Server (go/internal/web/server.go)

### Убрать

- Поле `hotkeys []config.Hotkey` из `Server` struct и параметр из `New()`
- Старые session-scoped rule routes: `/api/sessions/{id}/aliases`, `/triggers`, `/highlights`, `/groups`

### Добавить маршруты

```
/api/profiles
  GET  /                        → listProfiles
  POST /                        → createProfile

/api/profiles/{profileID}
  PUT    /                      → updateProfile
  DELETE /                      → deleteProfile
  GET    /aliases               → listProfileAliases
  POST   /aliases               → createProfileAlias
  PUT    /aliases/{id}          → updateProfileAlias
  DELETE /aliases/{id}          → deleteProfileAlias
  ...same for /triggers, /highlights, /hotkeys
  GET    /groups                → listProfileGroups
  POST   /groups/toggle         → toggleProfileGroup

/api/sessions/{sessionID}/profiles
  GET    /                      → listSessionProfiles
  POST   /                      → addProfileToSession  (body: {profile_id, order_index})
  DELETE /{profileID}           → removeProfileFromSession
  PUT    /reorder               → reorderSessionProfiles (body: [{profile_id, order_index}])
```

### sendRestoreState

Заменить `s.hotkeys` на загрузку из store:
```go
profileIDs, _ := s.store.GetOrderedProfileIDs(sess.ID())
hotkeys, _ := s.store.LoadHotkeysForProfiles(profileIDs)
// конвертировать в session.HotkeyJSON
```

### NotifySettingsChanged

В profile rule handlers — получить все затронутые сессии:
```go
sessionIDs, _ := s.store.GetSessionIDsForProfile(profileID)
for _, id := range sessionIDs {
    if sess, ok := s.manager.GetSession(id); ok {
        sess.NotifySettingsChanged(domain)
    }
}
```

---

## Settings UI (ui/src/SettingsApp.svelte)

### Изменение rule-табов

Rule-табы (Aliases, Triggers, Highlights, Groups) плюс новый Hotkeys:
- Session selector → **Profile selector**
- `fetchData`: `GET /api/profiles/{selectedProfileID}/aliases` etc.
- `saveItem`: `POST/PUT /api/profiles/{selectedProfileID}/aliases` etc.
- `deleteItem`: `DELETE /api/profiles/{selectedProfileID}/aliases/{id}`

### Новый таб "Profiles"

```
{ id: 'profiles', label: 'Profiles' }
```

Список всех профайлов: Name, Description, кнопки Edit (инлайн) / Delete.
Форма создания нового профайла (name + description).

### Sessions таб — секция "Active Profiles"

Для выбранной сессии:
- Список активных профайлов в порядке (order_index), с кнопками ↑ ↓ Remove
- Кнопка "Add Profile" → `<select>` из всех профайлов + кнопка Add

### Новый таб "Hotkeys"

```
{ id: 'hotkeys', label: 'Hotkeys' }
```

CRUD аналогично Aliases: поля `shortcut` + `command`, работает через profile selector.

---

## main.go (go/cmd/mudhost/main.go)

- Убрать hotkeys из `web.New()` — сервер загружает их из store
- Оставить `config.LoadHotkeys(path)` только для передачи в `storage.Open()` (для миграции)

---

## Критические файлы

| Файл | Что меняется |
|------|-------------|
| `go/internal/storage/types.go` | Новые structs: Profile, SessionProfile, HotkeyRule |
| `go/internal/storage/open.go` | AutoMigrate + MigrateToProfiles |
| `go/internal/storage/alias.go` | ProfileID field + LoadAliasesForProfiles |
| `go/internal/storage/trigger.go` | ProfileID field + LoadTriggersForProfiles |
| `go/internal/storage/highlight.go` | ProfileID field + LoadHighlightsForProfiles |
| `go/internal/storage/group.go` | Переключить на profileID |
| `go/internal/storage/profile.go` | **Новый** — Profile CRUD + session-profile join |
| `go/internal/storage/hotkey.go` | **Новый** — HotkeyRule CRUD |
| `go/internal/vm/runtime.go` | Reload() через GetOrderedProfileIDs |
| `go/internal/session/session.go` | VM-команды пишут в primary profile |
| `go/internal/web/server.go` | Новые routes, убрать hotkeys field, sendRestoreState |
| `go/internal/config/config.go` | LoadHotkeys остаётся только для миграции |
| `go/cmd/mudhost/main.go` | Убрать hotkeys из web.New() |
| `ui/src/SettingsApp.svelte` | Profile selector, новые табы, session-profile mgmt |

---

## Verification

1. `go build ./...` без ошибок
2. `go test ./...` все тесты проходят
3. Запустить с существующей БД → миграция создаёт профайлы, правила переехали
4. Settings UI: создать профайл → добавить алиас → назначить на сессию
5. Подключиться к сессии → алиас из профайла работает
6. Два профайла с одним алиасом → побеждает тот, что выше в списке сессии
7. Hotkeys в профайле → работают в game UI после переподключения
8. Старые данные не потеряны после миграции
