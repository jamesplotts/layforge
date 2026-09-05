// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// Layforge admin/operator settings panel (design doc §3.3). Hand-written,
// no build step — same "plain files, no bundler" style as
// master/web/app.js, and drives the JSON API package internal/admin's
// Server exposes on this same listener:
//   GET  /api/campaigns                   (real party size/last-played/
//                                           archived status per campaign)
//   POST /api/campaigns                   (create/name a campaign)
//   PUT  /api/campaigns/{id}/archive      (archive is a display filter
//                                           only — never blocks joining)
//   GET/PUT /api/campaigns/{id}/policy    (Campaign tab — applies live)
//   GET/PUT /api/campaigns/{id}/security  (Security tab — applies live)
//   GET/PUT /api/campaigns/{id}/pregens   (Pregens tab — applies live)
//   DELETE  /api/campaigns/{id}/pregens/{pregenId}
//   GET  /api/campaigns/{id}/characters   (Character Review tab)
//   PUT  /api/campaigns/{id}/characters/{characterId}/review
//                                          (Host approve/reject — always
//                                           overrides any prior status)
//   GET/PUT /api/system                   (System tab — persists only)
//   POST /api/system/restart              (System tab's "Save & Restart")
//   GET  /api/health
//
// This page is only ever reachable from the -admin-addr listener, which
// is bound to 127.0.0.1 by default — see main.go's doc comment. It's
// still same-origin-checked server-side (internal/admin/server.go's
// requireSameOrigin) against a same-machine drive-by fetch(), which is
// why every mutating call below is a plain same-page fetch() with no
// custom headers that would need CORS preflight to begin with.

const state = {
  campaignId: "",
};

const el = {
  tabButtons: document.querySelectorAll(".tab-button"),
  tabPanels: document.querySelectorAll(".tab-panel"),
  campaignSelect: document.getElementById("campaign-select"),
  campaignTableBody: document.getElementById("campaign-table-body"),
  campaignListNote: document.getElementById("campaign-list-note"),
  createCampaignId: document.getElementById("create-campaign-id"),
  createCampaignName: document.getElementById("create-campaign-name"),
  createCampaignSubmit: document.getElementById("create-campaign-submit"),
  createCampaignStatus: document.getElementById("create-campaign-status"),
  pvpPolicy: document.getElementById("pvp-policy"),
  pvpConsent: document.getElementById("pvp-consent"),
  maturityTierPrompt: document.getElementById("maturity-tier-prompt"),
  imageMaturityTierPrompt: document.getElementById("image-maturity-tier-prompt"),
  priceMultiplier: document.getElementById("price-multiplier"),
  minLevel: document.getElementById("min-level"),
  maxLevel: document.getElementById("max-level"),
  campaignSave: document.getElementById("campaign-save"),
  campaignPackDir: document.getElementById("campaign-pack-dir"),
  campaignPackCurrent: document.getElementById("campaign-pack-current"),
  campaignPackSave: document.getElementById("campaign-pack-save"),
  campaignPackSaveStatus: document.getElementById("campaign-pack-save-status"),
  pregenTableBody: document.getElementById("pregen-table-body"),
  pregenId: document.getElementById("pregen-id"),
  pregenName: document.getElementById("pregen-name"),
  pregenDescription: document.getElementById("pregen-description"),
  pregenSchemaVersion: document.getElementById("pregen-schema-version"),
  pregenCharacterJSON: document.getElementById("pregen-character-json"),
  pregenSave: document.getElementById("pregen-save"),
  pregenSaveStatus: document.getElementById("pregen-save-status"),
  characterTableBody: document.getElementById("character-table-body"),
  campaignSaveStatus: document.getElementById("campaign-save-status"),
  roomPassword: document.getElementById("room-password"),
  securitySave: document.getElementById("security-save"),
  securitySaveStatus: document.getElementById("security-save-status"),
  sysAddr: document.getElementById("sys-addr"),
  sysLLMURL: document.getElementById("sys-llm-url"),
  sysLLMModel: document.getElementById("sys-llm-model"),
  sysSystemEngineAddr: document.getElementById("sys-system-engine-addr"),
  sysComfyUIURL: document.getElementById("sys-comfyui-url"),
  sysComfyUIWorkflow: document.getElementById("sys-comfyui-workflow"),
  systemSave: document.getElementById("system-save"),
  systemSaveRestart: document.getElementById("system-save-restart"),
  systemSaveStatus: document.getElementById("system-save-status"),
  restartBanner: document.getElementById("restart-banner"),
};

