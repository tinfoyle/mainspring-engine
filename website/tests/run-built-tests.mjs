import { spawn } from "node:child_process";
import { cp, mkdir, readdir } from "node:fs/promises";
import http from "node:http";
import net from "node:net";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { publicCatalogFixture } from "./fixtures/public-catalog.mjs";

const websiteRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");

async function freePort() {
  const server = net.createServer();
  await new Promise((resolveListen, reject) => server.listen(0, "127.0.0.1", (error) => error ? reject(error) : resolveListen()));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("could not allocate test port");
  await new Promise((resolveClose, reject) => server.close((error) => error ? reject(error) : resolveClose()));
  return address.port;
}

async function listen(server, port) {
  await new Promise((resolveListen, reject) => server.listen(port, "127.0.0.1", (error) => error ? reject(error) : resolveListen()));
}

async function close(server) {
  await new Promise((resolveClose, reject) => server.close((error) => error ? reject(error) : resolveClose()));
}

async function waitForReady(origin, child) {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`Next.js exited before readiness with code ${child.exitCode}`);
    try {
      const response = await fetch(`${origin}/health/ready`, { signal: AbortSignal.timeout(1000) });
      if (response.ok) return;
    } catch {
      // Readiness is expected to fail while the server is still starting.
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 200));
  }
  throw new Error("Next.js did not become ready within 30 seconds");
}

async function stop(child) {
  if (child.exitCode !== null) return;
  child.kill("SIGTERM");
  await Promise.race([
    new Promise((resolveExit) => child.once("exit", resolveExit)),
    new Promise((resolveTimeout) => setTimeout(resolveTimeout, 5000)),
  ]);
  if (child.exitCode === null) child.kill("SIGKILL");
}

const catalogRequests = [];
const catalogPort = await freePort();
const catalogServer = http.createServer((request, response) => {
  if (request.url === "/__reset" && request.method === "POST") {
    catalogRequests.length = 0;
    response.writeHead(204).end();
    return;
  }
  if (request.url === "/__requests" && request.method === "GET") {
    response.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify(catalogRequests));
    return;
  }
  catalogRequests.push({ method: request.method, url: request.url });
  if (request.url === "/api/v1/catalog/public" && request.method === "GET") {
    response.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify(publicCatalogFixture));
    return;
  }
  response.writeHead(404).end("Not found");
});
await listen(catalogServer, catalogPort);

const applicationPort = await freePort();
const applicationOrigin = `http://127.0.0.1:${applicationPort}`;
const catalogOrigin = `http://127.0.0.1:${catalogPort}`;
const standaloneRoot = resolve(websiteRoot, ".next/standalone");
await mkdir(resolve(standaloneRoot, ".next"), { recursive: true });
await cp(resolve(websiteRoot, ".next/static"), resolve(standaloneRoot, ".next/static"), { recursive: true });
await cp(resolve(websiteRoot, "public"), resolve(standaloneRoot, "public"), { recursive: true });
const next = spawn(process.execPath, ["server.js"], {
  cwd: standaloneRoot,
  env: {
    ...process.env,
    HOSTNAME: "127.0.0.1",
    NEXT_TELEMETRY_DISABLED: "1",
    PORT: String(applicationPort),
    SPYGLASS_ACCOUNT_API_ORIGIN: catalogOrigin,
    SPYGLASS_APP_ORIGIN: "https://app.infiniteocean.net",
    SPYGLASS_ENVIRONMENT: "test",
  },
  stdio: ["ignore", "inherit", "inherit"],
});

let exitCode = 1;
try {
  await waitForReady(applicationOrigin, next);
  const testFiles = (await readdir(resolve(websiteRoot, "tests")))
    .filter((name) => name.endsWith(".test.mjs"))
    .sort()
    .map((name) => resolve(websiteRoot, "tests", name));
  const tests = spawn(process.execPath, ["--test", "--test-concurrency=1", ...testFiles], {
    cwd: websiteRoot,
    env: { ...process.env, TEST_CATALOG_ORIGIN: catalogOrigin, TEST_ORIGIN: applicationOrigin },
    stdio: "inherit",
  });
  exitCode = await new Promise((resolveExit, reject) => {
    tests.once("error", reject);
    tests.once("exit", (code, signal) => resolveExit(code ?? (signal ? 1 : 0)));
  });
} finally {
  await stop(next);
  await close(catalogServer);
}

process.exitCode = exitCode;
