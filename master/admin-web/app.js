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
  generatedPackSlug: "",
  campaigns: [],
  selectedCharacterId: "",
  pregenRollSession: "",
  pregenRollMode: "quick",
};

const el = {
  tabButtons: document.querySelectorAll(".tab-button"),
  tabPanels: document.querySelectorAll(".tab-panel"),
  termsModal: document.getElementById("terms-modal"),
  termsModalText: document.getElementById("terms-modal-text"),
  termsModalAgree: document.getElementById("terms-modal-agree"),
  campaignPickerBar: document.getElementById("campaign-picker-bar"),
  campaignManage: document.getElementById("campaign-manage"),
  sessionCampaignSelect: document.getElementById("session-campaign-select"),
  sessionActiveStatus: document.getElementById("session-active-status"),
  sessionStatusLine: document.getElementById("session-status-line"),
  sessionJoinLock: document.getElementById("session-join-lock"),
  sessionRosterBody: document.getElementById("session-roster-body"),
  sessionRosterEmpty: document.getElementById("session-roster-empty"),
  campaignSelect: document.getElementById("campaign-select"),
  campaignTableBody: document.getElementById("campaign-table-body"),
  campaignListNote: document.getElementById("campaign-list-note"),
  createCampaignId: document.getElementById("create-campaign-id"),
  createCampaignName: document.getElementById("create-campaign-name"),
  createCampaignSubmit: document.getElementById("create-campaign-submit"),
  createCampaignStatus: document.getElementById("create-campaign-status"),
  pvpPolicy: document.getElementById("pvp-policy"),
  maturityTierPrompt: document.getElementById("maturity-tier-prompt"),
  imageMaturityTierPrompt: document.getElementById("image-maturity-tier-prompt"),
  priceMultiplier: document.getElementById("price-multiplier"),
  minLevel: document.getElementById("min-level"),
  maxLevel: document.getElementById("max-level"),
  maxPlayers: document.getElementById("max-players"),
  joinAddress: document.getElementById("join-address"),
  registryListed: document.getElementById("registry-listed"),
  campaignSave: document.getElementById("campaign-save"),
  campaignPackDir: document.getElementById("campaign-pack-dir"),
  campaignPackCurrent: document.getElementById("campaign-pack-current"),
  campaignPackSave: document.getElementById("campaign-pack-save"),
  campaignPackSaveStatus: document.getElementById("campaign-pack-save-status"),
  generatePackDescription: document.getElementById("generate-pack-description"),
  generatePackMinLevel: document.getElementById("generate-pack-min-level"),
  generatePackMaxLevel: document.getElementById("generate-pack-max-level"),
  generatePackSlug: document.getElementById("generate-pack-slug"),
  generatePackSubmit: document.getElementById("generate-pack-submit"),
  generatePackStatus: document.getElementById("generate-pack-status"),
  generatePackReview: document.getElementById("generate-pack-review"),
  generatePackFiles: document.getElementById("generate-pack-files"),
  generatePackSave: document.getElementById("generate-pack-save"),
  generatePackSaveStatus: document.getElementById("generate-pack-save-status"),
  installLibraryUrl: document.getElementById("install-library-url"),
  installLibraryOverwrite: document.getElementById("install-library-overwrite"),
  installLibrarySubmit: document.getElementById("install-library-submit"),
  installLibraryStatus: document.getElementById("install-library-status"),
  installLibraryResults: document.getElementById("install-library-results"),
  pregenTableBody: document.getElementById("pregen-table-body"),
  pregenId: document.getElementById("pregen-id"),
  pregenName: document.getElementById("pregen-name"),
  pregenDescription: document.getElementById("pregen-description"),
  pregenSchemaVersion: document.getElementById("pregen-schema-version"),
  pregenCharacterJSON: document.getElementById("pregen-character-json"),
  pregenSave: document.getElementById("pregen-save"),
  pregenSaveStatus: document.getElementById("pregen-save-status"),
  pregenNewButton: document.getElementById("pregen-new-button"),
  pregenForm: document.getElementById("pregen-form"),
  pregenFormHint: document.getElementById("pregen-form-hint"),
  pregenCancel: document.getElementById("pregen-cancel"),
  pregenCampaignNote: document.getElementById("pregen-campaign-note"),
  pregenEmptyNote: document.getElementById("pregen-empty-note"),
  pregenCreateChoices: document.getElementById("pregen-create-choices"),
  pregenChoiceQuick: document.getElementById("pregen-choice-quick"),
  pregenChoiceDetailed: document.getElementById("pregen-choice-detailed"),
  pregenChoiceJson: document.getElementById("pregen-choice-json"),
  pregenChoiceCancel: document.getElementById("pregen-choice-cancel"),
  pregenRoll: document.getElementById("pregen-roll"),
  pregenRollNameStep: document.getElementById("pregen-roll-name-step"),
  pregenRollName: document.getElementById("pregen-roll-name"),
  pregenRollStart: document.getElementById("pregen-roll-start"),
  pregenRollCancel: document.getElementById("pregen-roll-cancel"),
  pregenRollPromptStep: document.getElementById("pregen-roll-prompt-step"),
  pregenRollPrompt: document.getElementById("pregen-roll-prompt"),
  pregenRollChoices: document.getElementById("pregen-roll-choices"),
  pregenRollFreetextLabel: document.getElementById("pregen-roll-freetext-label"),
  pregenRollFreetext: document.getElementById("pregen-roll-freetext"),
  pregenRollFreetextActions: document.getElementById("pregen-roll-freetext-actions"),
  pregenRollFreetextSubmit: document.getElementById("pregen-roll-freetext-submit"),
  pregenRollStatus: document.getElementById("pregen-roll-status"),
  connectedPlayersTableBody: document.getElementById("connected-players-table-body"),
  campaignCharactersTableBody: document.getElementById("campaign-characters-table-body"),
  allCharacterPicker: document.getElementById("all-character-picker"),
  allCharacterEmptyNote: document.getElementById("all-character-empty-note"),
  characterDetail: document.getElementById("character-detail"),
  cdName: document.getElementById("cd-name"),
  cdOwner: document.getElementById("cd-owner"),
  cdCampaign: document.getElementById("cd-campaign"),
  cdStatus: document.getElementById("cd-status"),
  cdCreated: document.getElementById("cd-created"),
  cdMoveSelect: document.getElementById("cd-move-select"),
  cdMoveButton: document.getElementById("cd-move-button"),
  cdReviewReason: document.getElementById("cd-review-reason"),
  cdApproveButton: document.getElementById("cd-approve-button"),
  cdRejectButton: document.getElementById("cd-reject-button"),
  cdJson: document.getElementById("cd-json"),
  cdDeleteButton: document.getElementById("cd-delete-button"),
  cdStatusLine: document.getElementById("cd-status-line"),
  campaignSaveStatus: document.getElementById("campaign-save-status"),
  roomPassword: document.getElementById("room-password"),
  securitySave: document.getElementById("security-save"),
  securitySaveStatus: document.getElementById("security-save-status"),
  sysAddr: document.getElementById("sys-addr"),
  sysLLMProvider: document.getElementById("sys-llm-provider"),
  sysLLMURL: document.getElementById("sys-llm-url"),
  sysLLMModel: document.getElementById("sys-llm-model"),
  sysLLMAPIKey: document.getElementById("sys-llm-api-key"),
  testLLMButton: document.getElementById("test-llm-button"),
  testLLMStatus: document.getElementById("test-llm-status"),
  sysSystemEngineAddr: document.getElementById("sys-system-engine-addr"),
  sysComfyUIURL: document.getElementById("sys-comfyui-url"),
  sysComfyUIWorkflow: document.getElementById("sys-comfyui-workflow"),
  sysDiscordClientID: document.getElementById("sys-discord-client-id"),
  sysDiscordClientSecret: document.getElementById("sys-discord-client-secret"),
  sysDiscordRedirectURL: document.getElementById("sys-discord-redirect-url"),
  discordRedirectSuggestion: document.getElementById("discord-redirect-suggestion"),
  discordRedirectCopy: document.getElementById("discord-redirect-copy"),
  systemSave: document.getElementById("system-save"),
  systemSaveRestart: document.getElementById("system-save-restart"),
  systemSaveStatus: document.getElementById("system-save-status"),
  restartBanner: document.getElementById("restart-banner"),
};

