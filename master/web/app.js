// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// Minimal V1 web client (design doc §4). Hand-written, not generated —
// design doc §6 envisions a proper JS reference SDK against
// protocol/asyncapi.yaml eventually; this predates that and must be kept
// in sync with the protocol by hand in the meantime (see PROTOCOL_VERSION
// below). Only what Master actually implements is wired up: the
// handshake, narrative.player_input -> narrative.player_bubble,
// safety.flag -> safety.flag_broadcast, log.history_request paging, a
// real join-time character-creation conversation (character.
// creation_start, then generic client.query / client.choice prompts and
// their *_response replies, ending in the same character.
// validation_result an ordinary character.upload also produces — design
// doc §9.4), the interactive dice roll (roll.check_request triggers the
// client.roll* message family — a clickable ghost die the roller reveals
// themselves, a read-only spectator twin for everyone else — see the
// "client.roll* interactive dice" section below), and now a read-only
// character sheet: character.
// schema_request/character.get, rendered generically from whatever
// json_schema the active system engine publishes (see
// character-sheet.js — not hardcoded to D&D's shape, design doc §4), and
// a combat-map sidebar thumbnail + lightbox (map.token_state, design doc
// §6.2) — a current-state widget, not appended to the scrolling log; see
// onMapTokenState. Push-to-talk (audio.chunk -> audio.transcription,
// design doc §4) is wired too — see the "Push-to-talk" section below —
// including a live-updating partial preview while still recording
// (Master re-transcribes the growing recording periodically; this
// client needs no protocol/capture change to receive that, since
// audio.transcription already carried is_final for exactly this).
//
// The dice roll (and the sheet) needs a character Master's store
// actually recognizes (roll.check_request/character.get are gated on
// store.Character.OwnerID — see package server's resolveCheck/
// sendCharacterState); onJoined now gets one through a real join-time
// choice (design doc §9.4) instead of the old auto-uploaded stopgap
// character — see the "client.query / client.choice prompt bubbles"
// section below, on which character.creation_start's conversation runs.

import { renderCharacterSheetTabs } from "./character-sheet.js";
import { buildDieVisual, tumbleAndSettle, TUMBLE_DURATION_MS } from "./dice3d.js";

// ES modules are always strict mode — no "use strict" directive needed.

const PROTOCOL_VERSION = "0.1.0";

// TERMS_VERSION must match master/internal/terms.Version exactly — a
// mismatch gets a real terms_version_mismatch system.error from Master
// (see onSystemError), which clears the stored acceptance below and
// re-shows the modal, so a stale cached copy of this file self-corrects
// rather than silently treating an old acceptance as still valid.
const TERMS_VERSION = "2026-09-07";
const TERMS_STORAGE_KEY = "layforge.termsAcceptedVersion";

function hasAcceptedCurrentTerms() {
  try {
    return localStorage.getItem(TERMS_STORAGE_KEY) === TERMS_VERSION;
  } catch {
    return false;
  }
}

function saveTermsAccepted() {
  try {
    localStorage.setItem(TERMS_STORAGE_KEY, TERMS_VERSION);
  } catch {
    // Best-effort — a private-browsing/storage-disabled session just
    // re-shows the modal next time.
  }
}

function clearAcceptedTerms() {
  try {
    localStorage.removeItem(TERMS_STORAGE_KEY);
  } catch {
    // best-effort, see saveTermsAccepted
  }
}

// --- Discord login (design doc §6.6) ---
// The session token is minted by this Master's /auth/discord/callback and
// handed back in the URL fragment (never a query string — keeps it out of
// access logs and Referer). It is presented as system.connect's
// auth_token. Stored per-browser like the terms acceptance; treated as a
// bearer secret, so it is never logged or shown.
const DISCORD_TOKEN_KEY = "layforge.discordToken";
const DISCORD_NAME_KEY = "layforge.discordName";

function loadStoredDiscordLogin() {
  try {
    return {
      token: localStorage.getItem(DISCORD_TOKEN_KEY) || "",
      name: localStorage.getItem(DISCORD_NAME_KEY) || "",
    };
  } catch {
    return { token: "", name: "" };
  }
}

function saveDiscordLogin(token, name) {
  try {
    localStorage.setItem(DISCORD_TOKEN_KEY, token);
    localStorage.setItem(DISCORD_NAME_KEY, name || "");
  } catch {
    // best-effort, see saveTermsAccepted
  }
}

function clearDiscordLogin() {
  try {
    localStorage.removeItem(DISCORD_TOKEN_KEY);
    localStorage.removeItem(DISCORD_NAME_KEY);
  } catch {
    // best-effort
  }
}

// captureDiscordLoginFromHash reads a "#lf_token=…&lf_name=…" fragment
// left by a completed /auth/discord/callback redirect, persists it, and
// scrubs it from the address bar so a reload or a shared link doesn't
// carry the token.
function captureDiscordLoginFromHash() {
  if (!location.hash || location.hash.length < 2) return;
  let params;
  try {
    params = new URLSearchParams(location.hash.slice(1));
  } catch {
    return;
  }
  const token = params.get("lf_token");
  if (!token) return;
  saveDiscordLogin(token, params.get("lf_name") || "");
  try {
    history.replaceState(null, "", location.pathname + location.search);
  } catch {
    location.hash = "";
  }
}

const state = {
  ws: null,
  wsUrl: "",
  campaignId: "",
  senderId: "",
  characterId: "",
  joined: false,
  // discordToken is this browser's Discord login session token, sent as
  // system.connect's auth_token when set. discordName is the display name
  // for the join screen only. Both empty when Discord OAuth isn't in use.
  discordToken: "",
  discordName: "",
  // sessionInfo is the last GET /api/session response — which campaign is
  // running, whether it needs a password, whether it's closed. Drives the
  // join screen; state.campaignId is taken from it, not typed.
  sessionInfo: null,
  // joinPassword is the campaign password the player typed, sent in
  // system.connect. Not persisted.
  joinPassword: "",
  // handshakeRejected is set by onSystemError when Master refuses the
  // join, so the close handler that fires right after doesn't overwrite
  // the reason with a generic message.
  handshakeRejected: false,
  // characterCreated flips true once character.validation_result lands a
  // real character_id — used to decide whether a system.error mid-setup
  // should offer a "start creation over" affordance.
  characterCreated: false,
  pendingJoinUrl: null,
  pendingInputMessageId: null,
  // oldestLoadedSequence/hasMoreOlder track the "load earlier" cursor —
  // see the History paging section below.
  oldestLoadedSequence: null,
  hasMoreOlder: false,
  // --- Reconnect (design doc §4) ---
  // hasJoinedOnce distinguishes the very first system.session_state
  // "joined" (runs onJoined: reveals the chat screen, starts the
  // character-creation conversation) from every later one after an
  // unplanned drop
  // (runs onReconnected instead: re-announce presence and catch up on
  // whatever was missed, without redoing one-time setup or resetting
  // state the player is mid-way through, like an open dice roll).
  hasJoinedOnce: false,
  // reconnectAttempts drives exponential backoff (capped at 30s);
  // reconnectTimer is the pending setTimeout handle, or null when no
  // reconnect is currently scheduled — used to avoid double-scheduling
  // if "error" and "close" both fire for the same drop.
  reconnectAttempts: 0,
  reconnectTimer: null,
  // pendingReconnectHistory marks the next log.history_response as a
  // post-reconnect catch-up fetch (append + de-dupe by message_id)
  // rather than the normal "load earlier"/initial-tail fetch (prepend)
  // — see onHistoryResponse.
  pendingReconnectHistory: false,
  // renderedMessageIds de-dupes by message_id across a reconnect: the
  // catch-up history fetch's "most recent page" necessarily overlaps
  // with whatever the client already rendered live before the drop, and
  // every message this protocol carries has a real, unique message_id
  // (design doc §5) to key that de-dupe on.
  renderedMessageIds: new Set(),
  // rollCharacterId is Master's own store.Character.ID, assigned once
  // character creation finishes (see onCharacterValidationResult). Every
  // message
  // that references the character mechanically — roll.check_request,
  // character.apply_effect, character.get, and narrative.player_input's
  // character_id (see onInputSubmit) — uses this, never characterId (the
  // display name typed at join). Sending the display name as
  // narrative.player_input's character_id was a real bug: the DM tool-use
  // slow pass (design doc §8) hands that value straight to the System
  // Engine, and "Kestrel" isn't a lookup key.
  rollCharacterId: null,
  // --- client.roll* interactive dice ---
  // openRolls tracks each roll bubble currently awaiting reveal, keyed
  // by prompt_id: { wrap, dieEls: Map(die_id -> element), summaryEl }.
  // See onClientRoll/onClientRollSpectate/onClientRollSpectateReveal/
  // onClientRollComplete.
  openRolls: new Map(),
  // --- Character sheet ---
  // characterSchema (parsed JSON Schema) and characterData (the raw
  // character_data object) each arrive independently (character.
  // schema_response / character.state) — the sheet only renders once
  // both are present, whichever order they happen to arrive in.
  characterSchema: null,
  characterData: null,
  // --- Push-to-talk (design doc §4) ---
  // audioRecorder/audioStream are only non-null while actively
  // recording. audioStreamId groups this recording's audio.chunk
  // messages; audioSequence is the next chunk's sequence number.
  audioRecorder: null,
  audioStream: null,
  audioStreamId: null,
  audioSequence: 0,
  // pendingInputSource records whether input-text's current content
  // came from an unedited voice transcription ("voice") or was typed/
  // edited by the player ("typed") — set to "voice" only by
  // onAudioTranscription, and reset to "typed" the instant the player
  // actually types anything afterward (see the input-text "input"
  // listener below), so a corrected transcript the player never touched
  // still records its real provenance, and a fully retyped message does
  // not.
  pendingInputSource: "typed",
};

