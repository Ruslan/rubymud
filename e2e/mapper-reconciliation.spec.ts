import { appendFileSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type Page } from "@playwright/test";

import { LiveStack, type LiveStackOptions } from "./live-stack";

let stack: LiveStack;
const allStacks: LiveStack[] = [];
let failed = false;

test.beforeAll(async () => { stack = await startStack(); });
test.afterEach(async ({}, testInfo) => { if (testInfo.status !== testInfo.expectedStatus) failed = true; });
test.afterAll(async () => {
  const preserve = failed || process.env.KEEP_E2E_ARTIFACTS === "1";
  for (const candidate of allStacks) {
    const target = await candidate.cleanup(preserve);
    if (target) console.log(`E2E artifacts: ${target}`);
  }
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
  await dialog.getByLabel("Display X").fill("1");
  await dialog.getByLabel("Display Y").fill("0");
  await dialog.getByLabel("Display L").fill("0");
  const apply = dialog.getByRole("button", { name: "Apply" });
  await expect(apply).toBeDisabled();
  await dialog.getByRole("button", { name: "Preview" }).click();
  await expect(dialog).toContainText("Preview ready: N");
  await expect(apply).toBeEnabled();
  expectFalseFace(initialRevision);

  await dialog.getByLabel("Display Y").fill("1");
  await expect(apply).toBeDisabled();
  await dialog.getByLabel("Display Y").fill("0");
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
	await reuseNPCInRoomTwoAndSynchronizeMCP(page, initialRevision + 1);

  await walkLoop(page);
  expectTraceLoops(2, true);
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
  expectTraceLoops(3, true);
  await expect(restarted.getByRole("dialog", { name: "Resolve mapper conflict" })).toHaveCount(0);
  await expectRecordingAtRoomOne(restarted);
  await context.close();
});

