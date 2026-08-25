import { beforeEach, describe, expect, it, vi } from "vitest";

const captures = vi.hoisted(() => ({
  seo: [] as Array<Record<string, unknown>>,
  head: [] as Array<{ link?: Array<Record<string, unknown>>; script?: Array<Record<string, unknown>> }>
}));

vi.mock("#imports", () => ({
  useSeoMeta: (value: Record<string, unknown>) => captures.seo.push(value),
  useHead: (value: { link?: Array<Record<string, unknown>>; script?: Array<Record<string, unknown>> }) => captures.head.push(value)
}));

import { usePublicSeo } from "../app/composables/usePublicSeo";

beforeEach(() => {
  captures.seo.length = 0;
  captures.head.length = 0;
});

describe("public discovery metadata", () => {
  it("publishes canonical, crawl, social-card and default structured metadata", () => {
    usePublicSeo({ title: "Work · Spyglass", description: "Governed work.", path: "/features/work" });

    expect(captures.seo).toEqual([expect.objectContaining({
      title: "Work · Spyglass",
      robots: "index, follow, max-image-preview:large",
      ogSiteName: "Infinite Ocean",
      ogUrl: "https://www.infiniteocean.net/features/work",
      ogImage: "https://www.infiniteocean.net/og/spyglass-social.png",
      ogImageWidth: 1200,
      ogImageHeight: 630,
      twitterCard: "summary_large_image"
    })]);
    expect(captures.head[0]?.link).toEqual([{ rel: "canonical", href: "https://www.infiniteocean.net/features/work" }]);

    const script = captures.head[0]?.script?.[0];
    expect(script).toEqual(expect.objectContaining({ id: "spyglass-structured-data", type: "application/ld+json" }));
    expect(JSON.parse(String(script?.textContent))).toEqual(expect.objectContaining({
      "@type": "WebPage",
      url: "https://www.infiniteocean.net/features/work"
    }));
  });

  it("escapes markup-significant characters in supplied JSON-LD", () => {
    usePublicSeo({
      title: "Safe metadata",
      description: "Structured metadata remains inert.",
      path: "/privacy",
      schema: { "@context": "https://schema.org", "@type": "WebPage", name: "</script><script>alert(1)</script>" }
    });

    const serialized = String(captures.head[0]?.script?.[0]?.textContent);
    expect(serialized).not.toContain("<");
    expect(JSON.parse(serialized).name).toBe("</script><script>alert(1)</script>");
  });
});
