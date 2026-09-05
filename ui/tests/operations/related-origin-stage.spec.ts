import { generateKeyPairSync, randomBytes } from "node:crypto";
import { expect, test } from "@playwright/test";

// Explicit Stage-only negative certificate. The synthetic credential exists only
// in Chromium, is never enrolled in an account, and must not open a staff session.
test("Stage accepts the related-origin ceremony but rejects an unknown passkey", async ({ page, context, request }) => {
  test.skip(process.env.SPYGLASS_OPERATIONS_STAGE_CHECK !== "1", "Explicit Stage check required");
  const origin = "https://ops.stage.infiniteocean.net";
  const metadata = await request.get("https://app.stage.infiniteocean.net/.well-known/webauthn");
  expect(metadata.status()).toBe(200);
  expect(await metadata.json()).toEqual({ origins: [origin] });
  const client = await context.newCDPSession(page);
  await client.send("WebAuthn.enable");
  const { authenticatorId } = await client.send("WebAuthn.addVirtualAuthenticator", { options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
  try {
    const { privateKey } = generateKeyPairSync("ec", { namedCurve: "prime256v1" });
    await client.send("WebAuthn.addCredential", { authenticatorId, credential: { credentialId: randomBytes(32).toString("base64"), isResidentCredential: true, rpId: "app.stage.infiniteocean.net", privateKey: privateKey.export({ type: "pkcs8", format: "der" }).toString("base64"), userHandle: randomBytes(32).toString("base64"), signCount: 0 } });
    await page.goto(origin);
    const completed = page.waitForResponse(response => response.url().includes("/passkey-login/challenges/") && response.url().endsWith("/complete"));
    await page.getByRole("button", { name: "Sign in with passkey", exact: true }).click();
    const response = await completed;
    expect([400, 401]).toContain(response.status());
    await expect(page.getByRole("alert")).toBeVisible();
    expect((await context.cookies(origin)).some(cookie => cookie.name === "__Host-spyglass_operations")).toBe(false);
  } finally {
    await client.send("WebAuthn.removeVirtualAuthenticator", { authenticatorId });
    await client.send("WebAuthn.disable");
  }
});
