// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// Minimal V1 web client (design doc §4). Hand-written, not generated —
// design doc §6 envisions a proper JS reference SDK against
// protocol/asyncapi.yaml eventually; this predates that and must be kept
// in sync with the protocol by hand in the meantime (see PROTOCOL_VERSION
// below). Only what Master actually implements is wired up: the
// handshake, narrative.player_input -> narrative.player_bubble,
// safety.flag -> safety.flag_broadcast, log.history_request paging, the
// dice tray (character.upload -> character.validation_result,
// roll.check_request -> roll.request/roll.result — see dice.js for the
// actual 3D die), and now a read-only character sheet: character.
// schema_request/character.get, rendered generically from whatever
// json_schema the active system engine publishes (see
// character-sheet.js — not hardcoded to D&D's shape, design doc §4), and
// a combat-map sidebar thumbnail + lightbox (map.token_state, design doc
// §6.2) — a current-state widget, not appended to the scrolling log; see
// onMapTokenState. Push-to-talk (audio.chunk -> audio.transcription,
// design doc §4) is wired too — see the "Push-to-talk" section below —
// but only transcribes into input-text for the player to edit before
// sending; it does not stream a live partial preview while recording.
//
// The dice tray (and now the sheet) needs a character Master's store
// actually recognizes (roll.check_request/character.get are gated on
// store.Character.OwnerID — see package server's resolveCheck/
// sendCharacterState), but there's no real character-creation/import UI
// yet. As a stopgap, onJoined silently uploads a minimal stock character
// built from the join screen's character name — see
// uploadStockCharacter. This is a placeholder for real character
// creation, not the intended long-term flow.

import { renderCharacterSheetTabs } from "./character-sheet.js";

// ES modules are always strict mode — no "use strict" directive needed.

const PROTOCOL_VERSION = "0.1.0";

const state = {
  ws: null,
  wsUrl: "",
  campaignId: "",
  senderId: "",
  characterId: "",
  joined: false,
  pendingInputMessageId: null,
  // oldestLoadedSequence/hasMoreOlder track the "load earlier" cursor —
  // see the History paging section below.
  oldestLoadedSequence: null,
  hasMoreOlder: false,
  // --- Reconnect (design doc §4) ---
  // hasJoinedOnce distinguishes the very first system.session_state
  // "joined" (runs onJoined: reveals the chat screen, uploads the
  // stopgap character) from every later one after an unplanned drop
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
  // --- Dice tray ---
  // rollCharacterId is Master's own store.Character.ID, assigned once the
  // stopgap upload (see uploadStockCharacter) resolves. Every message
  // that references the character mechanically — roll.check_request,
  // character.apply_effect, character.get, and narrative.player_input's
  // character_id (see onInputSubmit) — uses this, never characterId (the
  // display name typed at join). Sending the display name as
  // narrative.player_input's character_id was a real bug: the DM tool-use
  // slow pass (design doc §8) hands that value straight to the System
  // Engine, and "Kestrel" isn't a lookup key.
  rollCharacterId: null,
  pendingCharacterUploadMessageId: null,
  pendingRollMessageId: null,
  dieHandle: null,
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
  joinCampaign: document.getElementById("join-campaign"),
  joinCharacter: document.getElementById("join-character"),
  joinButton: document.getElementById("join-button"),
  joinError: document.getElementById("join-error"),
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
  diceStage: document.getElementById("dice-stage"),
  rollAbility: document.getElementById("roll-ability"),
  rollCheckButton: document.getElementById("roll-check-button"),
  diceSkinSelect: document.getElementById("dice-skin-select"),
  diceTrayResult: document.getElementById("dice-tray-result"),
  characterTabs: document.getElementById("character-tabs"),
  characterTabPanels: document.getElementById("character-tab-panels"),
  effectAmount: document.getElementById("effect-amount"),
  effectDamageButton: document.getElementById("effect-damage-button"),
  effectHealButton: document.getElementById("effect-heal-button"),
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
el.rollCheckButton.addEventListener("click", onRollCheckClick);
el.effectDamageButton.addEventListener("click", () => onApplyEffectClick("damage"));
el.effectHealButton.addEventListener("click", () => onApplyEffectClick("heal"));
el.diceSkinSelect.addEventListener("change", () => {
  Dice.applyDiceSkin(state.dieHandle, el.diceSkinSelect.value);
  Dice.saveDiceSkin(el.diceSkinSelect.value);
});
el.combatMapThumbButton.addEventListener("click", openCombatMapLightbox);
el.combatMapLightboxBackdrop.addEventListener("click", closeCombatMapLightbox);
el.combatMapLightboxClose.addEventListener("click", closeCombatMapLightbox);
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !el.combatMapLightbox.hidden) closeCombatMapLightbox();
});

