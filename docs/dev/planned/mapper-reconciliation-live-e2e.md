# Live E2E: Five-room Mapper Reconciliation

## Статус

План подготовлен 2026-08-19. Основной live smoke A–G реализован той же датой:
five-room TCP emulator, безопасный storage seed, отдельные SQLite/mudhost,
Playwright reconciliation и room-NPC dialog flows, второй круг и restart
persistence запускаются командой
`make e2e-mapper-reconciliation`. Negative scenarios расширенного набора пока
остаются в backlog ниже. Финальный основной spec прошёл три clean runs подряд
без retry; полный Go и UI regression suites также зелёные.

Он проверяет observation/conflict и manual reconciliation из
[`mapper-autorecord-reconciliation.md`](mapper-autorecord-reconciliation.md)
на полном локальном стеке:

```text
headless Chromium -> mudhost HTTP/WebSocket -> live Session -> TCP test MUD
                         |                         |
                         +---- isolated SQLite ---+
```

Стенд не использует `data/mudhost.db`, внешнюю сеть, реальные MUD-серверы или
пользовательские профили. Процессы слушают только loopback, runtime data живёт
в отдельном временном каталоге.

## Проверяемая модель

Физический тестовый мир без GMCP/MSDP room ID:

```text
1 --E--> 2 --E--> 3
^               |
N               S
|               v
5 <--W----------4
```

Круг `E E S W N` возвращает room blocks `2 3 4 5 1`.

До reconciliation SQLite намеренно содержит обычную единичную раскладку:

```text
1(0,0) -- 2(0,1) -- 3(0,2)
           ^          |
           N          S
           |          v
         5(1,1) -- 4(1,2)
```

Mapper ожидает `5 --N--> 2`, но живой MUD возвращает комнату `1`. OBS обязан
остановиться до topology write. Пользователь выбирает logical room `1` и
переносит только presentation position комнаты `5` в `(1,0,0)`:

```text
1(0,0) -- 2(0,1) -- 3(0,2)
^                      |
N                      S
|                      v
5(1,0) <-------------- 4(1,2)
```

После Apply существуют точный направленный corridor `5 --N--> 1`, внутреннее
непроходимое reconciled-подавление опровергнутого `2 --S--> 5` и длинный
presentation passage `4–5`. Logical coordinates не меняются; длинный passage
по-прежнему означает одну transport-команду.

Текущий slice не создаёт provisional rooms, поэтому пять logical rooms заранее
seed'ятся. Создание комнат continuous autorecord потребует отдельного E2E.

## Компоненты

### Five-room MUD emulator

Test-only Go process на standard library:

- слушает `127.0.0.1:0` и сообщает назначенный порт;
- хранит физическую комнату отдельно для каждого TCP connection;
- при connect отправляет полный RMUD-compatible block комнаты `1`;
- принимает только полные command lines и реализует таблицу `E E S W N`;
- отвечает стабильной refusal-строкой на невозможное направление;
- не отправляет room ID, GMCP или MSDP;
- игнорирует минимальные telnet negotiation bytes;
- пишет bounded JSONL trace: sequence, from, exact command, to;
- ограничивает input buffer и время бездействия.

Он не расширяет `ruby/test_mud_server.rb`: тот документирован как ручной QA
helper и не должен становиться зависимостью автоматических тестов.

### Изолированная SQLite fixture

Test-only Go seed command с build tag `e2e` использует настоящий `storage.Open`
и storage operations вместо raw SQL. Она:

1. создаёт `${RUN_DIR}/mudhost.db` и применяет migrations;
2. создаёт session `default` с loopback address emulator;
3. создаёт five-room import и форкает его в editable map set;
4. сохраняет exits так, чтобы derived face `5 --N--> 2` существовал;
5. назначает editable set активным;
6. выводит bounded JSON с `session_id`, `map_set_id` и координатами.

Команда отказывает, если DB path находится вне переданного run directory. Так
ошибка orchestration не сможет затронуть `data/mudhost.db`.

### Тестовый mudhost

Orchestrator собирает обычные UI assets и production binary, затем запускает:

```text
mudhost --db <RUN_DIR>/mudhost.db
        --config-dir <RUN_DIR>/config
        --listen 127.0.0.1:<ephemeral-web-port>
```

`--mud` не используется: session уже seeded. Readiness определяется HTTP GET
корневой страницы и REST-чтением session, а не фиксированным `sleep`. У каждого
wait есть deadline.

### Headless browser

Playwright/Chromium живёт в отдельном `e2e/` package, чтобы browser dependency
не попадала в UI runtime bundle. Browser binary не коммитится. Чистая машина
устанавливает его штатной командой Playwright; локально разрешён уже имеющийся
cache. Для запуска нужны Go toolchain, Node/npm, `sqlite3` CLI и системные
зависимости Chromium; первый `playwright install chromium` может требовать сеть.

Перед navigation fixture задаёт `pane-layout` с `main` и `~map`, затем открывает
`/?session_id=<id>`. REST используется только для fixture setup/readback:
connect, ожидание live room и начальный anchor в room `1`. OBS, movement input,
target selection, Preview и Apply выполняются через настоящие DOM controls.

## Основной сценарий

### A. Запуск и anchor

1. Запустить emulator, seed, mudhost и Chromium.
2. Дождаться WebSocket status `connected` и live room `1`.
3. Выполнить setup anchor в logical room `1`, дождаться green position.
4. Нажать `OBS`; состояние должно стать armed/`STOP OBS`.

### B. Первый круг и конфликт

1. Через `#input` отправить `E`, `E`, `S`, `W`, `N`, каждый раз ожидая полный
   room block.
2. Проверить emulator trace: пять команд и физический путь `1,2,3,4,5,1`.
3. После `N` проверить один conflict dialog:
   - expected room `2`, observed room `1`;
   - bounded diff реальных `hint`, `desc`, `exits`;
   - accepted command `N`.
4. До Preview проверить SQLite: face остаётся `5 --N--> 2`, corridor/layout
   отсутствуют, topology revision и room count не изменились.

### C. Read-only Preview

1. Выбрать существующую room `1` и display position room `5` = `(1,0,0)`.
2. Убедиться, что Apply disabled до Preview.
3. Нажать Preview и проверить server-owned summary/token.
4. Повторить DB snapshot: Preview ничего не записал.
5. Изменение любого поля обязано инвалидировать token и снова выключить Apply.

### D. Атомарный Apply

1. Нажать Apply и дождаться authoritative state/rooms WebSocket updates.
2. Проверить UI: dialog закрыта, position green в logical room `1`, OBS снова
   recording, room `5` под room `1`, passage `4–5` длинный без phantom targets.
3. Проверить SQLite:
   - один `kind='corridor'` face `5 --N--> 1`;
   - command `N`, source `reconciled`;
   - нет `5 --N--> 2`, проходимого reverse `2 --S--> 5` и автоматически
     созданного reverse `1 --> 5`; внутренний reconciled tombstone не попадает
     в routing/slim renderer и переживает rematerialization;
   - sparse layout room `5` равен `(1,0,0)`;
   - пять logical rooms не изменились;
   - topology revision увеличилась ровно один раз.
4. Сохранить screenshot страницы и `~map` canvas.

### E. Второй круг

1. Из физической room `1` снова отправить `E E S W N`.
2. Проверить второй emulator trace.
3. Последний `N` использует exact corridor и приходит в room `1` green без
   нового conflict; OBS остаётся recording, pending queue возвращается к нулю.

### F. Persistence/restart

1. Закрыть browser и mudhost, сохранив run directory.
2. Запустить mudhost с той же DB и открыть новый browser context.
3. Проверить восстановление exact corridor и layout.
4. Переподключиться к emulator, re-anchor в room `1`, пройти круг и подтвердить
   green landing `5 --N--> 1` без conflict.

### G. Room knowledge / NPC

1. Через настоящий canvas открыть свойства logical room `1`, затем
   `Knowledge… → Inhabitants → Add`.
2. Заполнить три основные строки сущности: имя, цель команды `look` и результат
   `look`; сохранить через DOM controls.