// --- Tabs ---

for (const button of el.tabButtons) {
  button.addEventListener("click", () => selectTab(button.dataset.tab));
}

function selectTab(tabId) {
  for (const button of el.tabButtons) button.classList.toggle("active", button.dataset.tab === tabId);
  for (const panel of el.tabPanels) panel.hidden = panel.dataset.tab !== tabId;
}

// --- Campaign list, picker, and creation ---

// formatLastActive renders an RFC3339 timestamp (or empty string, for a
// campaign nobody has joined yet) as a short human-readable string —
// this page has no build step / date library, so a plain locale format
// is enough; precision beyond "roughly when" isn't the point here.
function formatLastActive(iso) {
  if (!iso) return "never";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "never";
  return d.toLocaleString();
}

async function loadCampaignList() {
  const resp = await fetch("/api/campaigns");
  const data = await resp.json();
  const campaigns = data.campaigns || [];

  el.campaignSelect.innerHTML = "";
  el.campaignTableBody.innerHTML = "";

  for (const c of campaigns) {
    const option = document.createElement("option");
    option.value = c.campaign_id;
    option.textContent = c.display_name || c.campaign_id;
    el.campaignSelect.appendChild(option);

    const row = document.createElement("tr");
    if (c.archived) row.classList.add("campaign-row-archived");

    const nameCell = document.createElement("td");
    nameCell.textContent = c.display_name || c.campaign_id;
    if (c.display_name) {
      const idHint = document.createElement("span");
      idHint.className = "note campaign-id-hint";
      idHint.textContent = ` (${c.campaign_id})`;
      nameCell.appendChild(idHint);
    }
    row.appendChild(nameCell);

    const partyCell = document.createElement("td");
    partyCell.textContent = String(c.party_count || 0);
    row.appendChild(partyCell);

    const lastActiveCell = document.createElement("td");
    lastActiveCell.textContent = formatLastActive(c.last_active_at);
    row.appendChild(lastActiveCell);

    const selectCell = document.createElement("td");
    const selectButton = document.createElement("button");
    selectButton.type = "button";
    selectButton.className = "secondary";
    selectButton.textContent = "Edit";
    selectButton.addEventListener("click", () => selectCampaign(c.campaign_id));
    selectCell.appendChild(selectButton);
    row.appendChild(selectCell);

    const archiveCell = document.createElement("td");
    const archiveButton = document.createElement("button");
    archiveButton.type = "button";
    archiveButton.className = "secondary";
    archiveButton.textContent = c.archived ? "Unarchive" : "Archive";
    archiveButton.addEventListener("click", () => toggleArchived(c.campaign_id, !c.archived));
    archiveCell.appendChild(archiveButton);
    row.appendChild(archiveCell);

    // Delete only ever appears once a campaign is already archived —
    // archiving is the first real gate a host has to deliberately pass
    // through before this destructive option is even reachable.
    const deleteCell = document.createElement("td");
    if (c.archived) {
      const deleteButton = document.createElement("button");
      deleteButton.type = "button";
      deleteButton.className = "danger";
      deleteButton.textContent = "Delete";
      deleteButton.addEventListener("click", () => showDeleteConfirmRow(row, c.campaign_id));
      deleteCell.appendChild(deleteButton);
    }
    row.appendChild(deleteCell);

    el.campaignTableBody.appendChild(row);
  }

  if (campaigns.length) {
    if (!state.campaignId || !campaigns.some((c) => c.campaign_id === state.campaignId)) {
      selectCampaign(campaigns[0].campaign_id);
    } else {
      el.campaignSelect.value = state.campaignId;
    }
    el.campaignListNote.textContent = "";
  } else {
    el.campaignListNote.textContent = "No campaigns known yet — create one below.";
  }
}

