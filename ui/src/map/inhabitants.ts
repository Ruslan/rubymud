import { fetchWithToken } from "../dom";
import { supportedLocale, type MessageValues } from "../i18n";
import { createInhabitantsI18n, type InhabitantsI18n, type InhabitantsMessageKey, type InhabitantsPluralKey } from "./inhabitantsI18n";

export type InhabitantKind = "npc" | "monster" | "other";

export interface RoomInhabitant {
  id: number;
  placement_id: number;
  map_set_id: number;
  zone: string;
  x: number;
  y: number;
  l: number;
  name: string;
  kind: InhabitantKind;
  look_target: string;
  look_text: string;
  note: string;
  strategy: string;
  presence_text: string;
  resident: boolean;
  updated_at?: string | null;
  placement_updated_at?: string | null;
}

interface InhabitantCatalogItem {
  id: number;
  map_set_id: number;
  name: string;
  kind: InhabitantKind;
  look_target: string;
  strategy: string;
  placement_count: number;
}

export interface KnowledgeRoom {
  mapSetID: number;
  zone: string;
  x: number;
  y: number;
  l: number;
  name: string;
}

const textLimit = 2048;
const nameLimit = 256;
const roomPageLimit = 20;
const legacyRoomInhabitantCursorCode = 'legacy_room_inhabitant_cursor';
let knowledgePanelSequence = 0;

type RoomListLoadResult = 'loaded' | 'stale' | 'failed';

function sameRoom(a: KnowledgeRoom | null, mapSetID: number, zone: string, x: number, y: number, l: number): boolean {
  return !!a && a.mapSetID === mapSetID && a.zone === zone && a.x === x && a.y === y && a.l === l;
}

function roomParams(room: KnowledgeRoom): URLSearchParams {
  return new URLSearchParams({
    zone: room.zone,
    x: String(room.x),
    y: String(room.y),
    l: String(room.l),
  });
}

function labelWrap(text: string, control: HTMLElement): HTMLLabelElement {
  const label = document.createElement("label");
  const caption = document.createElement("span");
  caption.textContent = text;
  label.append(caption, control);
  return label;
}

function textInput(name: string, value: string, maxLength = textLimit): HTMLInputElement {
  const input = document.createElement("input");
  input.type = "text";
  input.name = name;
  input.value = value;
  input.maxLength = maxLength;
  return input;
}

function textArea(name: string, value: string): HTMLTextAreaElement {
  const area = document.createElement("textarea");
  area.name = name;
  area.value = value;
  area.maxLength = textLimit;
  area.rows = 3;
  return area;
}

// Owns the room-scoped Knowledge → Inhabitants surface. It has no zone cache and
// performs no eager request: open() is the sole initial load boundary.
export class RoomKnowledgeDialog {
  private backdrop: HTMLElement | null = null;
  private dialog: HTMLDialogElement | null = null;
  private room: KnowledgeRoom | null = null;
  private rows: RoomInhabitant[] = [];
  private listCursor: string | null = null;
  private listPageStarts: Array<string | null> = [null];
  private listPage = 0;
  private listHasMore = false;
  private listLoading = false;
  private list: HTMLElement | null = null;
  private message: HTMLElement | null = null;
  private addButton: HTMLButtonElement | null = null;
	private placeExistingButton: HTMLButtonElement | null = null;
  private retryButton: HTMLButtonElement | null = null;
  private previousButton: HTMLButtonElement | null = null;
  private loadMoreButton: HTMLButtonElement | null = null;
  private form: HTMLFormElement | null = null;
  private catalog: HTMLElement | null = null;
  private loadRequest: AbortController | null = null;
	private catalogRequest: AbortController | null = null;
  private writeRequest: AbortController | null = null;
	private catalogTimer: ReturnType<typeof setTimeout> | null = null;
  private generation = 0;
  private disposed = false;
  private pendingRefresh = false;
  private returnFocus: HTMLElement | null = null;
  private i18n: InhabitantsI18n;

  constructor(private readonly host: HTMLElement, locale?: unknown) {
    this.i18n = createInhabitantsI18n(locale);
  }

  setLocale(locale: unknown): boolean {
    const accepted = supportedLocale(locale);
    if (!accepted || accepted === this.i18n.locale) return false;
    this.i18n = createInhabitantsI18n(accepted);
    // Every authored node in the dialog carries its catalog key and raw values.
    // Updating those nodes in place preserves unsaved controls, pending writes,
    // the current catalog query, and focus while avoiding mixed-language copy.
    this.translateMarked();
    return true;
  }

  private copy(element: HTMLElement, key: InhabitantsMessageKey, values: MessageValues = {}): void {
    element.dataset.i18nKey = key;
    element.dataset.i18nValues = JSON.stringify(values);
    element.textContent = this.i18n.t(key, this.resolveValues(element, values));
  }