test("handles darkness, then creates one provisional room and a two-way passage", async ({ page }) => {
  stack = await startStack({ autorecord: true, darkRoom: 2, roomBoundary: true });
  await installMapLayout(page);
  await page.goto(`${stack.baseURL}/?session_id=${stack.seed.session_id}`);
  await connectAnchorAndArm(page);

  await expect.poll(() => traceCommandCount("scan", 1)).toBe(1);
  // The scan is UI-only: it must neither pre-create Room 2 nor turn the unknown
  // half-room into a graph node. The live canvas nevertheless paints beyond the
  // ordinary exit stub/player ring on the east side.
  expect(stack.query<{ n: number }>(
    `SELECT count(*) AS n FROM rooms WHERE map_set_id=${stack.seed.map_set_id}`,
  )).toEqual([{ n: 1 }]);
  await expect.poll(() => eastFrontierOpaquePixels(page)).toBeGreaterThan(10);
  await page.locator(".map-pane").screenshot({ path: join(stack.artifactsDir, "autorecord-scan-frontier.png") });

  expect(stack.query<{ n: number }>(
    `SELECT count(*) AS n FROM rooms WHERE map_set_id=${stack.seed.map_set_id}`,
  )).toEqual([{ n: 1 }]);

  // First entry is physically real but unreadable. The live client must stop
  // OBS without reconciliation or a guessed room write, keep the predicted
  // position yellow, and allow a visible reverse step to recover green.
  const token = await page.evaluate(() => (window as Window & { API_TOKEN?: string }).API_TOKEN ?? "");
  const headers = { "X-Session-Token": token };
  await page.locator("#input").fill("E");
  await page.locator("#input").press("Enter");
  await expect.poll(async () => {
    const response = await page.request.get(`${stack.baseURL}/api/sessions/${stack.seed.session_id}/map-recording-state`, { headers });
    const state = await response.json();
    return `${state.mode}:${state.reason_code}`;
  }).toBe("off:mapper.recording.room_obscured");
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: yellow.*room obscured/i);
  await expect(page.getByRole("dialog", { name: "Resolve mapper conflict" })).toHaveCount(0);
  expect(stack.query<{ n: number }>(
    `SELECT count(*) AS n FROM rooms WHERE map_set_id=${stack.seed.map_set_id}`,
  )).toEqual([{ n: 1 }]);

  await sendAndExpectRoom(page, "W", "Loop Room 1");
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: green/);
  await page.locator("#input").fill("torch");
  await page.locator("#input").press("Enter");
  await expect.poll(() => traceCommandCount("torch")).toBe(1);
  await obsButton(page).click();
  await expect(obsButton(page)).toHaveText("STOP OBS");

  // The second entry is visible and follows the normal autorecord path. No REST
  // topology write is used.
  await sendAndExpectRoom(page, "E", "Loop Room 2");
  await expect.poll(() => stack.query<{ n: number }>(
    `SELECT count(*) AS n FROM rooms WHERE map_set_id=${stack.seed.map_set_id}`,
  )).toEqual([{ n: 2 }]);
  await expectRecordingAtRoomOne(page);
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: green/);
  await expect.poll(() => traceCommandCount("scan", 2)).toBe(1);
  await expect.poll(() => eastFrontierOpaquePixels(page)).toBeGreaterThan(10);

  expect(stack.query<Record<string, unknown>>(
    `SELECT hint,desc,exits,verification_state,record_source,record_batch_id,record_origin FROM rooms WHERE map_set_id=${stack.seed.map_set_id} AND zone='Five Room Loop' AND x=0 AND y=1 AND l=0`,
  )).toEqual([{
    hint: "Loop Room 2", desc: "The second chamber is lined with blue tiles.", exits: "E W",
    verification_state: "provisional", record_source: "autorecord",
    record_batch_id: expect.stringMatching(/^session-\d+-epoch-\d+-move-\d+$/),
    record_origin: `session:${stack.seed.session_id}`,
  }]);
  expect(stack.query<Record<string, unknown>>(
    `SELECT kind,source,passable,dir,command,to_zone,to_x,to_y,to_l FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND from_zone='Five Room Loop' AND from_x=0 AND from_y=0 AND from_l=0 AND dir='E'`,
  )).toEqual([{
    kind: "corridor", source: "autorecord", passable: 1, dir: "E", command: "E",
    to_zone: "Five Room Loop", to_x: 0, to_y: 1, to_l: 0,
  }]);
  expect(stack.query<Record<string, unknown>>(
    `SELECT kind,source,passable,dir,command,to_zone,to_x,to_y,to_l FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND from_zone='Five Room Loop' AND from_x=0 AND from_y=1 AND from_l=0 AND dir='W'`,
  )).toEqual([{
    kind: "corridor", source: "autorecord", passable: 1, dir: "W", command: "w",
    to_zone: "Five Room Loop", to_x: 0, to_y: 0, to_l: 0,
  }]);

  // The user's live regression: the first recorded step defaults to a two-way
  // passage, so walking back and entering the same observed room again stays
  // green and does not create another room or edge pair.
  await sendAndExpectRoom(page, "W", "Loop Room 1");
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: green/);
  await sendAndExpectRoom(page, "E", "Loop Room 2");
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: green/);
  await expect.poll(() => traceCommandCount("scan", 2)).toBe(2);
  await expect.poll(() => eastFrontierOpaquePixels(page)).toBeGreaterThan(10);
  expect(stack.query<{ n: number }>(
    `SELECT count(*) AS n FROM rooms WHERE map_set_id=${stack.seed.map_set_id}`,
  )).toEqual([{ n: 2 }]);
  expect(stack.query<{ n: number }>(
    `SELECT count(*) AS n FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND source='autorecord'`,
  )).toEqual([{ n: 2 }]);
});