async function toggleArchived(id, archived) {
  await fetch(`/api/campaigns/${encodeURIComponent(id)}/archive`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ archived }),
  });
  loadCampaignList();
}

// showDeleteConfirmRow inserts a real "type the campaign_id to confirm"
// row directly beneath campaignRow — a deliberately higher-friction
// confirmation than a bare browser confirm() a habituated click can
// blow through, matching the weight of an action that permanently
// destroys a campaign's characters and entire event log. Only one
// confirm row exists at a time; clicking Delete on another row (or
// reloading the list) removes any previous one.
function showDeleteConfirmRow(campaignRow, campaignId) {
  const existing = document.querySelector(".campaign-delete-confirm-row");
  if (existing) existing.remove();

  const confirmRow = document.createElement("tr");
  confirmRow.className = "campaign-delete-confirm-row";
  const cell = document.createElement("td");
  cell.colSpan = 6;

  const label = document.createElement("span");
  label.className = "note";
  label.textContent = `Type "${campaignId}" to permanently delete it (this cannot be undone): `;
  cell.appendChild(label);

  const input = document.createElement("input");
  input.type = "text";
  input.className = "delete-confirm-input";
  input.placeholder = campaignId;
  cell.appendChild(input);

  const confirmButton = document.createElement("button");
  confirmButton.type = "button";
  confirmButton.className = "danger";
  confirmButton.textContent = "Permanently Delete";
  confirmButton.disabled = true;
  cell.appendChild(confirmButton);

  const cancelButton = document.createElement("button");
  cancelButton.type = "button";
  cancelButton.className = "secondary";
  cancelButton.textContent = "Cancel";
  cancelButton.addEventListener("click", () => confirmRow.remove());
  cell.appendChild(cancelButton);

  const status = document.createElement("span");
  status.className = "save-status";
  cell.appendChild(status);

  input.addEventListener("input", () => {
    confirmButton.disabled = input.value !== campaignId;
  });
  confirmButton.addEventListener("click", async () => {
    setStatus(status, "Deleting…");
    const resp = await fetch(`/api/campaigns/${encodeURIComponent(campaignId)}`, { method: "DELETE" });
    if (!resp.ok) {
      setStatus(status, `Failed: ${await errorText(resp)}`, true);
      return;
    }
    loadCampaignList();
  });

  confirmRow.appendChild(cell);
  campaignRow.after(confirmRow);
  input.focus();
}

el.createCampaignSubmit.addEventListener("click", async () => {
  const id = el.createCampaignId.value.trim();
  if (!id) {
    setStatus(el.createCampaignStatus, "Campaign ID is required.", true);
    return;
  }
  setStatus(el.createCampaignStatus, "Creating…");
  const resp = await fetch("/api/campaigns", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ campaign_id: id, display_name: el.createCampaignName.value.trim() }),
  });
  if (!resp.ok) {
    setStatus(el.createCampaignStatus, `Failed: ${await errorText(resp)}`, true);
    return;
  }
  el.createCampaignId.value = "";
  el.createCampaignName.value = "";
  setStatus(el.createCampaignStatus, "Created.");
  await loadCampaignList();
  selectCampaign(id);
});

el.campaignSelect.addEventListener("change", () => selectCampaign(el.campaignSelect.value));

async function selectCampaign(id) {
  state.campaignId = id;
  el.campaignSelect.value = id;
  await Promise.all([loadCampaignPolicy(id), loadCampaignSecurity(id), loadCampaignPack(id), loadPregens(id), loadCharacters(id)]);
}

// --- Campaign tab ---

