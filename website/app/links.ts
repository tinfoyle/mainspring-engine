const defaultAppOrigin = "https://app.infiniteocean.net";

function configuredAppOrigin(): string {
  const candidate = process.env.NEXT_PUBLIC_SPYGLASS_APP_ORIGIN ?? defaultAppOrigin;
  const parsed = new URL(candidate);
  if (parsed.protocol !== "https:" || parsed.origin !== candidate || parsed.username || parsed.password) {
    throw new Error("NEXT_PUBLIC_SPYGLASS_APP_ORIGIN must be an exact HTTPS origin");
  }
  return parsed.origin;
}

export function spyglassURL(path: "/" | "/signup", offerCode?: string): string {
  const target = new URL(path, configuredAppOrigin());
  if (offerCode) target.searchParams.set("offer", offerCode);
  return target.toString();
}