  private resolveValues(element: HTMLElement, values: MessageValues): MessageValues {
    return element.dataset.i18nRoomFallback === 'true'
      ? { ...values, room: this.i18n.t('roomFallback') }
      : values;
  }

  private plural(element: HTMLElement, key: InhabitantsPluralKey, count: number): void {
    element.dataset.i18nPlural = key;
    element.dataset.i18nCount = String(count);
    element.textContent = this.i18n.plural(key, count);
  }

  private translateMarked(): void {
    if (!this.dialog) return;
    for (const element of this.dialog.querySelectorAll<HTMLElement>('[data-i18n-key]')) {
      const key = element.dataset.i18nKey as InhabitantsMessageKey;
      const values = JSON.parse(element.dataset.i18nValues || '{}') as MessageValues;
      element.textContent = this.i18n.t(key, this.resolveValues(element, values));
    }
    for (const element of this.dialog.querySelectorAll<HTMLElement>('[data-i18n-plural]')) {
      element.textContent = this.i18n.plural(
        element.dataset.i18nPlural as InhabitantsPluralKey,
        Number(element.dataset.i18nCount),
      );
    }
    this.dialog.setAttribute('aria-label', this.i18n.t('knowledge'));
    for (const element of this.dialog.querySelectorAll<HTMLElement>('[data-i18n-aria]')) {
      const key = element.dataset.i18nAria as InhabitantsMessageKey;
      const values = JSON.parse(element.dataset.i18nAriaValues || '{}') as MessageValues;
      element.setAttribute('aria-label', this.i18n.t(key, values));
    }
    for (const element of this.dialog.querySelectorAll<HTMLInputElement>('[data-i18n-placeholder]')) {
      element.placeholder = this.i18n.t(element.dataset.i18nPlaceholder as InhabitantsMessageKey);
    }
  }

  private labelled(key: InhabitantsMessageKey, control: HTMLElement): HTMLLabelElement {
    const label = labelWrap('', control);
    this.copy(label.firstElementChild as HTMLElement, key);
    return label;
  }