async function loadCampaignPolicy(id) {
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(id)}/policy`);
  const data = await resp.json();
  el.pvpPolicy.value = data.pvp_policy || "pve_only";
  el.pvpConsent.value = (data.pvp_consent || []).join(", ");
  el.maturityTierPrompt.value = data.maturity_tier_prompt || "";
  el.imageMaturityTierPrompt.value = data.image_maturity_tier_prompt || "";
  el.priceMultiplier.value = data.price_multiplier ? String(data.price_multiplier) : "1.0";
  el.minLevel.value = data.min_level ? String(data.min_level) : "";
  el.maxLevel.value = data.max_level ? String(data.max_level) : "";
}

el.campaignSave.addEventListener("click", async () => {
  if (!state.campaignId) return;
  setStatus(el.campaignSaveStatus, "Saving…");
  const priceMultiplier = parseFloat(el.priceMultiplier.value);
  if (el.priceMultiplier.value.trim() !== "" && (Number.isNaN(priceMultiplier) || priceMultiplier < 0)) {
    setStatus(el.campaignSaveStatus, "Price Multiplier must be a non-negative number.", true);
    return;
  }
  const minLevel = parseInt(el.minLevel.value, 10);
  const maxLevel = parseInt(el.maxLevel.value, 10);
  if (el.minLevel.value.trim() !== "" && (Number.isNaN(minLevel) || minLevel < 0)) {
    setStatus(el.campaignSaveStatus, "Min Level must be a non-negative whole number.", true);
    return;
  }
  if (el.maxLevel.value.trim() !== "" && (Number.isNaN(maxLevel) || maxLevel < 0)) {
    setStatus(el.campaignSaveStatus, "Max Level must be a non-negative whole number.", true);
    return;
  }
  const body = {
    pvp_policy: el.pvpPolicy.value,
    pvp_consent: el.pvpConsent.value.split(",").map((s) => s.trim()).filter(Boolean),
    maturity_tier_prompt: el.maturityTierPrompt.value,
    image_maturity_tier_prompt: el.imageMaturityTierPrompt.value,
    price_multiplier: Number.isNaN(priceMultiplier) ? 0 : priceMultiplier,
    min_level: Number.isNaN(minLevel) ? 0 : minLevel,
    max_level: Number.isNaN(maxLevel) ? 0 : maxLevel,
  };
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(state.campaignId)}/policy`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  setStatus(el.campaignSaveStatus, resp.ok ? "Saved." : `Failed: ${await errorText(resp)}`, !resp.ok);
});

async function loadCampaignPack(id) {
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(id)}/pack`);
  const data = await resp.json();
  el.campaignPackDir.value = data.pack_dir || "";
  el.campaignPackCurrent.textContent = data.pack_dir
    ? `Currently bound: ${data.pack_id} (${data.pack_dir})`
    : "No campaign pack bound.";
}

el.campaignPackSave.addEventListener("click", async () => {
  if (!state.campaignId) return;
  setStatus(el.campaignPackSaveStatus, "Binding…");
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(state.campaignId)}/pack`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ pack_dir: el.campaignPackDir.value }),
  });
  if (!resp.ok) {
    setStatus(el.campaignPackSaveStatus, `Failed: ${await errorText(resp)}`, true);
    return;
  }
  setStatus(el.campaignPackSaveStatus, "Bound.");
  await loadCampaignPack(state.campaignId);
});

// --- Pregens tab ---

