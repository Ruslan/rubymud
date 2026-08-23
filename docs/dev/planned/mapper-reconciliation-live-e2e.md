# Live E2E: Five-room Mapper Reconciliation

## Статус

План подготовлен 2026-08-19. Актуализирован 2026-08-23. Основной live smoke A–G:
five-room TCP emulator, безопасный storage seed, отдельные SQLite/mudhost,
Playwright reconciliation, room-NPC create/reuse, MCP/UI synchronization,
второй круг и restart persistence запускаются командой
`make e2e-mapper-reconciliation`. Та же команда сейчас выполняет пять независимых
browser tests: основной reconciliation/NPC/restart, darkness+provisional
autorecord, mutable crate exit, реальный Hillo one-way и concealed/delayed hatch
рядом с legacy pipe. Negative scenarios расширенного набора остаются в backlog
ниже; session/web unit tests закрывают stale/double-apply. Последний полный run:
5/5 без retry вместе с production UI и Go build.

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

Основной reconciliation run seed'ит пять logical rooms заранее. В отдельном
изолированном autorecord run seed'ится только anchor room `1`, тогда как
физический emulator по-прежнему содержит полный мир. Один настоящий `E` после
OBS должен создать room `2` как provisional и записать двустороннюю пару
autorecord corridor; one-way остаётся явным свойством прохода.

One-way isolated stack повторяет реальный фрагмент `Хилло` из runtime SQLite:
`Вдоль оврага --W--> Лесистое предгорье`, причем целевая комната не печатает и
физически не принимает обратный `E`. Playwright проверяет solid объявленную
половину и dashed presumed reverse, ручное отрицание только обратной грани в
edge editor, недоступность обратного маршрута и штатную обработку MUD refusal.

Ещё два isolated stack закрепляют полевые исключения:

- команда `двигать ящик` добавляет mutable `D`; OBS принимает изменившийся exits
  только после явной маркировки mutating direction и не теряет этаж `-1`;
- скрытый W/E ведёт `Поворот ↔ Беседка`, а `Беседка D ↔ U Тупик` требует разных
  opener (`повернуть рычаг`/`нажать кнопку`) и двухсекундной задержки. В той же
  зоне лежит настоящий imported `pipe=true, edirs=[], ch=N|S`: canvas обязан
  оставить его сплошным, одновременно сохранив скрытый W/E пунктирным.

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
- пишет bounded JSONL trace: sequence, from, exact command, to, moved и
  `at_unix_milli` для проверки фактического интервала opener → movement;
- ограничивает input buffer и время бездействия.

Он не расширяет `ruby/test_mud_server.rb`: тот документирован как ручной QA
helper и не должен становиться зависимостью автоматических тестов.

### Изолированная SQLite fixture

Test-only Go seed command с build tag `e2e` использует настоящий `storage.Open`
и storage operations вместо raw SQL. Она:

1. создаёт `${RUN_DIR}/mudhost.db` и применяет migrations;
2. создаёт session `default` с loopback address emulator;
3. создаёт five-room import (или, для autorecord run, только anchor-room import)
   и форкает его в editable map set;
4. в обычном reconciliation fixture сохраняет exits так, чтобы derived face
   `5 --N--> 2` существовал (autorecord fixture intentionally seeds only room 1);
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
`/?session_id=<id>`. В основном reconciliation workflow REST используется только
для fixture setup/readback: connect, ожидание live room и начальный anchor в
room `1`; OBS, movement input, target selection, Preview и Apply выполняются
через настоящие DOM controls. Delayed-hatch test отдельно вызывает публичные
`map-path` и `map-walk` endpoints, но проверяет прогресс в DOM, команды по TCP
trace и итоговую зелёную позицию.

Autorecord E2E использует тот же connect/anchor/OBS путь и только читает REST
state для ожидания завершения записи; создание room/edge происходит через
реальный `#input` movement и live room event. Продуктовый режим одношаговый:
перед следующим исследовательским движением UI должен снова показать зелёную
позицию и ноль незавершённых записей.

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
   нового conflict; OBS остаётся recording, ожидающий шаг возвращается к нулю.

### F. Persistence/restart

1. Закрыть browser и mudhost, сохранив run directory.
2. Запустить mudhost с той же DB и открыть новый browser context.
3. Проверить восстановление exact corridor и layout.
4. Переподключиться к emulator, re-anchor в room `1`, пройти круг и подтвердить
   green landing `5 --N--> 1` без conflict.

