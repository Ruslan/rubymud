# Mapper Autorecord, Loop Reconciliation and Visual Relayout

## Статус

Карточка открыта; полный loop-reconciliation, solver/drag и server identity ещё
не реализованы. **Исторический Slice A (presentation foundation, 2026-08-19)**
добавил sparse `room_layouts`, effective collision validation,
fork/delete/zone-move/undo lifecycle, compact slim overrides и перевод canvas
rendering/hit-testing/follow/marker/selection на presentation coordinates при
сохранении logical identity. Самостоятельный публичный layout batch и ручной
drag/solver ещё отсутствуют; compound layout теперь применяется через C1/C2.

**Исторический Slice B (session observation foundation, 2026-08-19)**:
session хранит bounded structured evidence только успешно отправленных movement
commands (`command`, canonical direction, source, from-coordinate, sequence и
run epoch, exact expected target where the route supplies it), runtime-состояния
`off/armed/recording/conflict`, immutable snapshot
первого occupied-target fingerprint mismatch и рассылает состояние через strict
REST/WebSocket всем клиентам сессии; monotonic state revision не позволяет
позднему restore откатить UI. OBS хранит не более одного ожидаемого шага:
refusal очищает его, а второй шаг до завершения первого безопасно выключает
режим. Неоднозначный/частичный transport write, timeout, потеря позиции (`red`), disconnect, смена map set
и room format останавливают режим. Минимальный mapper toggle называется `OBS`,
создаёт conflict без спорных writes. C2 ниже теперь разрешает такой conflict
ручным relink/relayout; provisional writes, durable recovery и полный continuous
autorecord workflow остаются будущими срезами.

**C1 storage/runtime foundation реализован 2026-08-19**: `grid|corridor` делят
один SQLite directional face; точный направленный
`corridor` с literal command/source/attrs переживает incremental и full
rematerialization. Revision-fenced compound storage operation атомарно заменяет
face и применяет explicit sparse layouts, сохраняет literal edge/layout
before-image и восстанавливается одним Undo. Runtime index/tracker/path используют
точный endpoint и одну transport-команду; slim payload `co` отделён от seam/xz,
а canvas рисует один clipped display-коридор без фиктивных комнат. Публичный
workflow над этим storage contract реализован ограниченным C2 ниже.

**C2 manual reconciliation vertical slice реализован 2026-08-19 для editable
sets**: immutable OBS conflict выдаёт server-owned bounded preview/token; browser
передаёт только conflict id, существующий logical target и explicit layout,
тогда как from/direction/exact transport command/provenance/revision берутся из
session snapshot. Apply одноразовый, revision-fenced и сериализован с topology,
Stop и active-set switch не могут разорвать промежуток DB commit → session
publication. После успеха текущая игровая сессия re-anchor'ится green и её
клиентам приходят state/rooms changes. Доступный keyboard dialog требует server
preview перед Apply и сохраняется при stale error. Frozen-set reconciliation
пока явно возвращает conflict: COW отложен до отдельного тестируемого контракта
без orphan fork/active-set rollback. Selector текущего среза ограничен уже
загруженной зоной; cross-zone search и solver/drag не входят в C2.

**E1 пошаговый provisional autorecord реализован 2026-08-20** для сервера одного
игрока и одного персонажа: на активном
редактируемом наборе green anchor + полный live room + один принятый transport
move + один полный room event в пустой predicted cell создают ровно одну
provisional room и двустороннюю пару `corridor` (`source=autorecord`). Запись идёт
через однослотовый async handoff и существующий topology transaction,
journal/revision/undo; parser/VM не ждут SQLite. До зелёного подтверждения новой
клетки следующий шаг выключает OBS и не ставится в очередь. Replay становится no-op,
occupied mismatch остаётся conflict, а односторонность задаётся только явной
правкой свойств прохода. Stop/map-switch/reconnect и второй преждевременный шаг инвалидируют
отложенные jobs. `verification_state`, batch и origin сохраняются на комнате;
UI OBS прямо просит дождаться зелёной клетки. Межсессионного fanout и pipeline
записей намеренно нет. Полный loop closure,
server-ID identity, relayout и durable recovery остаются следующими срезами.

