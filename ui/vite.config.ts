import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: resolve(__dirname, "../web/assets"),
    emptyOutDir: false,
    sourcemap: false,
    rollupOptions: {
      input: resolve(__dirname, "src/main.tsx"),
      output: {
        entryFileNames: "v2.js",
        chunkFileNames: "v2-[name].js",
        assetFileNames: (asset) => asset.name?.endsWith(".css") ? "v2.css" : "v2-[name][extname]"
      }
    }
  }
});
