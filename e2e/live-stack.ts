import { spawn, execFileSync, type ChildProcess } from "node:child_process";
import { chmodSync, createWriteStream, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
export const repoRoot = resolve(here, "..");

type SeedResult = {
  session_id: number;
  map_set_id: number;
  rooms: Record<string, { zone: string; x: number; y: number; l: number }>;
};

export class LiveStack {
  readonly runDir: string;
  readonly artifactsDir: string;
  readonly dbPath: string;
  readonly tracePath: string;
  readonly seed: SeedResult;
  readonly baseURL: string;

  private emulator: ChildProcess;
  private mudhost: ChildProcess | null = null;
  private webPort: number;

  private constructor(runDir: string, emulator: ChildProcess, seed: SeedResult, webPort: number) {
    this.runDir = runDir;
    this.artifactsDir = join(runDir, "artifacts");
    this.dbPath = join(runDir, "mudhost.db");
    this.tracePath = join(runDir, "mud-trace.jsonl");
    this.emulator = emulator;
    this.seed = seed;
    this.webPort = webPort;
    this.baseURL = `http://127.0.0.1:${webPort}`;
  }

  static async start(): Promise<LiveStack> {
    const runDir = mkdtempSync(join(tmpdir(), "rubymud-mapper-e2e-"));
    const artifactsDir = join(runDir, "artifacts");
    mkdirSync(artifactsDir, { recursive: true });
    const tracePath = join(runDir, "mud-trace.jsonl");
    const emulator = spawn(join(repoRoot, "bin", "e2e-five-room-mud"), [
      "--listen", "127.0.0.1:0", "--trace", tracePath,
    ], { cwd: repoRoot, stdio: ["ignore", "pipe", "pipe"] });
    emulator.stderr!.pipe(createWriteStream(join(artifactsDir, "emulator.log")));
    const ready = await readJSONLine(emulator, 10_000) as { ready: boolean; host: string; port: number };
    if (!ready.ready || ready.host !== "127.0.0.1" || !Number.isInteger(ready.port)) {
      await stopChild(emulator);
      throw new Error(`invalid emulator readiness: ${JSON.stringify(ready)}`);
    }

    const dbPath = join(runDir, "mudhost.db");
    let seed: SeedResult;
    try {
      const output = execFileSync(join(repoRoot, "bin", "e2e-mapper-fixture"), [
        "--run-dir", runDir, "--db", dbPath,
        "--mud-host", ready.host, "--mud-port", String(ready.port),
      ], { cwd: repoRoot, encoding: "utf8", timeout: 15_000 });
      seed = JSON.parse(output) as SeedResult;
    } catch (error) {
      await stopChild(emulator);
      throw error;
    }

    const webPort = await reserveLoopbackPort();
    const stack = new LiveStack(runDir, emulator, seed, webPort);
    await stack.startMudhost();
    return stack;
  }

  async restartMudhost(): Promise<void> {
    if (this.mudhost) {
      await stopChild(this.mudhost);
      this.mudhost = null;
    }
    await this.startMudhost();
  }

  query<T = Record<string, unknown>>(sql: string): T[] {
    const output = execFileSync("sqlite3", ["-json", this.dbPath, sql], {
      cwd: repoRoot, encoding: "utf8", timeout: 10_000,
    }).trim();
    return output ? JSON.parse(output) as T[] : [];
  }

  async cleanup(preserve: boolean): Promise<string | null> {
    if (this.mudhost) await stopChild(this.mudhost);
    await stopChild(this.emulator);
    if (!preserve) {
      rmSync(this.runDir, { recursive: true, force: true });
      return null;
    }
	this.prepareShareableArtifacts();
    const targetRoot = join(repoRoot, "tmp", "e2e-artifacts");
    mkdirSync(targetRoot, { recursive: true });
    const target = join(targetRoot, basename(this.runDir));
    try {
      renameSync(this.runDir, target);
    } catch {
      cpSync(this.runDir, target, { recursive: true });
      rmSync(this.runDir, { recursive: true, force: true });
    }
    return target;
  }

	private prepareShareableArtifacts(): void {
		for (const name of ["browser.log", "mudhost.log", "emulator.log"]) {
			const path = join(this.artifactsDir, name);
			if (!existsSync(path)) continue;
			const sanitized = redactLog(readFileSync(path, "utf8"), String(this.seed.session_id));
			writeFileSync(path, sanitized);
			chmodSync(path, 0o600);
			if (sanitized.includes(`session_id=${this.seed.session_id}`)) throw new Error(`${name} still contains the session credential`);
		}
		chmodSync(this.dbPath, 0o600);
		for (const suffix of ["-shm", "-wal"]) {
			const sidecar = `${this.dbPath}${suffix}`;
			if (existsSync(sidecar)) chmodSync(sidecar, 0o600);
		}
		writeFileSync(join(this.artifactsDir, "SECURITY.md"), [
			"# Sensitive local diagnostics",
			"",
			"Text logs are sanitized before preservation. Playwright network tracing is disabled.",
			"The full `mudhost.db` is retained for local diagnosis and may contain API/session credentials.",
			"It is mode 0600 and must not be uploaded or shared; delete the artifact directory after use.",
			"",
		].join("\n"), { mode: 0o600 });
	}

  private async startMudhost(): Promise<void> {
    if (!existsSync(join(repoRoot, "bin", "mudhost"))) throw new Error("bin/mudhost is missing; run the E2E wrapper");
    const out = createWriteStream(join(this.artifactsDir, "mudhost.log"), { flags: "a" });
    this.mudhost = spawn(join(repoRoot, "bin", "mudhost"), [
      "--db", this.dbPath,
      "--config-dir", join(this.runDir, "config"),
      "--listen", `127.0.0.1:${this.webPort}`,
	  "--mcp",
    ], { cwd: repoRoot, stdio: ["ignore", "pipe", "pipe"] });
    this.mudhost.stdout!.pipe(out, { end: false });
    this.mudhost.stderr!.pipe(out, { end: false });
    await waitForHTTP(`${this.baseURL}/`, this.mudhost, 15_000);
  }
}

function redactLog(text: string, sessionID: string): string {
	return text
		.replace(new RegExp(`([?&]session_id=)${sessionID}(?=(&|\\s|$))`, "g"), "$1[REDACTED]")
		.replace(/([?&](?:token|api_token|preview_token)=)[^&\s]+/gi, "$1[REDACTED]")
		.replace(/(authorization:\s*bearer\s+)[^\s]+/gi, "$1[REDACTED]")
		.replace(/(x-session-token["':=\s]+)[^\s,"'}]+/gi, "$1[REDACTED]");
}

async function reserveLoopbackPort(): Promise<number> {
  return await new Promise((resolvePort, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (!address || typeof address === "string") return reject(new Error("failed to reserve port"));
      server.close((error) => error ? reject(error) : resolvePort(address.port));
    });
  });
}

async function readJSONLine(child: ChildProcess, timeoutMS: number): Promise<unknown> {
  return await new Promise((resolveLine, reject) => {
    let buffer = "";
    const timer = setTimeout(() => reject(new Error("emulator readiness timed out")), timeoutMS);
    child.once("exit", (code) => reject(new Error(`emulator exited before readiness (${code})`)));
    child.stdout!.on("data", (chunk: Buffer) => {
      buffer += chunk.toString("utf8");
      const newline = buffer.indexOf("\n");
      if (newline < 0) return;
      clearTimeout(timer);
      try { resolveLine(JSON.parse(buffer.slice(0, newline))); }
      catch (error) { reject(error); }
    });
  });
}

async function waitForHTTP(url: string, child: ChildProcess, timeoutMS: number): Promise<void> {
  const deadline = Date.now() + timeoutMS;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`mudhost exited before readiness (${child.exitCode})`);
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch { /* readiness poll */ }
    await new Promise((resolveWait) => setTimeout(resolveWait, 50));
  }
  throw new Error(`mudhost readiness timed out: ${url}`);
}

async function stopChild(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGTERM");
  const exited = new Promise<void>((resolveExit) => child.once("exit", () => resolveExit()));
  const graceful = await Promise.race([
    exited.then(() => true),
    new Promise<boolean>((resolveWait) => setTimeout(() => resolveWait(false), 3_000)),
  ]);
  if (!graceful && child.exitCode === null) {
    child.kill("SIGKILL");
    await exited;
  }
}