**E2 осторожный scan frontier реализован 2026-08-21**: только при активном OBS,
green position и неизвестном печатном выходе mapper один раз отправляет
server-specific команду из выбранного room format за каждое посещение клетки
(возврат после движения считается новым посещением). Захват принимает только
настроенные префиксы `N/E/S/W/U/D`, ограничен существующими block boundaries,
`max_lines` и коротким timeout; движение/смена позиции, карты, формата или режима
инвалидирует ответ. Scan-ответ изолирован от room detector, а любая последующая
игровая команда отменяет публикацию и оставляет bounded discard до границы.
Результат не пишется в SQLite и не участвует в fingerprint.
Canvas показывает его как удлинённый коридор с ближней половиной неизвестной
комнаты (для `U/D` — стрелка), без fake room, hit target или route node. Builtin
RMUD использует `scan` и проверенные по runtime logs фразы `На севере:`,
`На востоке:`, `На юге:`, `На западе:`, `Наверху:`, `Внизу:`; пользовательские
форматы без `scan` сохраняют прежнее поведение.

**E3 потеря видимости реализована 2026-08-21**: room format хранит ограниченный
список точных `obscured_lines`; builtin RMUD знает фразу
`Здесь кромешная тьма.`. Такой завершённый блок подтверждает физический шаг, но
не является identity комнаты: tracker переходит в ожидаемую клетку с yellow,
OBS останавливается с подсказкой восстановить освещение, fingerprint/conflict и
topology write не выполняются. Видимый обратный шаг штатно возвращает green.
В reconciliation dialog отдельно доступно явное «Отбросить наблюдение и
остановить НАБЛ»; Cancel/Escape по-прежнему только скрывают dialog.

Проверка C2 на полном live stack вынесена в
[`mapper-reconciliation-live-e2e.md`](mapper-reconciliation-live-e2e.md):
детерминированный five-room TCP MUD, отдельные mudhost/SQLite и headless Chromium.

План выделен из пунктов 2 и 3 общего mapper backlog
**2026-08-19** после обсуждения замыкания маршрута без стабильного server room ID.

Архитектурный pre-audit текущего кода выполнен двумя ролями до реализации. Он
подтвердил продуктовый контракт, но изменил порядок срезов: существующие
movement FIFO, `WriteTopology`, undo journal и runtime index нельзя безопасно
расширить одним UI-диалогом. Сначала нужны data/runtime foundations, затем
session conflict state и только после них continuous recording.

Безопасная ручная команда записи текущей комнаты уже существует. Первый
testable slice непрерывного autorecord теперь есть; полного
relayout/reconciliation workflow пока нет. Базовый sparse presentation layer
уже существует, но ещё не имеет пользовательского preview/apply для solver/drag.
Эта карточка объединяет их в одну пользовательскую функцию: отдельно
реализовать autorecord без reconciliation опасно, а relayout без фактического
замыкания графа не решает исходную задачу.

## История и исходный сценарий

Пользователь приходит на новый сервер, подтверждает начальную комнату и включает
autorecord. В мире есть цикл из пяти комнат:

```text
1 — 2 — 3
|       |
5 ———— 4
```

Игровые переходы дают последовательность `1 2 3 4 5 1 2 3 4 5 ...`. Между
`4` и `5` в интерфейсе нужен длинный коридор, но в игре это один переход и одна
команда движения.

До первого замыкания цикла mapper располагает новые комнаты единичными шагами:

```text
1 — 2 — 3
        |
    5 — 4
```

После движения из `5` на север dead reckoning ожидает комнату `2`, однако живой
room block описывает комнату `1`. До этого события данных о длине коридора было
недостаточно: растянуть можно было бы любую сторону цикла. Mapper не должен
угадывать, перезаписывать комнату `2` или создавать вторую копию комнаты `1`.

Если сервер сообщает стабильный room ID, mapper автоматически узнаёт комнату
`1`, замыкает граф и пересчитывает presentation layout. Если ID нет, autorecord
останавливается. Пользователь явно выбирает: «Я попал в существующую комнату
1». После этого mapper одним согласованным изменением:

1. создаёт точный направленный переход `5 -> 1` для фактически отправленной
   команды;