const el = {
  joinScreen: document.getElementById("join-screen"),
  chatScreen: document.getElementById("chat-screen"),
  joinUrl: document.getElementById("join-url"),
  joinNoGame: document.getElementById("join-no-game"),
  joinNowPlaying: document.getElementById("join-now-playing"),
  joinCampaignName: document.getElementById("join-campaign-name"),
  joinClosedNote: document.getElementById("join-closed-note"),
  joinPasswordLabel: document.getElementById("join-password-label"),
  joinPassword: document.getElementById("join-password"),
  joinButton: document.getElementById("join-button"),
  joinError: document.getElementById("join-error"),
  discordAuth: document.getElementById("discord-auth"),
  discordLoginButton: document.getElementById("discord-login-button"),
  discordSignedIn: document.getElementById("discord-signed-in"),
  discordName: document.getElementById("discord-name"),
  discordLogoutButton: document.getElementById("discord-logout-button"),
  termsModal: document.getElementById("terms-modal"),
  termsModalAgree: document.getElementById("terms-modal-agree"),
  termsModalDecline: document.getElementById("terms-modal-decline"),
  chatCampaignLabel: document.getElementById("chat-campaign-label"),
  chatStatus: document.getElementById("chat-status"),
  log: document.getElementById("log"),
  loadEarlierButton: document.getElementById("load-earlier-button"),
  safetyFlagButton: document.getElementById("safety-flag-button"),
  safetyFlagPanel: document.getElementById("safety-flag-panel"),
  safetyFlagTopic: document.getElementById("safety-flag-topic"),
  safetyFlagCancel: document.getElementById("safety-flag-cancel"),
  safetyFlagSend: document.getElementById("safety-flag-send"),
  inputForm: document.getElementById("input-form"),
  inputText: document.getElementById("input-text"),
  inputSend: document.getElementById("input-send"),
  micButton: document.getElementById("mic-button"),
  characterIdentity: document.getElementById("character-identity"),
  characterTabs: document.getElementById("character-tabs"),
  characterTabPanels: document.getElementById("character-tab-panels"),
  combatMapWidget: document.getElementById("combat-map-widget"),
  combatMapThumbButton: document.getElementById("combat-map-thumb-button"),
  combatMapThumb: document.getElementById("combat-map-thumb"),
  combatMapLightbox: document.getElementById("combat-map-lightbox"),
  combatMapLightboxImg: document.getElementById("combat-map-lightbox-img"),
  combatMapLightboxBackdrop: document.getElementById("combat-map-lightbox-backdrop"),
  combatMapLightboxClose: document.getElementById("combat-map-lightbox-close"),
};

el.joinUrl.value = defaultWsUrl();
el.joinButton.addEventListener("click", onJoinClick);
el.termsModalAgree.addEventListener("click", onTermsAgree);

captureDiscordLoginFromHash();
initDiscordAuth();
initSession();
el.discordLoginButton.addEventListener("click", () => {
  // Full-page navigation to Master's own login route (same origin as
  // this client when Discord OAuth is in use); it round-trips through
  // Discord and lands back here with a "#lf_token=…" fragment.
  location.assign("/auth/discord/login");
});
el.discordLogoutButton.addEventListener("click", onDiscordLogout);
el.termsModalDecline.addEventListener("click", () => {
  el.termsModal.hidden = true;
});
el.safetyFlagButton.addEventListener("click", openSafetyFlagPanel);
el.safetyFlagCancel.addEventListener("click", closeSafetyFlagPanel);
el.safetyFlagSend.addEventListener("click", onSafetyFlagSend);
el.inputForm.addEventListener("submit", onInputSubmit);
el.inputText.addEventListener("input", () => {
  // Only a real keystroke fires "input" — setting .value
  // programmatically (onAudioTranscription) does not, so this only
  // ever reverts an unedited transcription's provenance, never
  // overwrites voice provenance the moment it's set.
  state.pendingInputSource = "typed";
});
initMicButton();
el.loadEarlierButton.addEventListener("click", onLoadEarlierClick);
el.combatMapThumbButton.addEventListener("click", openCombatMapLightbox);
el.combatMapLightboxBackdrop.addEventListener("click", closeCombatMapLightbox);
el.combatMapLightboxClose.addEventListener("click", closeCombatMapLightbox);
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !el.combatMapLightbox.hidden) closeCombatMapLightbox();
});

