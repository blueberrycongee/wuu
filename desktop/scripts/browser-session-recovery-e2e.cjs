// Two real Electron processes verify persisted tabs, zoom and lazy restoration.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawn } = require("node:child_process");
const http = require("node:http");
const desktop = path.resolve(__dirname, "..");
const output = path.resolve(process.env.WUU_BROWSER_SESSION_OUTPUT || path.join(desktop, "out/e2e/browser-session-recovery"));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), "wuu-browser-session-"));
const entry = path.join(desktop, "out/main/browser-session-recovery.cjs");
const electron = process.env.WUU_E2E_ELECTRON || (process.platform === "darwin"
  ? path.join(desktop, "build/dev-host/Wuu Dev.app/Contents/MacOS/Electron") : require("electron"));
const server = http.createServer((_request, response) => {
  response.setHeader("Content-Type", "text/html");
  response.end("<!doctype html><title>Restored reading page</title><h1>Reading at my chosen size</h1><input placeholder='Retained tab'><p>Browser session recovery fixture</p>");
});
async function main() {
  fs.mkdirSync(output, { recursive: true });
  await require("esbuild").build({ entryPoints: [path.join(__dirname, "browser-session-recovery-e2e-entry.ts")],
    bundle: true, platform: "node", format: "cjs", outfile: entry, external: ["electron"], logLevel: "warning" });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  for (const phase of ["seed", "restore"]) {
    await new Promise((resolve, reject) => {
      const child = spawn(electron, [entry], { cwd: desktop, env: { ...process.env,
        WUU_BROWSER_SESSION_PHASE: phase, WUU_BROWSER_SESSION_FIXTURE: fixture,
        WUU_BROWSER_SESSION_URL: `http://127.0.0.1:${server.address().port}`,
        WUU_BROWSER_SESSION_OUTPUT: output }, stdio: ["ignore", "pipe", "pipe"] });
      const log = fs.createWriteStream(path.join(output, `${phase}.log`));
      child.stdout.pipe(log); child.stderr.pipe(log);
      const timer = setTimeout(() => { child.kill("SIGKILL"); reject(new Error(`${phase} timed out`)); }, 45000);
      child.on("error", reject);
      child.on("exit", code => { clearTimeout(timer); log.end(); code === 0 ? resolve() : reject(new Error(`${phase} failed: ${code}; see ${output}`)); });
    });
  }
  const report = JSON.parse(fs.readFileSync(path.join(output, "report.json"), "utf8"));
  assert.equal(report.status, "passed");
  console.log(JSON.stringify(report, null, 2));
}
main().catch(error => { console.error(error); process.exitCode = 1; }).finally(() => { server.close(); });
