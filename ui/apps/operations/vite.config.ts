import { fileURLToPath, URL } from "node:url";
import vue from "@vitejs/plugin-vue";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [vue()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  server: {
    port: 4175,
    proxy: { "/api": { target: process.env.SPYGLASS_OPERATIONS_API_ORIGIN ?? "http://127.0.0.1:8089" } }
  },
  build: { outDir: "dist", assetsDir: "operations-assets", sourcemap: true }
});