// --- Tabs ---

for (const button of el.tabButtons) {
  button.addEventListener("click", () => selectTab(button.dataset.tab));
}

// pickerTabs are the tabs whose panels act on the selected campaign and
// so need the campaign picker above them; campaignManageTab is the only
// one that also gets the full campaign list + creation actions.
const pickerTabs = ["campaign", "security", "pregens"];

function selectTab(tabId) {
  for (const button of el.tabButtons) button.classList.toggle("active", button.dataset.tab === tabId);
  for (const panel of el.tabPanels) panel.hidden = panel.dataset.tab !== tabId;
  el.campaignPickerBar.hidden = !pickerTabs.includes(tabId);
  el.campaignManage.hidden = tabId !== "campaign";
  if (tabId === "characters") loadAllCharacters();
  setSessionPolling(tabId === "session");
}

// --- Session tab ---

let sessionPollTimer = null;

function setSessionPolling(on) {
  if (sessionPollTimer) {
    clearInterval(sessionPollTimer);
    sessionPollTimer = null;
  }
  if (on) {
    loadSession();
    sessionPollTimer = setInterval(loadSession, 5000);
  }
}

async function loadSession() {
  let data;
  try {
    const resp = await fetch("/api/session");
    if (!resp.ok) return;
    data = await resp.json();
  } catch {
    return;
  }
  renderSession(data);
}

