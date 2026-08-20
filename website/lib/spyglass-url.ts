export function spyglassURLForOrigin(appOrigin: string, path: "/" | "/signup", offerCode?: string): string {
  const target = new URL(path, appOrigin);
  if (offerCode) target.searchParams.set("offer", offerCode);
  return target.toString();
}