test("confirms a crate-revealed mutable down exit and keeps OBS recording", async ({ page }) => {
  stack = await startStack({ dynamicCrate: true, roomBoundary: true });
  await installMapLayout(page);
  await page.goto(`${stack.baseURL}/?session_id=${stack.seed.session_id}`);
  await connectAnchorAndArm(page, "В окружении ящиков");

  await page.locator("#input").fill("двигать ящик");
  await page.locator("#input").press("Enter");
  await expect.poll(() => traceCommandCount("двигать ящик", 1)).toBe(1);
  await page.locator("#input").fill("scan");
  await page.locator("#input").press("Enter");
  await expect.poll(() => traceCommandCount("scan", 1)).toBeGreaterThanOrEqual(1);
  await expect.poll(() => downFrontierGreenPixels(page)).toBeGreaterThan(5);

  await sendAndExpectRoom(page, "N", "Проход между ящиками");
  await sendAndExpectRoom(page, "S", "В окружении ящиков");
  const dialog = page.getByRole("dialog", { name: "Resolve mapper conflict" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Only exits differ");
  await expect(dialog.locator(".map-reconciliation-diff")).toContainText("Observed: N D");
  await dialog.getByRole("button", { name: "Mark D mutable and continue OBS" }).click();
  await expect(dialog).toBeHidden();
  await expect(obsButton(page)).toHaveText("STOP OBS");
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: green/);
  expect(stack.query<{ mutable_exits: string }>(
    `SELECT mutable_exits FROM rooms WHERE map_set_id=${stack.seed.map_set_id} AND zone='Хилло' AND x=0 AND y=0 AND l=0`,
  )).toEqual([{ mutable_exits: `["D"]` }]);

  // The same live N+D variant is now trusted only at this exact room. Returning
  // through the known passage must stay green and must not reopen reconciliation.
  await sendAndExpectRoom(page, "N", "Проход между ящиками");
  await sendAndExpectRoom(page, "S", "В окружении ящиков");
  await expect(page.getByRole("dialog", { name: "Resolve mapper conflict" })).toHaveCount(0);
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: green/);

  // D is still an unexplored graph edge, so OBS records the lower room and its
  // default reverse U passage after the explicit mutable-room decision.
  await sendAndExpectRoom(page, "D", "Под ящиками");
  await expect.poll(() => stack.query<{ n: number }>(
    `SELECT count(*) AS n FROM rooms WHERE map_set_id=${stack.seed.map_set_id} AND zone='Хилло' AND l=-1`,
  )).toEqual([{ n: 1 }]);
  await expect(page.locator(".map-pane__floors button.on")).toHaveText("-1");
  expect(stack.query<{ dir: string; to_l: number }>(
    `SELECT dir,to_l FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND source='autorecord' AND from_zone='Хилло' ORDER BY from_l DESC,dir`,
  )).toEqual([{ dir: "D", to_l: -1 }, { dir: "U", to_l: 0 }]);
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: green/);
  await page.locator(".map-pane").screenshot({ path: join(stack.artifactsDir, "mutable-crate-down.png") });

  // Return to the parent, delete the explored leaf through the real nested cell
  // menu, then arm OBS and explore D again. This is the player's repeatable
  // manual-test loop; the server must stop OBS for the topology edit and leave
  // the delete undoable rather than silently retaining the old cell.
  await sendAndExpectRoom(page, "U", "В окружении ящиков");
  await page.locator(".map-pane__floors button", { hasText: "-1" }).click();
  const canvas = page.locator(".map-pane__canvas");
  const box = await canvas.boundingBox();
  expect(box).not.toBeNull();
  await canvas.click({ position: { x: box!.width / 2, y: box!.height / 2 } });
  const menu = page.locator(".map-cell-menu");
  await menu.locator("summary", { hasText: "Room properties…" }).click();
  await expect(menu.getByRole("button", { name: "Delete leaf room" }))
    .toHaveCSS("background-color", "rgb(111, 32, 32)");
  page.once("dialog", (dialog) => dialog.accept());
  await menu.getByRole("button", { name: "Delete leaf room" }).click();
  await expect(page.locator(".map-pane__status")).toContainText("Room deleted; Undo is available");
  await expect.poll(() => stack.query<{ n: number }>(
    `SELECT count(*) AS n FROM rooms WHERE map_set_id=${stack.seed.map_set_id} AND zone='Хилло' AND l=-1`,
  )).toEqual([{ n: 0 }]);
  await expect(obsButton(page)).toHaveText("OBS");
  await obsButton(page).click();
  await expect(obsButton(page)).toHaveText("STOP OBS");
  await sendAndExpectRoom(page, "D", "Под ящиками");
  await expect.poll(() => stack.query<{ n: number }>(
    `SELECT count(*) AS n FROM rooms WHERE map_set_id=${stack.seed.map_set_id} AND zone='Хилло' AND l=-1`,
  )).toEqual([{ n: 1 }]);
});

