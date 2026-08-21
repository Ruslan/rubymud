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
  knowledge: 'Knowledge', roomFallback: 'Room', roomLine: '{room} · {zone} ({x},{y},{l})', description: 'Description', exits: 'Exits', noDescription: 'No description recorded.', noExits: 'No exits recorded.', inhabitants: 'Inhabitants',
  addNew: 'Add new', placeExisting: 'Place existing', close: 'Close', changedPending: 'Room data changed; the list will refresh after the form closes.',
  loading: 'Loading…', loadingMore: 'Loading more…', loadingPrevious: 'Loading previous page…', loadMore: 'Load more', previousPage: 'Previous page', retry: 'Retry', noInhabitants: 'No inhabitants have been recorded for this room.', listTruncated: 'Showing the first 20 inhabitants; the room has more recorded entries.', listMoreTruncated: 'Showing {count} inhabitants; more recorded entries are available.', loadFailed: 'Failed to load inhabitants: {error}', cursorExpired: 'The list changed; reload the first page.',
  residentBadge: 'resident', presence: 'Presence', lookTarget: 'Look target', lookResult: 'Look result', note: 'Note', strategy: 'Strategy', edit: 'Edit', delete: 'Delete',
  editInhabitant: 'Edit inhabitant', addInhabitant: 'Add inhabitant', kindNPC: 'NPC', kindMonster: 'Monster', kindOther: 'Other',
  name: 'Name', type: 'Type', interactionStrategy: 'Interaction strategy', presenceText: 'Presence text', permanentResident: 'Permanent resident of this room', cancel: 'Cancel', save: 'Save',
  catalogHeading: 'Place existing inhabitant', catalogSearchPlaceholder: 'Search by name or look target', catalogSearch: 'Search catalog', searching: 'Searching…', catalogItem: '{name} · {kind} · {placements}', place: 'Place', placeAria: 'Place {name}',
  noReusable: 'No reusable inhabitants found.', catalogTruncated: 'Showing the first 50 matches; refine the search.', catalogFailed: 'Failed to search catalog: {error}',
  placeHeading: 'Place {name}', back: 'Back', placing: 'Placing…', placeFailed: 'Failed to place: {error}', saving: 'Saving…', saveFailed: 'Failed to save: {error}',
  removeConfirm: 'Remove "{name}" from this room?', deleting: 'Deleting…', deleteFailed: 'Failed to delete: {error}',
} as const;

export type InhabitantsMessageKey = keyof typeof en;
type Catalog = Readonly<Record<InhabitantsMessageKey, string>>;

const ru = {
  knowledge: 'Знания', roomFallback: 'Комната', roomLine: '{room} · {zone} ({x},{y},{l})', description: 'Описание', exits: 'Выходы', noDescription: 'Описание не записано.', noExits: 'Выходы не записаны.', inhabitants: 'Обитатели',
  addNew: 'Добавить нового', placeExisting: 'Разместить существующего', close: 'Закрыть', changedPending: 'Данные комнаты изменились; список обновится после закрытия формы.',
  loading: 'Загрузка…', loadingMore: 'Загрузка ещё…', loadingPrevious: 'Загрузка предыдущей страницы…', loadMore: 'Загрузить ещё', previousPage: 'Предыдущая страница', retry: 'Повторить', noInhabitants: 'Для этой комнаты ещё не записаны обитатели.', listTruncated: 'Показаны первые 20 обитателей; для комнаты записано больше.', listMoreTruncated: 'Показаны {count} обитателей; доступны ещё записи.', loadFailed: 'Не удалось загрузить обитателей: {error}', cursorExpired: 'Список изменился; загрузите первую страницу.',
  residentBadge: 'постоянный', presence: 'Присутствие', lookTarget: 'Цель осмотра', lookResult: 'Результат осмотра', note: 'Заметка', strategy: 'Стратегия', edit: 'Изменить', delete: 'Удалить',
  editInhabitant: 'Изменить обитателя', addInhabitant: 'Добавить обитателя', kindNPC: 'NPC', kindMonster: 'Монстр', kindOther: 'Другое',
  name: 'Имя', type: 'Тип', interactionStrategy: 'Стратегия взаимодействия', presenceText: 'Текст присутствия', permanentResident: 'Постоянный обитатель этой комнаты', cancel: 'Отмена', save: 'Сохранить',
  catalogHeading: 'Разместить существующего обитателя', catalogSearchPlaceholder: 'Поиск по имени или цели осмотра', catalogSearch: 'Поиск в каталоге', searching: 'Поиск…', catalogItem: '{name} · {kind} · {placements}', place: 'Разместить', placeAria: 'Разместить: {name}',
  noReusable: 'Подходящие обитатели не найдены.', catalogTruncated: 'Показаны первые 50 совпадений; уточните поиск.', catalogFailed: 'Не удалось выполнить поиск по каталогу: {error}',
  placeHeading: 'Разместить: {name}', back: 'Назад', placing: 'Размещение…', placeFailed: 'Не удалось разместить: {error}', saving: 'Сохранение…', saveFailed: 'Не удалось сохранить: {error}',
  removeConfirm: 'Убрать «{name}» из этой комнаты?', deleting: 'Удаление…', deleteFailed: 'Не удалось удалить: {error}',
} as const satisfies Catalog;

const catalogs: Readonly<Record<Locale, Catalog>> = { en, ru };
const pluralEn = {
  placement: { one: '{count} placement', few: '{count} placements', many: '{count} placements' },
} as const;
export type InhabitantsPluralKey = keyof typeof pluralEn;
type PluralForms = Readonly<Record<'one' | 'few' | 'many', string>>;
type PluralCatalog = Readonly<Record<InhabitantsPluralKey, PluralForms>>;
const pluralRu = {
  placement: { one: '{count} размещение', few: '{count} размещения', many: '{count} размещений' },
} as const satisfies PluralCatalog;
const plurals: Readonly<Record<Locale, PluralCatalog>> = { en: pluralEn, ru: pluralRu };

export interface InhabitantsI18n {
  readonly locale: Locale;
  t(key: InhabitantsMessageKey, values?: MessageValues): string;
  plural(key: InhabitantsPluralKey, count: number, values?: MessageValues): string;
}

export function createInhabitantsI18n(value: unknown): InhabitantsI18n {
  const locale = normalizeLocale(value);
  return {
    locale,
    t: (key, values = {}) => interpolate(catalogs[locale][key], values),
    plural: (key, count, values = {}) => interpolate(plurals[locale][key][selectPluralForm(locale, count)], { count, ...values }),
  };
}

export function inhabitantsCatalogKeys(locale: Locale): InhabitantsMessageKey[] { return (Object.keys(catalogs[locale]) as InhabitantsMessageKey[]).sort(); }
export function inhabitantsPluralKeys(locale: Locale): InhabitantsPluralKey[] { return (Object.keys(plurals[locale]) as InhabitantsPluralKey[]).sort(); }
export function inhabitantsPlaceholders(locale: Locale, key: InhabitantsMessageKey): string[] { return extractPlaceholders(catalogs[locale][key]); }
export function inhabitantsPluralPlaceholders(locale: Locale, key: InhabitantsPluralKey): Record<'one' | 'few' | 'many', string[]> { const forms = plurals[locale][key]; return { one: extractPlaceholders(forms.one), few: extractPlaceholders(forms.few), many: extractPlaceholders(forms.many) }; }

export const inhabitantsI18n = createInhabitantsI18n(readBootLocale());