### G. Room knowledge / NPC

1. Через настоящий canvas открыть logical room `1`, затем
   `Knowledge… → Inhabitants → Add new`.
2. Заполнить имя, цель/результат `look` и стратегию; сохранить через DOM controls.
3. Перейти физической командой в room `2`, открыть её через canvas и через
   `Place existing` найти/разместить ту же каталожную сущность, не создавая
   дубликат `map_inhabitants`.
4. Через настоящий loopback MCP вызвать bounded list, explicit detail и update;
   проверить, что открытая browser-карточка получила новый presence/strategy по
   `room_knowledge_changed`, list/`mud_look_map` не раскрывают `look_text`, а
   detail раскрывает его явно.
5. Проверить одну строку `map_inhabitants`, две строки `room_inhabitants` и
   неизменный `topology_revision`.
6. Сохранить отдельные screenshots формы и готовой карточки.
7. После restart снова открыть room `1` через canvas и подтвердить сохранность
   общей сущности и обоих placements до следующего физического круга.

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

## Continuous autorecord smoke

Второй isolated stack запускается с флагом `--autorecord`: в active editable
set остаётся только подтверждённая room `1`, но emulator всё ещё возвращает
полный block room `2`. Browser повторяет A (connect, anchor, OBS), отправляет
один `E`, а затем Playwright проверяет в SQLite ровно одну новую provisional
room, `source='autorecord'` corridors `1 --E--> 2` и `2 --W--> 1`, затем реально
проходит `W, E` обратно через эту пару и проверяет green position и `recording:0`. Этот сценарий
не вызывает REST topology write.

## Extended field regressions

Дополнительные browser tests используют отдельную DB каждый и не ослабляют
основной A–G:

1. Darkness/scan frontier подтверждает, что obscured block не становится новой
   identity, а видимый повторный проход восстанавливает green.
2. Mutable crate exit проверяет действие, изменение exits и создание этажа `-1`
   без ложной другой комнаты.
3. Hillo one-way проверяет parsed W, dashed presumed reverse E, реальный refusal
   и ручную blocked-грань.
4. Concealed hatch проверяет dashed W/E, поиск пути в обе стороны, `waiting_open`,
   реальные интервалы около 2000 ms перед D/U и отсутствие flatten в безопасном
   route workflow.
5. В hatch canvas встроен legacy pipe regression: fixture хранит N/S только в
   `ch` при пустом `edirs`; pixel assertion требует однородную сплошную линию.

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

## Исторический repro-first порядок реализации

1. Unit-test state table emulator и путь `E E S W N`.
2. Test seed command: пять rooms/editable active set для reconciliation и
   anchor-only editable active set для autorecord.
3. Написать Playwright spec A–E и зафиксировать первый failure.
4. Добавить минимальный orchestration и довести основной spec до green.
5. Добавить restart F, failure artifacts и cleanup regression.
6. Запустить три clean runs подряд без retry и fixed sleeps.
7. Запустить `go test ./...`, UI Vitest и production build.

## Acceptance gate

Стенд готов, когда:

- одна команда запускает весь сценарий из clean checkout;
- A–G и четыре extended field regressions проходят одной командой без retry;
- autorecord smoke три раза подряд создаёт ровно одну provisional room и два
  passable направления, затем проходит по ним обратно без потери green position;
- до Apply нет topology writes;
- после Apply существует только exact `5 --N--> 1`, без reverse/ложного face;
- второй круг и круг после restart заканчиваются green без conflict;
- screenshot подтверждает relayout и длинный `4–5` passage;
- pixel assertions одновременно подтверждают dashed concealed W/E и solid
  CH-backed legacy pipe; это разные семантики, а не общий стиль «не в edirs»;
- screenshots подтверждают заполнение основных NPC-полей и готовую room-scoped
  карточку; одна сущность, два placements и MCP-правка переживают restart без
  topology revision;
- искусственный failure оставляет bounded diagnostic bundle;
- основная DB и generated/static git status не меняются.

## Не входит

- multi-hop autorecord solver, authoritative room IDs и automatic provisional
  promotion;
- GMCP/MSDP identity auto-close;
- frozen-set COW reconciliation;
- solver/drag и cross-zone target search;
- внешний MUD или TLS/auth proxy.

Five-room emulator и orchestration должны быть переиспользуемыми для будущих
product slices.