  open(room: KnowledgeRoom, returnFocus?: HTMLElement): void {
    if (this.disposed) return;
    this.close(false);
    this.returnFocus = returnFocus ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    this.room = room;
    const backdrop = document.createElement("div");
    backdrop.className = "map-knowledge-dialog-backdrop";
    const dialog = document.createElement("dialog");
    dialog.className = "map-knowledge-dialog";
    dialog.setAttribute("role", "dialog");
    dialog.setAttribute("aria-modal", "true");
    dialog.setAttribute("aria-label", this.i18n.t('knowledge'));
    backdrop.appendChild(dialog);

    const title = document.createElement("h2");
    this.copy(title, 'knowledge');
    const roomLine = document.createElement("div");
    roomLine.className = "map-knowledge-dialog__room";
    if (!room.name) roomLine.dataset.i18nRoomFallback = 'true';
    this.copy(roomLine, 'roomLine', { room: room.name, zone: room.zone, x: room.x, y: room.y, l: room.l });
    const tabs = document.createElement("div");
    tabs.className = "map-knowledge-dialog__tabs";
    tabs.setAttribute("role", "tablist");
    const inhabitantsTab = document.createElement("button");
    inhabitantsTab.type = "button";
    inhabitantsTab.setAttribute("role", "tab");
    inhabitantsTab.setAttribute("aria-selected", "true");
    this.copy(inhabitantsTab, 'inhabitants');
    tabs.appendChild(inhabitantsTab);

    const panel = document.createElement("div");
    panel.className = "map-knowledge-dialog__panel";
    panel.setAttribute("role", "tabpanel");
    const panelID = `map-knowledge-inhabitants-${++knowledgePanelSequence}`;
    panel.id = panelID;
    inhabitantsTab.setAttribute("aria-controls", panelID);

    this.message = document.createElement("div");
    this.message.className = "map-knowledge-dialog__message";
    this.message.setAttribute("role", "status");
    this.message.setAttribute("aria-live", "polite");
    this.list = document.createElement("div");
    this.list.className = "map-knowledge-dialog__list";
    this.previousButton = document.createElement("button");
    this.previousButton.type = "button";
    this.previousButton.className = "map-knowledge-dialog__previous";
    this.previousButton.addEventListener("click", () => void this.load(false, this.listPage - 1));
    this.loadMoreButton = document.createElement("button");
    this.loadMoreButton.type = "button";
    this.loadMoreButton.className = "map-knowledge-dialog__load-more";
    this.loadMoreButton.addEventListener("click", () => void this.load(false, this.listPage + 1));
    const pageNavigation = document.createElement("div");
    pageNavigation.className = "map-knowledge-dialog__pagination";
    pageNavigation.append(this.previousButton, this.loadMoreButton);

    const actions = document.createElement("div");
    actions.className = "map-knowledge-dialog__actions";
    this.addButton = document.createElement("button");
    this.addButton.type = "button";
    this.copy(this.addButton, 'addNew');
    this.addButton.addEventListener("click", () => this.openForm(null));
    this.placeExistingButton = document.createElement("button");
	this.placeExistingButton.type = "button";
	this.copy(this.placeExistingButton, 'placeExisting');
	this.placeExistingButton.addEventListener("click", () => this.openCatalog());
    this.retryButton = document.createElement("button");
    this.retryButton.type = "button";
    this.retryButton.className = "map-knowledge-dialog__retry";
    this.retryButton.hidden = true;
    this.copy(this.retryButton, 'retry');
    this.retryButton.addEventListener("click", () => void this.retryList());
    const close = document.createElement("button");
    close.type = "button";
    this.copy(close, 'close');
    close.addEventListener("click", () => this.close());
    actions.append(this.addButton, this.placeExistingButton, this.retryButton, close);
    panel.append(this.message, this.list, pageNavigation, actions);
    dialog.append(title, roomLine, tabs, panel);

    backdrop.addEventListener("mousedown", (event) => {
      if (event.target === backdrop) this.close();
    });
    dialog.addEventListener("cancel", (event) => {
      event.preventDefault();
      this.close();
    });
    dialog.addEventListener("keydown", (event) => {
      if (event.key === "Escape") {
        event.preventDefault();
        this.close();
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = [...dialog.querySelectorAll<HTMLElement>(
        "button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled])",
      )];
      const first = focusable[0];
      const last = focusable.at(-1);
      if (!first || !last) return;
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    });

    this.backdrop = backdrop;
    this.dialog = dialog;
    this.host.appendChild(backdrop);
    if (typeof dialog.showModal === "function") {
      try { dialog.showModal(); } catch { /* positioned fallback */ }
    }
    queueMicrotask(() => inhabitantsTab.focus());
    void this.load();
  }

  close(_returnFocus = true): void {
    const returnFocus = this.returnFocus;
    this.loadRequest?.abort();
    this.loadRequest = null;
	this.clearCatalog();
    this.writeRequest?.abort();
    this.writeRequest = null;
    this.generation++;
    if (this.dialog?.open) this.dialog.close();
    this.backdrop?.remove();
    this.backdrop = null;
    this.dialog = null;
    this.room = null;
    this.rows = [];
    this.list = null;
    this.message = null;
    this.addButton = null;
	this.placeExistingButton = null;
    this.retryButton = null;
    this.previousButton = null;
    this.loadMoreButton = null;
    this.form = null;
    this.listCursor = null;
    this.listPageStarts = [null];
    this.listPage = 0;
    this.listHasMore = false;
    this.listLoading = false;
    this.pendingRefresh = false;
    this.returnFocus = null;
    if (_returnFocus) {
      queueMicrotask(() => {
        const target = returnFocus?.isConnected
          ? returnFocus
          : this.host.querySelector<HTMLElement>("button:not([disabled]), select:not([disabled]), input:not([disabled])");
        target?.focus();
      });
    }
  }

  dispose(): void {
    this.disposed = true;
    this.close(false);
  }

  changed(mapSetID: number, zone: string, x: number, y: number, l: number): void {
    if (!sameRoom(this.room, mapSetID, zone, x, y, l)) return;
    // Preserve unsaved edits when another UI/client changes the same room. The
    // latest server state is fetched as soon as this form is saved or cancelled.
    if (this.form) {
      this.pendingRefresh = true;
      this.setMessageKey('changedPending');
      return;
    }
    void this.load();
  }

  changedMapSet(mapSetID: number): void {
    if (!this.room || this.room.mapSetID !== mapSetID) return;
    if (this.form) {
      this.pendingRefresh = true;
      this.setMessageKey('changedPending');
      return;
    }
    void this.load();
  }

  private async load(reset = true, targetPage = 0, force = false): Promise<RoomListLoadResult> {
    const room = this.room;
    if (!room || !this.dialog) return 'stale';
    const page = reset ? 0 : targetPage;
    if (page < 0 || (!reset && !force && page === this.listPage)) return 'stale';
    const cursor = reset ? null : page === this.listPage + 1
      ? this.listCursor
      : this.listPageStarts[page];
    if (!reset && cursor === undefined) return 'stale';
    this.loadRequest?.abort();
    const controller = new AbortController();
    this.loadRequest = controller;
    const generation = ++this.generation;
    this.listLoading = true;
    if (this.retryButton) this.retryButton.hidden = true;
    this.setMessageKey(reset ? 'loading' : page > this.listPage ? 'loadingMore' : 'loadingPrevious');
    this.renderLoadMore();
    try {
      const params = roomParams(room);
      params.set('limit', String(roomPageLimit));
      if (cursor) params.set('cursor', cursor);
      const response = await fetchWithToken(
        `/api/map-sets/${room.mapSetID}/inhabitants?${params}`,
        { signal: controller.signal },
      );
      if (!response.ok) throw new Error(await response.text());
      const payload = await response.json() as RoomInhabitant[] | {
        items?: RoomInhabitant[];
        truncated?: boolean;
        next_cursor?: string;
      };
      if (!this.isCurrent(room, generation, controller)) return 'stale';
      // The envelope is the bounded REST contract. Accepting the historical
      // bare-array response keeps a pane usable while an older server is being
      // upgraded, but it is never emitted by the current server.
      const envelope = !Array.isArray(payload) && payload && typeof payload === "object" ? payload : null;
      const items = Array.isArray(payload) ? payload : Array.isArray(envelope?.items) ? envelope.items : [];
      // Keep one server page in the DOM. Previous/next page cursors make every
      // placement reachable without turning a pathological room into an
      // ever-growing card list.
      this.rows = items.slice(0, roomPageLimit)
        .sort((a, b) => a.name.localeCompare(b.name) || a.id - b.id);
      if (page === 0) this.listPageStarts = [null];
      else this.listPageStarts[page] = cursor ?? null;
      this.listPage = page;
      this.listCursor = typeof envelope?.next_cursor === 'string' && envelope.next_cursor !== ''
        ? envelope.next_cursor
        : null;
      const rowsTruncated = envelope?.truncated === true;
      this.listHasMore = rowsTruncated && !!this.listCursor && items.length > 0;
      if (this.rows.length === 0) this.setMessageKey('noInhabitants');
      else if (rowsTruncated) {
        this.setMessageKey(
          page === 0 ? 'listTruncated' : 'listMoreTruncated',
          { count: this.rows.length },
        );
      }
      else this.setMessage('');
      this.renderList();
      if (this.retryButton) this.retryButton.hidden = true;
      return 'loaded';
    } catch (error) {
      if (!this.isCurrent(room, generation, controller)) return 'stale';
      const detail = error instanceof Error ? error.message.trim() : String(error).trim();
      if (detail === legacyRoomInhabitantCursorCode && page > 0) {
        this.listCursor = null;
        this.listPageStarts = [null];
        this.listPage = 0;
        this.listHasMore = false;
        this.setMessageKey('cursorExpired', {}, true);
      } else {
        this.setMessageKey('loadFailed', { error: detail }, true);
      }
      if (this.retryButton) {
        this.retryButton.hidden = false;
        this.retryButton.focus();
      }
      return 'failed';
    } finally {
      if (this.loadRequest === controller) {
        this.loadRequest = null;
        this.listLoading = false;
        this.renderLoadMore();
      }
    }
  }

  private async retryList(): Promise<void> {
    if (!this.room || !this.dialog || this.form || this.catalog) return;
    const result = this.listPage === 0
      ? await this.load()
      : await this.load(false, this.listPage, true);
    if (result === 'failed') this.retryButton?.focus();
    else if (result === 'loaded') this.focusList();
  }

  private isCurrent(room: KnowledgeRoom, generation: number, controller: AbortController): boolean {
    return !this.disposed && !controller.signal.aborted && this.room === room && this.generation === generation && !!this.dialog;
  }

  private setMessage(text: string, error = false): void {
    if (!this.message) return;
    delete this.message.dataset.i18nKey;
    delete this.message.dataset.i18nValues;
    this.message.textContent = text;
    this.message.dataset.error = String(error);
  }

  private setMessageKey(key: InhabitantsMessageKey, values: MessageValues = {}, error = false): void {
    if (!this.message) return;
    this.copy(this.message, key, values);
    this.message.dataset.error = String(error);
  }

  private renderList(): void {
    if (!this.list) return;
    const restoreCatalogFocus = this.catalog !== null;
    this.form?.remove();
    this.form = null;
    this.clearCatalog();
    this.list.hidden = false;
    if (this.addButton) this.addButton.hidden = false;
    if (this.placeExistingButton) this.placeExistingButton.hidden = false;
    this.list.replaceChildren();
    for (const row of this.rows) this.list.appendChild(this.card(row));
    this.renderLoadMore();
    if (restoreCatalogFocus) this.placeExistingButton?.focus();
    if (this.pendingRefresh) {
      this.pendingRefresh = false;
      void this.load();
    }
  }

  private renderLoadMore(): void {
    if (!this.loadMoreButton || !this.previousButton) return;
    const wasNextFocused = document.activeElement === this.loadMoreButton;
    const wasPreviousFocused = document.activeElement === this.previousButton;
    this.previousButton.hidden = this.listPage === 0;
    this.previousButton.disabled = this.listLoading;
    this.copy(this.previousButton, this.listLoading ? 'loadingPrevious' : 'previousPage');
    this.loadMoreButton.hidden = !this.listHasMore;
    this.loadMoreButton.disabled = this.listLoading;
    this.copy(this.loadMoreButton, this.listLoading ? 'loadingMore' : 'loadMore');
    if ((wasNextFocused && this.loadMoreButton.hidden) || (wasPreviousFocused && this.previousButton.hidden)) {
      const target = this.list?.querySelector<HTMLButtonElement>('button');
      if (target) target.focus();
      else this.addButton?.focus();
    }
  }

  private mutationNeedsPageReload(): boolean {
    return this.rows.length >= roomPageLimit || this.listPage > 0 || this.listHasMore;
  }

  private closeFormAfterRefreshFailure(): void {
    this.form?.remove();
    this.form = null;
    this.clearCatalog();
    if (this.list) this.list.hidden = false;
    if (this.addButton) this.addButton.hidden = false;
    if (this.placeExistingButton) this.placeExistingButton.hidden = false;
    this.renderList();
    this.retryButton?.focus();
  }

  private focusList(preferredID?: number): void {
    const preferred = preferredID == null
      ? null
      : this.list?.querySelector<HTMLButtonElement>(`[data-inhabitant-id="${preferredID}"] button`);
    const target = preferred || this.list?.querySelector<HTMLButtonElement>('.map-inhabitant-card button')
      || (this.listPage > 0 ? this.previousButton : this.addButton);
    target?.focus();
  }

  private async reloadAfterMutation(preferredID?: number, targetPage = this.listPage): Promise<RoomListLoadResult> {
    this.pendingRefresh = false;
    const result = targetPage <= 0
      ? await this.load()
      : await this.load(false, targetPage, true);
    if (result === 'failed') this.closeFormAfterRefreshFailure();
    else if (result === 'loaded') this.focusList(preferredID);
    return result;
  }

  private async reloadAfterDelete(): Promise<RoomListLoadResult> {
    const page = this.listPage;
    const result = await this.reloadAfterMutation(undefined, page);
    if (result !== 'loaded') return result;
    if (this.rows.length === 0 && page > 0) {
      return this.reloadAfterMutation(undefined, page - 1);
    }
    return 'loaded';
  }

  private card(row: RoomInhabitant): HTMLElement {
    const card = document.createElement("article");
    card.className = "map-inhabitant-card";
    card.dataset.inhabitantId = String(row.id);
    const header = document.createElement("div");
    header.className = "map-inhabitant-card__header";
    const name = document.createElement("strong");
    name.textContent = row.name;
    const kind = document.createElement("span");
    kind.className = "map-inhabitant-card__kind";
    kind.textContent = row.kind;
    header.append(name, kind);
    if (row.resident) {
      const resident = document.createElement("span");
      resident.className = "map-inhabitant-card__resident";
      this.copy(resident, 'residentBadge');
      header.appendChild(resident);
    }
    card.appendChild(header);
    this.appendDetail(card, 'presence', row.presence_text);
    this.appendDetail(card, 'lookTarget', row.look_target);
    this.appendDetail(card, 'lookResult', row.look_text);
    this.appendDetail(card, 'note', row.note);
    this.appendDetail(card, 'strategy', row.strategy);
    const actions = document.createElement("div");
    actions.className = "map-inhabitant-card__actions";
    const edit = document.createElement("button");
    edit.type = "button";
    this.copy(edit, 'edit');
    edit.addEventListener("click", () => this.openForm(row));
    const remove = document.createElement("button");
    remove.type = "button";
    this.copy(remove, 'delete');
    remove.addEventListener("click", () => void this.remove(row));
    actions.append(edit, remove);
    card.appendChild(actions);
    return card;
  }

  private appendDetail(card: HTMLElement, key: InhabitantsMessageKey, value: string): void {
    if (!value) return;
    const line = document.createElement("div");
    line.className = "map-inhabitant-card__detail";
    const caption = document.createElement("span");
    this.copy(caption, key);
    line.append(caption, document.createTextNode(': '), document.createTextNode(value));
    card.appendChild(line);
  }

  private openForm(row: RoomInhabitant | null): void {
    if (!this.dialog || !this.list || !this.room) return;
    if (this.loadRequest) {
      this.loadRequest.abort();
      this.loadRequest = null;
      this.generation++;
      this.pendingRefresh = true;
    }
    this.writeRequest?.abort();
    this.writeRequest = null;
    this.clearCatalog();
    this.form?.remove();
    this.list.hidden = true;
    if (this.previousButton) this.previousButton.hidden = true;
    if (this.loadMoreButton) this.loadMoreButton.hidden = true;
    if (this.addButton) this.addButton.hidden = true;
	if (this.placeExistingButton) this.placeExistingButton.hidden = true;
    if (this.retryButton) this.retryButton.hidden = true;
    const form = document.createElement("form");
    form.className = "map-inhabitant-form";
    const heading = document.createElement("h3");
    this.copy(heading, row ? 'editInhabitant' : 'addInhabitant');
    form.appendChild(heading);

    const name = textInput("name", row?.name ?? "", nameLimit);
    name.required = true;
    const kind = document.createElement("select");
    kind.name = "kind";
    for (const [value, key] of [["npc", "kindNPC"], ["monster", "kindMonster"], ["other", "kindOther"]] as const) {
      const option = document.createElement("option");
      option.value = value;
      this.copy(option, key);
      kind.appendChild(option);
    }
    kind.value = row?.kind ?? "npc";
    const target = textInput("look_target", row?.look_target ?? "");
    const look = textArea("look_text", row?.look_text ?? "");
    const note = textArea("note", row?.note ?? "");
    const strategy = textArea("strategy", row?.strategy ?? "");
    const presence = textArea("presence_text", row?.presence_text ?? "");
    const resident = document.createElement("input");
    resident.type = "checkbox";
    resident.name = "resident";
    resident.checked = row?.resident ?? false;
    const residentLabel = document.createElement("label");
    residentLabel.className = "map-inhabitant-form__resident";
    const residentCaption = document.createElement('span');
    this.copy(residentCaption, 'permanentResident');
    residentLabel.append(resident, document.createTextNode(' '), residentCaption);
    form.append(
      this.labelled('name', name), this.labelled('type', kind), this.labelled('lookTarget', target),
      this.labelled('lookResult', look), this.labelled('note', note),
      this.labelled('interactionStrategy', strategy), this.labelled('presenceText', presence),
      residentLabel,
    );
    const message = document.createElement("div");
    message.className = "map-inhabitant-form__message";
    message.setAttribute("role", "status");
    form.appendChild(message);
    const actions = document.createElement("div");
    actions.className = "map-inhabitant-form__actions";
    const cancel = document.createElement("button");
    cancel.type = "button";
    this.copy(cancel, 'cancel');
    cancel.addEventListener("click", () => this.renderList());
    const save = document.createElement("button");
    save.type = "submit";
    this.copy(save, 'save');
    actions.append(cancel, save);
    form.appendChild(actions);
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      if (!form.reportValidity()) return;
      void this.save(row, form, message);
    });
    this.form = form;
    this.list.after(form);
    queueMicrotask(() => name.focus());
  }

	private openCatalog(): void {
	  if (!this.dialog || !this.list || !this.room) return;
	  if (this.loadRequest) {
		this.loadRequest.abort();
		this.loadRequest = null;
		this.generation++;
		this.pendingRefresh = true;
	  }
	  this.writeRequest?.abort();
	  this.writeRequest = null;
	  this.clearCatalog();
	  this.form?.remove();
	  this.form = null;
	  this.list.hidden = true;
	  if (this.previousButton) this.previousButton.hidden = true;
	  if (this.loadMoreButton) this.loadMoreButton.hidden = true;
	  if (this.addButton) this.addButton.hidden = true;
	  if (this.placeExistingButton) this.placeExistingButton.hidden = true;
	  if (this.retryButton) this.retryButton.hidden = true;

	  const panel = document.createElement("section");
	  panel.className = "map-inhabitant-catalog";
	  this.catalog = panel;
	  const heading = document.createElement("h3");
	  this.copy(heading, 'catalogHeading');
	  const search = textInput("catalog_query", "", nameLimit);
	  search.dataset.i18nPlaceholder = 'catalogSearchPlaceholder';
	  search.placeholder = this.i18n.t('catalogSearchPlaceholder');
	  const results = document.createElement("div");
	  results.className = "map-inhabitant-catalog__results";
	  const status = document.createElement("div");
	  status.className = "map-inhabitant-form__message";
	  status.setAttribute("role", "status");
	  const cancel = document.createElement("button");
	  cancel.type = "button";
	  this.copy(cancel, 'cancel');
	  cancel.addEventListener("click", () => {
		this.renderList();
		this.placeExistingButton?.focus();
	  });
	  panel.append(heading, this.labelled('catalogSearch', search), status, results, cancel);
	  this.list.after(panel);

	  const loadCatalog = async (): Promise<void> => {
		const room = this.room;
		if (!room || !panel.isConnected) return;
		this.catalogRequest?.abort();
		const controller = new AbortController();
		this.catalogRequest = controller;
		this.copy(status, 'searching');
		const params = new URLSearchParams({
		  query: search.value.trim(), limit: "50",
		  exclude_zone: room.zone, exclude_x: String(room.x), exclude_y: String(room.y), exclude_l: String(room.l),
		});
		try {
		  const response = await fetchWithToken(`/api/map-sets/${room.mapSetID}/inhabitants/catalog?${params}`, { signal: controller.signal });
		  if (!response.ok) throw new Error(await response.text());
		  const payload = await response.json() as { items?: InhabitantCatalogItem[]; truncated?: boolean };
		  if (controller.signal.aborted || this.room !== room || !panel.isConnected) return;
		  const items = Array.isArray(payload.items) ? payload.items : [];
		  results.replaceChildren();
		  for (const item of items) {
			const card = document.createElement("article");
			card.className = "map-inhabitant-catalog__item";
			const label = document.createElement("div");
			label.append(document.createTextNode(`${item.name} · ${item.kind} · `));
			const placements = document.createElement('span');
			this.plural(placements, 'placement', item.placement_count);
			label.appendChild(placements);
			const place = document.createElement("button");
			place.type = "button";
			this.copy(place, 'place');
			place.dataset.i18nAria = 'placeAria';
			place.dataset.i18nAriaValues = JSON.stringify({ name: item.name });
			place.setAttribute("aria-label", this.i18n.t('placeAria', { name: item.name }));
			place.addEventListener("click", () => this.openPlacementForm(item, panel, place));
			card.append(label, place);
			results.appendChild(card);
		  }
		  if (items.length === 0) this.copy(status, 'noReusable');
		  else if (payload.truncated) this.copy(status, 'catalogTruncated');
		  else { delete status.dataset.i18nKey; delete status.dataset.i18nValues; status.textContent = ''; }
		} catch (error) {
		  if (!controller.signal.aborted && this.room === room && panel.isConnected) {
			this.copy(status, 'catalogFailed', { error: error instanceof Error ? error.message : String(error) });
		  }
		} finally {
		  if (this.catalogRequest === controller) this.catalogRequest = null;
		}
	  };
	  search.addEventListener("input", () => {
		if (this.catalogTimer) clearTimeout(this.catalogTimer);
		this.catalogTimer = setTimeout(() => { void loadCatalog(); }, 150);
	  });
	  queueMicrotask(() => search.focus());
	  void loadCatalog();
	}

	private openPlacementForm(item: InhabitantCatalogItem, catalog: HTMLElement, opener: HTMLButtonElement): void {
	  if (!this.room || this.catalog !== catalog || !catalog.isConnected || !this.list) return;
	  if (this.loadRequest) {
		this.loadRequest.abort();
		this.loadRequest = null;
		this.generation++;
		this.pendingRefresh = true;
	  }
	  this.catalogRequest?.abort();
	  catalog.hidden = true;
	  if (this.previousButton) this.previousButton.hidden = true;
	  if (this.loadMoreButton) this.loadMoreButton.hidden = true;
	  if (this.retryButton) this.retryButton.hidden = true;
	  const form = document.createElement("form");
	  form.className = "map-inhabitant-form map-inhabitant-placement-form";
	  const heading = document.createElement("h3");
	  this.copy(heading, 'placeHeading', { name: item.name });
	  const presence = textArea("presence_text", "");
	  const resident = document.createElement("input");
	  resident.type = "checkbox";
	  resident.name = "resident";
	  const residentLabel = document.createElement("label");
	  residentLabel.className = "map-inhabitant-form__resident";
	  const residentCaption = document.createElement('span');
	  this.copy(residentCaption, 'permanentResident');
	  residentLabel.append(resident, document.createTextNode(' '), residentCaption);
	  const message = document.createElement("div");
	  message.className = "map-inhabitant-form__message";
	  message.setAttribute("role", "status");
	  const actions = document.createElement("div");
	  actions.className = "map-inhabitant-form__actions";
	  const back = document.createElement("button");
	  back.type = "button";
	  this.copy(back, 'back');
	  back.addEventListener("click", () => {
		if (this.pendingRefresh) {
		  this.renderList();
		  this.placeExistingButton?.focus();
		  return;
		}
		form.remove();
		catalog.hidden = false;
		this.form = null;
		opener.focus();
	  });
	  const save = document.createElement("button");
	  save.type = "submit";
	  this.copy(save, 'place');
	  actions.append(back, save);
	  form.append(heading, this.labelled('presenceText', presence), residentLabel, message, actions);
	  form.addEventListener("submit", (event) => {
		event.preventDefault();
		void this.placeExisting(item, form, message);
	  });
	  this.form = form;
	  catalog.after(form);
	  queueMicrotask(() => presence.focus());
	}

	private async placeExisting(item: InhabitantCatalogItem, form: HTMLFormElement, message: HTMLElement): Promise<void> {
	  const room = this.room;
	  if (!room || this.form !== form) return;
	  const controls = [...form.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLButtonElement>("input, textarea, button")];
	  controls.forEach((control) => { control.disabled = true; });
	  const controller = new AbortController();
	  this.writeRequest?.abort();
	  this.writeRequest = controller;
	  this.copy(message, 'placing');
	  try {
		const response = await fetchWithToken(`/api/map-sets/${room.mapSetID}/inhabitants`, {
		  method: "POST", headers: { "Content-Type": "application/json" }, signal: controller.signal,
		  body: JSON.stringify({
			inhabitant_id: item.id, zone: room.zone, x: room.x, y: room.y, l: room.l,
			presence_text: (form.elements.namedItem("presence_text") as HTMLTextAreaElement).value,
			resident: (form.elements.namedItem("resident") as HTMLInputElement).checked,
		  }),
		});
		if (!response.ok) throw new Error(await response.text());
		const saved = await response.json() as RoomInhabitant;
		if (controller.signal.aborted || this.room !== room || this.form !== form) return;
		const page = this.listPage;
		if (this.mutationNeedsPageReload()) {
			// A catalog placement can have an id below the current page's keyset
			// boundary. Rebuild page zero so both the visible slice and all saved
			// page starts are authoritative before allowing further traversal.
			await this.reloadAfterMutation(saved.id, page > 0 ? 0 : page);
		} else {
			this.rows = [...this.rows, saved]
			  .sort((a, b) => a.name.localeCompare(b.name) || a.id - b.id)
			  .slice(0, roomPageLimit);
			this.setMessage("");
			this.renderList();
			this.list?.querySelector<HTMLButtonElement>(`[data-inhabitant-id="${saved.id}"] button`)?.focus();
		}
	  } catch (error) {
		if (controller.signal.aborted || this.room !== room || this.form !== form) return;
		this.copy(message, 'placeFailed', { error: error instanceof Error ? error.message : String(error) });
		controls.forEach((control) => { control.disabled = false; });
	  } finally {
		if (this.writeRequest === controller) this.writeRequest = null;
	  }
	}

	private clearCatalog(): void {
	  this.catalogRequest?.abort();
	  this.catalogRequest = null;
	  if (this.catalogTimer) clearTimeout(this.catalogTimer);
	  this.catalogTimer = null;
	  this.catalog?.remove();
	  this.catalog = null;
	}

  private formBody(form: HTMLFormElement): Record<string, unknown> {
    const room = this.room!;
    const value = (name: string) => (form.elements.namedItem(name) as HTMLInputElement | HTMLTextAreaElement).value;
    return {
      zone: room.zone, x: room.x, y: room.y, l: room.l,
      name: value("name").trim(),
      kind: (form.elements.namedItem("kind") as HTMLSelectElement).value,
      look_target: value("look_target").trim(), look_text: value("look_text"),
      note: value("note"), strategy: value("strategy"), presence_text: value("presence_text"),
      resident: (form.elements.namedItem("resident") as HTMLInputElement).checked,
    };
  }

  private async save(row: RoomInhabitant | null, form: HTMLFormElement, message: HTMLElement): Promise<void> {
    const room = this.room;
    if (!room || !this.dialog || this.form !== form) return;
    this.writeRequest?.abort();
    const controller = new AbortController();
    this.writeRequest = controller;
    const controls = [...form.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement | HTMLButtonElement>("input, textarea, select, button")];
    controls.forEach((control) => { control.disabled = true; });
    this.copy(message, 'saving');
    const path = row
      ? `/api/map-sets/${room.mapSetID}/inhabitants/${row.id}`
      : `/api/map-sets/${room.mapSetID}/inhabitants`;
    try {
      const response = await fetchWithToken(path, {
        method: row ? "PATCH" : "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(this.formBody(form)),
        signal: controller.signal,
      });
      if (!response.ok) throw new Error(await response.text());
      const saved = await response.json() as RoomInhabitant;
      if (controller.signal.aborted || this.room !== room || this.form !== form) return;
      const page = this.listPage;
      if (this.mutationNeedsPageReload()) {
        await this.reloadAfterMutation(saved.id, row ? page : 0);
      } else {
        const at = this.rows.findIndex((candidate) => candidate.id === saved.id);
        if (at >= 0) this.rows[at] = saved;
        else this.rows.push(saved);
        this.rows.sort((a, b) => a.name.localeCompare(b.name) || a.id - b.id);
        this.setMessage("");
        this.renderList();
        this.focusList(saved.id);
      }
    } catch (error) {
      if (controller.signal.aborted || this.room !== room || this.form !== form) return;
      this.copy(message, 'saveFailed', { error: error instanceof Error ? error.message : String(error) });
      controls.forEach((control) => { control.disabled = false; });
    } finally {
      if (this.writeRequest === controller) this.writeRequest = null;
    }
  }

  private async remove(row: RoomInhabitant): Promise<void> {
    const room = this.room;
    if (!room || !this.dialog || !window.confirm(this.i18n.t('removeConfirm', { name: row.name }))) return;
    this.writeRequest?.abort();
    const controller = new AbortController();
    this.writeRequest = controller;
    this.setMessageKey('deleting');
    try {
      const response = await fetchWithToken(
        `/api/map-sets/${room.mapSetID}/inhabitants/${row.id}?${roomParams(room)}`,
        { method: "DELETE", signal: controller.signal },
      );
      if (!response.ok) throw new Error(await response.text());
      if (controller.signal.aborted || this.room !== room) return;
      if (this.mutationNeedsPageReload()) {
        await this.reloadAfterDelete();
      } else {
        this.rows = this.rows.filter((candidate) => candidate.id !== row.id);
        if (this.rows.length === 0) this.setMessageKey('noInhabitants');
        else this.setMessage('');
        this.renderList();
        this.focusList();
      }
    } catch (error) {
      if (controller.signal.aborted || this.room !== room) return;
      this.setMessageKey('deleteFailed', { error: error instanceof Error ? error.message : String(error) }, true);
    } finally {
      if (this.writeRequest === controller) this.writeRequest = null;
    }
  }
}