function defaultWsUrl() {
  // location.host is empty when opened via file://, and there's no
  // "same server" to default to in that case.
  if (location.protocol === "file:" || !location.host) {
    return "ws://localhost:8080/ws";
  }
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${location.host}/ws`;
}

function randomId() {
  try {
    if (crypto && crypto.randomUUID) return crypto.randomUUID();
    if (crypto && crypto.getRandomValues) {
      const bytes = crypto.getRandomValues(new Uint8Array(16));
      return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
    }
  } catch {
    // fall through to the low-quality fallback below
  }
  return "id-" + Math.random().toString(16).slice(2) + Date.now().toString(16);
}

// newEnvelope builds the fields every protocol message carries
// (protocol/asyncapi.yaml's Envelope schema, design doc §5). Callers
// spread a "payload" property onto the result.
function newEnvelope(type) {
  return {
    protocol_version: PROTOCOL_VERSION,
    message_id: randomId(),
    timestamp: new Date().toISOString(),
    sender_id: state.senderId,
    campaign_id: state.campaignId,
    type,
  };
}

function send(msg) {
  if (!state.ws || state.ws.readyState !== WebSocket.OPEN) return;
  state.ws.send(JSON.stringify(msg));
}

// --- Join flow ---

function onJoinClick() {
  const url = el.joinUrl.value.trim();
  if (!url) {
    showJoinError("A Master WebSocket URL is required (see Advanced).");
    return;
  }
  const info = state.sessionInfo;
  if (!info || !info.campaign_id) {
    showJoinError("There's no game to join yet — check back once the Host starts one.");
    return;
  }
  if (info.needs_password && !el.joinPassword.value) {
    showJoinError("This game needs a password.");
    return;
  }
  state.campaignId = info.campaign_id;
  state.joinPassword = el.joinPassword.value;
  // sender_id: the Discord account when signed in (Master keys ownership
  // on it regardless — see the identity plumbing), otherwise a stable
  // per-browser id so an unauthenticated player is still recognizable
  // across reconnects. The character's *name* comes from creation now,
  // not from here.
  state.senderId = state.discordToken ? discordSenderId() : persistentSenderId();

  if (!hasAcceptedCurrentTerms()) {
    state.pendingJoinUrl = url;
    el.termsModal.hidden = false;
    return;
  }
  connect(url);
}

// persistentSenderId returns a stable random id for this browser,
// generated once and kept in localStorage — the unauthenticated
// stand-in for an account id now that there's no typed character name to
// use.
function persistentSenderId() {
  try {
    let id = localStorage.getItem("lf_sender_id");
    if (!id) {
      id = randomId();
      localStorage.setItem("lf_sender_id", id);
    }
    return id;
  } catch {
    return randomId();
  }
}

// discordSenderId is a readable stand-in the client puts in envelopes
// when signed in; Master overrides ownership with the real account id
// from the session token, so this only needs to be stable, not secret.
function discordSenderId() {
  return "discord-web:" + (state.discordName || "player");
}

// onTermsAgree fires from the join-screen modal (a fresh join, using
// state.pendingJoinUrl) as well as never from anywhere else — a
// previously-accepted browser skips the modal entirely in onJoinClick
// above, so this is only ever reached via an explicit click.
function onTermsAgree() {
  saveTermsAccepted();
  el.termsModal.hidden = true;
  const url = state.pendingJoinUrl;
  state.pendingJoinUrl = null;
  if (url) connect(url);
}

function showJoinError(message) {
  el.joinError.textContent = message;
  el.joinError.hidden = false;
}

// initDiscordAuth probes whether this Master runs Discord OAuth and, if
// so, reveals the login controls. A failed/absent probe leaves the join
// screen exactly as it was — Discord is opt-in per Master.
async function initDiscordAuth() {
  const stored = loadStoredDiscordLogin();
  state.discordToken = stored.token;
  state.discordName = stored.name;
  try {
    const resp = await fetch("/auth/discord/enabled", { cache: "no-store" });
    if (!resp.ok) return;
  } catch {
    return;
  }
  el.discordAuth.hidden = false;
  renderDiscordAuth();
}

function renderDiscordAuth() {
  const signedIn = !!state.discordToken;
  el.discordLoginButton.hidden = signedIn;
  el.discordSignedIn.hidden = !signedIn;
  if (signedIn) {
    el.discordName.textContent = state.discordName || "your Discord account";
  }
}

// initSession fetches GET /api/session (the player-facing view of what
// the Host is running) and renders the join screen from it. Re-polled
// every 15s while the join screen is up, so a player waiting for the
// Host to start a game sees it appear without reloading.
async function initSession() {
  await loadSession();
  setInterval(() => {
    if (!state.joined) loadSession();
  }, 15000);
}

async function loadSession() {
  try {
    const resp = await fetch("/api/session", { cache: "no-store" });
    if (!resp.ok) return;
    state.sessionInfo = await resp.json();
  } catch {
    return;
  }
  renderJoinScreen();
}

function renderJoinScreen() {
  const info = state.sessionInfo || {};
  const hasGame = !!info.campaign_id;

  el.joinNoGame.hidden = hasGame;
  el.joinNowPlaying.hidden = !hasGame;
  el.joinClosedNote.hidden = !(hasGame && info.join_locked);
  el.joinPasswordLabel.hidden = !(hasGame && info.needs_password);
  el.joinButton.disabled = !hasGame;

  if (hasGame) {
    el.joinCampaignName.textContent = info.display_name || info.campaign_id;
  }
}

async function onDiscordLogout() {
  const token = state.discordToken;
  clearDiscordLogin();
  state.discordToken = "";
  state.discordName = "";
  renderDiscordAuth();
  if (!token) return;
  try {
    await fetch("/auth/discord/logout", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token }),
    });
  } catch {
    // The local token is already cleared; a failed server-side revoke
    // just means it lingers until it expires on its own.
  }
}

function connect(url) {
  el.joinError.hidden = true;
  el.joinButton.disabled = true;
  state.wsUrl = url;
  openSocket();
}

// openSocket opens state.wsUrl and wires it up — shared by the initial
// join and every later reconnect attempt, since both need the identical
// handshake/message/close handling. What differs is only what happens
// on a successful join (see the system.session_state case in
// handleMessage: onJoined the first time, onReconnected after) and, on
// an unplanned close, whether that's still "trying to join at all"
// (show the join-screen error, same as always) or "was already joined"
// (schedule a reconnect instead of just giving up).
function openSocket() {
  let ws;
  try {
    ws = new WebSocket(state.wsUrl);
  } catch (err) {
    if (!state.joined) {
      showJoinError("Could not open WebSocket: " + err.message);
      el.joinButton.disabled = false;
      return;
    }
    scheduleReconnect();
    return;
  }
  state.ws = ws;

  ws.addEventListener("open", () => {
    send({
      ...newEnvelope("system.connect"),
      payload: {
        client_kind: "player_web_v1",
        // Discord login token when this browser has one; campaign password
        // when the running game needs one. Either may be empty.
        auth_token: state.discordToken || "",
        campaign_password: state.joinPassword || "",
      },
    });
    // Reaching openSocket at all means this browser has already agreed
    // (onJoinClick only calls connect() after hasAcceptedCurrentTerms()
    // or a fresh onTermsAgree) — send terms.accept once per connection
    // so Master's own dispatch gate (internal/server/terms.go) actually
    // unblocks real messages, not just this client's own UI.
    send({
      ...newEnvelope("terms.accept"),
      payload: { version: TERMS_VERSION },
    });
  });

  ws.addEventListener("message", (event) => {
    let msg;
    try {
      msg = JSON.parse(event.data);
    } catch (err) {
      console.error("failed to parse message from Master", event.data, err);
      return;
    }
    handleMessage(msg);
  });

  ws.addEventListener("close", (event) => {
    if (!state.joined) {
      state.ws = null;
      el.joinButton.disabled = false;
      if (state.handshakeRejected) {
        // onSystemError already showed the real reason.
        state.handshakeRejected = false;
        return;
      }
      showJoinError(`Connection closed before joining (code ${event.code}). Try again.`);
      return;
    }
    scheduleReconnect();
  });

  ws.addEventListener("error", () => {
    if (!state.joined) {
      showJoinError("Could not reach that WebSocket URL.");
      el.joinButton.disabled = false;
    }
    // Once joined, "error" is normally followed by "close" per the
    // WebSocket spec — scheduleReconnect() runs there, not here, so a
    // drop that fires both doesn't schedule two overlapping reconnects.
  });
}

// scheduleReconnect backs off exponentially (1s, 2s, 4s, ... capped at
// 30s) and retries indefinitely — a dropped WebSocket is assumed
// recoverable (a laptop sleeping, a flaky connection, Master restarting)
// rather than something the player must notice and manually fix.
// reconnectTimer guards against scheduling twice for the same drop.
function scheduleReconnect() {
  if (state.reconnectTimer) return;
  const delayMs = Math.min(1000 * 2 ** state.reconnectAttempts, 30000);
  state.reconnectAttempts++;
  setStatus(`reconnecting (attempt ${state.reconnectAttempts})…`, "reconnecting");
  state.reconnectTimer = setTimeout(() => {
    state.reconnectTimer = null;
    openSocket();
  }, delayMs);
}

// onReconnected runs instead of onJoined for every system.session_state
// "joined" after the first (see handleMessage) — the chat screen is
// already showing and the character-creation conversation (or the
// finished character) already exists server-side from the original
// join, so redoing either would be wrong (a second creation_start,
// a screen flicker). All that's actually needed is announcing the
// reconnect succeeded and catching up on whatever was missed while
// disconnected.
function onReconnected() {
  state.reconnectAttempts = 0;
  setStatus("connected", "connected");
  state.pendingReconnectHistory = true;
  requestHistory({});
}

function setStatus(text, cssClass) {
  el.chatStatus.textContent = text;
  el.chatStatus.className = "status " + (cssClass || "");
}

// --- Inbound message routing ---

function handleMessage(msg) {
  // De-dupe by message_id before anything else: a post-reconnect catch-up
  // history fetch (onReconnected) necessarily overlaps with whatever was
  // already rendered live before the drop, and every message on this
  // protocol carries a real, unique message_id (design doc §5) to key
  // that on. Harmless outside a reconnect too, since a fresh id is
  // generated per message (see newMessage/server-side equivalent) —
  // nothing legitimately reuses one.
  if (msg.message_id) {
    if (state.renderedMessageIds.has(msg.message_id)) return;
    state.renderedMessageIds.add(msg.message_id);
  }

  switch (msg.type) {
    case "system.session_state":
      if (msg.payload && msg.payload.state === "joined") {
        if (msg.payload.identity && msg.payload.identity.display_name) {
          // Master verified who we are (Discord OAuth). Keep the stored
          // name in sync with what Master actually resolved.
          state.discordName = msg.payload.identity.display_name;
          saveDiscordLogin(state.discordToken, state.discordName);
        }
        if (state.hasJoinedOnce) {
          onReconnected();
        } else {
          state.hasJoinedOnce = true;
          onJoined(msg.payload.existing_character_id || "");
        }
      }
      break;
    case "system.error":
      onSystemError(msg);
      break;
    case "narrative.player_bubble":
      onNarrativeBubble(msg);
      break;
    case "narrative.dm_thinking":
      appendDmThinkingBubble(msg.payload || {});
      break;
    case "safety.flag_broadcast":
      appendSafetyBanner(msg.payload ? msg.payload.topic : "");
      break;
    case "log.history_response":
      onHistoryResponse(msg.payload);
      break;
    case "character.validation_result":
      onCharacterValidationResult(msg);
      break;
    case "character.schema_response":
      onCharacterSchemaResponse(msg);
      break;
    case "character.state":
      onCharacterStateResponse(msg);
      break;
    case "client.display":
      clearDmThinkingBubble(msg.payload && msg.payload.in_reply_to_message_id);
      appendDmBubble(msg.payload ? msg.payload.text : "");
      break;
    case "tool.result":
      appendToolResultNote(msg.payload || {});
      maybeRefreshCharacterAfterTool(msg.payload || {});
      break;
    case "turn.state":
      appendTurnStateNote(msg.payload || {});
      break;
    case "client.image":
      appendSceneImage(msg.payload || {});
      break;
    case "map.token_state":
      onMapTokenState(msg.payload || {});
      break;
    case "audio.transcription":
      onAudioTranscription(msg);
      break;
    case "client.query":
      onClientQuery(msg.payload || {});
      break;
    case "client.choice":
      onClientChoice(msg.payload || {});
      break;
    case "client.roll":
      onClientRoll(msg.payload || {});
      break;
    case "client.roll_spectate":
      onClientRollSpectate(msg.payload || {});
      break;
    case "client.roll_spectate_reveal":
      onClientRollSpectateReveal(msg.payload || {});
      break;
    case "client.roll_complete":
      onClientRollComplete(msg.payload || {});
      break;
    case "client.ability_score_rolls":
      onClientAbilityScoreRolls(msg.payload || {});
      break;
    case "character.review_result":
      appendCharacterReviewNote(msg.payload || {});
      break;
    default:
      console.warn("unhandled message type from Master", msg.type, msg);
  }
}

// setChatHeader renders "LayForge: <campaign name> - <player name>" at
// the top of the chat screen — the campaign's display name (not its id),
// and the Discord name when signed in.
function setChatHeader() {
  const campaign = (state.sessionInfo && state.sessionInfo.display_name) || state.campaignId;
  el.chatCampaignLabel.textContent =
    "LayForge: " + campaign + (state.discordName ? " - " + state.discordName : "");
}

// onJoined runs once, on this connection's first "joined"
// system.session_state (see handleMessage). existingCharacterId is set
// when Master's own join-time lookup (server.go's findOwnedCharacter)
// found a non-rejected character this account/sender_id already owns in
// this campaign — resume it instead of starting character creation over.
// Fixes a live-reported bug: a fresh page load (a browser back-button
// navigation followed by logging back in) always looked like a
// brand-new join to this client on its own, with no way to tell "I
// already finished creating a character" apart from "I'm a new player"
// — so it restarted character creation every time, on top of an
// already-playable character, even though the chat history it also
// fetches (below) still showed everything that came before.
function onJoined(existingCharacterId) {
  state.joined = true;
  el.joinScreen.hidden = true;
  el.chatScreen.hidden = false;
  setChatHeader();
  setStatus("connected", "connected");
  // No bounds set: Master returns the most recent page (design doc §10)
  // — "where things stand now," the natural first page for a chat-style
  // scrollback, not the campaign's very first message.
  requestHistory({});
  if (existingCharacterId) {
    resumeCharacter(existingCharacterId);
    return;
  }
  // Character creation (design doc §9.4) starts by asking the player to
  // name their character — the first step now that the join screen no
  // longer collects a name. Submitting sends character.creation_start
  // with the name; Master then replies with its own top-level
  // import/roll/pregen prompt as a client.choice (onClientChoice).
  promptForCharacterName();
}

// promptForCharacterName renders the first character-creation step as a
// chat bubble with a text field. On submit it sends
// character.creation_start carrying the name and records it for local
// display (state.characterId).
function promptForCharacterName() {
  const wrap = document.createElement("div");
  wrap.className = "client-prompt";

  const text = document.createElement("div");
  text.className = "client-prompt-text";
  text.textContent = "What's your character's name?";
  wrap.appendChild(text);

  const controls = document.createElement("div");
  controls.className = "client-prompt-controls";
  const input = document.createElement("input");
  input.type = "text";
  input.className = "client-prompt-input";
  input.placeholder = "e.g. Kestrel";
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = "Continue";

  const submit = () => {
    const name = input.value.trim();
    if (!name) {
      input.focus();
      return;
    }
    state.characterId = name;
    input.disabled = true;
    button.disabled = true;
    send({ ...newEnvelope("character.creation_start"), payload: { character_name: name } });
  };
  button.addEventListener("click", submit);
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") submit();
  });

  controls.appendChild(input);
  controls.appendChild(button);
  wrap.appendChild(controls);
  el.log.appendChild(wrap);
  el.log.scrollTop = el.log.scrollHeight;
  input.focus();
}

function onSystemError(msg) {
  const message = (msg.payload && msg.payload.message) || "An error occurred.";
  if (msg.payload && msg.payload.code === "handshake_rejected" && !state.joined) {
    // Master refused the join (wrong password, game closed, no game
    // running). Show it on the join screen; the imminent close handler
    // checks this flag so it doesn't overwrite the real reason with a
    // generic one, and we don't try to reconnect.
    state.handshakeRejected = true;
    showJoinError(message.replace(/^not authorized to join this campaign: /, ""));
    el.joinButton.disabled = false;
    // A fresh session probe in case the game just closed / ended.
    loadSession();
    return;
  }
  if (msg.payload && msg.payload.code === "terms_version_mismatch") {
    // This browser's stored acceptance is for a since-changed terms
    // text — clear it so the next join attempt (a page reload picks up
    // this file's own updated TERMS_VERSION/modal text) shows the
    // modal again instead of silently treating the stale acceptance as
    // still valid.
    clearAcceptedTerms();
  }
  appendErrorNote(message);
  const inReplyTo = msg.payload && msg.payload.in_reply_to_message_id;
  if (inReplyTo && inReplyTo === state.pendingInputMessageId) {
    clearPendingBubble();
  }
  // sendSlowPassFailureNotice (server.go) is this system.error — a
  // slow-pass turn that produced no usable narration. Only the acting
  // player ever receives it (deliberately private, see that function's
  // own doc comment), so this is the one path where clearing the
  // dm_thinking indicator can't rely on client.display ever arriving;
  // everyone else at the table falls back to appendDmThinkingBubble's
  // own safety timeout instead.
  clearDmThinkingBubble(inReplyTo);
  // A character-creation failure (e.g. the chosen path needs a system
  // engine the Host hasn't configured) deletes the session server-side
  // and disables the prompt's buttons — leaving the player with no way
  // forward. Offer a fresh start so they can pick a different path or
  // retry once the Host fixes it.
  if (state.joined && !state.characterCreated) {
    appendCreationRetry();
  }
}

// appendCreationRetry adds a "Start character creation over" button to
// the log — re-sends character.creation_start with the name already
// entered, kicking the flow back to the top-level choice.
function appendCreationRetry() {
  const wrap = document.createElement("div");
  wrap.className = "client-prompt";
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = "Start character creation over";
  button.addEventListener("click", () => {
    button.disabled = true;
    send({ ...newEnvelope("character.creation_start"), payload: { character_name: state.characterId || "" } });
  });
  wrap.appendChild(button);
  el.log.appendChild(wrap);
  el.log.scrollTop = el.log.scrollHeight;
}

function onNarrativeBubble(msg) {
  const payload = msg.payload || {};
  if (payload.character_id === state.rollCharacterId) {
    clearPendingBubble();
  }
  appendBubble(bubbleDisplayName(payload.character_id), payload.text);
}

// bubbleDisplayName resolves a narrative.player_bubble's character_id
// (Master's real store ID, see onInputSubmit) to something readable in
// the "who" tag. This client only ever knows its own character's typed
// name (state.characterId) — there's no campaign roster/name-lookup
// endpoint yet — so another player's bubble falls back to showing their
// raw ID. Acceptable for the current single-character-per-connection
// testing this client is built for; a real roster lookup is future work.
function bubbleDisplayName(characterId) {
  if (characterId && characterId === state.rollCharacterId) {
    return state.characterId;
  }
  return characterId;
}

// --- History paging ---
//
// Master's log.history_request/response (design doc §10) supports two
// paging directions: after_sequence ("continue toward now") and
// before_sequence ("load earlier"). This client only ever uses the
// latter plus the no-bounds tail default — live updates already arrive
// via broadcast, so there's never a need to ask Master "what's newer
// than X" the way a client resuming from cached history might.
//
// Every history response, including the very first (tail) one, is
// content that belongs *before* whatever's currently in the log — so
// onHistoryResponse always prepends. Insert-before-firstChild degrades
// to a plain append when the log is still empty, so one code path
// handles both "initial load" and "load earlier" without a branch.
const HISTORY_PAGE_SIZE = 20;

function requestHistory(bounds) {
  send({
    ...newEnvelope("log.history_request"),
    payload: { ...bounds, limit: HISTORY_PAGE_SIZE },
  });
}

function onLoadEarlierClick() {
  if (state.oldestLoadedSequence == null) return;
  requestHistory({ before_sequence: state.oldestLoadedSequence });
}

function onHistoryResponse(payload) {
  if (state.pendingReconnectHistory) {
    state.pendingReconnectHistory = false;
    appendReconnectCatchUp(payload);
    return;
  }

  const events = (payload && payload.events) || [];
  const wasEmpty = el.log.firstChild === null;

  const fragment = document.createDocumentFragment();
  for (const raw of events) {
    if (raw.message_id) state.renderedMessageIds.add(raw.message_id);
    const rendered = renderHistoryEvent(raw);
    if (rendered) fragment.appendChild(rendered);
  }
  el.log.insertBefore(fragment, el.log.firstChild);

  if (payload && payload.next_before_sequence) {
    state.oldestLoadedSequence = payload.next_before_sequence;
  }
  state.hasMoreOlder = !!(payload && payload.has_more);
  el.loadEarlierButton.hidden = !state.hasMoreOlder;

  // Only jump to the bottom on the very first (tail) page — loading
  // earlier history on top of what's already visible shouldn't yank the
  // viewport away from wherever the reader currently is. (A real
  // implementation would preserve scroll offset around the insertion
  // point; skipped here — see README.md.)
  if (wasEmpty) {
    el.log.scrollTop = el.log.scrollHeight;
  }
}

// appendReconnectCatchUp handles the history page fetched right after a
// reconnect (onReconnected) — unlike the normal tail/"load earlier"
// fetch above, this one necessarily re-includes messages already
// rendered live before the drop, so every event is de-duped by
// message_id first. What's left, if anything, is appended at the
// bottom (continuing the log forward in time), not prepended — this is
// "what did I miss," not "here's more of the past."
function appendReconnectCatchUp(payload) {
  const events = (payload && payload.events) || [];
  const fragment = document.createDocumentFragment();
  for (const raw of events) {
    if (raw.message_id) {
      if (state.renderedMessageIds.has(raw.message_id)) continue;
      state.renderedMessageIds.add(raw.message_id);
    }
    const rendered = renderHistoryEvent(raw);
    if (rendered) fragment.appendChild(rendered);
  }
  if (fragment.childNodes.length === 0) return;
  el.log.appendChild(fragment);
  el.log.scrollTop = el.log.scrollHeight;
}

// renderHistoryEvent returns a DOM element for raw, or null if this
// event type is deliberately not shown in scrollback — system.connect /
// system.session_state / narrative.player_input / safety.flag /
// log.history_* are handshake and audit-trail entries, not things a
// player wants to see, not an oversight.
function renderHistoryEvent(raw) {
  switch (raw.type) {
    case "narrative.player_bubble":
      return bubbleEl(bubbleDisplayName(raw.payload.character_id), raw.payload.text);
    case "client.display":
      return dmBubbleEl(raw.payload ? raw.payload.text : "");
    case "tool.result":
      return toolResultNoteEl(raw.payload || {});
    case "turn.state":
      return turnStateNoteEl(raw.payload || {});
    case "client.image":
      return sceneImageEl(raw.payload || {});
    case "safety.flag_broadcast":
      return safetyBannerEl(raw.payload ? raw.payload.topic : "");
    default:
      return null;
  }
}

// --- Sending player input ---

function onInputSubmit(event) {
  event.preventDefault();
  const text = el.inputText.value.trim();
  if (!text) return;

  // narrative.player_input's character_id must be Master's real
  // store.Character.ID (state.rollCharacterId), not the display name
  // (state.characterId) — the DM tool-use slow pass (design doc §8)
  // hands this straight to resolve_check/apply_effect/get_character_status,
  // which look the character up by that ID. Sending the display name here
  // was a real bug: every DM-triggered tool call failed with
  // character_not_found because "Kestrel" isn't a store ID. Guard against
  // submitting before character creation has finished and set it.
  if (!state.rollCharacterId) {
    appendErrorNote("Still setting up your character — try again in a moment.");
    return;
  }

  const envelope = newEnvelope("narrative.player_input");
  state.pendingInputMessageId = envelope.message_id;
  send({
    ...envelope,
    payload: { character_id: state.rollCharacterId, text, source: state.pendingInputSource },
  });

  el.inputText.value = "";
  state.pendingInputSource = "typed";
  showPendingBubble();
}

// --- Push-to-talk (design doc §4) ---
//
// Hold mic-button to record, release to stop. While still held, Master
// periodically re-transcribes the growing recording (see internal/
// server/audio.go's runPartialTranscription) and this client shows each
// one live in input-text as it arrives (is_final: false) — a genuine
// preview, not just cosmetic, since it can catch a misheard word before
// the player has even let go. Releasing sends the Final chunk; the
// completed transcription (is_final: true) replaces the preview one
// last time via the same onAudioTranscription handler, and the player
// edits it before actually sending, same as anything typed by hand.

// PREFERRED_MIME_TYPES is checked in order; MediaRecorder.isTypeSupported
// varies by browser (Chrome/Firefox default to webm/opus, Safari to
// mp4/aac) — Master doesn't care which, it forwards mime_type to the
// transcription provider as-is (see AudioChunkPayload's own doc
// comment), so this just picks whatever the browser can actually
// record.
const PREFERRED_MIME_TYPES = ["audio/webm;codecs=opus", "audio/webm", "audio/mp4", "audio/ogg;codecs=opus"];

function pickRecorderMimeType() {
  if (typeof MediaRecorder === "undefined" || !MediaRecorder.isTypeSupported) return "";
  for (const type of PREFERRED_MIME_TYPES) {
    if (MediaRecorder.isTypeSupported(type)) return type;
  }
  return "";
}

// initMicButton reveals mic-button only when the browser actually
// supports the APIs push-to-talk needs — a browser without them leaves
// it hidden (its default state in index.html) rather than shown-but-
// broken. Whether *Master* has transcription configured at all is a
// separate, server-side question this function has no way to check in
// advance; a recording sent to an unconfigured Master just gets a real
// system.error, same as any other unavailable-on-this-deployment
// feature (see onSystemError).
function initMicButton() {
  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia || typeof MediaRecorder === "undefined") {
    return;
  }
  el.micButton.hidden = false;
  el.micButton.addEventListener("pointerdown", onMicPointerDown);
  el.micButton.addEventListener("pointerup", onMicPointerUp);
  el.micButton.addEventListener("pointercancel", onMicPointerUp);
  el.micButton.addEventListener("pointerleave", onMicPointerUp);
}

function onMicPointerDown(event) {
  event.preventDefault();
  if (state.audioRecorder) return; // already recording

  navigator.mediaDevices
    .getUserMedia({ audio: true })
    .then((stream) => {
      // The button may already have been released (a very quick tap)
      // by the time the permission prompt resolves — don't start a
      // recording nobody is holding down for anymore.
      if (!el.micButton.classList.contains("armed")) {
        stream.getTracks().forEach((track) => track.stop());
        return;
      }
      startRecording(stream);
    })
    .catch((err) => {
      el.micButton.classList.remove("armed");
      appendErrorNote("Could not access microphone: " + err.message);
    });
  el.micButton.classList.add("armed");
}

function startRecording(stream) {
  const mimeType = pickRecorderMimeType();
  let recorder;
  try {
    recorder = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream);
  } catch (err) {
    stream.getTracks().forEach((track) => track.stop());
    appendErrorNote("Could not start recording: " + err.message);
    return;
  }

  state.audioRecorder = recorder;
  state.audioStream = stream;
  state.audioStreamId = randomId();
  state.audioSequence = 0;
  el.micButton.classList.add("recording");

  recorder.addEventListener("dataavailable", (event) => {
    const isFinal = recorder.state === "inactive";
    if (event.data.size === 0 && !isFinal) return;
    sendAudioChunk(event.data, isFinal, recorder.mimeType || mimeType);
  });
  recorder.addEventListener("stop", () => {
    stream.getTracks().forEach((track) => track.stop());
    state.audioRecorder = null;
    state.audioStream = null;
  });
  recorder.addEventListener("error", (event) => {
    appendErrorNote("Recording error: " + (event.error ? event.error.message : "unknown"));
  });

  // timeslice (250ms) streams chunks while held, matching design doc
  // §4's "chunked" description; the current server-side implementation
  // buffers them all and transcribes once on the final chunk regardless
  // (see internal/server/audio.go), but the client streams incrementally
  // either way so a future incremental-transcription Master doesn't need
  // a client change too.
  recorder.start(250);
}

function sendAudioChunk(blob, isFinal, mimeType) {
  blob
    .arrayBuffer()
    .then((buffer) => {
      send({
        ...newEnvelope("audio.chunk"),
        payload: {
          stream_id: state.audioStreamId,
          sequence: state.audioSequence++,
          audio_base64: arrayBufferToBase64(buffer),
          final: isFinal,
          mime_type: mimeType || "application/octet-stream",
        },
      });
    })
    .catch((err) => {
      appendErrorNote("Could not read recorded audio: " + err.message);
    });
}

// arrayBufferToBase64 chunks the conversion (rather than a single
// String.fromCharCode.apply(null, bytes)) so a longer recording doesn't
// blow the JS engine's call-stack argument limit.
function arrayBufferToBase64(buffer) {
  const bytes = new Uint8Array(buffer);
  const chunkSize = 0x8000;
  let binary = "";
  for (let i = 0; i < bytes.length; i += chunkSize) {
    binary += String.fromCharCode.apply(null, bytes.subarray(i, i + chunkSize));
  }
  return btoa(binary);
}

function onMicPointerUp(event) {
  event.preventDefault();
  el.micButton.classList.remove("armed");
  el.micButton.classList.remove("recording");
  if (state.audioRecorder && state.audioRecorder.state !== "inactive") {
    state.audioRecorder.stop();
  }
}

// onAudioTranscription populates input-text with the latest
// transcription so the player can edit it before sending — never sent
// automatically (design doc §4's own stated goal for this feature).
// Handles both a live partial (is_final: false, arrives repeatedly
// while the mic button is still held) and the finished result
// (is_final: true, on release) identically for the text itself — the
// box simply always shows the most recent transcription Master has
// produced. Focus only moves to the box on the final result: doing that
// on every partial too would yank focus (and, on mobile, pop the
// on-screen keyboard) every couple of seconds while the player is still
// actively holding the mic button down, not done typing anything yet.
// stream_id isn't checked against state.audioStreamId: only one
// recording can be in flight from this client at a time (mic-button is
// a single hold-to-record control), so whatever transcription arrives
// is necessarily the one just requested.
function onAudioTranscription(msg) {
  const text = msg.payload && msg.payload.text;
  if (!text) return;
  el.inputText.value = text;
  state.pendingInputSource = "voice";
  if (msg.payload.is_final) {
    el.inputText.focus();
  }
}

function openSafetyFlagPanel() {
  el.safetyFlagTopic.value = "";
  el.safetyFlagPanel.hidden = false;
  el.safetyFlagTopic.focus();
}

function closeSafetyFlagPanel() {
  el.safetyFlagPanel.hidden = true;
}

function onSafetyFlagSend() {
  const topic = el.safetyFlagTopic.value.trim();
  send({
    ...newEnvelope("safety.flag"),
    payload: topic ? { topic } : {},
  });
  closeSafetyFlagPanel();
}

// --- client.query / client.choice prompt bubbles ---
//
// Master drives every ask-the-player interaction through the generic
// client.* prompt family (design doc §4 / protocol/asyncapi.yaml):
//
//   client.query  — free text: a prompt, a text box, a submit button.
//                   Answered with client.query_response { prompt_id, text }.
//   client.choice — a prompt plus one button per option; each option
//                   carries a value (what comes back) and a label (the
//                   button text). Answered with client.choice_response
//                   { prompt_id, value }.
//
// Character creation (design doc §9.4) is the first and main consumer:
// character.creation_start (sent once, from the "name your character"
// step promptForCharacterName shows on join) kicks off a conversation of
// these prompts — Master's own top-level choice (import / quick roll /
// detailed roll / pregen), then either the import/pregen sub-flow or the
// System Engine's own question sequence for rolling — ending in a
// character.validation_result (see onCharacterValidationResult). Each
// prompt is a direct reply to this connection only, rendered as an
// ordinary-looking chat bubble rather than a separate screen so every
// player works through their own character at their own pace without
// blocking the table. A client.query with accepts_file_upload set (the
// import sub-flow's "paste your JSON" step) also gets a file picker and
// a Cancel button — the one creation prompt a player can otherwise get
// stuck on after picking "import" by mistake, since every other step is
// either a choice (always has other options) or the engine's own
// free-text questions (never the first, always mid-flow already).
function onClientQuery(payload) {
  const wrap = clientPromptWrap(payload.prompt_text);
  const controls = wrap.querySelector(".client-prompt-controls");

  const settle = () => {
    controls.querySelectorAll("button, textarea, input").forEach((c) => {
      c.disabled = true;
    });
    wrap.classList.add("answered");
  };

  const input = document.createElement("textarea");
  input.className = "client-prompt-input";
  input.rows = 3;
  if (payload.input_placeholder) input.placeholder = payload.input_placeholder;
  controls.appendChild(input);

  if (payload.accepts_file_upload) {
    const fileInput = document.createElement("input");
    fileInput.type = "file";
    fileInput.accept = "application/json,.json";
    fileInput.className = "client-prompt-file";
    const maxBytes = 256 * 1024;
    fileInput.addEventListener("change", () => {
      const file = fileInput.files && fileInput.files[0];
      if (!file) return;
      if (file.size > maxBytes) {
        appendErrorNote(`"${file.name}" is too large (${Math.round(file.size / 1024)} KB) — character files are expected well under 256 KB.`);
        fileInput.value = "";
        return;
      }
      const reader = new FileReader();
      reader.onload = () => {
        input.value = typeof reader.result === "string" ? reader.result : "";
      };
      reader.onerror = () => appendErrorNote(`Could not read "${file.name}".`);
      reader.readAsText(file);
    });
    controls.appendChild(fileInput);

    // Cancel: only offered on the paste-your-JSON prompt (this is where
    // a player who meant to quick/detailed-roll instead, or clicked
    // "import" by mistake, would otherwise be stuck) — re-sends
    // character.creation_start, the same restart appendCreationRetry's
    // own button already uses, kicking the flow back to the top-level
    // choice instead of leaving this prompt as the only way forward.
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "secondary";
    cancel.textContent = "Cancel";
    cancel.addEventListener("click", () => {
      settle();
      send({ ...newEnvelope("character.creation_start"), payload: { character_name: state.characterId || "" } });
    });
    controls.appendChild(cancel);
  }

  const submit = document.createElement("button");
  submit.type = "button";
  submit.textContent = payload.submit_label || "Send";
  submit.addEventListener("click", () => {
    const text = input.value.trim();
    if (!text) {
      input.focus();
      return;
    }
    settle();
    send({
      ...newEnvelope("client.query_response"),
      payload: { prompt_id: payload.prompt_id, text },
    });
  });
  controls.appendChild(submit);

  el.log.appendChild(wrap);
  el.log.scrollTop = el.log.scrollHeight;
}

function onClientChoice(payload) {
  const wrap = clientPromptWrap(payload.prompt_text);
  const controls = wrap.querySelector(".client-prompt-controls");

  const settle = () => {
    controls.querySelectorAll("button").forEach((c) => {
      c.disabled = true;
    });
    wrap.classList.add("answered");
  };

  for (const option of payload.options || []) {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = option.label || option.value;
    button.addEventListener("click", () => {
      settle();
      send({
        ...newEnvelope("client.choice_response"),
        payload: { prompt_id: payload.prompt_id, value: option.value },
      });
    });
    controls.appendChild(button);
  }

  el.log.appendChild(wrap);
  el.log.scrollTop = el.log.scrollHeight;
}

// clientPromptWrap builds the shared bubble shell (prompt text + an empty
// controls row) that onClientQuery / onClientChoice fill in.
function clientPromptWrap(promptText) {
  const wrap = document.createElement("div");
  wrap.className = "client-prompt";

  const text = document.createElement("div");
  text.className = "client-prompt-text";
  text.textContent = promptText || "";
  wrap.appendChild(text);

  const controls = document.createElement("div");
  controls.className = "client-prompt-controls";
  wrap.appendChild(controls);

  return wrap;
}

function onCharacterValidationResult(msg) {
  const payload = msg.payload || {};
  if (!payload.character_id) {
    appendErrorNote("Character setup failed.");
    return;
  }
  resumeCharacter(payload.character_id);
}

// resumeCharacter adopts characterId as this connection's own character
// — the shared tail of both a just-finished creation flow
// (character.validation_result, above) and a rejoin that skipped
// creation entirely because Master's own join-time lookup already found
// this account's existing character (onJoined's existingCharacterId).
function resumeCharacter(characterId) {
  state.rollCharacterId = characterId;
  state.characterCreated = true;

  // Schema is engine-wide, not per-character — fetch it once and reuse
  // it for every character.state that comes in afterward.
  if (!state.characterSchema) {
    send({ ...newEnvelope("character.schema_request"), payload: {} });
  }
  requestCharacterState();
}

function requestCharacterState() {
  if (!state.rollCharacterId) return;
  send({
    ...newEnvelope("character.get"),
    payload: { character_id: state.rollCharacterId },
  });
}

function onCharacterSchemaResponse(msg) {
  const payload = msg.payload || {};
  try {
    state.characterSchema = JSON.parse(payload.json_schema);
  } catch (err) {
    console.error("failed to parse character schema", err);
    return;
  }
  maybeRenderCharacterSheet();
}

function onCharacterStateResponse(msg) {
  const payload = msg.payload || {};
  state.characterData = payload.character_data || null;
  // A resumed character (onJoined's existingCharacterId — see its own
  // doc comment) never goes through promptForCharacterName, so
  // state.characterId is otherwise never set at all for one — leaving
  // bubbleDisplayName with nothing to show for this player's own
  // narrative.player_bubble "who" tag, forever, not just until this
  // response arrives. The real engine name is authoritative here anyway
  // (a detailed_roll character's engine-assigned name can differ from
  // what was typed at creation), so just keep state.characterId in sync
  // with it whenever real data comes back.
  if (state.characterData && state.characterData.name) {
    state.characterId = state.characterData.name;
  }
  renderCharacterIdentity(state.characterData); // doesn't need the schema
  maybeRenderCharacterSheet();
}

function maybeRenderCharacterSheet() {
  if (!state.characterSchema || !state.characterData) return;
  renderCharacterSheetTabs(el.characterTabs, el.characterTabPanels, state.characterSchema, state.characterData);
}

// RACE_ADJECTIVES maps an SRD race name to its adjective form. Human and
// Halfling are unchanged; the map only holds the ones that differ.
const RACE_ADJECTIVES = { Dwarf: "Dwarven", Elf: "Elven" };

// renderCharacterIdentity fills the one-line "Reorx, Male Dwarven Fighter"
// summary under the sidebar's Character header, from the engine's
// character_data (name, gender, raceName, and the class from the first
// class-level entry). Also keeps the narrative input's placeholder
// personalized ("What does Reorx do?") once a real name is known,
// falling back to the generic "What do you do?" before one is.
function renderCharacterIdentity(data) {
  if (!data) {
    el.characterIdentity.hidden = true;
    el.inputText.placeholder = "What do you do?";
    return;
  }
  const name = data.name || state.characterId || "Character";
  const race = data.raceName ? RACE_ADJECTIVES[data.raceName] || data.raceName : "";
  const classes = (data.levelManager && data.levelManager.classes) || [];
  const className = classes.length ? classes[0].className : "";
  const descriptor = [data.gender, race, className].filter(Boolean).join(" ");
  el.characterIdentity.textContent = descriptor ? `${name}, ${descriptor}` : name;
  el.characterIdentity.hidden = false;
  el.inputText.placeholder = `What does ${name} do?`;
}

// onMapTokenState handles map.token_state (design doc §6.2) — a
// current-state replace, the same semantics onCharacterStateResponse
// above already has, not client.image's append-to-history
// pattern below: each message is this recipient's own complete,
// already-fog-of-war-filtered view (see combat_map.go's doc comments on
// the Master side), so the sidebar thumbnail simply swaps to whatever
// image_url this message carries rather than accumulating anything.
// Also keeps the open lightbox (if any) in sync, so a player watching
// the enlarged view during a fight sees it update live rather than
// going stale until they close and reopen it.
function onMapTokenState(payload) {
  if (!payload.image_url) return;
  el.combatMapWidget.hidden = false;
  el.combatMapThumb.src = payload.image_url;
  if (!el.combatMapLightbox.hidden) {
    el.combatMapLightboxImg.src = payload.image_url;
  }
}

function openCombatMapLightbox() {
  if (!el.combatMapThumb.src) return;
  el.combatMapLightboxImg.src = el.combatMapThumb.src;
  el.combatMapLightbox.hidden = false;
}

function closeCombatMapLightbox() {
  el.combatMapLightbox.hidden = true;
}

// --- client.roll* interactive dice ---
//
// A resolved check (resolve_check, whether DM-triggered or player-
// initiated, or an automatic death save) is pre-computed server-side —
// the client never determines a result itself — but the reveal is
// interactive: client.roll shows the roller (this connection, if it
// owns the acting character) a clickable outlined die whose already-
// decided result is hidden until clicked; client.roll_spectate shows
// everyone else the same outline with no result at all (anti-
// metagaming, design doc §9.7), filled in by client.roll_spectate_reveal
// the instant the roller reveals it. client.roll_complete settles the
// whole bubble and shows the labeled breakdown line.
//
// state.openRolls tracks each bubble currently awaiting reveal, keyed by
// prompt_id, so the *_reveal/*_complete handlers below know which DOM
// elements to update.

// The die's actual 3D rendering (real per-shape geometry — tetrahedron/
// cube/octahedron/pentagonal-trapezohedron/icosahedron for d4/d6/d8/d10/
// d20 — with a physically-lit material and a cannon-es-driven contained
// tumble) lives in dice3d.js, imported at the top of this file. This
// section only wires that module into the die element's lifecycle:
// buildDieEl mounts a die's initial (resting) visual, settleDie triggers
// its tumble-and-reveal. See dice3d.js for the rendering/physics/pooling
// itself — nothing shape- or WebGL-specific belongs here.

// buildDieEl builds one die element — a real <button> when the roller
// can click it, a plain <div> for a spectator's read-only ghost. The 3D
// visual (a resting-pose snapshot to start — see dice3d.js's
// buildDieVisual) is the first child; .roll-die-face is layered on top
// of it and stays empty until settleDie fills it in, exactly as before.
function buildDieEl(die, interactive) {
  const dieEl = document.createElement(interactive ? "button" : "div");
  if (interactive) dieEl.type = "button";
  dieEl.className = "roll-die " + (interactive ? "interactive" : "ghost");
  dieEl.dataset.dieId = die.id;
  dieEl.dataset.sides = die.sides;
  dieEl.appendChild(buildDieVisual(dieEl, Number(die.sides)));
  const face = document.createElement("span");
  face.className = "roll-die-face";
  dieEl.appendChild(face);
  return dieEl;
}

// settleDie plays a short tumble then shows result — the face was
// already decided server-side; this is purely a reveal animation, never
// a computation. dropped (used by the ability-score-roll dice below, not
// combat's client.roll) adds a visual "excluded from the total" marker.
// A physical d10 is printed 0-9 (there is no face reading "10"), so a
// server result of 10 on a d10 displays as "0" here; every other die
// size always shows its literal number. The DOM number overlay (this
// function) and dice3d.js's 3D tumble animation are two independent
// timers that both key off TUMBLE_DURATION_MS so the number lands right
// as the die visually settles — the overlay never depends on the 3D
// animation actually completing (see dice3d.js's tumbleAndSettle), which
// is what keeps a roll's result legible even if the renderer pool is
// ever exhausted.
function settleDie(dieEl, result, dropped) {
  dieEl.classList.add("tumbling");
  tumbleAndSettle(dieEl);
  window.setTimeout(() => {
    dieEl.classList.remove("tumbling");
    dieEl.classList.add("revealed");
    if (dropped) dieEl.classList.add("dropped");
    const sides = Number(dieEl.dataset.sides);
    const display = sides === 10 && result === 10 ? 0 : result;
    dieEl.querySelector(".roll-die-face").textContent = String(display);
  }, TUMBLE_DURATION_MS);
}

function rollBubbleWrap(text) {
  const wrap = clientPromptWrap(text);
  wrap.classList.add("client-roll");
  return wrap;
}

// renderRollDice builds the die row + summary line shared by
// onClientRoll/onClientRollSpectate. Interactive dice send
// client.roll_reveal on click; ghost dice have no click handler at all
// (a spectator never decides when a die reveals — only the roller does).
function renderRollDice(controls, promptId, dice, interactive) {
  const row = document.createElement("div");
  row.className = "roll-die-row";
  const dieEls = new Map();
  for (const die of dice) {
    const dieEl = buildDieEl(die, interactive);
    if (interactive) {
      dieEl.addEventListener("click", () => {
        if (dieEl.classList.contains("revealed") || dieEl.classList.contains("tumbling")) return;
        settleDie(dieEl, die.result);
        send({ ...newEnvelope("client.roll_reveal"), payload: { prompt_id: promptId, die_id: die.id } });
      });
    }
    row.appendChild(dieEl);
    dieEls.set(die.id, dieEl);
  }
  controls.appendChild(row);
  const summary = document.createElement("div");
  summary.className = "roll-summary";
  controls.appendChild(summary);
  return { dieEls, summaryEl: summary };
}

function onClientRoll(payload) {
  const wrap = rollBubbleWrap(payload.text);
  const controls = wrap.querySelector(".client-prompt-controls");
  const { dieEls, summaryEl } = renderRollDice(controls, payload.prompt_id, payload.dice || [], true);
  state.openRolls.set(payload.prompt_id, { wrap, dieEls, summaryEl });
  el.log.appendChild(wrap);
  el.log.scrollTop = el.log.scrollHeight;
}

function onClientRollSpectate(payload) {
  const wrap = rollBubbleWrap(`${bubbleDisplayName(payload.character_id)} — ${payload.text}`);
  const controls = wrap.querySelector(".client-prompt-controls");
  const { dieEls, summaryEl } = renderRollDice(controls, payload.prompt_id, payload.dice || [], false);
  state.openRolls.set(payload.prompt_id, { wrap, dieEls, summaryEl });
  el.log.appendChild(wrap);
  el.log.scrollTop = el.log.scrollHeight;
}

function onClientRollSpectateReveal(payload) {
  const open = state.openRolls.get(payload.prompt_id);
  const dieEl = open && open.dieEls.get(payload.die_id);
  if (!dieEl || dieEl.classList.contains("revealed")) return;
  settleDie(dieEl, payload.result);
}

// onClientRollComplete settles the whole bubble (the same .answered
// dimming client.query/client.choice bubbles already use) and shows the
// labeled breakdown line beside the dice. Also re-fetches this client's
// own character state — a check/save/death-save can be immediately
// followed by a mechanics-pass effect that changes HP/currency, the same
// reason the old roll.result handler always did this.
function onClientRollComplete(payload) {
  const open = state.openRolls.get(payload.prompt_id);
  if (open) {
    open.summaryEl.textContent = payload.result_summary || `Total: ${payload.total}`;
    open.wrap.classList.add("answered");
    state.openRolls.delete(payload.prompt_id);
  }
  requestCharacterState();
}

// --- client.ability_score_rolls interactive dice (character creation) ---
//
// The 4d6-drop-lowest ability-score generation step (design doc §9.4) —
// a private, single-player conversation, unlike client.roll's combat
// checks, so there's no spectator to hide a result from: every die's
// result arrives already decided, and each reveal is a purely local
// animation with no round trip per die (contrast client.roll_reveal).
// The six sets appear as six sequential bubbles — set N+1 only appears
// once set N's four dice are all revealed — matching the tabletop feel
// of rolling one score at a time. Once the sixth set is fully revealed,
// the client sends client.ability_score_rolls_ack, which is what
// actually advances the session to the by-ability assignment questions
// (ordinary client.choice bubbles from there on, unrelated to this code).

const ABILITY_SCORE_ROLL_ORDINALS = ["first", "second", "third", "fourth", "fifth", "sixth"];

// abilityScoreRollBubbleText: the engine's own intro line (payload.text)
// covers the whole six-set sequence, so only the first bubble uses it;
// the rest get a short client-composed ordinal line — UI pacing text,
// not domain content the engine needs to author.
function abilityScoreRollBubbleText(index, introText) {
  if (index === 0) return introText;
  const ordinal = ABILITY_SCORE_ROLL_ORDINALS[index] || `${index + 1}th`;
  return `Roll your ${ordinal} ability score.`;
}

// renderAbilityScoreRollSet builds one set's bubble (4 dice + summary
// line) and wires its own advance-to-the-next-set (or, on the last set,
// send-the-ack) logic — recursing forward rather than a shared loop,
// since each set's completion is driven by that set's own click events.
function renderAbilityScoreRollSet(promptId, sets, index, introText) {
  const wrap = rollBubbleWrap(abilityScoreRollBubbleText(index, introText));
  const controls = wrap.querySelector(".client-prompt-controls");
  const set = sets[index];
  const dice = set.dice || [];

  const row = document.createElement("div");
  row.className = "roll-die-row";
  const summary = document.createElement("div");
  summary.className = "roll-summary";

  let revealedCount = 0;
  for (const die of dice) {
    const dieEl = buildDieEl(die, true);
    dieEl.addEventListener("click", () => {
      if (dieEl.classList.contains("revealed") || dieEl.classList.contains("tumbling")) return;
      settleDie(dieEl, die.result, die.dropped);
      revealedCount++;
      if (revealedCount < dice.length) return;
      // Wait out the last die's own tumble (settleDie's 550ms) before
      // showing the sum and advancing, so the summary never appears
      // before the player can see what it's summing.
      window.setTimeout(() => {
        const kept = dice.filter((d) => !d.dropped).map((d) => d.result);
        summary.textContent = `${kept.join(" + ")} = ${set.total}`;
        wrap.classList.add("answered");
        if (index + 1 < sets.length) {
          const next = renderAbilityScoreRollSet(promptId, sets, index + 1, introText);
          el.log.appendChild(next);
          el.log.scrollTop = el.log.scrollHeight;
        } else {
          send({ ...newEnvelope("client.ability_score_rolls_ack"), payload: { prompt_id: promptId } });
        }
      }, 600);
    });
    row.appendChild(dieEl);
  }
  controls.appendChild(row);
  controls.appendChild(summary);
  return wrap;
}

function onClientAbilityScoreRolls(payload) {
  const sets = payload.rolls || [];
  if (sets.length === 0) return;
  const wrap = renderAbilityScoreRollSet(payload.prompt_id, sets, 0, payload.text);
  el.log.appendChild(wrap);
  el.log.scrollTop = el.log.scrollHeight;
}

// --- Rendering ---
//
// Each *El function builds a detached element (used directly for live
// messages, or batched into a fragment for a history page — see
// onHistoryResponse); each append* function is the live-message case:
// build, append to the end of the log, scroll to it.

function bubbleEl(characterId, text, extraClass) {
  const bubble = document.createElement("div");
  bubble.className = extraClass ? `bubble ${extraClass}` : "bubble";

  const who = document.createElement("span");
  who.className = "who";
  who.textContent = characterId || "DM";

  const body = document.createElement("span");
  body.className = "text";
  body.textContent = text;

  bubble.append(who, body);
  return bubble;
}

// dmBubbleEl is client.display's rendering (design doc §7's slow
// pass) — visually distinguished from a player's own narrative bubble
// (bubbleEl's plain case) via the dm-bubble class, since it's DM/NPC
// narration the player didn't write, not their own action rendered back
// to them.
function dmBubbleEl(text) {
  return bubbleEl(null, text, "dm-bubble");
}

function safetyBannerEl(topic) {
  const banner = document.createElement("div");
  banner.className = "safety-banner";
  banner.textContent = topic ? `⚑ Safety flag raised — topic: ${topic}` : "⚑ Safety flag raised";
  return banner;
}

function appendBubble(characterId, text) {
  el.log.appendChild(bubbleEl(characterId, text));
  el.log.scrollTop = el.log.scrollHeight;
}

function appendDmBubble(text) {
  el.log.appendChild(dmBubbleEl(text));
  el.log.scrollTop = el.log.scrollHeight;
}

// appendDmThinkingBubble renders narrative.dm_thinking
// (internal/server/dm_slow_pass.go's sendDmThinkingIndicator) — a
// transient "the DM is working on a reply" bubble shown to the whole
// table the moment the slow pass launches, so a real, sometimes
// multi-minute wait against a slow local LLM doesn't read as a frozen
// game. Tagged with the player_input message_id it's replying to
// (dataset.inReplyTo) so clearDmThinkingBubble removes only the right
// one, leaving a different, still-in-flight turn's own indicator alone.
function appendDmThinkingBubble(payload) {
  const text = payload.text || "The DM is pondering the scene.";
  const inReplyTo = payload.in_reply_to_message_id || "";
  const bubble = dmBubbleEl(text);
  bubble.classList.add("dm-thinking");
  bubble.dataset.inReplyTo = inReplyTo;
  el.log.appendChild(bubble);
  el.log.scrollTop = el.log.scrollHeight;

  // Safety net for a viewer who was never going to receive a matching
  // clear message at all — a spectator watching someone else's turn
  // fail: sendSlowPassFailureNotice (server.go) is private to the
  // acting player only, deliberately (see its own doc comment), so a
  // spectator's copy of this bubble would otherwise sit in the log
  // forever. mechanicsPassTimeout (180s) + narrationPassTimeout (90s)
  // bounds how long a real slow pass can legitimately run; double that
  // with margin before giving up on it client-side.
  setTimeout(() => clearDmThinkingBubble(inReplyTo), 300000);
}

// clearDmThinkingBubble removes the dm_thinking bubble(s) tagged with
// inReplyTo. A falsy inReplyTo clears every one currently shown — a
// defensive fallback, not the expected path, since every real
// narrative.dm_thinking carries one.
function clearDmThinkingBubble(inReplyTo) {
  el.log.querySelectorAll(".dm-thinking").forEach((bubble) => {
    if (!inReplyTo || bubble.dataset.inReplyTo === inReplyTo) {
      bubble.remove();
    }
  });
}

// TOOL_FLAVOR_CATEGORY groups every DM tool name (design doc §8,
// internal/server/dm_tools.go and friends) into what it looks like the
// DM is doing from the table's side, not what it's actually called —
// "DM called list_locations" means nothing to a player. Unlisted/future
// tool names fall through to DEFAULT_TOOL_FLAVOR below, so a new tool
// added server-side without an entry here never shows literally nothing
// or breaks, just the generic phrase until it earns a real category.
const TOOL_FLAVOR_CATEGORY = {
  list_locations: "lore", list_npcs: "lore", list_encounters: "lore",
  list_vehicles: "lore", get_character_schema: "lore",
  resolve_check: "rules", get_available_actions: "rules", get_character_status: "rules",
  melee_attack: "combat_action", ranged_attack: "combat_action", offhand_attack: "combat_action",
  grapple: "combat_action", shove: "combat_action", apply_effect: "combat_action", cast_spell: "combat_action",
  start_combat: "combat_flow", advance_turn: "combat_flow", end_combat: "combat_flow", generate_combat_map: "combat_flow",
  equip_item: "inventory", unequip_item: "inventory", give_item: "inventory", receive_item: "inventory",
  discard_item: "inventory", generate_loot: "inventory", add_currency: "inventory", transfer_currency: "inventory",
  retrieve_currency: "inventory", stash_currency: "inventory", retrieve_item: "inventory", stash_item: "inventory",
  pack_item: "inventory", draw_item: "inventory", spend_currency: "inventory",
  check_item_price: "vendor", list_vendor_inventory: "vendor", vendor_buy_item: "vendor", vendor_sell_item: "vendor",
  create_npc: "npc",
  travel_to: "travel", claim_location: "travel",
  acquire_vehicle: "vehicle", stable_vehicle: "vehicle", take_vehicle: "vehicle",
  narrate_privately: "private",
  generate_scene_image: "image",
  review_character: "review",
};

// TOOL_FLAVOR_PHRASES: a few options per category so the same category
// firing repeatedly (e.g. a fight full of melee_attack calls) doesn't
// show the identical line every time — one is picked at random per note.
const TOOL_FLAVOR_PHRASES = {
  lore: ["The DM flips through their notes.", "The DM checks the campaign notes.", "The DM double-checks who's around."],
  rules: ["The DM checks the rulebook.", "The DM works the numbers.", "The DM peers at the character sheet."],
  combat_action: ["The DM rolls behind the screen.", "The DM works out what happens next.", "The DM resolves the blow."],
  combat_flow: ["The DM tracks initiative.", "The DM sets the scene.", "The DM sketches out the battlefield."],
  inventory: ["The DM checks the ledger.", "The DM tallies up the gear.", "The DM counts out the coin."],
  vendor: ["The DM checks the price list.", "The DM haggles under their breath."],
  npc: ["The DM sketches a new face.", "The DM invents someone new."],
  travel: ["The DM marks the map.", "The DM notes the new ground."],
  vehicle: ["The DM checks the stables.", "The DM notes where it's parked."],
  private: ["The DM leans in for a quiet word."],
  image: ["The DM reaches for their illustration board.", "The DM sketches the scene."],
  review: ["The DM looks over the new arrival's paperwork."],
};

const DEFAULT_TOOL_FLAVOR = ["The DM consults their notes."];

// toolFlavorText picks one flavor phrase for toolName — stable enough to
// read naturally, varied enough not to feel like a progress-bar label.
function toolFlavorText(toolName) {
  const category = TOOL_FLAVOR_CATEGORY[toolName];
  const phrases = (category && TOOL_FLAVOR_PHRASES[category]) || DEFAULT_TOOL_FLAVOR;
  return phrases[Math.floor(Math.random() * phrases.length)];
}

// toolResultNoteEl renders one design doc §8 DM tool-use call as a
// transparency note (design doc §8: "every tool call/result is logged")
// — not a chat bubble, since it's bookkeeping about how the DM arrived
// at its narration, not narration itself. The visible text is in-fiction
// flavor (toolFlavorText) rather than the raw tool name, which means
// nothing to a player; the real tool_name/success/reason_code still ride
// along in the title attribute for anyone who hovers or wants to know
// exactly what happened (e.g. while reporting a bug).
function toolResultNoteEl(payload) {
  const note = document.createElement("div");
  note.className = "note tool-result-note" + (payload.success ? "" : " error-note");
  const icon = payload.success ? "🎲" : "⚠";
  const detail = payload.success ? "" : ` (${payload.reason_code || "failed"})`;
  note.textContent = `${icon} ${toolFlavorText(payload.tool_name)}`;
  note.title = `${payload.tool_name}${detail}`;
  return note;
}

function appendToolResultNote(payload) {
  el.log.appendChild(toolResultNoteEl(payload));
  el.log.scrollTop = el.log.scrollHeight;
}

// TOOL_CATEGORIES_AFFECTING_CHARACTER_DATA are the TOOL_FLAVOR_CATEGORY
// buckets whose underlying DM tool call can change a character's own
// currency/inventory/equipment. A real, live-observed bug this closes:
// the narration would describe gold or an item changing hands, the
// server-side character record really did update, but the open
// character sheet kept showing stale numbers — nothing ever told the
// client to re-fetch it. requestCharacterState() was already called
// after every roll.result regardless of whose roll it was; tool.result
// carries no character_id at all (only tool_name/success/reason_code),
// so this follows that exact same "just refetch, unconditionally"
// precedent rather than trying to work out whether it was actually this
// client's own character that changed.
const TOOL_CATEGORIES_AFFECTING_CHARACTER_DATA = new Set(["inventory", "vendor"]);

function maybeRefreshCharacterAfterTool(payload) {
  if (!payload.success) return;
  if (TOOL_CATEGORIES_AFFECTING_CHARACTER_DATA.has(TOOL_FLAVOR_CATEGORY[payload.tool_name])) {
    requestCharacterState();
  }
}

// turnStateNoteEl renders a turn.state broadcast (design doc §3.1, §9.3)
// — Master's own turn-order bookkeeping, not something the DM narrates
// itself. bubbleDisplayName resolves current_character_id to this
// client's own typed name when it's this player's turn, else falls back
// to the raw ID (see bubbleDisplayName's own doc comment on why).
function turnStateNoteEl(payload) {
  const note = document.createElement("div");
  note.className = "note turn-state-note";
  if (!payload.active) {
    note.textContent = "⚔ Combat ends";
    return note;
  }
  note.textContent = `⚔ Round ${payload.round} — ${bubbleDisplayName(payload.current_character_id)}'s turn`;
  return note;
}

