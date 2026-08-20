import { exactRuntimeOrigin } from "@/lib/runtime-origin";
import { spyglassURLForOrigin } from "@/lib/spyglass-url";

const defaultAppOrigin = "https://app.infiniteocean.net";

export function configuredAppOrigin(): string {
  return exactRuntimeOrigin(process.env.SPYGLASS_APP_ORIGIN ?? defaultAppOrigin, "SPYGLASS_APP_ORIGIN", process.env.SPYGLASS_ENVIRONMENT);
}

export function spyglassURL(path: "/" | "/signup", offerCode?: string): string {
  return spyglassURLForOrigin(configuredAppOrigin(), path, offerCode);
}
