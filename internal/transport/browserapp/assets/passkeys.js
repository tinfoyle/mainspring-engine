(() => {
  "use strict";

  const status = document.querySelector("#passkey-status");
  const encoder = value => {
    const bytes = new Uint8Array(value);
    let raw = "";
    bytes.forEach(byte => { raw += String.fromCharCode(byte); });
    return btoa(raw).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  };
  const decoder = value => {
    const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
    const padded = normalized + "=".repeat((4 - normalized.length % 4) % 4);
    const raw = atob(padded);
    return Uint8Array.from(raw, character => character.charCodeAt(0));
  };
  const setStatus = (message, failed = false) => {
    if (!status) return;
    status.textContent = message;
    status.classList.toggle("error", failed);
  };
  const request = async (path, options = {}) => {
    const response = await fetch(path, { credentials: "same-origin", ...options });
    if (!response.ok) {
      let message = "The passkey operation could not be completed.";
      try {
        const problem = await response.json();
        if (problem.detail) message = problem.detail;
      } catch (_) {}
      const error = new Error(message);
      error.status = response.status;
      throw error;
    }
    if (response.status === 204) return null;
    return response.json();
  };
  const creationOptions = payload => {
    const value = payload.public_key.publicKey || payload.public_key;
    value.challenge = decoder(value.challenge);
    value.user.id = decoder(value.user.id);
    (value.excludeCredentials || []).forEach(item => { item.id = decoder(item.id); });
    return value;
  };
  const requestOptions = payload => {
    const value = payload.public_key.publicKey || payload.public_key;
    value.challenge = decoder(value.challenge);
    (value.allowCredentials || []).forEach(item => { item.id = decoder(item.id); });
    return value;
  };
  const credentialJSON = credential => {
    const response = {};
    ["clientDataJSON", "attestationObject", "authenticatorData", "signature", "userHandle"].forEach(name => {
      if (credential.response[name] != null) response[name] = encoder(credential.response[name]);
    });
    if (typeof credential.response.getTransports === "function") response.transports = credential.response.getTransports();
    return {
      id: credential.id,
      rawId: encoder(credential.rawId),
      type: credential.type,
      authenticatorAttachment: credential.authenticatorAttachment,
      clientExtensionResults: credential.getClientExtensionResults(),
      response
    };
  };
  const body = value => ({ method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(value) });
  const run = async action => {
    if (!window.PublicKeyCredential || !navigator.credentials) {
      setStatus("This browser does not support passkeys.", true);
      return;
    }
    setStatus("Waiting for your passkey…");
    try {
      await action();
    } catch (error) {
      const message = error.name === "NotAllowedError" ? "Passkey confirmation was canceled or timed out." : error.message;
      setStatus(message, true);
    }
  };

  document.querySelector("#passkey-login")?.addEventListener("click", event => run(async () => {
    const begun = await request("/api/v1/passkey-login/challenges", { method: "POST" });
    const credential = await navigator.credentials.get({ publicKey: requestOptions(begun) });
    await request(`/api/v1/passkey-login/challenges/${encodeURIComponent(begun.ceremony_id)}/complete`, body({ credential: credentialJSON(credential), client_label: navigator.userAgent }));
    const target = event.currentTarget.dataset.returnTo;
    window.location.assign(target && target.startsWith("/") && !target.startsWith("//") ? target : "/app");
  }));

  document.querySelector("#passkey-register")?.addEventListener("click", () => run(async () => {
    const name = document.querySelector("#passkey-name")?.value.trim() || "My passkey";
    const begun = await request("/api/v1/passkey-registrations", { method: "POST" });
    const credential = await navigator.credentials.create({ publicKey: creationOptions(begun) });
    await request(`/api/v1/passkey-registrations/${encodeURIComponent(begun.ceremony_id)}/complete`, body({ name, credential: credentialJSON(credential) }));
    window.location.assign("/app/security");
  }));

  document.querySelector("#passkey-reauthenticate")?.addEventListener("click", () => run(async () => {
    const begun = await request("/api/v1/passkey-reauthentications", { method: "POST" });
    const credential = await navigator.credentials.get({ publicKey: requestOptions(begun) });
    await request(`/api/v1/passkey-reauthentications/${encodeURIComponent(begun.ceremony_id)}/complete`, body({ credential: credentialJSON(credential) }));
    window.location.assign("/app/security?status=confirmed");
  }));

  document.querySelectorAll(".passkey-remove").forEach(button => button.addEventListener("click", () => run(async () => {
    if (!window.confirm("Remove this passkey from your Infinite Ocean identity?")) {
      setStatus("");
      return;
    }
    await request(`/api/v1/passkeys/${encodeURIComponent(button.dataset.credentialId)}`, { method: "DELETE" });
    window.location.assign("/app/security");
  })));
})();
