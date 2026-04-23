# Profiles Feature Plan (Fixed)

## Context

Rules (aliases, triggers, highlights) и hotkeys сейчас привязаны к сессии.
Это не позволяет переиспользовать одни и те же правила в разных сессиях.

**Профайл** — именованный набор aliases + triggers + highlights + hotkeys.
Сессия имеет упорядоченный список активных профайлов. VM при загрузке мёрджит правила из всех профайлов сессии в порядке приоритета: **чем больше `order_index` профиля в сессии (чем он ниже в списке), тем выше его приоритет**.

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
    Position  int    `json:"position"`
    Shortcut  string `json:"shortcut"`
    Command   string `json:"command"`
}
```

### Изменения существующих таблиц

Убрать поле `SessionID` из Go-структур и добавить `ProfileID int64 json:"profile_id"`, а также поле `Position int json:"position"`:
- `AliasRule` (alias.go)
- `TriggerRule` (trigger.go)
- `HighlightRule` (highlight.go)

У старых клиентов в БД останется мусорная колонка `session_id`, удалять ее через DROP TABLE не нужно. У новых клиентов её просто не будет создано.

Поле `Position` отвечает за порядок правил внутри одного профиля. Важно для детерминированного срабатывания триггеров (особенно тех, что останавливают выполнение других) и для порядка при экспорте.

---

## Миграция данных (open.go)

Создание профилей и перенос данных в них (можно сделать через сырой SQL, так как поля `SessionID` в структурах уже не будет):

```
Если profiles таблица пуста:
  Для каждой session (сырой запрос):
    1. Создать Profile{Name: session.Name}
    2. UPDATE alias_rules SET profile_id=?, position=id WHERE session_id=? AND profile_id=0
    3. UPDATE trigger_rules SET profile_id=?, position=id WHERE session_id=? AND profile_id=0
    4. UPDATE highlight_rules SET profile_id=?, position=id WHERE session_id=? AND profile_id=0
    5. INSERT INTO session_profiles(session_id, profile_id, order_index=0)
  Если старые hotkeys из конфига не пусты:
    Создать Profile{Name: "Hotkeys"}
    INSERT hotkey_rules для каждого hotkey, устанавливая position по порядку
    INSERT INTO session_profiles для каждой session (order_index=1)
```
*(Для инициализации Position можно использовать ID или просто счетчик)*

---

## Storage Layer

### Новые файлы

**go/internal/storage/profile.go**
CRUD для `Profile` и связи `SessionProfile`.

**go/internal/storage/hotkey.go**
CRUD для `HotkeyRule` (с учетом `ProfileID` и `Position`).

### Изменения существующих файлов

В `alias.go`, `trigger.go`, `highlight.go` добавить загрузку для списка профилей:
```go
LoadAliasesForProfiles(profileIDs []int64) ([]AliasRule, error)
// и т.д.
```

CRUD-методы (`ListAliases`, `SaveAlias`, `UpdateAlias`...) переключаются на работу только с `profileID`. При создании нового правила ему присваивается `Position = MAX(position) + 1` в рамках этого профиля.

---

## VM (go/internal/vm/runtime.go) & Приоритеты

`Reload()` собирает правила по профилям.

**Приоритет слияния (Merging):**
1. Сначала берутся правила из профиля с **наивысшим `order_index`** (самый старший профиль, перекрывающий остальных). Внутри этого профиля правила сортируются по `Position` ASC (от меньшего к большему).
2. Затем берутся правила из профиля с `order_index` поменьше и добавляются в конец массива (их `Position` также ASC внутри их группы).
3. И так далее до профиля с самым низким `order_index`.

Таким образом, VM сначала проверяет триггеры старшего профиля (в их заданном порядке), и если там есть триггер с остановкой, младшие профили даже не начнут проверяться.
Аналогично с алиасами: первое совпадение при проходе массива (от старших к младшим) побеждает.

VM-команды `#alias`, `#action`, `#highlight` и др., вводимые из консоли сессии, пишут в primary profile сессии (обычно это профиль с высшим `order_index` или специальный default).

---

## Server (go/internal/web/server.go)

- Старые session-scoped rule routes убираются.
- Добавляются routes для `/api/profiles` и `/api/profiles/{profileID}/aliases` (и др. сущностей).
- В `/api/sessions/{sessionID}/profiles` настраивается список и порядок (order_index) профилей сессии.
- Уведомления об изменении `NotifySettingsChanged` рассылаются всем сессиям, к которым привязан измененный профиль.

---

## Settings UI (ui/src/SettingsApp.svelte)

### Изменение rule-табов (Aliases, Triggers, Highlights, Hotkeys)
При редактировании объектов пользователь работает **только с одним профилем за раз**.
В верхней части вкладки добавляется селектор профиля:
`[ Выберите профиль: Default ▾ ]`
В таблице ниже выводятся правила только выбранного профиля. При переключении профиля таблица обновляется.

### Новый таб "Profiles" (Управление профилями)
Список всех профилей (Name, Description).
По умолчанию должен существовать хотя бы один профиль (например, `default`).
Для каждого профиля доступны кнопки:
- **Edit / Delete** (удаление с подтверждением, базовый профиль удалить нельзя)
- **Export**: выгрузка всех правил (алиасы, триггеры, подсветки, хоткеи) профиля в JSON-файл в порядке их `Position`. Предупреждение пользователю: "Сохранить файл на диск?".
- **Import**: загрузка правил из файла. Номер строки (порядок) в массиве файла становится новым `Position`. Выдавать предупреждение: "Внимание! Все текущие правила этого профиля будут удалены и заменены новыми. Продолжить?".

### Таб "Sessions" — секция "Active Profiles"
Для выбранной сессии отображается список активных профилей.
Управление порядком (кнопки вверх/вниз для изменения `order_index`) и возможность добавить/удалить профиль из сессии.
Профиль внизу списка имеет бОльший `order_index` и является более приоритетным.

### Редактирование Position (UI)
Для таблиц с алиасами, триггерами и хоткеями добавить возможность менять их `Position` (стрелочки вверх/вниз или drag-and-drop), чтобы пользователь мог управлять порядком срабатывания внутри одного профиля.