function appendTurnStateNote(payload) {
  el.log.appendChild(turnStateNoteEl(payload));
  el.log.scrollTop = el.log.scrollHeight;
}

// sceneImageEl renders a client.image broadcast (design doc §6.3) as an
// inline image with its caption (falling back to the generator prompt)
// underneath — Master neither authors nor hosts the image itself, this
// just displays whatever URL the configured imagegen.Provider returned.
function sceneImageEl(payload) {
  const figure = document.createElement("figure");
  figure.className = "scene-image";
  const img = document.createElement("img");
  img.src = payload.image_url || "";
  const label = payload.caption || payload.prompt || "";
  img.alt = label || "DM-generated scene illustration";
  img.loading = "lazy";
  const caption = document.createElement("figcaption");
  caption.textContent = label;
  figure.append(img, caption);
  return figure;
}

function appendSceneImage(payload) {
  el.log.appendChild(sceneImageEl(payload));
  el.log.scrollTop = el.log.scrollHeight;
}

function appendSafetyBanner(topic) {
  el.log.appendChild(safetyBannerEl(topic));
  el.log.scrollTop = el.log.scrollHeight;
}

function appendErrorNote(text) {
  const note = document.createElement("div");
  note.className = "note error-note";
  note.textContent = text;
  el.log.appendChild(note);
  el.log.scrollTop = el.log.scrollHeight;
}

