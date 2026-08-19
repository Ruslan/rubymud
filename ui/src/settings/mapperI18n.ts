import {
  extractPlaceholders,
  interpolate,
  normalizeLocale,
  readBootLocale,
  selectPluralForm,
  type Locale,
  type MessageValues,
} from '../i18n';

const en = {
  tabRoomFormats: 'Room Formats', tabMapSets: 'Map Sets',
  mapSetsTitle: 'Map Sets', mapSetsDescription: 'Import a ZIP archive as a new map set, then choose it for a session on the Sessions tab.',
  mapZipAria: 'Map ZIP archive', importing: 'Importing...', importZip: 'Import ZIP', chooseZip: 'Choose a ZIP archive.', onlyZip: 'Only .zip map archives are supported.',
  uploadFailed: 'Upload failed ({status})', deleteMapSetConfirm: 'Delete map set "{name}"? This also removes its rooms and generated room images.', deleteFailed: 'Delete failed ({status})', cleanupWarning: 'Map set was deleted, but image cleanup needs attention: {error}',
  unresolvedSeams: 'Unresolved seams: {seams}', conflicts: 'Conflicts: {conflicts}', conflictTruncated: 'Conflict report was truncated.',
  duplicateZone: 'duplicate_zone', duplicateRoom: 'duplicate_room', conflictFirst: 'first: {source}', conflictAgain: 'again: {source}',
  name: 'Name', map: 'Map', import: 'Import', actions: 'Actions', manual: 'manual', deleting: 'Deleting...', delete: 'Delete', noMapSets: 'No map sets imported yet.', loadingMapSets: 'Loading map sets...',
  roomFormatsTitle: 'Room Formats', formatsDescriptionA: "A room format tells the mapper how to recognize a room block in the MUD's output: the exits line, where the block starts and ends, how the left column (the ASCII minimap) is cut off, and which line is the room name. Each session picks one on the Sessions tab; the builtin",
  formatsDescriptionB: 'format is used when none is chosen.', formatsCustomBefore: 'Text rules and ANSI-delimited room names can be configured here. Formats that need a custom state machine can use a', formatsCustomDetector: 'contributed Go detector', formatsCustomAfter: ': a small reviewed file compiled into rubymud. Such a detector is listed here read-only, because there is no editable spec behind it.',
  formatValid: 'Format is valid.', formatNamePlaceholder: 'Format name (e.g. rmud-custom)', formatNameAria: 'Room format name', newFormat: 'New format', editingNumber: 'Editing #{id}',
  exits: 'Exits', guardLabel: 'Guard (literal prefix)', exitsGuardAria: 'Exits guard', noneToken: 'None token', exitsNoneAria: 'Exits none token', exitsPatternLabel: 'Exits pattern (regex, exactly one capture group)', exitsPatternAria: 'Exits pattern',
  alternativeGuard: 'Alternative guard', alternativePattern: 'Alternative pattern', alternativeGuardAria: 'Alternative exits guard {index}', alternativePatternAria: 'Alternative exits pattern {index}', removeAlternative: 'Remove exits alternative', removeAlternativeAria: 'Remove exits alternative {index}', maxAlternativesTitle: 'Maximum {count} alternative exits wordings', addAlternative: '+ Alternative exits wording', maxAlternativesReached: 'Maximum {count} alternative exits wordings reached.',
  block: 'Block', stopBlank: 'Stop at a blank line', stopPrompt: 'Stop at the prompt', stopMax: 'Stop at max lines', maxLines: 'Max lines', blockMaxAria: 'Block max lines', promptPatternLabel: 'Prompt pattern (regex; required when stopping at the prompt)', promptPlaceholder: "(empty — RMUD's boundary is the blank line)", promptAria: 'Prompt pattern',
  doorCommandLabel: 'Door-opening command (sent by a computed route before a door hop)', doorPlaceholder: '(empty — routes send nothing and doors are opened by hand)', doorAria: 'Door-opening command', doorHelpA: 'Use', doorHelpB: 'for the Russian direction word or', doorHelpC: 'for the letter (n/s/e/w/u/d) — e.g.', doorHelpD: 'on RMUD,', doorHelpE: 'on an English MUD. This is a command sent to the SERVER, so it follows the MUD, not the interface language. A per-edge command set in the map\'s edge editor always wins over it.',
  strip: 'Left-column strip', mode: 'Mode', stripModeAria: 'Strip mode', greedyOption: 'greedy — cut the whole leading glyph run', minRunOption: 'min_run — cut it only when long enough', fixedWidthOption: 'fixed_width — cut at a fixed column', glyphs: 'Glyphs', glyphsFixedNote: ' — not used by the fixed_width cut, but the header rule finds the room name through them', glyphsAria: 'Left column glyphs', minRun: 'Min run', stripMinRunAria: 'Strip min run', width: 'Width', stripWidthAria: 'Strip width',
  header: 'Header', rule: 'Rule', headerRuleAria: 'Header rule', lastBeforeOption: 'last_before_stripped — last line above the column', firstOption: 'first — the first non-blank line', ansiOption: 'ansi_delimited — text marked by an ANSI SGR', columnRun: 'Column run', headerColumnAria: 'Header column run', startSGR: 'Start SGR parameters', headerSGRAria: 'Header ANSI start SGR parameters',
  add: 'Add', save: 'Save', clear: 'Clear', replay: 'Replay', session: 'Session', replaySessionAria: 'Replay session', recentLines: 'Recent lines', replayLimitAria: 'Replay line limit', running: 'Running…', runReplay: 'Run replay', replaySummary: '{rooms} from {lines}; {candidates}.', replayMatchesAria: 'Replay matches', noRoomTitle: '(no room title)',
  reference: 'Reference', exitsGuard: 'Exits guard', stripColumn: 'Strip', headerColumn: 'Header', forMud: 'For: {mud}', goDetectorReadOnly: 'Go detector compiled into rubymud — read-only, and it cannot be copied into an editable format.', builtinReadOnly: 'read-only — copy it to make changes', makeCopy: 'Make a copy', editing: 'Editing…', edit: 'Edit', copy: 'Copy',
  builtinSuffix: '(builtin)', verifiedLabel: 'Verified', starterLabel: 'Starter', contributedLabel: 'Contributed Go detector', userLabel: 'User', verifiedNote: 'field-tested against a live server.', starterNote: 'a plausible format nobody has run against a live server. Copy it and adjust.', contributedNote: "correctness is exactly its test fixtures'.", userNote: 'Your own format.',
  validationFailed: 'Validation failed (HTTP {status}).', formatInvalid: 'Room format is not valid.', replayFailed: 'Replay failed (HTTP {status}).', formatNameRequired: 'A format name is required.', saveFormatFailed: 'Could not save the room format (HTTP {status}).', discardFormatConfirm: 'Discard unsaved room format changes?', deleteFormatConfirm: 'Delete this room format? Sessions using it will fall back to the builtin.', deleteFormatFailed: 'Could not delete the room format (HTTP {status}).',
  sessionMapSetHeading: 'Map Set for {session}', sessionMapSetDescriptionBefore: 'Choose the world map this session follows. The', sessionMapSetDescriptionAfter: "buffer renders it and tracks the player's position.", mapSetAria: 'Map set', none: '(none)', mapSetError: 'Map set error: {error}',
  sessionRoomFormatHeading: 'Room Format for {session}', sessionFormatDescriptionA: "How this session's output is parsed into rooms. Manage the formats on the", sessionFormatDescriptionB: 'tab. Changing this applies to a running session immediately.', roomFormatAria: 'Room format', defaultFormat: 'Default — follow whichever format ships as the default (currently {format})', builtInPinned: 'Built in — pinned to this exact format', yourFormats: 'Your formats', sessionGoDetector: 'This is a Go detector compiled into rubymud. It is read-only and cannot be copied into an editable format — there is no regex spec behind it.', formatNotApplied: 'Room format not applied: {error}', currentlyUsing: 'Currently using:', roomFormatError: 'Room format error: {error}', formatReference: '{reference}',
} as const;

