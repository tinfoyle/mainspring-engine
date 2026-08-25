import vue from "@vitejs/plugin-vue";
import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      "#imports": fileURLToPath(new URL("./test/nuxt-imports.ts", import.meta.url)),
      "~": fileURLToPath(new URL("./app", import.meta.url))
    }
  }
});
