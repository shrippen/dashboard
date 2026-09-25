// Passkey ceremonies: buttons with data-passkey="login" or "register".
// Binary fields travel as base64url in JSON, as ArrayBuffer in WebAuthn.
(() => {
  const toBytes = (s) => Uint8Array.from(atob(s.replace(/-/g, "+").replace(/_/g, "/")), (c) => c.charCodeAt(0));
  const toB64 = (buf) => btoa(String.fromCharCode(...new Uint8Array(buf))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const csrf = () => document.querySelector("meta[name=csrf]")?.content || "";

  async function post(url, body) {
    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf() },
      body: body ? JSON.stringify(body) : "{}",
    });
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || "failed");
    }
    return data;
  }

  // Credential → JSON the server's parser expects.
  function credentialJSON(cred) {
    const r = cred.response;
    const response = { clientDataJSON: toB64(r.clientDataJSON) };
    if (r.attestationObject) {
      response.attestationObject = toB64(r.attestationObject);
      response.transports = r.getTransports ? r.getTransports() : [];
    } else {
      response.authenticatorData = toB64(r.authenticatorData);
      response.signature = toB64(r.signature);
      response.userHandle = r.userHandle ? toB64(r.userHandle) : null;
    }
    return { id: cred.id, rawId: toB64(cred.rawId), type: cred.type, response };
  }

  async function register(button) {
    const options = (await post("/me/passkeys/begin")).publicKey;
    options.challenge = toBytes(options.challenge);
    options.user.id = toBytes(options.user.id);
    for (const c of options.excludeCredentials || []) {
      c.id = toBytes(c.id);
    }
    const cred = await navigator.credentials.create({ publicKey: options });
    const name = document.getElementById(button.dataset.nameField)?.value || "";
    return post("/me/passkeys/finish?name=" + encodeURIComponent(name), credentialJSON(cred));
  }

  async function login() {
    const begin = await post("/login/passkey/begin");
    const options = begin.options.publicKey;
    options.challenge = toBytes(options.challenge);
    const cred = await navigator.credentials.get({ publicKey: options });
    return post("/login/passkey/finish?ceremony=" + encodeURIComponent(begin.ceremony), credentialJSON(cred));
  }

  document.addEventListener("DOMContentLoaded", () => {
    if (!window.PublicKeyCredential) {
      return;
    }
    for (const button of document.querySelectorAll("[data-passkey]")) {
      button.hidden = false;
      button.addEventListener("click", async (event) => {
        event.preventDefault();
        const error = document.querySelector("[data-passkey-error]");
        try {
          const done = await (button.dataset.passkey === "login" ? login() : register(button));
          location.href = done.redirect;
        } catch (e) {
          if (error) {
            error.hidden = false;
          }
        }
      });
    }
  });
})();
