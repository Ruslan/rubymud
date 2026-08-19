import { fetchWithToken } from "../dom";

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
let knowledgePanelSequence = 0;

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
  private list: HTMLElement | null = null;
  private message: HTMLElement | null = null;
  private addButton: HTMLButtonElement | null = null;
  private form: HTMLFormElement | null = null;
  private loadRequest: AbortController | null = null;
  private writeRequest: AbortController | null = null;
  private generation = 0;
  private disposed = false;
  private pendingRefresh = false;
  private returnFocus: HTMLElement | null = null;

  constructor(private readonly host: HTMLElement) {}

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
    dialog.setAttribute("aria-label", "Knowledge");
    backdrop.appendChild(dialog);

    const title = document.createElement("h2");
    title.textContent = "Knowledge";
    const roomLine = document.createElement("div");
    roomLine.className = "map-knowledge-dialog__room";
    roomLine.textContent = `${room.name || "Room"} · ${room.zone} (${room.x},${room.y},${room.l})`;
    const tabs = document.createElement("div");
    tabs.className = "map-knowledge-dialog__tabs";
    tabs.setAttribute("role", "tablist");
    const inhabitantsTab = document.createElement("button");
    inhabitantsTab.type = "button";
    inhabitantsTab.setAttribute("role", "tab");
    inhabitantsTab.setAttribute("aria-selected", "true");
    inhabitantsTab.textContent = "Inhabitants";
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

    const actions = document.createElement("div");
    actions.className = "map-knowledge-dialog__actions";
    this.addButton = document.createElement("button");
    this.addButton.type = "button";
    this.addButton.textContent = "Add";
    this.addButton.addEventListener("click", () => this.openForm(null));
    const close = document.createElement("button");
    close.type = "button";
    close.textContent = "Close";
    close.addEventListener("click", () => this.close());
    actions.append(this.addButton, close);
    panel.append(this.message, this.list, actions);
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
    this.form = null;
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
      this.setMessage("Room data changed; the list will refresh after the form closes.");
      return;
    }
    void this.load();
  }

  private async load(): Promise<void> {
    const room = this.room;
    if (!room || !this.dialog) return;
    this.loadRequest?.abort();
    const controller = new AbortController();
    this.loadRequest = controller;
    const generation = ++this.generation;
    this.setMessage("Loading…");
    try {
      const response = await fetchWithToken(
        `/api/map-sets/${room.mapSetID}/inhabitants?${roomParams(room)}`,
        { signal: controller.signal },
      );
      if (!response.ok) throw new Error(await response.text());
      const rows = await response.json() as RoomInhabitant[];
      if (!this.isCurrent(room, generation, controller)) return;
      this.rows = Array.isArray(rows) ? rows : [];
      this.setMessage(this.rows.length === 0 ? "No inhabitants have been recorded for this room." : "");
      this.renderList();
    } catch (error) {
      if (!this.isCurrent(room, generation, controller)) return;
      this.setMessage(`Failed to load inhabitants: ${error instanceof Error ? error.message : String(error)}`, true);
    } finally {
      if (this.loadRequest === controller) this.loadRequest = null;
    }
  }

  private isCurrent(room: KnowledgeRoom, generation: number, controller: AbortController): boolean {
    return !this.disposed && !controller.signal.aborted && this.room === room && this.generation === generation && !!this.dialog;
  }

  private setMessage(text: string, error = false): void {
    if (!this.message) return;
    this.message.textContent = text;
    this.message.dataset.error = String(error);
  }

  private renderList(): void {
    if (!this.list) return;
    this.form?.remove();
    this.form = null;
    this.list.hidden = false;
    if (this.addButton) this.addButton.hidden = false;
    this.list.replaceChildren();
    for (const row of this.rows) this.list.appendChild(this.card(row));
    if (this.pendingRefresh) {
      this.pendingRefresh = false;
      void this.load();
    }
  }

  private card(row: RoomInhabitant): HTMLElement {
    const card = document.createElement("article");
    card.className = "map-inhabitant-card";
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
      resident.textContent = "resident";
      header.appendChild(resident);
    }
    card.appendChild(header);
    this.appendDetail(card, "Presence", row.presence_text);
    this.appendDetail(card, "Look target", row.look_target);
    this.appendDetail(card, "Look result", row.look_text);
    this.appendDetail(card, "Note", row.note);
    this.appendDetail(card, "Strategy", row.strategy);
    const actions = document.createElement("div");
    actions.className = "map-inhabitant-card__actions";
    const edit = document.createElement("button");
    edit.type = "button";
    edit.textContent = "Edit";
    edit.addEventListener("click", () => this.openForm(row));
    const remove = document.createElement("button");
    remove.type = "button";
    remove.textContent = "Delete";
    remove.addEventListener("click", () => void this.remove(row));
    actions.append(edit, remove);
    card.appendChild(actions);
    return card;
  }

  private appendDetail(card: HTMLElement, label: string, value: string): void {
    if (!value) return;
    const line = document.createElement("div");
    line.className = "map-inhabitant-card__detail";
    const caption = document.createElement("span");
    caption.textContent = `${label}: `;
    line.append(caption, document.createTextNode(value));
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
    this.form?.remove();
    this.list.hidden = true;
    if (this.addButton) this.addButton.hidden = true;
    const form = document.createElement("form");
    form.className = "map-inhabitant-form";
    const heading = document.createElement("h3");
    heading.textContent = row ? "Edit inhabitant" : "Add inhabitant";
    form.appendChild(heading);

    const name = textInput("name", row?.name ?? "", nameLimit);
    name.required = true;
    const kind = document.createElement("select");
    kind.name = "kind";
    for (const [value, label] of [["npc", "NPC"], ["monster", "Monster"], ["other", "Other"]] as const) {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = label;
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
    residentLabel.append(resident, document.createTextNode(" Permanent resident of this room"));
    form.append(
      labelWrap("Name", name), labelWrap("Type", kind), labelWrap("Look target", target),
      labelWrap("Look result", look), labelWrap("Note", note),
      labelWrap("Interaction strategy", strategy), labelWrap("Presence text", presence),
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
    cancel.textContent = "Cancel";
    cancel.addEventListener("click", () => this.renderList());
    const save = document.createElement("button");
    save.type = "submit";
    save.textContent = "Save";
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
    message.textContent = "Saving…";
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
      const at = this.rows.findIndex((candidate) => candidate.id === saved.id);
      if (at >= 0) this.rows[at] = saved;
      else this.rows.push(saved);
      this.rows.sort((a, b) => a.name.localeCompare(b.name) || a.id - b.id);
      this.setMessage("");
      this.renderList();
    } catch (error) {
      if (controller.signal.aborted || this.room !== room || this.form !== form) return;
      message.textContent = `Failed to save: ${error instanceof Error ? error.message : String(error)}`;
      controls.forEach((control) => { control.disabled = false; });
    } finally {
      if (this.writeRequest === controller) this.writeRequest = null;
    }
  }

  private async remove(row: RoomInhabitant): Promise<void> {
    const room = this.room;
    if (!room || !this.dialog || !window.confirm(`Remove "${row.name}" from this room?`)) return;
    this.writeRequest?.abort();
    const controller = new AbortController();
    this.writeRequest = controller;
    this.setMessage("Deleting…");
    try {
      const response = await fetchWithToken(
        `/api/map-sets/${room.mapSetID}/inhabitants/${row.id}?${roomParams(room)}`,
        { method: "DELETE", signal: controller.signal },
      );
      if (!response.ok) throw new Error(await response.text());
      if (controller.signal.aborted || this.room !== room) return;
      this.rows = this.rows.filter((candidate) => candidate.id !== row.id);
      this.setMessage(this.rows.length === 0 ? "No inhabitants have been recorded for this room." : "");
      this.renderList();
    } catch (error) {
      if (controller.signal.aborted || this.room !== room) return;
      this.setMessage(`Failed to delete: ${error instanceof Error ? error.message : String(error)}`, true);
    } finally {
      if (this.writeRequest === controller) this.writeRequest = null;
    }
  }
}