// Populate the skin picker from the manifest (dice-skins.js) rather than
// hardcoding <option>s in index.html — see that file for how a community
// skin gets added.
for (const skin of Dice.listSkins()) {
  const option = document.createElement("option");
  option.value = skin.id;
  option.textContent = skin.label;
  el.diceSkinSelect.appendChild(option);
}
const savedSkin = Dice.loadSavedDiceSkin(Dice.DEFAULT_SKIN_ID);
el.diceSkinSelect.value = savedSkin;
// The die is mounted once at load — .dice-tray-stage's CSS size (110x110)
// is fixed regardless of the chat screen's visibility, so this doesn't
// need to wait for onJoined to show it.
state.dieHandle = Dice.mountDie(el.diceStage, savedSkin);

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

// randomUuid returns a proper UUID v4 string — unlike randomId() above,
// this is required to actually be UUID-shaped: it becomes CreatureState's
// Id field (see uploadStockCharacter), which the system engine
// deserializes into a real C# Guid, not just an opaque unique string.
function randomUuid() {
  if (crypto && crypto.randomUUID) return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40; // version 4
  bytes[8] = (bytes[8] & 0x3f) | 0x80; // variant 10
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0"));
  return `${hex.slice(0, 4).join("")}-${hex.slice(4, 6).join("")}-${hex.slice(6, 8).join("")}-${hex.slice(8, 10).join("")}-${hex.slice(10, 16).join("")}`;
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
  const campaign = el.joinCampaign.value.trim();
  const character = el.joinCharacter.value.trim();
  if (!url || !campaign || !character) {
    showJoinError("All fields are required.");
    return;
  }
  state.campaignId = campaign;
  state.characterId = character;
  // sender_id doubles as the player's identity for now — there's no
  // auth/account system yet (design doc §6.6's Discord OAuth isn't
  // implemented), so a client-chosen character name is all Master has
  // to identify who's who.
  state.senderId = character;
  connect(url);
}

function showJoinError(message) {
  el.joinError.textContent = message;
  el.joinError.hidden = false;
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
      payload: { client_kind: "player_web_v1" },
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
      showJoinError(`Connection closed before joining (code ${event.code}). Check the campaign ID and URL, then try again.`);
      el.joinButton.disabled = false;
      state.ws = null;
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
// already showing and the stopgap character already exists server-side
// from the original join, so redoing either would be wrong (a second
// uploadStockCharacter call, a screen flicker). All that's actually
// needed is announcing the reconnect succeeded and catching up on
// whatever was missed while disconnected.
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
        if (state.hasJoinedOnce) {
          onReconnected();
        } else {
          state.hasJoinedOnce = true;
          onJoined();
        }
      }
      break;
    case "system.error":
      onSystemError(msg);
      break;
    case "narrative.player_bubble":
      onNarrativeBubble(msg);
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
    case "roll.request":
      onRollRequest();
      break;
    case "roll.result":
      onRollResult(msg);
      break;
    case "character.schema_response":
      onCharacterSchemaResponse(msg);
      break;
    case "character.state":
      onCharacterStateResponse(msg);
      break;
    case "narrative.dm_prose":
      appendDmBubble(msg.payload ? msg.payload.text : "");
      break;
    case "tool.result":
      appendToolResultNote(msg.payload || {});
      break;
    case "turn.state":
      appendTurnStateNote(msg.payload || {});
      break;
    case "narrative.scene_image":
      appendSceneImage(msg.payload || {});
      break;
    case "map.token_state":
      onMapTokenState(msg.payload || {});
      break;
    case "audio.transcription":
      onAudioTranscription(msg);
      break;
    default:
      console.warn("unhandled message type from Master", msg.type, msg);
  }
}

function onJoined() {
  state.joined = true;
  el.joinScreen.hidden = true;
  el.chatScreen.hidden = false;
  el.chatCampaignLabel.textContent = state.campaignId;
  setStatus("connected", "connected");
  // No bounds set: Master returns the most recent page (design doc §10)
  // — "where things stand now," the natural first page for a chat-style
  // scrollback, not the campaign's very first message.
  requestHistory({});
  uploadStockCharacter();
}

