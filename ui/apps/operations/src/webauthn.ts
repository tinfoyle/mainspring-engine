import type { OperationsPasskeyCeremony } from "@spyglass/api";

function decode(value: string): Uint8Array<ArrayBuffer> {
  const normalized = value.replaceAll("-", "+").replaceAll("_", "/");
  const raw = atob(normalized + "=".repeat((4 - normalized.length % 4) % 4));
  return Uint8Array.from(raw, (character) => character.charCodeAt(0));
}

function encode(value: ArrayBuffer): string {
  let raw = "";
  new Uint8Array(value).forEach((byte) => { raw += String.fromCharCode(byte); });
  return btoa(raw).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

export async function getOperationsAssertion(ceremony: OperationsPasskeyCeremony): Promise<Readonly<Record<string, unknown>>> {
  if (!window.PublicKeyCredential || !navigator.credentials) throw new Error("This browser does not support passkeys.");
  const envelope = structuredClone(ceremony.public_key) as Record<string, unknown>;
  const rawOptions = (envelope.publicKey ?? envelope) as Record<string, unknown>;
  rawOptions.challenge = decode(String(rawOptions.challenge));
  for (const item of (rawOptions.allowCredentials ?? []) as Array<Record<string, unknown>>) item.id = decode(String(item.id));
  const credential = await navigator.credentials.get({ publicKey: rawOptions as unknown as PublicKeyCredentialRequestOptions });
  if (!(credential instanceof PublicKeyCredential)) throw new Error("Passkey sign-in was canceled.");
  const response = credential.response as AuthenticatorAssertionResponse;
  const attachment = credential.authenticatorAttachment;
  return {
    id: credential.id,
    rawId: encode(credential.rawId),
    type: "public-key",
    ...(attachment === "platform" || attachment === "cross-platform" ? { authenticatorAttachment: attachment } : {}),
    clientExtensionResults: credential.getClientExtensionResults() as unknown as Readonly<Record<string, unknown>>,
    response: {
      clientDataJSON: encode(response.clientDataJSON),
      authenticatorData: encode(response.authenticatorData),
      signature: encode(response.signature),
      userHandle: response.userHandle ? encode(response.userHandle) : null
    }
  };
}