test("shows the Hillo one-way passage and lets the player block only its reverse face", async ({ page }) => {
  stack = await startStack({ oneWayHillo: true });
  await installMapLayout(page);
  await page.goto(`${stack.baseURL}/?session_id=${stack.seed.session_id}`);
  await connectAnchorAndArm(page, "Вдоль оврага");

  // Captured from data/mudhost.db on 2026-08-21: the source advertises W while
  // the adjacent target advertises N/S/W and deliberately has no reverse E.
  expect(stack.query<{ hint: string; exits: string }>(
    `SELECT hint,exits FROM rooms WHERE map_set_id=${stack.seed.map_set_id} ORDER BY y DESC`,
  )).toEqual([
    { hint: "Вдоль оврага", exits: "N W" },
    { hint: "Лесистое предгорье", exits: "N S W" },
  ]);
  expect(stack.query<Record<string, unknown>>(
    `SELECT source,passable,to_x,to_y FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND from_zone='Хилло' AND from_x=0 AND from_y=-1 AND dir='E'`,
  )).toEqual([{ source: "parsed", passable: 1, to_x: 0, to_y: 0 }]);

  // Room text owns the ordinary stubs, so the physical mismatch must look torn:
  // the known W half is painted and the absent E half stays transparent.
  await expect.poll(async () => {
    const pixels = await westOneWayPixels(page);
    return pixels.source > 10 && pixels.missing === 0;
  }).toBe(true);
  await page.locator(".map-pane").screenshot({ path: join(stack.artifactsDir, "hillo-one-way-before-mark.png") });

  await sendAndExpectRoom(page, "W", "Лесистое предгорье");
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: green/);
  expectTraceMove("W", 1, 2, true);

  const token = await page.evaluate(() => (window as Window & { API_TOKEN?: string }).API_TOKEN ?? "");
  const headers = { "X-Session-Token": token };
  const routeBefore = await page.request.post(
    `${stack.baseURL}/api/sessions/${stack.seed.session_id}/map-path`,
    { headers, data: { to: stack.seed.rooms["1"] } },
  );
  expect(routeBefore.ok()).toBeTruthy();
  expect(await routeBefore.json()).toEqual(expect.objectContaining({ reachable: true, directions: ["e"] }));

  // The edge editor exposes both directed facts. Unchecking only the target's E
  // face records the rare exception without damaging the valid W traversal.
  const canvas = page.locator(".map-pane__canvas");
  const box = await canvas.boundingBox();
  expect(box).not.toBeNull();
  await canvas.click({ position: { x: box!.width / 2 + 20, y: box!.height / 2 } });
  const menu = page.locator(".map-edge-menu");
  await expect(menu).toBeVisible();
  const reverse = menu.locator(".map-edge-menu__side", {
    hasText: "from 'Лесистое предгорье' to 'Вдоль оврага' (east)",
  });
  await expect(reverse).toBeVisible();
  const reversePassage = reverse.getByRole("checkbox", { name: "passage exists" });
  await expect(reversePassage).toBeChecked();
  // The write intentionally closes the popup on change; click rather than
  // uncheck so Playwright does not keep waiting on the now-detached checkbox.
  await reversePassage.click();
  await expect(menu).toBeHidden();

  await expect.poll(() => stack.query<Record<string, unknown>>(
    `SELECT source,passable,to_x,to_y FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND from_zone='Хилло' AND from_x=0 AND from_y=-1 AND dir='E'`,
  )).toEqual([{ source: "manual", passable: 0, to_x: 0, to_y: 0 }]);
  expect(stack.query<Record<string, unknown>>(
    `SELECT source,passable,to_x,to_y FROM map_edges WHERE map_set_id=${stack.seed.map_set_id} AND from_zone='Хилло' AND from_x=0 AND from_y=0 AND dir='W'`,
  )).toEqual([{ source: "parsed", passable: 1, to_x: 0, to_y: -1 }]);

  await expect.poll(async () => {
    const response = await page.request.post(
      `${stack.baseURL}/api/sessions/${stack.seed.session_id}/map-path`,
      { headers, data: { to: stack.seed.rooms["1"] } },
    );
    return await response.json() as { reachable: boolean };
  }).toEqual(expect.objectContaining({ reachable: false }));

  // The fake MUD is the physical authority too: trying the absent E step is
  // refused, and the refusal cancels the pending tracker move without losing
  // the confirmed target room.
  await page.locator("#input").fill("E");
  await page.locator("#input").press("Enter");
  await expect.poll(() => traceHasMove("E", 2, 2, false)).toBe(true);
  await expect(page.locator(".map-pane__confidence")).toHaveAttribute("title", /Confidence: green/);
  await expect(page.getByRole("dialog", { name: "Resolve mapper conflict" })).toHaveCount(0);
  await page.locator(".map-pane").screenshot({ path: join(stack.artifactsDir, "hillo-one-way-blocked.png") });
});

