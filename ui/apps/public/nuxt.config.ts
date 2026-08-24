export default defineNuxtConfig({
  compatibilityDate: "2026-08-24",
  devtools: { enabled: false },
  runtimeConfig: {
    public: { appOrigin: process.env.NUXT_PUBLIC_APP_ORIGIN ?? "https://app.infiniteocean.net" }
  },
  css: ["@spyglass/design-system/tokens.css", "~/assets/site.css"],
  app: {
    head: {
      htmlAttrs: { lang: "en" },
      meta: [
        { name: "viewport", content: "width=device-width, initial-scale=1" },
        { name: "theme-color", content: "#052f39" }
      ]
    }
  },
  nitro: { preset: "node-server" },
  typescript: { typeCheck: true, strict: true }
});