2. не создаёт ложный переход `5 -> 2`;
3. предлагает или применяет однозначный минимальный relayout;
4. размещает `5` под `1`, из-за чего визуальный коридор `4–5` становится длинным;
5. сохраняет один понятный Undo для relink и relayout.

## Продуктовый контракт

1. Autorecord включается отдельно для каждой сессии и только из подтверждённой
   green-позиции на активном map set.
2. Обычный подтверждённый переход может создать новую provisional-комнату и
   направленное ребро. Запись происходит только после полного room block, а не
   на отправке команды.
3. Совпадение ожидаемой комнаты позволяет продолжить запись. Несовпадение
   останавливает её **до любой спорной topology write**.
4. Стабильный внешний room ID является авторитетной идентичностью в пределах
   явно заданного namespace сервера/протокола. Название или fingerprint таким
   доказательством не являются.
5. Без внешнего ID mapper может предложить кандидатов по fingerprint, но не
   сливает комнаты автоматически. Цель подтверждает пользователь.
6. Пользовательское подтверждение целевой комнаты создаёт точный topology link,
   а не меняет текст существующей ожидаемой клетки.
7. Топология и экранная геометрия независимы. Длина нарисованного коридора не
   равна числу отправляемых команд и не создаёт фиктивные комнаты.
8. Все переходы направленные. Обратное ребро появляется только из наблюдения
   обратного прохода, достоверных exit-данных либо отдельного явного
   подтверждения.
9. Reconciliation применяет topology relink и presentation relayout атомарно.
   Ошибка или revision conflict не оставляет половину изменения.
10. Любая автоматическая догадка остаётся видимой: provisional state,
    provenance и причина остановки не прячутся в `note`.
11. Pending movement хранит не только направление, но и точную принятую
    transport-команду, from-room, sequence/epoch и source. Room event нельзя
    связывать с восстановленной догадкой по одному `dir`.
12. SQLite topology write не выполняется под session `mapMu`. Session сначала
    резервирует immutable operation, storage применяет revision-fenced
    транзакцию off-lock, затем session принимает результат только при том же
    epoch, active set и conflict ID.
13. Operational state всегда runtime-only и после reconnect/restart становится
    `off`. Persisted настройка может помнить предпочтение, но не вооружает
    запись без нового green anchor.

## Термины

- **Logical room** — комната и её устойчивый адрес внутри текущего map set.
  Существующие `zone,x,y,l` пока выполняют эту роль.
- **Topology edge** — фактический направленный переход между двумя logical rooms.
- **Presentation position** — координата, в которой комната нарисована. Она не
  участвует в tracker, route или идентичности комнаты.
- **Expected room** — клетка, в которую tracker ожидал попасть по текущему графу
  или единичному dead reckoning.
- **Observed room** — последний полный room block от detector.
- **Provisional** — автоматически записанное, но ещё не подтверждённое
  замыканием, устойчивым ID или человеком знание.
- **Conflict** — observed room нельзя безопасно отождествить с expected room.
- **Relink** — выбор фактической logical room и запись точного topology edge.
- **Relayout** — изменение только presentation positions и вида коридоров.
- **Reconciliation** — одно действие, объединяющее relink, relayout и перевод
  затронутых provisional-данных в подтверждённое состояние.

## Почему relink и relayout должны делаться вместе

Ручной выбор «это комната 1» решает идентичность назначения, но создаёт
геометрическое ограничение: северный переход из `5` должен визуально приходить
в `1`. Если сохранить старую раскладку, ребро пересечёт карту по диагонали или
будет выглядеть порталом, хотя это обычный коридор. Если только передвинуть
комнаты, но не записать точный endpoint, tracker снова вычислит неверную клетку
по единичной дельте.

Поэтому пользователь видит один workflow, а реализация сохраняет два слоя:

```text
observed mismatch
    -> pause + immutable conflict snapshot
    -> choose actual logical room
    -> build exact directed edge
    -> solve presentation constraints
    -> preview/apply one compound operation
    -> re-anchor tracker and resume on request
```

## Идентичность комнаты

### Стабильный ID сервера

