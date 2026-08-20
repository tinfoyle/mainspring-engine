const httpEnvironments = new Set(["development", "local", "stage", "test"]);

export function exactRuntimeOrigin(candidate: string, name: string, environment = "production"): string {
  let parsed: URL;
  try {
    parsed = new URL(candidate);
  } catch {
    throw new Error(`${name} must be an exact origin`);
  }
  const protocolAllowed = parsed.protocol === "https:" || (parsed.protocol === "http:" && httpEnvironments.has(environment));
  if (!protocolAllowed || parsed.origin !== candidate || parsed.username || parsed.password || parsed.pathname !== "/" || parsed.search || parsed.hash) {
    throw new Error(`${name} must be an exact ${httpEnvironments.has(environment) ? "HTTP(S)" : "HTTPS"} origin`);
  }
  return parsed.origin;
}
