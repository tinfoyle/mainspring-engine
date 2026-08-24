import { fileURLToPath, URL } from "node:url";
import vue from "@vitejs/plugin-vue";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [vue()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  server: {
    port: 4173,
    proxy: { "/api": { target: process.env.SPYGLASS_ACCOUNT_API_ORIGIN ?? "http://127.0.0.1:8080" } }
  },
  build: { outDir: "dist", assetsDir: "ui-assets", sourcemap: true }
});
