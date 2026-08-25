import {
  beginPasskeyReauthentication, beginPasskeyRegistration, completePasskeyReauthentication,
  completePasskeyRegistration, type PasskeyCredential, type WebAuthnAssertionCredential,
  type WebAuthnCreationCredential, type WebAuthnCeremony
} from "@spyglass/api";

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
function options(ceremony: WebAuthnCeremony, creation: boolean): PublicKeyCredentialCreationOptions | PublicKeyCredentialRequestOptions {
  const value = structuredClone(ceremony.public_key.publicKey) as Record<string, unknown>;
  value.challenge = decode(String(value.challenge));
  if (creation) {
    const user = value.user as Record<string, unknown>; user.id = decode(String(user.id));
    for (const item of (value.excludeCredentials ?? []) as Array<Record<string, unknown>>) item.id = decode(String(item.id));
  } else for (const item of (value.allowCredentials ?? []) as Array<Record<string, unknown>>) item.id = decode(String(item.id));
  return value as unknown as PublicKeyCredentialCreationOptions | PublicKeyCredentialRequestOptions;
}
function base(credential: PublicKeyCredential): Pick<WebAuthnCreationCredential, "id" | "rawId" | "type" | "authenticatorAttachment" | "clientExtensionResults"> {
  const attachment = credential.authenticatorAttachment;
  return {
    id: credential.id, rawId: encode(credential.rawId), type: "public-key",
    ...(attachment === "platform" || attachment === "cross-platform" ? { authenticatorAttachment: attachment } : {}),
    clientExtensionResults: credential.getClientExtensionResults() as unknown as Readonly<Record<string, unknown>>
  };
}
function creationJSON(credential: PublicKeyCredential): WebAuthnCreationCredential {
  const response = credential.response as AuthenticatorAttestationResponse;
  return { ...base(credential), response: { clientDataJSON: encode(response.clientDataJSON), attestationObject: encode(response.attestationObject), transports: response.getTransports?.() } };
}
function assertionJSON(credential: PublicKeyCredential): WebAuthnAssertionCredential {
  const response = credential.response as AuthenticatorAssertionResponse;
  return { ...base(credential), response: { clientDataJSON: encode(response.clientDataJSON), authenticatorData: encode(response.authenticatorData), signature: encode(response.signature), userHandle: response.userHandle ? encode(response.userHandle) : null } };
}
function supported(): void {
  if (!window.PublicKeyCredential || !navigator.credentials) throw new Error("This browser does not support passkeys.");
}
export async function registerPasskey(name: string): Promise<PasskeyCredential> {
  supported(); const ceremony = await beginPasskeyRegistration();
  const credential = await navigator.credentials.create({ publicKey: options(ceremony, true) as PublicKeyCredentialCreationOptions });
  if (!(credential instanceof PublicKeyCredential)) throw new Error("Passkey creation was canceled.");
  return completePasskeyRegistration(ceremony.ceremony_id, name, creationJSON(credential));
}
export async function reauthenticateWithPasskey(): Promise<void> {
  supported(); const ceremony = await beginPasskeyReauthentication();
  const credential = await navigator.credentials.get({ publicKey: options(ceremony, false) as PublicKeyCredentialRequestOptions });
  if (!(credential instanceof PublicKeyCredential)) throw new Error("Passkey confirmation was canceled.");
  return completePasskeyReauthentication(ceremony.ceremony_id, assertionJSON(credential));
}