При наличии GMCP/MSDP или другого проверенного источника внешний ID хранится
отдельно от SQLite `rooms.id`. Локальный surrogate ID не подходит как server
identity: он меняется при fork, delete/undo и reimport.

Рекомендуемая привязка:

```text
room_identities
  map_set_id
  namespace       # конкретный сервер + протокол/room-format
  external_id
  zone,x,y,l      # logical room
  source
  last_observed_at
```

Уникальность — `(map_set_id, namespace, external_id)`. Namespace не позволяет
случайно склеить одинаковые ID разных миров или протоколов. Источник ID должен
быть частью эффективного session room format и учитывать профильные слои через
обычное разрешение `GetOrderedProfileIDs`.

«Стопроцентное» замыкание действует только внутри контракта конкретного сервера:
ID должен быть стабильным и обозначать физическую комнату. Повторное появление
одного ID с несовместимым сохранённым содержимым создаёт диагностическое
предупреждение, но identity всё равно следует серверному ID до явного сброса его
namespace.

### Сервер без room ID

Fingerprint из нормализованных `hint + очищенный desc + exits` используется для:

- подтверждения ожидаемой комнаты и выбора между одинаково названными соседними
  комнатами с разными описаниями;
- ранжирования существующих кандидатов в диалоге конфликта;
- показа различий пользователю.

Fingerprint не является глобальным ключом: одноимённые комнаты и повторяющиеся
описания обычны для MUD. Даже единственный найденный в базе кандидат при
расхождении с expected room требует явного выбора пользователя.

### Выбор пользователя

Диалог разрешения конфликта позволяет:

1. выбрать существующую комнату на карте или через поиск;
2. подтвердить, что observed room всё-таки является expected room, и показать
   diff перед обновлением её текста;
3. создать новую logical room в свободном адресе;
4. re-anchor без создания ребра — для teleport/follow/admin move;
5. отменить pending-переход и оставить autorecord остановленным.

## Состояния autorecord

```text
off
  -> armed       # пользователь включил режим на green anchor
  -> recording   # безопасные room events записываются
  -> conflict    # mutation остановлена, ожидается решение
  -> recording   # reconciliation применена и пользователь продолжил
  -> off         # stop, red/lost, disconnect или смена map set
```

Состояние принадлежит session, а не профилю и не map set. Все подключённые
браузеры одной сессии видят одинаковое состояние; resolve использует conflict
ID, session epoch, active set и revision, поэтому два клиента не могут применить
конфликт дважды. Смена active profile останавливает run: identity provider и
room detector нельзя безопасно заменить посреди room block.

После рестарта процесса autorecord не продолжает запись вслепую. Уже сохранённые
provisional rooms остаются видимыми, но режим возвращается в `off` и требует
нового green anchor. Persisted preference может помнить желание пользователя,
но не имеет права автоматически вооружить запись.

### Снимок конфликта

При остановке session хранит immutable snapshot:

- active map set и `topology_revision`;
- confirmed from-room;
- фактически отправленную movement command и нормализованное направление;
- expected room или ожидаемую свободную клетку;
- monotonic movement sequence, session epoch и source команды;
- полный observed room block и внешний ID, если он был;
- pending movement queue на момент события;
- ограниченный список fingerprint-кандидатов;
- причину: content mismatch, occupied coordinate, ambiguous match, teleport,
  queue drift или смена зоны.

Новый room event не должен молча заменить открытый conflict. Он либо добавляется
в ограниченный диагностический хвост, либо переводит состояние в `off` с
просьбой заново поставить anchor.

## Модель данных

### Presentation layer

Рекомендуется отдельная таблица, а не изменение импортных `DX/DY/DL`:

```text
room_layouts
  map_set_id
  zone,x,y,l          # logical room key
  display_x
  display_y
  display_l
  pinned
  updated_at
```

Отсутствие строки означает `display == logical`. Это сохраняет компактность
обычных импортных карт. Таблица копируется при fork, переносится при zone
move/rename, удаляется вместе с map set и может пережить временный room delete
ради текущего undo-контракта.

Renderer и hit-testing используют display coordinates. Tracker, route, MCP
room selectors, annotations, inhabitants и topology writes продолжают
использовать logical coordinates. Slim zone JSON получает только маленький
layout override, без отдельной полной карты.

