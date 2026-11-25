const pairingButton = document.getElementById("redeem-pairing");
const pairingInstructions = document.getElementById("pairing-instructions");
const pairingStatus = document.getElementById("pairing-status");
const connectionDetails = document.getElementById("connection-details");

function pairingMessage(message) {
  pairingStatus.textContent = message;
  pairingStatus.hidden = false;
}

function readPairing() {
  const fragment = new URLSearchParams(location.hash.slice(1));
  if (!fragment.has("gateway") && !fragment.has("code")) return;
  history.replaceState(null, "", location.pathname + location.search);

  const origin = fragment.get("gateway");
  const code = fragment.get("code");
  try {
    const gateway = new URL(origin);
    if (gateway.protocol === "https:" && origin === gateway.origin && /^[a-f0-9]{64}$/.test(code)) {
      return { gateway, code };
    }
  } catch {
    // QR data is untrusted.
  }
  pairingMessage("This pairing QR code is invalid. Generate a new QR code in Lumiqot and scan it.");
}

let pairing = readPairing();
if (pairing) {
  pairingInstructions.hidden = true;
  pairingButton.hidden = false;
}

pairingButton.addEventListener("click", async () => {
  if (!pairing || pairingButton.disabled) return;
  pairingButton.disabled = true;
  pairingMessage("Getting connection details…");

  try {
    const response = await fetch(new URL("/lumiqot-mcp-pairing", pairing.gateway), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code: pairing.code }),
      credentials: "omit",
      redirect: "error",
      cache: "no-store",
      signal: AbortSignal.timeout(15000)
    });
    if (response.status === 410) {
      pairing = undefined;
      pairingButton.hidden = true;
      pairingInstructions.hidden = false;
      pairingMessage("This QR code expired or was already used. Generate a new QR code in Lumiqot and scan it.");
      return;
    }
    if (!response.ok) throw new Error("Pairing failed");

    const details = await response.json();
    const serverURL = new URL(details.server_url);
    if (serverURL.origin !== pairing.gateway.origin || serverURL.username || serverURL.password ||
        serverURL.search || serverURL.hash ||
        !/^\/(?:[A-Za-z0-9_-]+\/)*[A-Za-z0-9_-]+$/.test(serverURL.pathname) ||
        !/^lqk_[a-f0-9]{64}$/.test(details.token)) {
      throw new Error("Invalid connection details");
    }

    pairing = undefined;
    pairingButton.hidden = true;
    document.getElementById("server-url").textContent = serverURL.href;
    document.getElementById("authorization-token").textContent = details.token;
    connectionDetails.hidden = false;
    pairingMessage("Connection details are ready.");
  } catch {
    pairingMessage("Could not get connection details. Check that the Lumiqot Gateway is reachable and try again.");
  } finally {
    pairingButton.disabled = false;
  }
});
