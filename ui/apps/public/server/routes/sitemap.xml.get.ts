import { publicFeatures } from "../../app/content/features";

const origin = "https://www.infiniteocean.net";
const routes = ["/", "/features", ...publicFeatures.map((feature) => `/features/${feature.slug}`), "/pricing", "/privacy", "/affiliate-terms"];

export default defineEventHandler((event): string => {
  setResponseHeader(event, "Content-Type", "application/xml; charset=utf-8");
  setResponseHeader(event, "Cache-Control", "public, max-age=3600, stale-while-revalidate=86400");
  const entries = routes.map((path) => `  <url><loc>${origin}${path === "/" ? "/" : path}</loc></url>`).join("\n");
  return `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${entries}\n</urlset>\n`;
});