// appendCharacterReviewNote renders a character.review_result — the
// conclusion of design doc §9.4's character-import review flow, sent
// privately to this connection only (a deterministic campaign
// level-range check, the DM AI's own balance judgment, or a later Host
// decision from the admin panel). Approved/rejected get their own class
// so a table's stylesheet can distinguish them at a glance, matching
// error-note's own pattern.
function appendCharacterReviewNote(payload) {
  const note = document.createElement("div");
  const approved = payload.status === "approved";
  note.className = `note ${approved ? "character-approved-note" : "character-rejected-note"}`;
  const verb = approved ? "approved" : "rejected";
  const reason = payload.reason ? `: ${payload.reason}` : ".";
  note.textContent = `Your character was ${verb}${reason}`;
  el.log.appendChild(note);
  el.log.scrollTop = el.log.scrollHeight;
}


function showPendingBubble() {
  clearPendingBubble();
  const bubble = bubbleEl("DM", "…");
  bubble.classList.add("pending");
  bubble.id = "pending-bubble";
  el.log.appendChild(bubble);
  el.log.scrollTop = el.log.scrollHeight;
  el.inputSend.disabled = true;
}

function clearPendingBubble() {
  const existing = document.getElementById("pending-bubble");
  if (existing) existing.remove();
  el.inputSend.disabled = false;
  state.pendingInputMessageId = null;
}
