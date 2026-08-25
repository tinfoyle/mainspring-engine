import { useHead, useSeoMeta } from "#imports";

const siteOrigin = "https://www.infiniteocean.net";
const socialImage = `${siteOrigin}/og/spyglass-social.png`;

interface PublicSeoOptions {
  title: string;
  description: string;
  path: string;
  schema?: Readonly<Record<string, unknown>>;
}

export function usePublicSeo(options: PublicSeoOptions): void {
  const canonical = new URL(options.path, siteOrigin).href;
  const schema = options.schema ?? {
    "@context": "https://schema.org",
    "@type": "WebPage",
    name: options.title,
    description: options.description,
    url: canonical,
    isPartOf: { "@type": "WebSite", name: "Infinite Ocean", url: `${siteOrigin}/` }
  };
  const serializedSchema = JSON.stringify(schema).replaceAll("<", "\\u003c");

  useSeoMeta({
    title: options.title,
    description: options.description,
    robots: "index, follow, max-image-preview:large",
    ogTitle: options.title,
    ogDescription: options.description,
    ogSiteName: "Infinite Ocean",
    ogType: "website",
    ogUrl: canonical,
    ogImage: socialImage,
    ogImageAlt: "Infinite Ocean: Spyglass — Know what needs you next",
    ogImageWidth: 1200,
    ogImageHeight: 630,
    twitterCard: "summary_large_image",
    twitterTitle: options.title,
    twitterDescription: options.description,
    twitterImage: socialImage,
    twitterImageAlt: "Infinite Ocean: Spyglass — Know what needs you next"
  });
  useHead({
    link: [{ rel: "canonical", href: canonical }],
    script: [{ id: "spyglass-structured-data", type: "application/ld+json", textContent: serializedSchema }]
  });
}