### Точные обычные коридоры

Текущий `grid` edge предполагает endpoint в соседней logical coordinate, а
`seam`/portal визуально означает специальный переход. Для loop reconciliation
нужен обычный направленный коридор с точным endpoint, направлением и одной
movement command независимо от расстояния между display coordinates.

Предпочтительное расширение `map_edges` — отдельный `kind='corridor'`:

- `from_*` и `to_*` — точные logical endpoints;
- `dir` — физическое направление `N/S/E/W/U/D`;
- `command` — команда одного перехода, если она отличается от направления;
- `source='autorecord'|'manual'` либо отдельное provenance-поле;
- routing attributes работают так же, как у directed grid edge;
- на одном `from + dir` не могут одновременно существовать разные активные
  `grid` и `corridor` endpoints без явного conflict resolution.

Последний инвариант обеспечивается в SQLite, а не только Go-проверкой. Миграция
сначала проверяет/явно разрешает исторические дубликаты, затем вводит общую
face-ownership uniqueness для directional `grid|corridor`. Re-materialization
parsed grid edges не удаляет authoritative corridor и не воскрешает competing
grid face.

Не следует маскировать такой переход под portal: он должен рисоваться как
обычный коридор, участвовать в directional tracker и в маршруте стоить один
шаг. Обратный `corridor` является отдельной строкой.

### Provisional provenance

Автоматически созданным rooms и edges нужны структурированные поля либо
связанный bounded recording batch:

- `verification_state = provisional|verified`;
- `record_source = import|manual|autorecord|server_id`;
- `record_batch_id`;
- время и session origin.

Эти данные не должны храниться в пользовательской заметке. Замыкание по
стабильному ID или человеческое reconciliation может подтвердить связный
provisional segment. Оно не подтверждает боковые ответвления, которые игрок не
проходил.

## Запись обычного шага

1. Movement dispatcher сообщает tracker фактически принятую transport-команду.
2. Detector выдаёт полный observed room block.
3. Tracker под одним session lock связывает команду, from-room, expected-room и
   observation. Нельзя отдельно прочитать позицию и позднее последний room text.
4. Если внешний ID ведёт в известную room, применяется точный endpoint.
5. Иначе expected room сравнивается с observation. Совпадение подтверждает шаг.
6. Если expected cell свободна, создаётся provisional room и edge после проверки
   revision.
7. Если клетка занята несовпадающей room либо найдено несколько кандидатов,
   создаётся conflict snapshot без спорной topology mutation.
8. UI получает одно batched state/change event. Tracker index перезагружается
   один раз на принятую topology operation, а не на каждое поле.

Текущий `Tracker.PushMove` с FIFO из одних направлений перед этой логикой
заменяется структурированным `PendingMove`. Alias-expanded/custom phrase должна
сохраняться ровно в том виде, в котором transport принял frame. Failed/refused
write не попадает в recording FIFO.

Speedwalk не должен записывать несколько комнат вперёд. Каждый шаг завершается
только своим room event. При первом конфликте очередь auto-walk останавливается,
чтобы последующие ответы не были ошибочно приписаны другим командам.

## Reconciliation и relayout

### Ограничения раскладки

Directional edge задаёт мягкое геометрическое ограничение:

- `N/S` предпочитает общую display X и правильный знак display Y;
- `E/W` предпочитает общую display Y и правильный знак display X;
- расстояние может быть больше одной клетки;
- `U/D` меняет display level либо использует явно выбранный стиль перехода;
- portal/seam не навязывает прямую геометрию обычного коридора.

Confirmed и вручную pinned rooms имеют больший вес, provisional rooms можно
двигать первыми. Целевая функция минимизирует:

1. число перемещённых confirmed rooms;
2. число перемещённых provisional rooms;
3. суммарное расстояние перемещения;
4. число новых длинных коридоров и пересечений;
5. изменение уже показанной пользователю устойчивой части карты.

В исходном примере выбор комнаты `1` делает минимальным решение «сдвинуть `5`
под `1`». Ребро `4–5` автоматически становится длиннее в presentation layer,
но остаётся одним topology step.

### Однозначность