async function startStack(options: LiveStackOptions = {}): Promise<LiveStack> {
  const started = await LiveStack.start(options);
  allStacks.push(started);
  return started;
}

async function installMapLayout(page: Page): Promise<void> {
  await page.context().addInitScript(mapLayoutInit);
}

function traceCommandCount(command: string, from?: number): number {
  const raw = readFileSync(stack.tracePath, "utf8").trim();
  if (!raw) return 0;
  return raw.split("\n")
    .map((line) => JSON.parse(line) as { command: string; from: number })
    .filter((row) => row.command === command && (from == null || row.from === from))
    .length;
}

async function eastFrontierOpaquePixels(page: Page): Promise<number> {
  return page.locator(".map-pane__canvas").evaluate((canvas: HTMLCanvasElement) => {
    const ctx = canvas.getContext("2d");
    if (!ctx || canvas.clientWidth === 0 || canvas.clientHeight === 0) return 0;
    const sx = canvas.width / canvas.clientWidth;
    const sy = canvas.height / canvas.clientHeight;
    const x = Math.floor((canvas.clientWidth / 2 + 24) * sx);
    const y = Math.floor((canvas.clientHeight / 2 - 22) * sy);
    const width = Math.max(1, Math.floor(150 * sx));
    const height = Math.max(1, Math.floor(44 * sy));
    const data = ctx.getImageData(x, y, Math.min(width, canvas.width - x), Math.min(height, canvas.height - y)).data;
    let opaque = 0;
    for (let i = 3; i < data.length; i += 4) if (data[i] > 0) opaque++;
    return opaque;
  });
}

