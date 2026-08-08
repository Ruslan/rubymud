import { describe, expect, it, vi } from "vitest";

import { MapPaneController } from "./pane";
import type { EdgeSide } from "./edgeEditor";

type WeightInputFactory = {
  attrNumber(
    label: string,
    value: number,
    commit: (value: number) => void,
  ): HTMLElement;
  attrText(
    label: string,
    value: string,
    placeholder: string,
    commit: (value: string) => void,
  ): HTMLElement;
};

type GridAttrEditor = {
  doorOpen: string;
  writeEdgeAttrs(side: EdgeSide, patch: { door?: boolean }): Promise<boolean>;
  appendEdgeAttrControls(box: HTMLElement, side: EdgeSide): void;
};

function weightInput(value: number, commit: (value: number) => void): HTMLInputElement {
  const pane = Object.create(MapPaneController.prototype) as WeightInputFactory;
  return pane.attrNumber("Route weight", value, commit).querySelector("input")!;
}

describe("edge routing weight input", () => {
  it("commits every positive finite fraction, including values below 1e-6", () => {
    const commit = vi.fn();
    const input = weightInput(1, commit);
    input.value = "0.0000001";

    expect(input.checkValidity()).toBe(true);
    input.dispatchEvent(new FocusEvent("blur"));

    expect(commit).toHaveBeenCalledOnce();
    expect(commit).toHaveBeenCalledWith(0.0000001);
  });

  it.each(["", "0", "-1"])("keeps invalid value %j local", (raw) => {
    const commit = vi.fn();
    const input = weightInput(1, commit);
    input.reportValidity = vi.fn(() => false);
    input.value = raw;

    input.dispatchEvent(new FocusEvent("blur"));

    expect(commit).not.toHaveBeenCalled();
    expect(input.validationMessage).not.toBe("");
  });

  it("does not commit a numerically unchanged value", () => {
    const commit = vi.fn();
    const input = weightInput(1, commit);
    input.value = "1.0";

    input.dispatchEvent(new FocusEvent("blur"));

    expect(commit).not.toHaveBeenCalled();
  });

  it("does not commit the same edit again when blur follows Enter", () => {
    const commit = vi.fn();
    const input = weightInput(1, commit);
    input.value = "0.5";

    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter" }));
    input.dispatchEvent(new FocusEvent("blur"));

    expect(commit).toHaveBeenCalledOnce();
    expect(commit).toHaveBeenCalledWith(0.5);
  });

  it("does not duplicate a text-attribute commit when blur follows Enter", () => {
    const commit = vi.fn();
    const pane = Object.create(MapPaneController.prototype) as WeightInputFactory;
    const input = pane.attrText("Phrase", "old", "", commit).querySelector("input")!;
    input.value = "new";

    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter" }));
    input.dispatchEvent(new FocusEvent("blur"));

    expect(commit).toHaveBeenCalledOnce();
    expect(commit).toHaveBeenCalledWith("new");
  });

  it("keeps the grid editor in place and reveals door fields after a successful write", async () => {
    const pane = Object.create(MapPaneController.prototype) as GridAttrEditor;
    pane.doorOpen = "";
    pane.writeEdgeAttrs = vi.fn().mockResolvedValue(true);
    const side: EdgeSide = {
      from: { z: "A", x: 0, y: 0, l: 0 },
      to: { z: "A", x: 1, y: 0, l: 0 },
      dir: "E",
      fromLabel: "one",
      toLabel: "two",
      state: "derived",
      passable: true,
      provenance: "derived",
      attrs: { weight: 1, phrase: "", door: false, locked: false, openCmd: "", noRoute: false },
      attrsEditable: true,
    };
    const host = document.createElement("div");
    document.body.appendChild(host);
    pane.appendEdgeAttrControls(host, side);

    [...host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("door"))!
      .click();

    await vi.waitFor(() => expect(pane.writeEdgeAttrs).toHaveBeenCalledWith(side, { door: true }));
    const door = [...host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
      .find((input) => input.parentElement?.textContent?.includes("door"));
    expect(door?.checked).toBe(true);
    expect([...host.querySelectorAll<HTMLInputElement>('input[type="text"]')]
      .find((input) => input.parentElement?.textContent?.includes("opening command")))
      .toBeDefined();
    host.remove();
  });
});