Если есть одно решение с явно лучшей стоимостью и оно не двигает pinned rooms,
подтверждение фактической комнаты может применить его сразу. UI всё равно
показывает итог и Undo.

Если несколько вариантов равнозначны, есть display collision, требуется
двигать pinned/verified область или коридоры нельзя нарисовать без спорного
пересечения, mapper показывает preview вариантов. Пользователь выбирает область
для сдвига, закрепляет комнаты или выполняет ручной drag.

### Атомарная операция

Apply передаёт caller-owned expected revision в storage; storage не имеет права
тихо прочитать более новую revision и применить к ней старый preview. Проверка
связывает map revision с conflict ID, session epoch и active set. В одной
транзакции операция:

1. при необходимости форкает frozen map set один раз;
2. создаёт/заменяет точный directed edge;
3. записывает выбранные layout overrides;
4. обновляет identity/provisional state;
5. добавляет один compound undo snapshot;
6. увеличивает revision один раз.

После commit session переключается на fork, tracker index перезагружается один
раз и позиция re-anchor-ится на выбранную target room. При ошибке всё остаётся в
состоянии conflict.

Per-map-set undo journal восстанавливает edge, layout и verification state одной
DB-операцией, но **не владеет session anchor**. После reconciliation единственная
игровая сессия явно re-anchor-ится на выбранную room; после Undo она сохраняет
позицию, только если та остаётся валидной в новом index, иначе требует ручного
anchor. Межсессионный fanout намеренно не поддерживается: продукт рассчитан на
одного игрока и одного активного персонажа.

## Ручной relayout вне конфликта

Тот же presentation layer позже поддерживает обычное редактирование карты:

- drag одной комнаты или выделенной группы мышью и touch;
- pin/unpin устойчивой части раскладки;
- preview коридоров и display collisions до Apply;
- reset выбранных overrides к logical layout;
- один revision-checked undo на жест drag, а не на каждый pointer move.

Drag никогда не меняет logical coordinates, topology endpoints, annotations,
inhabitants или текущую tracker position. Поэтому косметическое исправление не
может сломать route и не требует переноса room knowledge.

## UI

### Управление записью

- Per-session настройка находится в Settings и дублируется быстрым toggle в
  mapper pane.
- Включение доступно только при green anchor и полном live room block.
- Provisional rooms и corridors имеют лёгкое, не раздувающее DOM/canvas
  обозначение.
- Статус показывает `off / recording / paused: conflict`, количество
  provisional rooms и действие Stop.

### Диалог конфликта

Диалог показывает:

- «Вышли из 5 командой `n`»;
- ожидаемую room `2` и её сохранённое описание;
- observed room block и field-level diff;
- кандидатов с зоной, координатой и причиной совпадения;
- выбор клетки непосредственно на карте;
- действия Existing room, New room, Expected room is stale, Teleport/re-anchor,
  Cancel;
- будет ли добавлено только прямое ребро либо подтверждено и обратное;
- relayout preview и список перемещаемых комнат.

Диалог помещается в viewport, прокручивает длинные описания внутри, удерживает
keyboard focus и не закрывается при промежуточном выборе клетки.

## REST, WebSocket и MCP

Предварительный room-scoped/session-scoped контракт:

- `GET /api/sessions/{id}/map-recording-state`;
- `POST .../map-recording-start` и `.../map-recording-stop`;
- `POST .../map-reconcile-preview` с conflict ID и выбранной target room;
- `POST .../map-reconcile-apply` с conflict ID, revision и preview token;
- `POST .../map-relayout-preview|apply` для ручного layout.

Все write-body имеют лимит, strict JSON и reject trailing data. Preview token
связывает Apply с точными входными snapshot/revision, а не доверяет присланному
клиентом списку SQL-изменений.

Stale preview, сменившийся active set или уже разрешённый conflict возвращают
явный conflict response без orphan fork, journal entry или частичной записи.

WebSocket рассылает batched `map_recording_state` и обычный
`map_topology_changed`; открытый конфликт не требует перезагрузки slim JSON на
каждый новый MUD output.

MCP может читать состояние и предлагать кандидатов. Первый срез не должен
позволять агенту автоматически подтверждать неоднозначную identity без точного
logical target и того же preview/apply контракта, что использует UI.