export type SettingsMapperMessageKey = keyof typeof en;
type Catalog = Readonly<Record<SettingsMapperMessageKey, string>>;

const ru = {
  tabRoomFormats: 'Форматы комнат', tabMapSets: 'Наборы карт',
  mapSetsTitle: 'Наборы карт', mapSetsDescription: 'Импортируйте ZIP-архив как новый набор карт, затем выберите его для сессии на вкладке «Сессии».',
  mapZipAria: 'ZIP-архив карты', importing: 'Импорт…', importZip: 'Импортировать ZIP', chooseZip: 'Выберите ZIP-архив.', onlyZip: 'Поддерживаются только архивы карт .zip.', uploadFailed: 'Не удалось загрузить ({status})', deleteMapSetConfirm: 'Удалить набор карт «{name}»? Его комнаты и созданные изображения комнат также будут удалены.', deleteFailed: 'Не удалось удалить ({status})', cleanupWarning: 'Набор карт удалён, но очистка изображений требует внимания: {error}',
  unresolvedSeams: 'Неразрешённые переходы: {seams}', conflicts: 'Конфликты: {conflicts}', conflictTruncated: 'Отчёт о конфликтах был сокращён.', duplicateZone: 'дубликат зоны', duplicateRoom: 'дубликат комнаты', conflictFirst: 'первый источник: {source}', conflictAgain: 'повторно: {source}',
  name: 'Название', map: 'Карта', import: 'Импорт', actions: 'Действия', manual: 'вручную', deleting: 'Удаление…', delete: 'Удалить', noMapSets: 'Наборы карт ещё не импортированы.', loadingMapSets: 'Загрузка наборов карт…',
  roomFormatsTitle: 'Форматы комнат', formatsDescriptionA: 'Формат комнаты определяет, как маппер распознаёт блок комнаты в выводе MUD: строку выходов, начало и конец блока, отсечение левого столбца (ASCII-карты) и строку с названием комнаты. Для каждой сессии формат выбирается на вкладке «Сессии»; встроенный формат', formatsDescriptionB: 'используется, если ничего не выбрано.', formatsCustomBefore: 'Здесь настраиваются текстовые правила и названия комнат, выделенные ANSI. Для форматов с собственным конечным автоматом можно использовать', formatsCustomDetector: 'внесённый в проект Go-детектор', formatsCustomAfter: ': небольшой проверенный файл, встроенный в rubymud. Такой детектор доступен здесь только для чтения, поскольку у него нет редактируемой спецификации.',
  formatValid: 'Формат корректен.', formatNamePlaceholder: 'Название формата (например, rmud-custom)', formatNameAria: 'Название формата комнаты', newFormat: 'Новый формат', editingNumber: 'Редактируется #{id}',
  exits: 'Выходы', guardLabel: 'Маркер (буквальный префикс)', exitsGuardAria: 'Маркер выходов', noneToken: 'Маркер отсутствия', exitsNoneAria: 'Маркер отсутствия выходов', exitsPatternLabel: 'Шаблон выходов (регулярное выражение, ровно одна группа захвата)', exitsPatternAria: 'Шаблон выходов', alternativeGuard: 'Альтернативный маркер', alternativePattern: 'Альтернативный шаблон', alternativeGuardAria: 'Альтернативный маркер выходов {index}', alternativePatternAria: 'Альтернативный шаблон выходов {index}', removeAlternative: 'Удалить альтернативу выходов', removeAlternativeAria: 'Удалить альтернативу выходов {index}', maxAlternativesTitle: 'Не более {count} альтернативных формулировок выходов', addAlternative: '+ Альтернативная формулировка выходов', maxAlternativesReached: 'Достигнут максимум альтернативных формулировок выходов: {count}.',
  block: 'Блок', stopBlank: 'Остановиться на пустой строке', stopPrompt: 'Остановиться на приглашении', stopMax: 'Остановиться по лимиту строк', maxLines: 'Максимум строк', blockMaxAria: 'Максимум строк блока', promptPatternLabel: 'Шаблон приглашения (регулярное выражение; обязателен при остановке на приглашении)', promptPlaceholder: '(пусто — граница RMUD определяется пустой строкой)', promptAria: 'Шаблон приглашения', doorCommandLabel: 'Команда открытия двери (маршрут отправит её перед переходом через дверь)', doorPlaceholder: '(пусто — маршрут ничего не отправляет, двери открываются вручную)', doorAria: 'Команда открытия двери', doorHelpA: 'Используйте', doorHelpB: 'для русского слова направления или', doorHelpC: 'для буквы (n/s/e/w/u/d), например', doorHelpD: 'в RMUD,', doorHelpE: 'в англоязычном MUD. Эта команда отправляется СЕРВЕРУ, поэтому зависит от MUD, а не от языка интерфейса. Команда конкретного ребра, заданная в редакторе карты, всегда имеет приоритет.',
  strip: 'Отсечение левого столбца', mode: 'Режим', stripModeAria: 'Режим отсечения', greedyOption: 'greedy — отсечь всю начальную последовательность глифов', minRunOption: 'min_run — отсечь только достаточно длинную последовательность', fixedWidthOption: 'fixed_width — отсечь по фиксированному столбцу', glyphs: 'Глифы', glyphsFixedNote: ' — не используются самим отсечением fixed_width, но по ним правило заголовка находит название комнаты', glyphsAria: 'Глифы левого столбца', minRun: 'Мин. длина', stripMinRunAria: 'Минимальная длина отсечения', width: 'Ширина', stripWidthAria: 'Ширина отсечения',
  header: 'Заголовок', rule: 'Правило', headerRuleAria: 'Правило заголовка', lastBeforeOption: 'last_before_stripped — последняя строка над столбцом', firstOption: 'first — первая непустая строка', ansiOption: 'ansi_delimited — текст, отмеченный ANSI SGR', columnRun: 'Длина столбца', headerColumnAria: 'Длина столбца заголовка', startSGR: 'Начальные параметры SGR', headerSGRAria: 'Начальные параметры ANSI SGR заголовка',
  add: 'Добавить', save: 'Сохранить', clear: 'Очистить', replay: 'Повтор', session: 'Сессия', replaySessionAria: 'Сессия для повтора', recentLines: 'Последние строки', replayLimitAria: 'Лимит строк повтора', running: 'Выполняется…', runReplay: 'Запустить повтор', replaySummary: '{rooms} из {lines}; {candidates}.', replayMatchesAria: 'Совпадения повтора', noRoomTitle: '(без названия комнаты)', reference: 'Ссылка', exitsGuard: 'Маркер выходов', stripColumn: 'Отсечение', headerColumn: 'Заголовок', forMud: 'Для: {mud}', goDetectorReadOnly: 'Go-детектор встроен в rubymud — только для чтения, его нельзя скопировать в редактируемый формат.', builtinReadOnly: 'только для чтения — скопируйте, чтобы изменить', makeCopy: 'Создать копию', editing: 'Редактируется…', edit: 'Изменить', copy: 'Копировать',
  builtinSuffix: '(встроенный)', verifiedLabel: 'Проверен', starterLabel: 'Заготовка', contributedLabel: 'Внесённый Go-детектор', userLabel: 'Свой', verifiedNote: 'испытан на реальном сервере.', starterNote: 'правдоподобный формат, который никто не проверял на реальном сервере. Скопируйте и настройте его.', contributedNote: 'корректность подтверждается только тестовыми примерами.', userNote: 'Ваш собственный формат.',
  validationFailed: 'Проверка не удалась (HTTP {status}).', formatInvalid: 'Формат комнаты некорректен.', replayFailed: 'Повтор не удался (HTTP {status}).', formatNameRequired: 'Укажите название формата.', saveFormatFailed: 'Не удалось сохранить формат комнаты (HTTP {status}).', discardFormatConfirm: 'Отменить несохранённые изменения формата комнаты?', deleteFormatConfirm: 'Удалить этот формат комнаты? Использующие его сессии вернутся к встроенному.', deleteFormatFailed: 'Не удалось удалить формат комнаты (HTTP {status}).',
  sessionMapSetHeading: 'Набор карт для {session}', sessionMapSetDescriptionBefore: 'Выберите карту мира для этой сессии. Буфер', sessionMapSetDescriptionAfter: 'отображает её и отслеживает позицию игрока.', mapSetAria: 'Набор карт', none: '(нет)', mapSetError: 'Ошибка набора карт: {error}', sessionRoomFormatHeading: 'Формат комнат для {session}', sessionFormatDescriptionA: 'Способ преобразования вывода этой сессии в комнаты. Управляйте форматами на вкладке', sessionFormatDescriptionB: 'Изменение немедленно применяется к запущенной сессии.', roomFormatAria: 'Формат комнаты', defaultFormat: 'По умолчанию — следовать за форматом, который поставляется как основной (сейчас {format})', builtInPinned: 'Встроенные — закрепить точный формат', yourFormats: 'Ваши форматы', sessionGoDetector: 'Это Go-детектор, встроенный в rubymud. Он доступен только для чтения и не может быть скопирован в редактируемый формат: за ним нет спецификации с регулярными выражениями.', formatNotApplied: 'Формат комнаты не применён: {error}', currentlyUsing: 'Сейчас используется:', roomFormatError: 'Ошибка формата комнаты: {error}', formatReference: '{reference}',
} as const satisfies Catalog;