async function loadPregens(id) {
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(id)}/pregens`);
  const pregens = (await resp.json()) || [];

  el.pregenTableBody.innerHTML = "";
  for (const p of pregens) {
    const row = document.createElement("tr");

    const idCell = document.createElement("td");
    idCell.textContent = p.id;
    row.appendChild(idCell);

    const nameCell = document.createElement("td");
    nameCell.textContent = p.name;
    row.appendChild(nameCell);

    const descriptionCell = document.createElement("td");
    descriptionCell.textContent = p.description;
    row.appendChild(descriptionCell);

    const deleteCell = document.createElement("td");
    const deleteButton = document.createElement("button");
    deleteButton.type = "button";
    deleteButton.className = "secondary";
    deleteButton.textContent = "Delete";
    deleteButton.addEventListener("click", () => deletePregen(id, p.id));
    deleteCell.appendChild(deleteButton);
    row.appendChild(deleteCell);

    el.pregenTableBody.appendChild(row);
  }
}

el.pregenSave.addEventListener("click", async () => {
  if (!state.campaignId) return;
  setStatus(el.pregenSaveStatus, "Saving…");

  let characterJSON;
  try {
    characterJSON = JSON.parse(el.pregenCharacterJSON.value);
  } catch (err) {
    setStatus(el.pregenSaveStatus, `Character JSON is not valid JSON: ${err.message}`, true);
    return;
  }

  const resp = await fetch(`/api/campaigns/${encodeURIComponent(state.campaignId)}/pregens`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      id: el.pregenId.value.trim(),
      name: el.pregenName.value.trim(),
      description: el.pregenDescription.value.trim(),
      schema_version: el.pregenSchemaVersion.value.trim(),
      character_json: characterJSON,
    }),
  });
  if (!resp.ok) {
    setStatus(el.pregenSaveStatus, `Failed: ${await errorText(resp)}`, true);
    return;
  }
  setStatus(el.pregenSaveStatus, "Saved.");
  el.pregenId.value = "";
  el.pregenName.value = "";
  el.pregenDescription.value = "";
  el.pregenSchemaVersion.value = "";
  el.pregenCharacterJSON.value = "";
  await loadPregens(state.campaignId);
});

async function deletePregen(campaignId, pregenId) {
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(campaignId)}/pregens/${encodeURIComponent(pregenId)}`, {
    method: "DELETE",
  });
  if (!resp.ok) {
    setStatus(el.pregenSaveStatus, `Failed to delete: ${await errorText(resp)}`, true);
    return;
  }
  await loadPregens(campaignId);
}

// --- Character Review tab ---
//
// Lists every character store.CharacterStore.ListCharacters returns for
// this campaign (design doc §9.4) — quick/detailed-rolled characters
// and claimed pregens included, though they're already Approved and so
// have nothing to review; the table's own point is the imported ones
// still PendingReview, or previously Approved/Rejected by either the
// automatic pass or an earlier Host decision. Approve/Reject always
// overrides whatever status a character already has — the Host is this
// system's final authority (see handleReviewCharacter's own doc
// comment, package admin).

// characterDisplayName reads the character's own name straight off the
// parsed object the JSON API response already gives us — character_json
// is a json.RawMessage on the Go side, which encodes/decodes as a plain
// embedded JSON value (an object), not a string, so there is nothing to
// JSON.parse() here; doing so throws (an object isn't valid JSON text).
function characterDisplayName(characterJSON) {
  return characterJSON && characterJSON.name ? characterJSON.name : "(unnamed)";
}

async function loadCharacters(id) {
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(id)}/characters`);
  const characters = (await resp.json()) || [];

  el.characterTableBody.innerHTML = "";
  for (const c of characters) {
    const row = document.createElement("tr");

    const ownerCell = document.createElement("td");
    ownerCell.textContent = c.owner_id;
    row.appendChild(ownerCell);

    const statusCell = document.createElement("td");
    statusCell.textContent = c.status;
    row.appendChild(statusCell);

    const nameCell = document.createElement("td");
    nameCell.textContent = characterDisplayName(c.character_json);
    row.appendChild(nameCell);

    const approveCell = document.createElement("td");
    const approveButton = document.createElement("button");
    approveButton.type = "button";
    approveButton.textContent = "Approve";
    approveButton.addEventListener("click", () => reviewCharacter(id, c.id, "approved"));
    approveCell.appendChild(approveButton);
    row.appendChild(approveCell);

    const rejectCell = document.createElement("td");
    const rejectButton = document.createElement("button");
    rejectButton.type = "button";
    rejectButton.className = "secondary";
    rejectButton.textContent = "Reject";
    rejectButton.addEventListener("click", () => reviewCharacter(id, c.id, "rejected"));
    rejectCell.appendChild(rejectButton);
    row.appendChild(rejectCell);

    el.characterTableBody.appendChild(row);
  }
}

async function reviewCharacter(campaignId, characterId, status) {
  const reason = window.prompt(`Reason for marking this character "${status}" (shown to the player, optional):`, "") || "";
  const resp = await fetch(
    `/api/campaigns/${encodeURIComponent(campaignId)}/characters/${encodeURIComponent(characterId)}/review`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status, reason }),
    },
  );
  if (!resp.ok) {
    window.alert(`Failed: ${await errorText(resp)}`);
    return;
  }
  await loadCharacters(campaignId);
}

// --- Security tab ---

async function loadCampaignSecurity(id) {
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(id)}/security`);
  const data = await resp.json();
  el.roomPassword.value = data.room_password || "";
}