async function downFrontierGreenPixels(page: Page): Promise<number> {
  return page.locator(".map-pane__canvas").evaluate((canvas: HTMLCanvasElement) => {
    const ctx = canvas.getContext("2d");
    if (!ctx || canvas.clientWidth === 0 || canvas.clientHeight === 0) return 0;
    const sx = canvas.width / canvas.clientWidth;
    const sy = canvas.height / canvas.clientHeight;
    const x = Math.floor((canvas.clientWidth / 2 + 12) * sx);
    const y = Math.floor((canvas.clientHeight / 2 + 10) * sy);
    const width = Math.max(1, Math.floor(220 * sx));
    const height = Math.max(1, Math.floor(55 * sy));
    const data = ctx.getImageData(x, y, Math.min(width, canvas.width - x), Math.min(height, canvas.height - y)).data;
    let green = 0;
    for (let i = 0; i < data.length; i += 4) {
      if (data[i] >= 70 && data[i] <= 100 && data[i + 1] >= 210 && data[i + 1] <= 250 && data[i + 2] >= 115 && data[i + 2] <= 155 && data[i + 3] > 0) green++;
    }
    return green;
  });
}

async function westOneWayPixels(page: Page): Promise<{ source: number; missing: number }> {
  return page.locator(".map-pane__canvas").evaluate((canvas: HTMLCanvasElement) => {
    const ctx = canvas.getContext("2d");
    if (!ctx || canvas.clientWidth === 0 || canvas.clientHeight === 0) return { source: 0, missing: -1 };
    const sx = canvas.width / canvas.clientWidth;
    const sy = canvas.height / canvas.clientHeight;
    const cy = canvas.clientHeight / 2;
    const count = (left: number, right: number): number => {
      const x = Math.floor((canvas.clientWidth / 2 + left) * sx);
      const y = Math.floor((cy - 2) * sy);
      const width = Math.max(1, Math.floor((right - left) * sx));
      const height = Math.max(1, Math.floor(4 * sy));
      const data = ctx.getImageData(x, y, width, height).data;
      let opaque = 0;
      for (let i = 3; i < data.length; i += 4) if (data[i] > 0) opaque++;
      return opaque;
    };
    // A small zone starts at the renderer's 40px pitch cap. Room centres are
    // therefore 40px apart: the source stub ends at -20 and the target square
    // begins at -30, leaving the absent reverse half in between.
    return { source: count(-18, -12), missing: count(-28, -22) };
  });
}

function expectTraceMove(command: string, from: number, to: number, moved: boolean): void {
  expect(traceHasMove(command, from, to, moved)).toBe(true);
}

function traceHasMove(command: string, from: number, to: number, moved: boolean): boolean {
  const rows = readFileSync(stack.tracePath, "utf8").trim().split("\n")
    .filter(Boolean)
    .map((line) => JSON.parse(line) as { command: string; from: number; to: number; moved: boolean });
  return rows.some((row) => row.command === command && row.from === from && row.to === to && row.moved === moved);
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

async function connectAnchorAndArm(page: Page, expectedRoom = "Loop Room 1"): Promise<void> {
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
  }).toBe(expectedRoom);
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
  return page.getByRole("button", { name: "OBS records one explored room at a time; wait for the new cell to turn green before moving again" });
}