function renderSession(data) {
  // Populate the active-campaign options from the campaign list cache
  // (loadCampaignList keeps state.campaigns current).
  const active = data.active_campaign_id || "";
  el.sessionCampaignSelect.replaceChildren();
  const none = document.createElement("option");
  none.value = "";
  none.textContent = "— none (players can't join yet) —";
  el.sessionCampaignSelect.appendChild(none);
  for (const c of state.campaigns) {
    const opt = document.createElement("option");
    opt.value = c.campaign_id;
    opt.textContent = c.display_name || c.campaign_id;
    el.sessionCampaignSelect.appendChild(opt);
  }
  el.sessionCampaignSelect.value = active;

  el.sessionJoinLock.checked = Boolean(data.join_locked);
  el.sessionJoinLock.disabled = !active;

  const players = data.connected_players || [];
  const characters = data.characters || [];
  if (!active) {
    el.sessionStatusLine.textContent = "No campaign is running.";
  } else {
    el.sessionStatusLine.textContent =
      `Running "${data.active_campaign_name || active}" — ${players.length} connected, ${characters.length} character${characters.length === 1 ? "" : "s"}.`;
  }

  const online = new Set(players);
  el.sessionRosterBody.replaceChildren();
  for (const c of characters) {
    const tr = document.createElement("tr");
    for (const text of [
      c.name || characterName(c.character_json) || c.id,
      c.owner_id || "—",
      c.status || "—",
      online.has(c.owner_id) || online.has(c.id) ? "online" : "",
    ]) {
      const td = document.createElement("td");
      td.textContent = text;
      tr.appendChild(td);
    }
    el.sessionRosterBody.appendChild(tr);
  }
  el.sessionRosterEmpty.hidden = characters.length > 0 || !active;
}

el.sessionCampaignSelect.addEventListener("change", async () => {
  setStatus(el.sessionActiveStatus, "Saving…");
  try {
    const resp = await fetch("/api/session/active-campaign", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ campaign_id: el.sessionCampaignSelect.value }),
    });
    if (!resp.ok) {
      setStatus(el.sessionActiveStatus, `Failed: ${await errorText(resp)}`, true);
      return;
    }
    setStatus(el.sessionActiveStatus, "Saved.");
    renderSession(await resp.json());
  } catch (err) {
    setStatus(el.sessionActiveStatus, `Failed: ${err}`, true);
  }
});

el.sessionJoinLock.addEventListener("change", async () => {
  try {
    const resp = await fetch("/api/session/join-lock", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ locked: el.sessionJoinLock.checked }),
    });
    if (resp.ok) renderSession(await resp.json());
  } catch {
    // leave the checkbox as the user set it; next poll corrects it
  }
});

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

// state.campaigns caches the last /api/campaigns response so the
// character-move dropdown (Characters tab) can list every campaign
// without its own fetch.
async function loadCampaignList() {
  const resp = await fetch("/api/campaigns");
  const data = await resp.json();
  const campaigns = data.campaigns || [];
  state.campaigns = campaigns;

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
  await Promise.all([
    loadCampaignPolicy(id),
    loadCampaignSecurity(id),
    loadCampaignPack(id),
    loadPregens(id),
    loadCampaignCharacters(id),
    loadConnectedPlayers(id),
  ]);
}

// --- Campaign tab ---

async function loadCampaignPolicy(id) {
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(id)}/policy`);
  const data = await resp.json();
  el.pvpPolicy.value = data.pvp_policy || "pve_only";
  el.maturityTierPrompt.value = data.maturity_tier_prompt || "";
  el.imageMaturityTierPrompt.value = data.image_maturity_tier_prompt || "";
  el.priceMultiplier.value = data.price_multiplier ? String(data.price_multiplier) : "1.0";
  el.minLevel.value = data.min_level ? String(data.min_level) : "";
  el.maxLevel.value = data.max_level ? String(data.max_level) : "";
  el.maxPlayers.value = data.max_players ? String(data.max_players) : "";
  el.joinAddress.value = data.join_address || "";
  el.registryListed.checked = Boolean(data.registry_listed);
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
  const maxPlayers = parseInt(el.maxPlayers.value, 10);
  if (el.maxPlayers.value.trim() !== "" && (Number.isNaN(maxPlayers) || maxPlayers < 0)) {
    setStatus(el.campaignSaveStatus, "Max Players must be a non-negative whole number.", true);
    return;
  }
  if (el.registryListed.checked && !el.joinAddress.value.trim()) {
    setStatus(el.campaignSaveStatus, "Join Address is required to list this campaign publicly.", true);
    return;
  }
  const body = {
    pvp_policy: el.pvpPolicy.value,
    maturity_tier_prompt: el.maturityTierPrompt.value,
    image_maturity_tier_prompt: el.imageMaturityTierPrompt.value,
    price_multiplier: Number.isNaN(priceMultiplier) ? 0 : priceMultiplier,
    min_level: Number.isNaN(minLevel) ? 0 : minLevel,
    max_level: Number.isNaN(maxLevel) ? 0 : maxLevel,
    max_players: Number.isNaN(maxPlayers) ? 0 : maxPlayers,
    registry_listed: el.registryListed.checked,
    join_address: el.joinAddress.value.trim(),
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

// --- Generate a campaign pack with AI ---

// Tracks the most recently generated set of files (path -> textarea
// element) so Save can read back whatever the Host may have edited,
// keyed the same way the server returned them.
let generatedPackFiles = [];

el.generatePackSubmit.addEventListener("click", async () => {
  const description = el.generatePackDescription.value.trim();
  if (!description) {
    setStatus(el.generatePackStatus, "A description is required.", true);
    return;
  }
  el.generatePackSubmit.disabled = true;
  setStatus(el.generatePackStatus, "Generating… this can take a little while.");
  el.generatePackReview.hidden = true;
  try {
    const resp = await fetch("/api/campaign-packs/generate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        description,
        min_level: parseInt(el.generatePackMinLevel.value, 10) || 0,
        max_level: parseInt(el.generatePackMaxLevel.value, 10) || 0,
        slug: el.generatePackSlug.value.trim(),
      }),
    });
    if (!resp.ok) {
      setStatus(el.generatePackStatus, `Failed: ${await errorText(resp)}`, true);
      return;
    }
    const data = await resp.json();
    state.generatedPackSlug = data.slug;
    renderGeneratedPackFiles(data.files || []);
    el.generatePackReview.hidden = false;
    if (data.validation_error) {
      // A single file's syntax mistake shouldn't discard an otherwise-
      // good multi-minute generation — files are still shown for
      // editing; Save Pack re-validates for real once fixed.
      setStatus(el.generatePackStatus, `Generated, but doesn't validate yet — fix it below, then Save Pack: ${data.validation_error}`, true);
    } else {
      setStatus(el.generatePackStatus, "Generated — review and edit below, then Save Pack.");
    }
  } finally {
    el.generatePackSubmit.disabled = false;
  }
});