function onSystemError(msg) {
  const message = (msg.payload && msg.payload.message) || "An error occurred.";
  appendErrorNote(message);
  const inReplyTo = msg.payload && msg.payload.in_reply_to_message_id;
  if (inReplyTo && inReplyTo === state.pendingInputMessageId) {
    clearPendingBubble();
  }
  if (inReplyTo && inReplyTo === state.pendingCharacterUploadMessageId) {
    state.pendingCharacterUploadMessageId = null;
    el.diceTrayResult.textContent = "Dice tray unavailable: character setup failed.";
  }
  if (inReplyTo && inReplyTo === state.pendingRollMessageId) {
    state.pendingRollMessageId = null;
    el.rollCheckButton.disabled = false;
  }
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
    case "narrative.dm_prose":
      return dmBubbleEl(raw.payload ? raw.payload.text : "");
    case "tool.result":
      return toolResultNoteEl(raw.payload || {});
    case "turn.state":
      return turnStateNoteEl(raw.payload || {});
    case "narrative.scene_image":
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
  // submitting before uploadStockCharacter's response has set it.
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
// Hold mic-button to record, release to stop; Master transcribes the
// complete recording once (no live partial preview — see
// internal/server/audio.go's own doc comment for why that's a
// deliberate scope decision, not a gap) and the result lands in
// input-text via onAudioTranscription for the player to edit before
// actually sending, same as anything typed by hand.

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

// onAudioTranscription populates input-text with the finished
// transcription so the player can edit it before sending — never sent
// automatically (design doc §4's own stated goal for this feature).
// stream_id isn't checked against state.audioStreamId: only one
// recording can be in flight from this client at a time (mic-button is
// a single hold-to-record control), so whatever transcription arrives
// is necessarily the one just requested.
function onAudioTranscription(msg) {
  const text = msg.payload && msg.payload.text;
  if (!text) return;
  el.inputText.value = text;
  state.pendingInputSource = "voice";
  el.inputText.focus();
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

// --- Dice tray ---
//
// uploadStockCharacter (see the file-level doc comment for why this is a
// stopgap) sends a minimal but real CreatureState JSON, shaped to
// OpenCombatEngine's schema (design doc §6.1) — Master forwards it
// opaquely to the system engine's FromJson without interpreting it
// itself, so this client-side shape is the one part of the whole flow
// that's genuinely system-engine-specific, unlike everything else here.
function uploadStockCharacter() {
  const characterJson = JSON.stringify({
    id: randomUuid(),
    name: state.characterId || "Adventurer",
    team: "Player",
    abilityScores: { strength: 12, dexterity: 12, constitution: 12, intelligence: 12, wisdom: 12, charisma: 12 },
    hitPoints: { current: 10, max: 10, temporary: 0 },
  });

  const envelope = newEnvelope("character.upload");
  state.pendingCharacterUploadMessageId = envelope.message_id;
  send({
    ...envelope,
    payload: { character_json: characterJson, schema_version: "opencombatengine-v1" },
  });
}

function onCharacterValidationResult(msg) {
  state.pendingCharacterUploadMessageId = null;
  const payload = msg.payload || {};
  if (!payload.character_id) {
    el.diceTrayResult.textContent = "Dice tray unavailable: character setup failed.";
    return;
  }
  state.rollCharacterId = payload.character_id;
  el.rollCheckButton.disabled = false;
  el.effectDamageButton.disabled = false;
  el.effectHealButton.disabled = false;

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
  maybeRenderCharacterSheet();
}

function maybeRenderCharacterSheet() {
  if (!state.characterSchema || !state.characterData) return;
  renderCharacterSheetTabs(el.characterTabs, el.characterTabPanels, state.characterSchema, state.characterData);
}

// onMapTokenState handles map.token_state (design doc §6.2) — a
// current-state replace, the same semantics onCharacterStateResponse
// above already has, not narrative.scene_image's append-to-history
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

// onApplyEffectClick sends character.apply_effect for a "damage" or
// "heal" effect — effectType/amount here match OpenCombatEngine's own
// GrpcSidecar ApplyEffect switch (see that repo's
// SystemEngineGrpcService.cs); a different system engine's client UI
// would send whatever effect shape that engine expects instead, since
// Master forwards this object opaquely (protocol/asyncapi.yaml
// components.messages.CharacterApplyEffect).
function onApplyEffectClick(effectType) {
  if (!state.rollCharacterId) return;
  const amount = Number(el.effectAmount.value);
  if (!Number.isFinite(amount) || amount <= 0) return;

  send({
    ...newEnvelope("character.apply_effect"),
    payload: { character_id: state.rollCharacterId, effect: { effectType, amount } },
  });
}

function onRollCheckClick() {
  if (!state.rollCharacterId || state.pendingRollMessageId) return;

  const envelope = newEnvelope("roll.check_request");
  state.pendingRollMessageId = envelope.message_id;
  el.rollCheckButton.disabled = true;
  el.diceTrayResult.textContent = "Rolling…";
  send({
    ...envelope,
    payload: { character_id: state.rollCharacterId, check_type: "ability_check", ability: el.rollAbility.value },
  });
}

function onRollRequest() {
  Dice.startTumble(state.dieHandle);
}

function onRollResult(msg) {
  state.pendingRollMessageId = null;
  el.rollCheckButton.disabled = false;

  const payload = msg.payload || {};
  const rolls = payload.rolls || [];
  const firstDie = rolls[0];

  Dice.settleOnResult(state.dieHandle, firstDie ? firstDie.result : 1, () => {
    const dieText = firstDie ? `d${firstDie.sides}: ${firstDie.result}` : "";
    el.diceTrayResult.innerHTML = "";
    const strongTotal = document.createElement("strong");
    strongTotal.textContent = `Total: ${payload.total}`;
    el.diceTrayResult.append(strongTotal, dieText ? ` (${dieText})` : "");
  });

  appendRollNote(payload);

  // Nothing mutates a character from a bare ability check yet (no
  // ApplyEffect wired to check results), but refreshing here is cheap
  // and keeps the sheet correct once something eventually does.
  requestCharacterState();
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

// dmBubbleEl is narrative.dm_prose's rendering (design doc §7's slow
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

// toolResultNoteEl renders one design doc §8 DM tool-use call as a
// transparency note (design doc §8: "every tool call/result is logged")
// — not a chat bubble, since it's bookkeeping about how the DM arrived
// at its narration, not narration itself.
function toolResultNoteEl(payload) {
  const note = document.createElement("div");
  note.className = "note tool-result-note" + (payload.success ? "" : " error-note");
  const icon = payload.success ? "🎲" : "⚠";
  const detail = payload.success ? "" : ` (${payload.reason_code || "failed"})`;
  note.textContent = `${icon} DM called ${payload.tool_name}${detail}`;
  return note;
}

function appendToolResultNote(payload) {
  el.log.appendChild(toolResultNoteEl(payload));
  el.log.scrollTop = el.log.scrollHeight;
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

// sceneImageEl renders a narrative.scene_image broadcast (design doc
// §6.3) as an inline image with its prompt as a caption — Master
// neither authors nor hosts the image itself, this just displays
// whatever URL the configured imagegen.Provider returned.
function sceneImageEl(payload) {
  const figure = document.createElement("figure");
  figure.className = "scene-image";
  const img = document.createElement("img");
  img.src = payload.image_url || "";
  img.alt = payload.prompt || "DM-generated scene illustration";
  img.loading = "lazy";
  const caption = document.createElement("figcaption");
  caption.textContent = payload.prompt || "";
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

// appendRollNote renders a roll.result broadcast — every roll in the
// campaign, not just this client's own, per the shared-dice-tray design
// (design doc §3.1, §4: every client animates every roll). Deliberately
// doesn't claim a specific ability (e.g. "Strength check") the way an
// earlier version did: that text used to come from this client's own
// roll-ability dropdown, which is simply wrong for anyone else's roll or
// a DM-triggered one (initiative, resolve_check) — RollResultPayload
// carries no ability field to report correctly instead (see
// protocol.RollResultPayload), so result_summary (if the engine set one)
// is the only characterization used, rather than guessing.
function appendRollNote(payload) {
  const note = document.createElement("div");
  note.className = "note";
  const die = (payload.rolls || [])[0];
  const dieText = die ? ` (d${die.sides}: ${die.result})` : "";
  const summary = payload.result_summary ? ` (${payload.result_summary})` : "";
  note.textContent = `🎲 ${bubbleDisplayName(payload.character_id)} rolled: ${payload.total}${dieText}${summary}`;
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
