import { appendFileSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type Page } from "@playwright/test";

import { LiveStack } from "./live-stack";

let stack: LiveStack;
let failed = false;

test.beforeAll(async () => { stack = await LiveStack.start(); });
test.afterEach(async ({}, testInfo) => { if (testInfo.status !== testInfo.expectedStatus) failed = true; });
test.afterAll(async () => {
  const target = await stack.cleanup(failed || process.env.KEEP_E2E_ARTIFACTS === "1");
  if (target) console.log(`E2E artifacts: ${target}`);
});

test("reconciles the live five-room loop and survives restart", async ({ browser, page }) => {
  const consolePath = join(stack.artifactsDir, "browser.log");
  page.on("console", (message) => appendFileSync(consolePath, `[console:${message.type()}] ${message.text()}\n`));
  page.on("pageerror", (error) => appendFileSync(consolePath, `[pageerror] ${error.stack ?? error.message}\n`));
  await installMapLayout(page);
  await page.goto(`${stack.baseURL}/?session_id=${stack.seed.session_id}`);
  await connectAnchorAndArm(page);

  const initialRevision = topologyRevision();
  await walkLoop(page);
  expectTraceLoops(1);
  const dialog = page.getByRole("dialog", { name: "Resolve mapper conflict" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Observed: Loop Room 1");
  await expect(dialog).toContainText("Expected: Loop Room 2");
  await expect(dialog.locator(".map-reconciliation-diff")).toContainText("The second chamber is lined with blue tiles.");
  await expect(dialog.locator(".map-reconciliation-diff")).toContainText("The first chamber has a brass compass rose.");
  expectFalseFace(initialRevision);

  const target = dialog.getByLabel("Existing target room");
  await target.selectOption({ label: "Loop Room 1 — (0, 0, 0)" });
  await dialog.getByLabel("display_x").fill("1");
  await dialog.getByLabel("display_y").fill("0");
  await dialog.getByLabel("display_l").fill("0");
  const apply = dialog.getByRole("button", { name: "Apply" });
  await expect(apply).toBeDisabled();
  await dialog.getByRole("button", { name: "Preview" }).click();
  await expect(dialog).toContainText("Preview ready: N");
  await expect(apply).toBeEnabled();
  expectFalseFace(initialRevision);

  await dialog.getByLabel("display_y").fill("1");
  await expect(apply).toBeDisabled();
  await dialog.getByLabel("display_y").fill("0");
  await dialog.getByRole("button", { name: "Preview" }).click();
  await expect(apply).toBeEnabled();
  await apply.click();
  await expect(dialog).toBeHidden();
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: green/);
  await expect(obsButton(page)).toHaveText("STOP OBS");
	await expect.poll(async () => obsButton(page).evaluate((button) => ({ client: button.clientWidth, scroll: button.scrollWidth })))
		.toEqual(expect.objectContaining({ client: expect.any(Number), scroll: expect.any(Number) }));
	const obsBox = await obsButton(page).evaluate((button) => ({ client: button.clientWidth, scroll: button.scrollWidth }));
	expect(obsBox.client).toBeGreaterThanOrEqual(obsBox.scroll);
  expectReconciled(initialRevision + 1);

	const canvas = page.locator(".map-pane__canvas");
	const canvasBox = await canvas.boundingBox();
	expect(canvasBox?.width ?? 0).toBeGreaterThan(500);
	expect(canvasBox?.height ?? 0).toBeGreaterThan(250);
	await canvas.hover({ position: { x: (canvasBox?.width ?? 600) / 2, y: (canvasBox?.height ?? 300) / 2 } });
	for (let i = 0; i < 9; i += 1) await page.mouse.wheel(0, -120);
	await page.locator(".map-pane__bar").hover();
	await page.waitForTimeout(100);
  await page.screenshot({ path: join(stack.artifactsDir, "reconciled-page.png"), fullPage: true });
  await page.locator(".map-pane").screenshot({ path: join(stack.artifactsDir, "reconciled-map.png") });

  await addNPCToRoomOne(page, initialRevision + 1);

  await walkLoop(page);
  expectTraceLoops(2);
  await expect(page.getByRole("dialog", { name: "Resolve mapper conflict" })).toHaveCount(0);
  await expectRecordingAtRoomOne(page);

  await page.close();
  await stack.restartMudhost();
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  await context.addInitScript(mapLayoutInit);
  const restarted = await context.newPage();
  await restarted.goto(`${stack.baseURL}/?session_id=${stack.seed.session_id}`);
  await connectAnchorAndArm(restarted);
  expectReconciled(initialRevision + 1);
  await expectNPCInRoomOne(restarted, initialRevision + 1);
  await walkLoop(restarted);
  expectTraceLoops(3);
  await expect(restarted.getByRole("dialog", { name: "Resolve mapper conflict" })).toHaveCount(0);
  await expectRecordingAtRoomOne(restarted);
  await context.close();
});

async function installMapLayout(page: Page): Promise<void> {
  await page.context().addInitScript(mapLayoutInit);
}

function mapLayoutInit(): void {
  localStorage.setItem("pane-layout", JSON.stringify({
    columns: [{
      id: "col-e2e",
      panes: [{ id: "pane-main", buffer: "main" }, { id: "pane-map", buffer: "~map" }],
      rowSizes: [45, 55],
    }],
    colSizes: [100],
  }));
}

async function connectAnchorAndArm(page: Page): Promise<void> {
  const session = stack.seed.session_id;
  await expect(page.locator("#connection-status")).toHaveText("disconnected");
  await page.locator("#connection-status").click();
  await expect(page.locator("#connection-status")).toHaveText("connected");
  const token = await page.evaluate(() => (window as Window & { API_TOKEN?: string }).API_TOKEN ?? "");
  const headers = { "X-Session-Token": token };
  await expect.poll(async () => {
    const response = await page.request.get(`${stack.baseURL}/api/sessions/${session}/map-live-room`, { headers });
    const body = await response.json();
    return body.seen ? body.hint : "";
  }).toBe("Loop Room 1");
  const room = stack.seed.rooms["1"];
  const anchor = await page.request.post(`${stack.baseURL}/api/sessions/${session}/map-anchor`, { data: room, headers });
  expect(anchor.ok()).toBeTruthy();
  expect((await anchor.json()).exact).toBe(true);
  await expect(page.locator(".map-pane")).toBeVisible();
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: green/);
  await obsButton(page).click();
  await expect(obsButton(page)).toHaveText("STOP OBS");
}