const catalogs: Readonly<Record<Locale, Catalog>> = { en, ru };

const pluralEn = {
  zone: { one: '{count} zone', few: '{count} zones', many: '{count} zones' },
  room: { one: '{count} room', few: '{count} rooms', many: '{count} rooms' },
  seam: { one: '{count} seam', few: '{count} seams', many: '{count} seams' },
  line: { one: '{count} line', few: '{count} lines', many: '{count} lines' },
  lineAfterFrom: { one: '{count} line', few: '{count} lines', many: '{count} lines' },
  exitsCandidate: { one: '{count} exits candidate', few: '{count} exits candidates', many: '{count} exits candidates' },
} as const;
export type SettingsMapperPluralKey = keyof typeof pluralEn;
type PluralForms = Readonly<Record<'one' | 'few' | 'many', string>>;
type PluralCatalog = Readonly<Record<SettingsMapperPluralKey, PluralForms>>;
const pluralRu = {
  zone: { one: '{count} зона', few: '{count} зоны', many: '{count} зон' },
  room: { one: '{count} комната', few: '{count} комнаты', many: '{count} комнат' },
  seam: { one: '{count} переход', few: '{count} перехода', many: '{count} переходов' },
  line: { one: '{count} строка', few: '{count} строки', many: '{count} строк' },
  lineAfterFrom: { one: '{count} строки', few: '{count} строк', many: '{count} строк' },
  exitsCandidate: { one: '{count} строка-кандидат с выходами', few: '{count} строки-кандидата с выходами', many: '{count} строк-кандидатов с выходами' },
} as const satisfies PluralCatalog;
const plurals: Readonly<Record<Locale, PluralCatalog>> = { en: pluralEn, ru: pluralRu };