function renderGeneratedPackFiles(files) {
  el.generatePackFiles.innerHTML = "";
  generatedPackFiles = files.map((f) => {
    const wrap = document.createElement("div");
    wrap.className = "generated-file";
    const label = document.createElement("label");
    label.textContent = f.path;
    const textarea = document.createElement("textarea");
    textarea.value = f.content;
    label.appendChild(textarea);
    wrap.appendChild(label);
    el.generatePackFiles.appendChild(wrap);
    return { path: f.path, textarea };
  });
}

el.generatePackSave.addEventListener("click", async () => {
  setStatus(el.generatePackSaveStatus, "Saving…");
  const files = generatedPackFiles.map((f) => ({ path: f.path, content: f.textarea.value }));
  const resp = await fetch("/api/campaign-packs/save", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ slug: state.generatedPackSlug, files }),
  });
  if (!resp.ok) {
    setStatus(el.generatePackSaveStatus, `Failed: ${await errorText(resp)}`, true);
    return;
  }
  const data = await resp.json();
  // Pre-fill the existing "Campaign Pack Directory" field — binding it
  // is that same existing "Bind Pack" button, not new bind logic.
  el.campaignPackDir.value = data.pack_dir;
  setStatus(el.generatePackSaveStatus, `Saved to ${data.pack_dir} — select a campaign above and click "Bind Pack" to use it.`);
});

el.installLibrarySubmit.addEventListener("click", async () => {
  el.installLibrarySubmit.disabled = true;
  el.installLibraryResults.hidden = true;
  el.installLibraryResults.replaceChildren();
  setStatus(el.installLibraryStatus, "Downloading and unpacking…");
  try {
    const resp = await fetch("/api/campaign-packs/install-library", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        url: el.installLibraryUrl.value.trim(),
        overwrite: el.installLibraryOverwrite.checked,
      }),
    });
    if (!resp.ok) {
      setStatus(el.installLibraryStatus, `Failed: ${await errorText(resp)}`, true);
      return;
    }
    const data = await resp.json();
    const results = data.results || [];
    const counts = { installed: 0, skipped_exists: 0, failed: 0 };
    for (const res of results) {
      if (res.status in counts) counts[res.status]++;
      const li = document.createElement("li");
      li.classList.add(`install-result-${res.status}`);
      let line = res.detail
        ? `${res.slug} — ${res.status}: ${res.detail}`
        : `${res.slug} — ${res.status}`;
      if (res.campaign_added) line += ` → campaign "${res.campaign}" added`;
      else if (res.campaign) line += ` → campaign "${res.campaign}"`;
      li.textContent = line;
      el.installLibraryResults.appendChild(li);
    }
    el.installLibraryResults.hidden = results.length === 0;

    // Bring the new campaigns into the dropdown above.
    if (data.campaigns_added > 0) {
      await loadCampaignList();
    }

    const added = data.campaigns_added || 0;
    setStatus(
      el.installLibraryStatus,
      `From ${data.source}: ${counts.installed} unpacked, ${counts.skipped_exists} already present, ${counts.failed} failed — ` +
        (added > 0
          ? `${added} campaign${added === 1 ? "" : "s"} added to the dropdown above.`
          : "no new campaigns added."),
      counts.failed > 0,
    );
  } catch (err) {
    setStatus(el.installLibraryStatus, `Failed: ${err}`, true);
  } finally {
    el.installLibrarySubmit.disabled = false;
  }
});