function obsButton(page: Page) {
  return page.locator('.map-pane button[aria-label="Diagnostic movement observation; no rooms are written"]');
}

async function openRoomOneKnowledge(page: Page) {
  const canvas = page.locator(".map-pane__canvas");
  const box = await canvas.boundingBox();
  expect(box).not.toBeNull();
  await canvas.click({ position: { x: box!.width / 2, y: box!.height / 2 } });
  const menu = page.locator(".map-cell-menu");
  await expect(menu).toBeVisible();
  await menu.getByRole("button", { name: "Knowledge…" }).click();
  const dialog = page.getByRole("dialog", { name: "Knowledge" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Loop Room 1");
  return dialog;
}

async function addNPCToRoomOne(page: Page, topologyRevisionBefore: number): Promise<void> {
  const dialog = await openRoomOneKnowledge(page);
  await expect(dialog).toContainText("No inhabitants have been recorded for this room.");
  await dialog.getByRole("button", { name: "Add" }).click();
  const form = dialog.locator(".map-inhabitant-form");
  await expect(form).toBeVisible();
  await form.getByLabel("Name").fill("Хранитель круга");
  await form.getByLabel("Type").selectOption("npc");
  await form.getByLabel("Look target").fill("хранитель");
  await form.getByLabel("Look result").fill("Высокий страж в плаще с вышитым знаком круга.");
  await page.screenshot({ path: join(stack.artifactsDir, "npc-form-page.png"), fullPage: true });
  await dialog.screenshot({ path: join(stack.artifactsDir, "npc-form-dialog.png") });
  await form.getByRole("button", { name: "Save" }).click();
  const card = dialog.locator(".map-inhabitant-card", { hasText: "Хранитель круга" });
  await expect(card).toBeVisible();
  await expect(card).toContainText("npc");
  await expect(card).toContainText("Look target: хранитель");
  await expect(card).toContainText("Look result: Высокий страж в плаще с вышитым знаком круга.");
  await page.screenshot({ path: join(stack.artifactsDir, "npc-card-page.png"), fullPage: true });
  await dialog.screenshot({ path: join(stack.artifactsDir, "npc-card-dialog.png") });
  expectNPCStored(topologyRevisionBefore);
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toBeHidden();
}

async function expectNPCInRoomOne(page: Page, topologyRevisionBefore: number): Promise<void> {
  const dialog = await openRoomOneKnowledge(page);
  const card = dialog.locator(".map-inhabitant-card", { hasText: "Хранитель круга" });
  await expect(card).toContainText("Look target: хранитель");
  await expect(card).toContainText("Высокий страж в плаще с вышитым знаком круга.");
  expectNPCStored(topologyRevisionBefore);
  await dialog.getByRole("button", { name: "Close" }).click();
}

function expectNPCStored(topologyRevisionBefore: number): void {
  expect(stack.query(
    `SELECT name,kind,look_target,look_text FROM map_inhabitants WHERE map_set_id=${stack.seed.map_set_id}`,
  )).toEqual([{
    name: "Хранитель круга", kind: "npc", look_target: "хранитель",
    look_text: "Высокий страж в плаще с вышитым знаком круга.",
  }]);
  expect(stack.query(
    `SELECT r.tag,ri.resident,ri.presence_text FROM room_inhabitants ri JOIN rooms r ON r.map_set_id=ri.map_set_id AND r.zone=ri.zone AND r.x=ri.x AND r.y=ri.y AND r.l=ri.l WHERE ri.map_set_id=${stack.seed.map_set_id}`,
  )).toEqual([{ tag: 1, resident: 0, presence_text: "" }]);
  expect(topologyRevision()).toBe(topologyRevisionBefore);
}

async function walkLoop(page: Page): Promise<void> {
  const token = await page.evaluate(() => (window as Window & { API_TOKEN?: string }).API_TOKEN ?? "");
  const headers = { "X-Session-Token": token };
  const hints = ["Loop Room 2", "Loop Room 3", "Loop Room 4", "Loop Room 5", "Loop Room 1"];
  for (const [index, command] of ["E", "E", "S", "W", "N"].entries()) {
    await page.locator("#input").fill(command);
    await page.locator("#input").press("Enter");
    await expect.poll(async () => {
      const response = await page.request.get(`${stack.baseURL}/api/sessions/${stack.seed.session_id}/map-live-room`, { headers });
      return (await response.json()).hint;
    }).toBe(hints[index]);
  }
}

function topologyRevision(): number {
  const rows = stack.query<{ topology_revision: number }>(
    `SELECT topology_revision FROM map_sets WHERE id=${stack.seed.map_set_id}`,
  );
  expect(rows).toHaveLength(1);
  return rows[0]!.topology_revision;
}

function expectFalseFace(revision: number): void {
  const rows = stack.query<{ kind: string; to_x: number; to_y: number }>(
    `SELECT kind,to_x,to_y FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND from_zone='Five Room Loop' AND from_x=1 AND from_y=1 AND from_l=0 AND dir='N'`,
  );
  expect(rows).toEqual([{ kind: "grid", to_x: 0, to_y: 1 }]);
  expect(stack.query(`SELECT 1 AS present FROM room_layouts WHERE map_set_id=${stack.seed.map_set_id}`)).toHaveLength(0);
  expect(topologyRevision()).toBe(revision);
}

function expectReconciled(revision: number): void {
  const rows = stack.query<Record<string, unknown>>(
    `SELECT kind,source,from_x,from_y,dir,to_x,to_y,command FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND from_zone='Five Room Loop' AND from_x=1 AND from_y=1 AND from_l=0 AND dir='N'`,
  );
  expect(rows).toEqual([{
    kind: "corridor", source: "reconciled", from_x: 1, from_y: 1,
    dir: "N", to_x: 0, to_y: 0, command: "N",
  }]);
  expect(stack.query(
    `SELECT id FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND from_zone='Five Room Loop' AND from_x=0 AND from_y=0 AND to_x=1 AND to_y=1`,
  )).toHaveLength(0);
	expect(stack.query(
		`SELECT source,passable,to_x,to_y FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND kind='grid' AND from_zone='Five Room Loop' AND from_x=0 AND from_y=1 AND from_l=0 AND dir='S'`,
	)).toEqual([{ source: "reconciled", passable: 0, to_x: 1, to_y: 1 }]);
	expect(stack.query(
		`SELECT id FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND from_zone='Five Room Loop' AND from_x=0 AND from_y=1 AND dir='S' AND passable=1`,
	)).toHaveLength(0);
  expect(stack.query(
    `SELECT display_x,display_y,display_l FROM room_layouts WHERE map_set_id=${stack.seed.map_set_id} AND zone='Five Room Loop' AND x=1 AND y=1 AND l=0`,
  )).toEqual([{ display_x: 1, display_y: 0, display_l: 0 }]);
  expect(stack.query<{ n: number }>(`SELECT count(*) AS n FROM rooms WHERE map_set_id=${stack.seed.map_set_id}`)).toEqual([{ n: 5 }]);
  expect(stack.query(
    `SELECT tag,x,y,l,hint FROM rooms WHERE map_set_id=${stack.seed.map_set_id} ORDER BY tag`,
  )).toEqual([
    { tag: 1, x: 0, y: 0, l: 0, hint: "Loop Room 1" },
    { tag: 2, x: 0, y: 1, l: 0, hint: "Loop Room 2" },
    { tag: 3, x: 0, y: 2, l: 0, hint: "Loop Room 3" },
    { tag: 4, x: 1, y: 2, l: 0, hint: "Loop Room 4" },
    { tag: 5, x: 1, y: 1, l: 0, hint: "Loop Room 5" },
  ]);
	const display = stack.query<{ tag: number; display_x: number; display_y: number }>(
		`SELECT r.tag,coalesce(l.display_x,r.x) AS display_x,coalesce(l.display_y,r.y) AS display_y FROM rooms r LEFT JOIN room_layouts l ON l.map_set_id=r.map_set_id AND l.zone=r.zone AND l.x=r.x AND l.y=r.y AND l.l=r.l WHERE r.map_set_id=${stack.seed.map_set_id} ORDER BY r.tag`,
	);
	const byTag = new Map(display.map((room) => [room.tag, room]));
	expect(byTag.get(5)?.display_y).toBe(byTag.get(1)?.display_y);
	expect(byTag.get(5)?.display_x).toBeGreaterThan(byTag.get(1)?.display_x ?? 99);
	expect(byTag.get(4)?.display_x).toBe(byTag.get(5)?.display_x);
	expect(Math.abs((byTag.get(4)?.display_y ?? 0) - (byTag.get(5)?.display_y ?? 0))).toBeGreaterThan(1);
  expect(topologyRevision()).toBe(revision);
}

function expectTraceLoops(loopCount: number): void {
  const raw = readFileSync(stack.tracePath, "utf8").trim();
  const rows = raw ? raw.split("\n").map((line) => JSON.parse(line) as {
    from: number; command: string; to: number; moved: boolean;
  }) : [];
  const oneLoop = [
    { from: 1, command: "E", to: 2, moved: true },
    { from: 2, command: "E", to: 3, moved: true },
    { from: 3, command: "S", to: 4, moved: true },
    { from: 4, command: "W", to: 5, moved: true },
    { from: 5, command: "N", to: 1, moved: true },
  ];
  expect(rows.map(({ from, command, to, moved }) => ({ from, command, to, moved })))
    .toEqual(Array.from({ length: loopCount }, () => oneLoop).flat());
}

async function expectRecordingAtRoomOne(page: Page): Promise<void> {
  const token = await page.evaluate(() => (window as Window & { API_TOKEN?: string }).API_TOKEN ?? "");
  const headers = { "X-Session-Token": token };
  await expect.poll(async () => {
    const response = await page.request.get(`${stack.baseURL}/api/sessions/${stack.seed.session_id}/map-recording-state`, { headers });
    const state = await response.json();
    return `${state.mode}:${state.pending}`;
  }).toBe("recording:0");
}
