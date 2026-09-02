// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// Layforge admin/operator settings panel (design doc §3.3). Hand-written,
// no build step — same "plain files, no bundler" style as
// master/web/app.js, and drives the JSON API package internal/admin's
// Server exposes on this same listener:
//   GET  /api/campaigns
//   GET/PUT /api/campaigns/{id}/policy    (Campaign tab — applies live)
//   GET/PUT /api/campaigns/{id}/security  (Security tab — applies live)
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
  campaignManual: document.getElementById("campaign-manual"),
  campaignManualUse: document.getElementById("campaign-manual-use"),
  campaignPickerNote: document.getElementById("campaign-picker-note"),
  pvpPolicy: document.getElementById("pvp-policy"),
  pvpConsent: document.getElementById("pvp-consent"),
  maturityTierPrompt: document.getElementById("maturity-tier-prompt"),
  imageMaturityTierPrompt: document.getElementById("image-maturity-tier-prompt"),
  campaignSave: document.getElementById("campaign-save"),
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

// --- Campaign picker ---

async function loadCampaignList() {
  const resp = await fetch("/api/campaigns");
  const data = await resp.json();
  el.campaignSelect.innerHTML = "";
  for (const id of data.campaign_ids || []) {
    const option = document.createElement("option");
    option.value = id;
    option.textContent = id;
    el.campaignSelect.appendChild(option);
  }
  if (data.campaign_ids && data.campaign_ids.length) {
    selectCampaign(data.campaign_ids[0]);
  } else {
    el.campaignPickerNote.textContent = "No campaigns known yet — type one above to pre-configure it.";
  }
}

el.campaignSelect.addEventListener("change", () => selectCampaign(el.campaignSelect.value));
el.campaignManualUse.addEventListener("click", () => {
  const id = el.campaignManual.value.trim();
  if (!id) return;
  if (![...el.campaignSelect.options].some((o) => o.value === id)) {
    const option = document.createElement("option");
    option.value = id;
    option.textContent = id;
    el.campaignSelect.appendChild(option);
  }
  el.campaignSelect.value = id;
  selectCampaign(id);
});

async function selectCampaign(id) {
  state.campaignId = id;
  el.campaignSelect.value = id;
  el.campaignPickerNote.textContent = "";
  await Promise.all([loadCampaignPolicy(id), loadCampaignSecurity(id)]);
}

// --- Campaign tab ---

async function loadCampaignPolicy(id) {
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(id)}/policy`);
  const data = await resp.json();
  el.pvpPolicy.value = data.pvp_policy || "pve_only";
  el.pvpConsent.value = (data.pvp_consent || []).join(", ");
  el.maturityTierPrompt.value = data.maturity_tier_prompt || "";
  el.imageMaturityTierPrompt.value = data.image_maturity_tier_prompt || "";
}

el.campaignSave.addEventListener("click", async () => {
  if (!state.campaignId) return;
  setStatus(el.campaignSaveStatus, "Saving…");
  const body = {
    pvp_policy: el.pvpPolicy.value,
    pvp_consent: el.pvpConsent.value.split(",").map((s) => s.trim()).filter(Boolean),
    maturity_tier_prompt: el.maturityTierPrompt.value,
    image_maturity_tier_prompt: el.imageMaturityTierPrompt.value,
  };
  const resp = await fetch(`/api/campaigns/${encodeURIComponent(state.campaignId)}/policy`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  setStatus(el.campaignSaveStatus, resp.ok ? "Saved." : `Failed: ${await errorText(resp)}`, !resp.ok);
});

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