// --- Pregens tab ---

async function loadPregens(id) {
  const label = campaignLabel(id);
  el.pregenCampaignNote.textContent = `Templates below belong to campaign ${label}. Switch campaigns with the picker above.`;

  const resp = await fetch(`/api/campaigns/${encodeURIComponent(id)}/pregens`);
  const pregens = (await resp.json()) || [];

  el.pregenTableBody.innerHTML = "";
  el.pregenEmptyNote.hidden = pregens.length > 0;
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

// Pregen creation is a small state machine: New button -> choices ->
// (roll flow | paste-JSON form). resetPregenCreate collapses all of it.
function resetPregenCreate() {
  el.pregenNewButton.hidden = false;
  for (const node of [el.pregenCreateChoices, el.pregenRoll, el.pregenForm]) node.hidden = true;
  el.pregenRollNameStep.hidden = false;
  el.pregenRollPromptStep.hidden = true;
  for (const id of ["pregenId", "pregenName", "pregenDescription", "pregenSchemaVersion", "pregenCharacterJSON", "pregenRollName", "pregenRollFreetext"]) {
    el[id].value = "";
  }
  setStatus(el.pregenSaveStatus, "");
  setStatus(el.pregenRollStatus, "");
  state.pregenRollSession = "";
}

el.pregenNewButton.addEventListener("click", () => {
  el.pregenNewButton.hidden = true;
  el.pregenCreateChoices.hidden = false;
});
el.pregenChoiceCancel.addEventListener("click", resetPregenCreate);
el.pregenCancel.addEventListener("click", resetPregenCreate);
el.pregenRollCancel.addEventListener("click", resetPregenCreate);

el.pregenChoiceJson.addEventListener("click", () => {
  el.pregenCreateChoices.hidden = true;
  el.pregenForm.hidden = false;
  el.pregenFormHint.textContent = "Paste a full character JSON below, then set an ID and Create Template.";
});
el.pregenChoiceQuick.addEventListener("click", () => startPregenRoll("quick"));
el.pregenChoiceDetailed.addEventListener("click", () => startPregenRoll("detailed"));

function startPregenRoll(mode) {
  state.pregenRollMode = mode;
  el.pregenCreateChoices.hidden = true;
  el.pregenRoll.hidden = false;
  el.pregenRollNameStep.hidden = false;
  el.pregenRollPromptStep.hidden = true;
  el.pregenRollName.focus();
}

el.pregenRollStart.addEventListener("click", async () => {
  const name = el.pregenRollName.value.trim();
  if (!name) {
    setStatus(el.pregenRollStatus, "A name is required.", true);
    return;
  }
  setStatus(el.pregenRollStatus, "Starting…");
  await pregenRollStep("/api/character-creation/start", { mode: state.pregenRollMode, name });
});

async function pregenRollStep(path, body) {
  el.pregenRollStart.disabled = true;
  el.pregenRollFreetextSubmit.disabled = true;
  try {
    const resp = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!resp.ok) {
      setStatus(el.pregenRollStatus, `Failed: ${await errorText(resp)}`, true);
      return;
    }
    const step = await resp.json();
    state.pregenRollSession = step.session_id;
    if (step.done) {
      finishPregenRoll(step);
      return;
    }
    renderPregenRollPrompt(step);
  } finally {
    el.pregenRollStart.disabled = false;
    el.pregenRollFreetextSubmit.disabled = false;
  }
}

function renderPregenRollPrompt(step) {
  el.pregenRollNameStep.hidden = true;
  el.pregenRollPromptStep.hidden = false;
  el.pregenRollPrompt.textContent = step.prompt_text || "Choose:";
  el.pregenRollChoices.replaceChildren();
  setStatus(el.pregenRollStatus, "");

  const choices = step.choices || [];
  const freetext = choices.length === 0;
  el.pregenRollFreetextLabel.hidden = !freetext;
  el.pregenRollFreetextActions.hidden = !freetext;
  el.pregenRollFreetext.value = "";

  for (const choice of choices) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "secondary";
    b.textContent = choice;
    b.addEventListener("click", () => submitPregenAnswer(choice));
    el.pregenRollChoices.appendChild(b);
  }
  if (freetext) el.pregenRollFreetext.focus();
}

el.pregenRollFreetextSubmit.addEventListener("click", () => submitPregenAnswer(el.pregenRollFreetext.value.trim()));

async function submitPregenAnswer(answer) {
  setStatus(el.pregenRollStatus, "…");
  await pregenRollStep("/api/character-creation/answer", { session_id: state.pregenRollSession, answer });
}