el.securitySave.addEventListener("click", async () => {
  if (!state.campaignId) return;
  setStatus(el.securitySaveStatus, "Saving…");
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(state.campaignId)}/security`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ room_password: el.roomPassword.value }),
  });
  setStatus(el.securitySaveStatus, resp.ok ? "Saved." : `Failed: ${await errorText(resp)}`, !resp.ok);
});

// --- System tab ---

async function loadSystemSettings() {
  const resp = await fetch("/api/system");
  const data = await resp.json();
  el.sysAddr.value = data.addr || "";
  el.sysLLMURL.value = data.llm_url || "";
  el.sysLLMModel.value = data.llm_model || "";
  el.sysSystemEngineAddr.value = data.system_engine_addr || "";
  el.sysComfyUIURL.value = data.comfyui_url || "";
  el.sysComfyUIWorkflow.value = data.comfyui_workflow_path || "";
}

function systemSettingsBody() {
  return {
    addr: el.sysAddr.value,
    llm_url: el.sysLLMURL.value,
    llm_model: el.sysLLMModel.value,
    system_engine_addr: el.sysSystemEngineAddr.value,
    comfyui_url: el.sysComfyUIURL.value,
    comfyui_workflow_path: el.sysComfyUIWorkflow.value,
  };
}

el.systemSave.addEventListener("click", async () => {
  setStatus(el.systemSaveStatus, "Saving…");
  const resp = await fetch("/api/system", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(systemSettingsBody()),
  });
  setStatus(
    el.systemSaveStatus,
    resp.ok ? "Saved — takes effect on the next restart." : `Failed: ${await errorText(resp)}`,
    !resp.ok,
  );
});

el.systemSaveRestart.addEventListener("click", async () => {
  if (!confirm("This restarts Master and disconnects every connected player. Continue?")) return;
  setStatus(el.systemSaveStatus, "Saving…");
  const resp = await fetch("/api/system/restart", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(systemSettingsBody()),
  });
  if (!resp.ok) {
    setStatus(el.systemSaveStatus, `Failed: ${await errorText(resp)}`, true);
    return;
  }
  setStatus(el.systemSaveStatus, "");
  el.restartBanner.hidden = false;
  el.systemSave.disabled = true;
  el.systemSaveRestart.disabled = true;
  pollUntilBackUpThenReload();
});

// pollUntilBackUpThenReload polls /api/health once Master has (per the
// restart handler's own ordering) already started shutting down — the
// first several polls are expected to fail while the old process exits
// and the new one boots, that's normal, not an error state.
function pollUntilBackUpThenReload() {
  const intervalMs = 1000;
  const maxAttempts = 60;
  let attempts = 0;
  const timer = setInterval(async () => {
    attempts += 1;
    try {
      const resp = await fetch("/api/health", { cache: "no-store" });
      if (resp.ok) {
        clearInterval(timer);
        location.reload();
        return;
      }
    } catch {
      // Connection refused while the old process is down / new one is
      // still starting — expected, keep polling.
    }
    if (attempts >= maxAttempts) {
      clearInterval(timer);
      el.restartBanner.textContent = "Master hasn't come back up yet — check its logs, then reload manually.";
    }
  }, intervalMs);
}

// --- helpers ---

function setStatus(target, text, isError) {
  target.textContent = text;
  target.classList.toggle("save-status-error", Boolean(isError));
}

async function errorText(resp) {
  try {
    const data = await resp.json();
    return data.error || resp.statusText;
  } catch {
    return resp.statusText;
  }
}

loadCampaignList();
loadSystemSettings();
