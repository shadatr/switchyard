// Captures dashboard screenshots into docs/screenshots using headless Chrome
// over the DevTools protocol (no extra dependencies; Node 22+).
//
//   SWITCHYARD_ADMIN_TOKEN=... node scripts/screenshots.mjs
//
// Expects the dashboard on DASHBOARD_URL (default http://localhost:3100) and a
// running gateway with data. The admin token is put in sessionStorage — it is
// never placed in a URL.
import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";
import { setTimeout as sleep } from "node:timers/promises";

const CHROME = process.env.CHROME ?? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const BASE = process.env.DASHBOARD_URL ?? "http://localhost:3100";
const TOKEN = process.env.SWITCHYARD_ADMIN_TOKEN;
const OUT = process.env.SCREENSHOT_DIR ? process.env.SCREENSHOT_DIR.replace(/\/?$/, "/") : new URL("../../docs/screenshots/", import.meta.url).pathname;
const PORT = 9333;
const PROFILE = "/tmp/switchyard-screenshots";

if (!TOKEN) {
  console.error("SWITCHYARD_ADMIN_TOKEN is required");
  process.exit(1);
}
mkdirSync(OUT, { recursive: true });
rmSync(PROFILE, { recursive: true, force: true });

const chrome = spawn(CHROME, ["--headless=new", `--remote-debugging-port=${PORT}`, "--hide-scrollbars",
  `--user-data-dir=${PROFILE}`, "--no-first-run", "about:blank"], { stdio: "ignore" });

let ws;
try {
  let targets;
  for (let i = 0; i < 50 && !targets; i++) {
    await sleep(200);
    targets = await fetch(`http://127.0.0.1:${PORT}/json`).then((r) => r.json()).catch(() => undefined);
  }
  const page = targets.find((t) => t.type === "page");
  ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject; });

  let seq = 0;
  const pending = new Map();
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id && pending.has(m.id)) {
      pending.get(m.id)(m);
      pending.delete(m.id);
    }
  };
  const send = (method, params = {}) => new Promise((resolve) => {
    const id = ++seq;
    pending.set(id, resolve);
    ws.send(JSON.stringify({ id, method, params }));
  });
  const run = (expression) => send("Runtime.evaluate", { expression, awaitPromise: true });
  const viewport = (width, height) => send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 2, mobile: width < 600 });

  async function shot(file, { path = "/", width = 1440, height = 900, full = false, wait = 3000, setup, range } = {}) {
    await viewport(width, height);
    if (range) await run(`localStorage.setItem("switchyard.range", ${JSON.stringify(range)})`);
    await send("Page.navigate", { url: BASE + path });
    await sleep(wait);
    if (setup) {
      await run(setup);
      await sleep(1500);
    }
    if (full) {
      // Grow the viewport to the page height so fixed/sticky elements (the
      // sidebar) render at full length instead of stopping at the fold.
      const m = await send("Page.getLayoutMetrics");
      await viewport(width, Math.ceil(m.result.cssContentSize.height));
      await sleep(800);
    }
    const r = await send("Page.captureScreenshot", { format: "png" });
    writeFileSync(OUT + file, Buffer.from(r.result.data, "base64"));
    console.log("saved", file);
  }

  await send("Page.enable");
  // Signed-out first.
  await shot("signin.png", { wait: 2000 });
  // Sign in by seeding sessionStorage for this origin.
  await run(`sessionStorage.setItem("switchyard.admin_token", ${JSON.stringify(TOKEN)})`);

  const clickText = (sel, text) =>
    `[...document.querySelectorAll(${JSON.stringify(sel)})].find(e => e.textContent.trim().includes(${JSON.stringify(text)}))?.click()`;

  await shot("overview.png", { range: "1h", full: true, wait: 4000 });
  await shot("overview-24h.png", { range: "24h", full: true, wait: 4000 });
  await shot("overview-7d.png", { range: "7d", wait: 4000 });
  await shot("overview-mobile.png", { range: "1h", width: 390, height: 844, full: true, wait: 4000 });
  await shot("requests.png", { path: "/requests", range: "1h" });
  await shot("request-drawer.png", { path: "/requests", range: "1h", setup: `document.querySelector("tbody tr")?.click()` });
  await shot("live.png", { path: "/live", wait: 6000 });
  await shot("keys.png", { path: "/keys" });
  await shot("keys-create.png", {
    path: "/keys",
    setup: `(${clickText("button", "Create key")}, new Promise(r => setTimeout(r, 500)))`,
  });
  await shot("targets.png", { path: "/targets" });
} finally {
  ws?.close();
  chrome.kill();
}