## Performance и согласованность

- Detector продолжает работать на полном room block; никаких DB-запросов на
  каждую входящую строку.
- Lookup внешнего ID индексирован по `(map_set_id, namespace, external_id)`.
- Fingerprint candidates индексированы, имеют жёсткий limit и загружают полное
  описание только для показанных кандидатов.
- Topology writes сериализованы для сессии и не блокируют network read loop
  дольше сборки одного bounded immutable operation; DB apply идёт off `mapMu`.
- Canvas получает только layout overrides текущей зоны/этажа; длинный коридор
  остаётся одной render primitive.
- Несколько браузеров видят общий conflict, но применить его может только один
  revision winner.
- Смена active profile пересобирает effective room detector/identity provider;
  смена active map set останавливает текущую запись.

## План реализации: repro-first срезы

Continuous toggle нельзя показывать до завершения срезов A–C: это будет UI над
небезопасной write-моделью.

### Срез A. Data/runtime foundations

1. Сначала storage/runtime тесты exact corridor: `5 -> 1`, без ложного `5 -> 2`;
   reconciliation relink остаётся направленным, а обычная запись новой комнаты
   создаёт двустороннюю пару. Path/tracker отправляют одну команду и используют exact endpoint.
2. Добавить corridor face invariant, migration preflight и защиту от competing
   grid/corridor; re-materialization не стирает authoritative corridor.
3. Добавить `room_layouts`, fork/delete/zone move/rename semantics, renderer и
   hit-testing с round-trip из display position в logical key.
4. Добавить compound storage operation с caller ExpectedRevision, одной revision
   и generalized before-snapshot для edge + layout + provenance.
5. Forced failure на каждом sub-write должен откатывать всю транзакцию. Stale
   first write не оставляет orphan fork или active-set switch.

### Срез B. Session observation foundation

1. Заменить direction-only evidence на один bounded `PendingMove` с exact command,
   from-room, sequence/epoch и source.
2. Сначала тест: expected occupied cell имеет другой fingerprint — DB, revision
   и journal не меняются, immutable conflict не заменяется следующим event.
3. Реализовать state machine, coherent snapshot и двухфазный reserve/off-lock
   apply/epoch-checked accept.
4. Добавить strict bounded REST/WS state/start/stop без topology writes.
5. Проверить failed transport write, aliases/custom phrase, остановку на втором шаге,
   pipe-run, disconnect, profile/map-set switch и два browser client.

### Срез C. Ручная reconciliation

1. Сначала полный тест цикла `1-2-3-4-5`, где шаг из `5` ожидает `2`, а человек
   выбирает существующую `1`.
2. Добавить preview/apply exact relink с explicit layout choice; solver пока не
   обязателен.
3. Проверить два Apply одного token, concurrent topology write, frozen fork и
   one compound revision/journal entry. Направленный reconciliation не должен
   автоматически создавать reverse; это отдельный случай от обычной записи комнаты.
4. После commit обновить index текущей сессии; anchor
   остаётся session-owned по контракту выше.

### Срез D. Bounded solver и ручной relayout

1. Сначала тест исходного цикла: solver двигает provisional `5`, а `4–5`
   рисуется одним длинным corridor.
2. Реализовать bounded local constraint solver и preview равнозначных решений.
3. Добавить pin, collision guards, drag selection, touch/keyboard и один Apply
   на gesture вместо записи каждого pointer move.
4. Проверить search centering, follow viewport, floors, edge clipping, restore
   view и zone slim cache: каждый выбирает display, но возвращает logical key.

### Срез E. Пошаговый provisional autorecord

1. Добавить structured provenance/batch вместо заметок.
2. Записывать ровно одну новую room после одного завершённого observation.
3. Включить OBS только теперь; разрешать один незавершённый шаг и просить игрока
   дождаться зелёной клетки перед следующим движением.
4. Добавить Settings/runtime sync и browser smoke полного workflow.

### Срез F. Автоматическое замыкание по server room ID

1. Сначала replay fixture конкретного сервера/протокола со стабильными ID и
   корреляцией protocol event с тем же completed room block.