// finishPregenRoll drops the finished character into the paste-JSON form
// so the Host just sets an ID/description and clicks Create Template.
function finishPregenRoll(step) {
  el.pregenRoll.hidden = true;
  el.pregenForm.hidden = false;
  el.pregenFormHint.textContent = "Character rolled. Give it an ID (and tweak the JSON if you like), then Create Template.";
  el.pregenName.value = el.pregenRollName.value.trim();
  el.pregenSchemaVersion.value = step.schema_version || "";
  el.pregenCharacterJSON.value = step.character_json ? JSON.stringify(step.character_json, null, 2) : "";
  el.pregenId.focus();
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
  resetPregenCreate();
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

// --- Characters ---
//
// The Campaign tab shows the characters IN the selected campaign
// (loadCampaignCharacters) with move/remove. The Characters tab is a
// cross-campaign roster (loadAllCharacters): a picker, then a detail
// panel with move, approve/reject (design doc §9.4's import veto — Host
// overrides any prior status), and delete.

// character_json comes back as a real embedded JSON object (Go
// json.RawMessage), not a string — read .name straight off it.
function characterName(characterJSON) {
  return characterJSON && characterJSON.name ? characterJSON.name : "(unnamed)";
}

function campaignLabel(campaignId) {
  const c = (state.campaigns || []).find((c) => c.campaign_id === campaignId);
  return c && c.display_name ? `${c.display_name} (${campaignId})` : campaignId;
}

// fillCampaignOptions populates a <select> with every known campaign,
// optionally excluding one id (the character's current campaign, for a
// move dropdown).
function fillCampaignOptions(selectEl, excludeId) {
  selectEl.innerHTML = "";
  for (const c of state.campaigns || []) {
    if (c.campaign_id === excludeId) continue;
    const option = document.createElement("option");
    option.value = c.campaign_id;
    option.textContent = c.display_name || c.campaign_id;
    selectEl.appendChild(option);
  }
}

// --- Campaign tab: characters in this campaign ---

async function loadCampaignCharacters(id) {
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(id)}/characters`);
  const characters = (await resp.json()) || [];

  el.campaignCharactersTableBody.innerHTML = "";
  if (characters.length === 0) {
    const row = document.createElement("tr");
    const cell = document.createElement("td");
    cell.colSpan = 5;
    cell.className = "note";
    cell.textContent = "No characters in this campaign yet.";
    row.appendChild(cell);
    el.campaignCharactersTableBody.appendChild(row);
    return;
  }

  for (const c of characters) {
    const row = document.createElement("tr");

    const nameCell = document.createElement("td");
    nameCell.textContent = c.name || characterName(c.character_json);
    row.appendChild(nameCell);

    const ownerCell = document.createElement("td");
    ownerCell.textContent = c.owner_id;
    row.appendChild(ownerCell);

    const statusCell = document.createElement("td");
    statusCell.textContent = c.status;
    row.appendChild(statusCell);

    const moveCell = document.createElement("td");
    const moveSelect = document.createElement("select");
    fillCampaignOptions(moveSelect, id);
    const moveButton = document.createElement("button");
    moveButton.type = "button";
    moveButton.className = "secondary";
    moveButton.textContent = "Move";
    moveButton.disabled = moveSelect.options.length === 0;
    moveButton.addEventListener("click", () => moveCharacter(c.id, moveSelect.value));
    moveCell.append(moveSelect, moveButton);
    row.appendChild(moveCell);

    const removeCell = document.createElement("td");
    const removeButton = document.createElement("button");
    removeButton.type = "button";
    removeButton.className = "danger";
    removeButton.textContent = "Remove";
    removeButton.addEventListener("click", () => deleteCharacter(c.id));
    removeCell.appendChild(removeButton);
    row.appendChild(removeCell);

    el.campaignCharactersTableBody.appendChild(row);
  }
}

// --- Characters tab: cross-campaign roster ---

async function loadAllCharacters() {
  const resp = await fetch("/api/characters");
  const characters = (await resp.json()) || [];
  state.allCharacters = characters;

  el.allCharacterPicker.innerHTML = "";
  el.allCharacterEmptyNote.hidden = characters.length > 0;
  el.characterDetail.hidden = characters.length === 0;

  for (const c of characters) {
    const option = document.createElement("option");
    option.value = c.id;
    const name = c.name || characterName(c.character_json);
    option.textContent = `${name} — ${c.owner_id} — ${c.campaign_id}`;
    el.allCharacterPicker.appendChild(option);
  }

  if (characters.length === 0) return;
  const keep = characters.some((c) => c.id === state.selectedCharacterId)
    ? state.selectedCharacterId
    : characters[0].id;
  el.allCharacterPicker.value = keep;
  renderCharacterDetail(characters.find((c) => c.id === keep));
}

el.allCharacterPicker.addEventListener("change", () => {
  const c = (state.allCharacters || []).find((c) => c.id === el.allCharacterPicker.value);
  if (c) renderCharacterDetail(c);
});

function renderCharacterDetail(c) {
  state.selectedCharacterId = c.id;
  el.cdName.textContent = c.name || characterName(c.character_json);
  el.cdOwner.textContent = c.owner_id;
  el.cdCampaign.textContent = campaignLabel(c.campaign_id);
  el.cdStatus.textContent = c.status;
  el.cdCreated.textContent = c.created_at ? new Date(c.created_at).toLocaleString() : "";
  el.cdJson.textContent = JSON.stringify(c.character_json, null, 2);
  el.cdReviewReason.value = "";
  setStatus(el.cdStatusLine, "");

  fillCampaignOptions(el.cdMoveSelect, c.campaign_id);
  el.cdMoveButton.disabled = el.cdMoveSelect.options.length === 0;
}

el.cdMoveButton.addEventListener("click", () =>
  moveCharacter(state.selectedCharacterId, el.cdMoveSelect.value, el.cdStatusLine),
);
el.cdDeleteButton.addEventListener("click", () => deleteCharacter(state.selectedCharacterId, el.cdStatusLine));
el.cdApproveButton.addEventListener("click", () => reviewSelectedCharacter("approved"));
el.cdRejectButton.addEventListener("click", () => reviewSelectedCharacter("rejected"));

async function refreshCharacterViews() {
  const tasks = [loadAllCharacters(), loadCampaignList()];
  if (state.campaignId) tasks.push(loadCampaignCharacters(state.campaignId), loadConnectedPlayers(state.campaignId));
  await Promise.all(tasks);
}

async function moveCharacter(characterId, campaignId, statusEl) {
  if (!characterId || !campaignId) return;
  const resp = await fetch(`/api/characters/${encodeURIComponent(characterId)}/campaign`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ campaign_id: campaignId }),
  });
  if (!resp.ok) {
    if (statusEl) setStatus(statusEl, `Move failed: ${await errorText(resp)}`, true);
    return;
  }
  if (statusEl) setStatus(statusEl, `Moved to ${campaignId}.`);
  await refreshCharacterViews();
}

async function deleteCharacter(characterId, statusEl) {
  if (!characterId) return;
  const resp = await fetch(`/api/characters/${encodeURIComponent(characterId)}`, { method: "DELETE" });
  if (!resp.ok && resp.status !== 204) {
    if (statusEl) setStatus(statusEl, `Delete failed: ${await errorText(resp)}`, true);
    return;
  }
  if (characterId === state.selectedCharacterId) state.selectedCharacterId = "";
  await refreshCharacterViews();
}

async function reviewSelectedCharacter(status) {
  const c = (state.allCharacters || []).find((c) => c.id === state.selectedCharacterId);
  if (!c) return;
  const resp = await fetch(
    `/api/campaigns/${encodeURIComponent(c.campaign_id)}/characters/${encodeURIComponent(c.id)}/review`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status, reason: el.cdReviewReason.value.trim() }),
    },
  );
  if (!resp.ok) {
    setStatus(el.cdStatusLine, `Failed: ${await errorText(resp)}`, true);
    return;
  }
  setStatus(el.cdStatusLine, `Marked ${status}.`);
  await refreshCharacterViews();
}

async function loadConnectedPlayers(id) {
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(id)}/players`);
  const data = await resp.json();
  const senderIds = (data && data.sender_ids) || [];

  el.connectedPlayersTableBody.innerHTML = "";
  if (senderIds.length === 0) {
    const row = document.createElement("tr");
    const cell = document.createElement("td");
    cell.colSpan = 2;
    cell.className = "note";
    cell.textContent = "No players currently connected.";
    row.appendChild(cell);
    el.connectedPlayersTableBody.appendChild(row);
    return;
  }
  for (const senderId of senderIds) {
    const row = document.createElement("tr");

    const senderCell = document.createElement("td");
    senderCell.textContent = senderId;
    row.appendChild(senderCell);

    const kickCell = document.createElement("td");
    const kickButton = document.createElement("button");
    kickButton.type = "button";
    kickButton.className = "secondary";
    kickButton.textContent = "Kick";
    kickButton.addEventListener("click", () => kickPlayer(id, senderId));
    kickCell.appendChild(kickButton);
    row.appendChild(kickCell);

    el.connectedPlayersTableBody.appendChild(row);
  }
}

