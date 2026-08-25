import { gzipSync } from "node:zlib";
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const kib = 1024;
const budgets = [
  { label: "private SPA JavaScript", directory: "apps/app/dist/ui-assets", suffix: ".js", maximum: 140 * kib },
  { label: "private SPA CSS", directory: "apps/app/dist/ui-assets", suffix: ".css", maximum: 20 * kib },
  { label: "public site JavaScript", directory: "apps/public/.output/public/_nuxt", suffix: ".js", maximum: 120 * kib },
  { label: "public site CSS", directory: "apps/public/.output/public/_nuxt", suffix: ".css", maximum: 15 * kib }
];

let failed = false;
for (const budget of budgets) {
  const directory = resolve(budget.directory);
  const files = readdirSync(directory).filter((name) => name.endsWith(budget.suffix)).sort();
  if (files.length === 0) throw new Error(`${budget.label} has no built ${budget.suffix} assets`);
  const compressed = gzipSync(Buffer.concat(files.map((name) => readFileSync(resolve(directory, name))))).byteLength;
  const actual = (compressed / kib).toFixed(1);
  const maximum = (budget.maximum / kib).toFixed(0);
  console.log(`${budget.label}: ${actual} KiB gzip / ${maximum} KiB budget`);
  if (compressed > budget.maximum) failed = true;
}

if (failed) {
  console.error("UI performance budget exceeded; split or remove customer-delivered code before release.");
  process.exitCode = 1;
}