3. Проверить карточку NPC и обе нормализованные SQLite-записи:
   `map_inhabitants` для сущности и `room_inhabitants` для размещения в room `1`.
4. Убедиться, что knowledge overlay не изменил `topology_revision`.
5. Сохранить отдельные screenshots формы и готовой карточки.
6. После restart снова открыть room `1` через canvas и подтвердить сохранность
   карточки NPC до следующего физического круга.

## Negative scenarios

Каждый использует новую временную DB:

1. **Cancel/Escape:** dialog закрывается без write; `OBS!` открывает тот же
   immutable conflict.
2. **Stale revision:** topology write после Preview даёт Apply `409`, dialog и
   conflict остаются, partial corridor/layout отсутствуют.
3. **Double Apply:** две pages одной session дают ровно один success и один
   `409`, revision увеличивается один раз.
4. **Refusal:** невозможная команда отменяет evidence head без conflict/write.
5. **Undo:** одно действие восстанавливает literal face `5 --N--> 2` и удаляет
   layout override; session anchor не считается частью shared topology undo.

Обязательный первый smoke — A–G. Stale/double-apply уже детерминированно покрыты
session/web tests и не блокируют первый live harness.

## Артефакты

При ошибке `${RUN_DIR}/artifacts` содержит:

- screenshots (network trace намеренно выключен, поскольку содержит ephemeral
  preview/apply token);
- browser console/page errors;
- mudhost stdout/stderr;
- emulator JSONL trace;
- рядом в сохранённом run directory — изолированную SQLite DB.

На успехе run directory удаляется. `KEEP_E2E_ARTIFACTS=1` переносит его в
gitignored `tmp/e2e-artifacts/<run-id>`. Перед переносом текстовые логи
санитизируются, а DB получает mode `0600`; `artifacts/SECURITY.md` явно
предупреждает, что полная диагностическая DB может содержать API/session
credentials и не предназначена для загрузки или передачи. Основная
`data/mudhost.db` в артефакты не входит.

## Cleanup и таймауты

- Run directory создаётся Node `mkdtempSync`.
- Orchestrator хранит конкретные `ChildProcess` emulator и mudhost.
- Playwright `afterAll` сначала посылает этим child `SIGTERM`, ждёт до трёх
  секунд и только затем посылает `SIGKILL` тому же PID.
- Запрещены `pkill`, широкая очистка портов и удаление workspace/data.
- Playwright test timeout — 90 секунд; readiness и subprocess operations имеют
  отдельные bounded deadlines.
- Повторный run не переиспользует DB, порты или browser context.

## Repro-first порядок реализации

1. Unit-test state table emulator и путь `E E S W N`.
2. Test seed command: пять rooms, editable active set, false `5 --N--> 2`.
3. Написать Playwright spec A–E и зафиксировать первый failure.
4. Добавить минимальный orchestration и довести основной spec до green.
5. Добавить restart F, failure artifacts и cleanup regression.
6. Запустить три clean runs подряд без retry и fixed sleeps.
7. Запустить `go test ./...`, UI Vitest и production build.

## Acceptance gate

Стенд готов, когда:

- одна команда запускает весь сценарий из clean checkout;
- A–G три раза подряд проходят без retry;
- до Apply нет topology writes;
- после Apply существует только exact `5 --N--> 1`, без reverse/ложного face;
- второй круг и круг после restart заканчиваются green без conflict;
- screenshot подтверждает relayout и длинный `4–5` passage;
- screenshots подтверждают заполнение трёх основных NPC-полей и готовую
  room-scoped карточку; сущность и placement переживают restart без topology
  revision;
- искусственный failure оставляет bounded diagnostic bundle;
- основная DB и generated/static git status не меняются.

## Не входит

- provisional room creation continuous autorecord;
- GMCP/MSDP identity auto-close;
- frozen-set COW reconciliation;
- solver/drag и cross-zone target search;
- внешний MUD или TLS/auth proxy.

Five-room emulator и orchestration должны быть переиспользуемыми для будущих
product slices.
