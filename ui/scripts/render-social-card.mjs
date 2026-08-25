import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { Resvg } from "@resvg/resvg-js";

const source = fileURLToPath(new URL("../apps/public/public/og/spyglass-social.svg", import.meta.url));
const destination = fileURLToPath(new URL("../apps/public/public/og/spyglass-social.png", import.meta.url));
const svg = await readFile(source);
const rendered = new Resvg(svg, {
  fitTo: { mode: "width", value: 1200 },
  font: {
    loadSystemFonts: true,
    defaultFontFamily: "DejaVu Sans",
    sansSerifFamily: "DejaVu Sans",
    serifFamily: "DejaVu Serif"
  }
}).render();

if (rendered.width !== 1200 || rendered.height !== 630) {
  throw new Error(`Expected a 1200x630 social card, received ${rendered.width}x${rendered.height}`);
}

let lightPixels = 0;
const pixels = rendered.pixels;
for (let y = 180; y < 480; y += 1) {
  for (let x = 60; x < 1140; x += 1) {
    const offset = (y * rendered.width + x) * 4;
    if (pixels[offset] > 170 && pixels[offset + 1] > 170 && pixels[offset + 2] > 170 && pixels[offset + 3] > 200) {
      lightPixels += 1;
    }
  }
}
if (lightPixels < 3_000) {
  throw new Error("Social-card text did not render. Install DejaVu Sans and DejaVu Serif before regenerating the PNG.");
}

await writeFile(destination, rendered.asPng());
console.log(`Rendered ${rendered.width}x${rendered.height} social card to ${destination}`);