2. Добавить namespaced identity storage и provider из resolved profile layers.
3. Автоматически выбирать exact target только по авторитетному ID.
4. Проверить ID reuse, разные namespaces, reconnect, fork, reimport, mid-block
   profile/provider swap и смену сервера.

### Срез G. Укрепление

1. Нагрузочный тест длинного последовательного autorecord run и большой зоны.
2. MCP read/preview parity без обхода человеческого подтверждения.
3. Документация состояний provisional и восстановления после ошибки.

## Матрица обязательных тестов

### Identity и tracker

- известный стабильный ID автоматически замыкает цикл;
- отсутствие ID останавливает запись на несовпадении;
- одинаковые fingerprints дают кандидатов, но не auto-merge;
- mismatch не изменяет expected room;
- teleport/follow не создаёт corridor;
- закрытая дверь и неуспешная transport write не создают room event;
- pipe-run не создаёт комнату; второй шаг до зелёного подтверждения выключает OBS;
- reconnect/restart требуют нового green anchor.

### Topology

- точный `5 -> 1` заменяет ложное ожидание `5 -> 2`;
- новая autorecorded room получает passable reverse по умолчанию; explicit
  one-way edge edit удаляет/блокирует только выбранную сторону;
- route проходит corridor одной командой;
- tracker использует exact endpoint, а не logical coordinate delta;
- seam/portal и corridor остаются разными типами;
- frozen set форкается один раз;
- Apply и Undo атомарно возвращают edge, layout и verification; session anchor
  следует отдельному явно протестированному контракту.

### Layout

- пятикомнатный цикл получает ожидаемый длинный `4–5` corridor;
- logical coordinates всех комнат неизменны;
- pin защищает подтверждённую область;
- равнозначные решения требуют выбора;
- collision не применяется частично;
- zone/level switch, reload, fork, move/rename и room delete/undo сохраняют
  overrides корректно;
- hit-testing и выбор клетки используют display position, API — logical key.

### UI и синхронизация

- конфликт виден всем клиентам сессии;
- два Apply одного conflict дают один success и один revision refusal;
- длинные описания не выводят popup за viewport;
- keyboard-only пользователь может выбрать существующую комнату;
- Stop всегда доступен и не удаляет уже записанные данные;
- Settings и mapper toggle показывают одно session state.

## Критерии готовности

Функция считается готовой, когда пользователь может на пустой карте:

1. поставить green anchor и включить autorecord;
2. пройти новые комнаты, видя их provisional status;
3. получить безопасную остановку при первом ложном loop expectation;
4. без server ID выбрать существующую фактическую комнату;
5. с server ID получить то же замыкание автоматически;
6. применить точный directed relink и понятный relayout одной операцией;
7. увидеть длинный визуальный коридор, который маршрут проходит одной командой;
8. отменить reconciliation одним Undo без повреждения карты;
9. продолжить запись только после корректного re-anchor.

## Не входит в первый релиз

- вероятностное автоматическое слияние комнат только по тексту;
- глобальная оптимизация всей импортированной карты при каждом шаге;
- генерация фиктивных corridor rooms;
- автоматическое создание обратных переходов без evidence;
- GMCP/MSDP «вообще» без fixture конкретного сервера;
- durable topology undo между рестартами;
- одновременное редактирование одного map set несколькими игровыми сессиями без
  optimistic revision conflict.

## Открытые продуктовые вопросы

1. Применять ли однозначный минимальный relayout сразу после выбора target или
   всегда требовать отдельное подтверждение preview? Рекомендация: применять
   сразу, если не двигаются pinned rooms и нет collisions; всегда показывать Undo.
2. Как долго сохранять открытый conflict при продолжающемся MUD output?
   Рекомендация: до первого следующего полного room block, после чего остановить
   режим и оставить диагностический snapshot для ручного re-anchor.
3. Нужен ли persisted список незавершённых provisional batches после restart?
   Рекомендация: данные оставлять на карте, но сам runtime run не возобновлять.
4. Следует ли человеко-подтверждённый corridor получать `source=manual` или
   отдельный `source=reconciled`? Рекомендация: отдельный provenance, чтобы UI
   отличал его от произвольного ручного portal и от server-ID closure.