async function kickPlayer(campaignId, senderId) {
  if (!confirm(`Disconnect "${senderId}" from this campaign now? They can rejoin afterward.`)) return;
  const resp = await fetch(
    `/api/campaigns/${encodeURIComponent(campaignId)}/players/${encodeURIComponent(senderId)}/kick`,
    { method: "POST" },
  );
  if (!resp.ok) {
    window.alert(`Failed: ${await errorText(resp)}`);
  }
  await loadConnectedPlayers(campaignId);
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
  el.sysLLMProvider.value = data.llm_provider || "ollama";
  el.sysLLMURL.value = data.llm_url || "";
  el.sysLLMModel.value = data.llm_model || "";
  el.sysLLMAPIKey.value = data.llm_api_key || "";
  el.sysSystemEngineAddr.value = data.system_engine_addr || "";
  el.sysComfyUIURL.value = data.comfyui_url || "";
  el.sysComfyUIWorkflow.value = data.comfyui_workflow_path || "";
  el.sysDiscordClientID.value = data.discord_client_id || "";
  el.sysDiscordClientSecret.value = data.discord_client_secret || "";
  el.sysDiscordRedirectURL.value = data.discord_redirect_url || "";
  updateDiscordRedirectSuggestion();
}

// discordRedirectSuggestion mirrors Go's listenURL(addr): the player-
// facing listen address turned into a browser-usable origin, plus the
// fixed callback path. A bare or all-interfaces host shows as localhost —
// none of ":8085", "0.0.0.0:8085", "[::]:8085" are things you can
// actually type into a browser or register with Discord.
function discordRedirectSuggestion() {
  let addr = (el.sysAddr.value || "").trim();
  if (!addr) addr = location.host; // fall back to however this panel was reached
  let host = addr;
  let port = "";
  const lastColon = addr.lastIndexOf(":");
  if (lastColon !== -1 && addr.indexOf("]") < lastColon) {
    host = addr.slice(0, lastColon);
    port = addr.slice(lastColon + 1);
  }
  host = host.replace(/^\[|\]$/g, "");
  if (host === "" || host === "0.0.0.0" || host === "::") host = "localhost";
  const origin = "http://" + host + (port ? ":" + port : "");
  return origin + "/auth/discord/callback";
}