async function openCurrentRoomKnowledge(page: Page, roomName: string) {
  const canvas = page.locator(".map-pane__canvas");
  const box = await canvas.boundingBox();
  expect(box).not.toBeNull();
  await canvas.click({ position: { x: box!.width / 2, y: box!.height / 2 } });
  const menu = page.locator(".map-cell-menu");
  await expect(menu).toBeVisible();
  await menu.getByRole("button", { name: "Knowledge…" }).click();
  const dialog = page.getByRole("dialog", { name: "Knowledge" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText(roomName);
  return dialog;
}

async function openRoomOneKnowledge(page: Page) {
	return openCurrentRoomKnowledge(page, "Loop Room 1");
}

async function addNPCToRoomOne(page: Page, topologyRevisionBefore: number): Promise<void> {
  const dialog = await openRoomOneKnowledge(page);
  await expect(dialog).toContainText("No inhabitants have been recorded for this room.");
  await dialog.getByRole("button", { name: "Add new" }).click();
  const form = dialog.locator(".map-inhabitant-form");
  await expect(form).toBeVisible();
  await form.getByLabel("Name").fill("Хранитель круга");
  await form.getByLabel("Type").selectOption("npc");
  await form.getByLabel("Look target").fill("хранитель");
  await form.getByLabel("Look result").fill("Высокий страж в плаще с вышитым знаком круга.");
	await form.getByLabel("Interaction strategy").fill("Сначала назвать пароль.");
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
  expectNPCStored(topologyRevisionBefore, [{ tag: 1, resident: 0, presence_text: "" }]);
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toBeHidden();
}

async function expectNPCInRoomOne(page: Page, topologyRevisionBefore: number): Promise<void> {
  const dialog = await openRoomOneKnowledge(page);
  const card = dialog.locator(".map-inhabitant-card", { hasText: "Хранитель круга" });
  await expect(card).toContainText("Look target: хранитель");
  await expect(card).toContainText("Высокий страж в плаще с вышитым знаком круга.");
  expectNPCStored(topologyRevisionBefore, [
	{ tag: 1, resident: 0, presence_text: "" },
	{ tag: 2, resident: 1, presence_text: "Хранитель проверяет восточный зал." },
  ]);
  await dialog.getByRole("button", { name: "Close" }).click();
}

function expectNPCStored(topologyRevisionBefore: number, placements: Array<{ tag: number; resident: number; presence_text: string }>): void {
  expect(stack.query(
    `SELECT name,kind,look_target,look_text FROM map_inhabitants WHERE map_set_id=${stack.seed.map_set_id}`,
  )).toEqual([{
    name: "Хранитель круга", kind: "npc", look_target: "хранитель",
    look_text: "Высокий страж в плаще с вышитым знаком круга.",
  }]);
  expect(stack.query(
    `SELECT r.tag,ri.resident,ri.presence_text FROM room_inhabitants ri JOIN rooms r ON r.map_set_id=ri.map_set_id AND r.zone=ri.zone AND r.x=ri.x AND r.y=ri.y AND r.l=ri.l WHERE ri.map_set_id=${stack.seed.map_set_id} ORDER BY r.tag`,
  )).toEqual(placements);
  expect(topologyRevision()).toBe(topologyRevisionBefore);
}

async function reuseNPCInRoomTwoAndSynchronizeMCP(page: Page, topologyRevisionBefore: number): Promise<void> {
	const entity = stack.query<{ id: number }>(
	  `SELECT id FROM map_inhabitants WHERE map_set_id=${stack.seed.map_set_id}`,
	);
	expect(entity).toHaveLength(1);
	const inhabitantID = entity[0]!.id;

	const lookAtRoomOne = await mcpTool("mud_look_map", { session_id: stack.seed.session_id });
	expect(lookAtRoomOne).toContain("Known inhabitants:");
	expect(lookAtRoomOne).toContain("Хранитель круга [npc]");
	expect(lookAtRoomOne).toContain("strategy=Сначала назвать пароль.");
	expect(lookAtRoomOne).not.toContain("Высокий страж в плаще");

	await sendAndExpectRoom(page, "E", "Loop Room 2");
	const dialog = await openCurrentRoomKnowledge(page, "Loop Room 2");
	await expect(dialog).toContainText("No inhabitants have been recorded for this room.");
	await dialog.getByRole("button", { name: "Place existing" }).click();
	await dialog.getByLabel("Search catalog").fill("хранитель");
	const candidate = dialog.locator(".map-inhabitant-catalog__item", { hasText: "Хранитель круга" });
	await expect(candidate).toBeVisible();
	await candidate.getByRole("button", { name: "Place" }).click();
	const placement = dialog.locator(".map-inhabitant-placement-form");
	await placement.getByLabel("Presence text").fill("Хранитель осматривает восточный зал.");
	await placement.getByLabel("Permanent resident of this room").check();
	await placement.getByRole("button", { name: "Place" }).click();
	await expect(dialog.locator(".map-inhabitant-card", { hasText: "Хранитель круга" })).toBeVisible();

	const updated = await mcpTool("mud_inhabitant_write", {
	  session_id: stack.seed.session_id, operation: "update", inhabitant_id: inhabitantID,
	  room: stack.seed.rooms["2"], name: "Хранитель круга", kind: "npc", look_target: "хранитель",
	  look_text: "Высокий страж в плаще с вышитым знаком круга.", note: "Помнит обходы.",
	  strategy: "Назвать пароль и предложить чай.", presence_text: "Хранитель проверяет восточный зал.", resident: true,
	});
	expect(updated).toContain(`updated inhabitant id=${inhabitantID}`);
	const refreshed = dialog.locator(".map-inhabitant-card", { hasText: "Хранитель круга" });
	await expect(refreshed).toContainText("Presence: Хранитель проверяет восточный зал.");
	await expect(refreshed).toContainText("Strategy: Назвать пароль и предложить чай.");

	const listed = await mcpTool("mud_inhabitants", {
	  session_id: stack.seed.session_id, room: stack.seed.rooms["2"], limit: 20,
	});
	expect(listed).toContain(`id=${inhabitantID}`);
	expect(listed).toContain("resident=true");
	expect(listed).not.toContain("Высокий страж в плаще");
	const detail = await mcpTool("mud_inhabitant", { session_id: stack.seed.session_id, inhabitant_id: inhabitantID });
	expect(detail).toContain("Высокий страж в плаще с вышитым знаком круга.");
	expect(detail).toContain("placements: 2");
	const lookAtRoomTwo = await mcpTool("mud_look_map", { session_id: stack.seed.session_id });
	expect(lookAtRoomTwo).toContain("Хранитель круга [npc, resident]");
	expect(lookAtRoomTwo).not.toContain("Высокий страж в плаще");

	expectNPCStored(topologyRevisionBefore, [
	  { tag: 1, resident: 0, presence_text: "" },
	  { tag: 2, resident: 1, presence_text: "Хранитель проверяет восточный зал." },
	]);
	await dialog.getByRole("button", { name: "Close" }).click();
	await sendAndExpectRoom(page, "W", "Loop Room 1");
}

async function mcpTool(name: string, args: Record<string, unknown>): Promise<string> {
	const response = await fetch(`${stack.baseURL}/mcp`, {
	  method: "POST", headers: { "Content-Type": "application/json" },
	  body: JSON.stringify({ jsonrpc: "2.0", method: "tools/call", id: 1, params: { name, arguments: args } }),
	});
	expect(response.ok).toBeTruthy();
	const body = await response.json() as { result?: { isError?: boolean; content?: Array<{ text?: string }> }; error?: unknown };
	expect(body.error).toBeUndefined();
	expect(body.result?.isError).not.toBe(true);
	return body.result?.content?.[0]?.text ?? "";
}

async function sendAndExpectRoom(page: Page, command: string, hint: string): Promise<void> {
	const token = await page.evaluate(() => (window as Window & { API_TOKEN?: string }).API_TOKEN ?? "");
	await page.locator("#input").fill(command);
	await page.locator("#input").press("Enter");
	await expect.poll(async () => {
	  const response = await page.request.get(`${stack.baseURL}/api/sessions/${stack.seed.session_id}/map-live-room`, {
		headers: { "X-Session-Token": token },
	  });
	  return (await response.json()).hint;
	}).toBe(hint);
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

function expectTraceLoops(loopCount: number, withKnowledgeDetour = false): void {
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
  const expected = Array.from({ length: loopCount }, () => oneLoop).flat();
	if (withKnowledgeDetour) {
	  expected.splice(oneLoop.length, 0,
		{ from: 1, command: "E", to: 2, moved: true },
		{ from: 2, command: "W", to: 1, moved: true },
	  );
	}
  expect(rows.map(({ from, command, to, moved }) => ({ from, command, to, moved })))
    .toEqual(expected);
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