export interface SettingsMapperI18n {
  readonly locale: Locale;
  t(key: SettingsMapperMessageKey, values?: MessageValues): string;
  plural(key: SettingsMapperPluralKey, count: number, values?: MessageValues): string;
}

export function createSettingsMapperI18n(value: unknown): SettingsMapperI18n {
  const locale = normalizeLocale(value);
  return {
    locale,
    t: (key, values = {}) => interpolate(catalogs[locale][key], values),
    plural: (key, count, values = {}) => interpolate(plurals[locale][key][selectPluralForm(locale, count)], { count, ...values }),
  };
}

export function settingsMapperCatalogKeys(locale: Locale): SettingsMapperMessageKey[] { return (Object.keys(catalogs[locale]) as SettingsMapperMessageKey[]).sort(); }
export function settingsMapperPluralKeys(locale: Locale): SettingsMapperPluralKey[] { return (Object.keys(plurals[locale]) as SettingsMapperPluralKey[]).sort(); }
export function settingsMapperPlaceholders(locale: Locale, key: SettingsMapperMessageKey): string[] { return extractPlaceholders(catalogs[locale][key]); }
export function settingsMapperPluralPlaceholders(locale: Locale, key: SettingsMapperPluralKey): Record<'one' | 'few' | 'many', string[]> { const forms = plurals[locale][key]; return { one: extractPlaceholders(forms.one), few: extractPlaceholders(forms.few), many: extractPlaceholders(forms.many) }; }

export const settingsMapperI18n = createSettingsMapperI18n(readBootLocale());