function updateDiscordRedirectSuggestion() {
  if (el.discordRedirectSuggestion) {
    el.discordRedirectSuggestion.textContent = discordRedirectSuggestion();
  }
}

function systemSettingsBody() {
  return {
    addr: el.sysAddr.value,
    llm_provider: el.sysLLMProvider.value,
    llm_url: el.sysLLMURL.value,
    llm_model: el.sysLLMModel.value,
    llm_api_key: el.sysLLMAPIKey.value,
    system_engine_addr: el.sysSystemEngineAddr.value,
    comfyui_url: el.sysComfyUIURL.value,
    comfyui_workflow_path: el.sysComfyUIWorkflow.value,
    discord_client_id: el.sysDiscordClientID.value,
    discord_client_secret: el.sysDiscordClientSecret.value,
    discord_redirect_url: el.sysDiscordRedirectURL.value,
  };
}

function resetLLMTestState() {
  el.testLLMButton.classList.remove("test-success", "test-failure");
  setStatus(el.testLLMStatus, "");
}

// Editing any of the four tested fields invalidates the last result —
// a stale green/red no longer reflects what's actually in the form.
for (const field of [el.sysLLMProvider, el.sysLLMURL, el.sysLLMModel, el.sysLLMAPIKey]) {
  field.addEventListener("input", resetLLMTestState);
  field.addEventListener("change", resetLLMTestState);
}

// The suggested Discord redirect URL is derived from the listen address,
// so keep it live as the operator edits that field.
el.sysAddr.addEventListener("input", updateDiscordRedirectSuggestion);
if (el.discordRedirectCopy) {
  el.discordRedirectCopy.addEventListener("click", () => {
    const url = discordRedirectSuggestion();
    el.sysDiscordRedirectURL.value = url;
    if (navigator.clipboard) navigator.clipboard.writeText(url).catch(() => {});
  });
}

el.testLLMButton.addEventListener("click", async () => {
  el.testLLMButton.disabled = true;
  el.testLLMButton.classList.remove("test-success", "test-failure");
  setStatus(el.testLLMStatus, "Testing…");
  try {
    const resp = await fetch("/api/system/test-llm", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        llm_provider: el.sysLLMProvider.value,
        llm_url: el.sysLLMURL.value,
        llm_model: el.sysLLMModel.value,
        llm_api_key: el.sysLLMAPIKey.value,
      }),
    });
    if (!resp.ok) {
      el.testLLMButton.classList.add("test-failure");
      setStatus(el.testLLMStatus, `Failed: ${await errorText(resp)}`, true);
      return;
    }
    const data = await resp.json();
    el.testLLMButton.classList.add("test-success");
    setStatus(el.testLLMStatus, `Connected — model replied: ${data.response}`);
  } catch (err) {
    el.testLLMButton.classList.add("test-failure");
    setStatus(el.testLLMStatus, `Failed: ${err.message}`, true);
  } finally {
    el.testLLMButton.disabled = false;
  }
});

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

// --- Host/operator terms modal ---

async function loadTerms() {
  const resp = await fetch("/api/terms");
  const data = await resp.json();
  el.termsModalText.textContent = data.operator_text;
  el.termsModal.hidden = data.accepted;
}

el.termsModalAgree.addEventListener("click", async () => {
  el.termsModalAgree.disabled = true;
  try {
    const resp = await fetch("/api/terms/accept", { method: "POST" });
    if (resp.ok) {
      el.termsModal.hidden = true;
    } else {
      el.termsModalAgree.disabled = false;
    }
  } catch {
    el.termsModalAgree.disabled = false;
  }
});

loadTerms();
loadSystemSettings();
// Session is the default tab; load the campaign list first so its
// active-campaign dropdown is populated, then kick off the tab (which
// starts the 5s poll).
loadCampaignList().then(() => selectTab("session"));
