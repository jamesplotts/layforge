// Copyright (c) 2026 James Duane Plotts. Licensed under the MIT License.
// See LICENSE in the repository root.
//
// Minimal V1 web client (design doc §4). Hand-written, not generated —
// design doc §6 envisions a proper JS reference SDK against
// protocol/asyncapi.yaml eventually; this predates that and must be kept
// in sync with the protocol by hand in the meantime (see PROTOCOL_VERSION
// below). Only what Master actually implements is wired up: the
// handshake, narrative.player_input -> narrative.player_bubble,
// safety.flag -> safety.flag_broadcast, and log.history_request paging.
// No stat panel, dice tray, or push-to-talk — Master has no system
// engine or audio pipeline yet for those to talk to.

"use strict";

const PROTOCOL_VERSION = "0.1.0";

const state = {
  ws: null,
  campaignId: "",
  senderId: "",
  characterId: "",
  joined: false,
  pendingInputMessageId: null,
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
  safetyFlagButton: document.getElementById("safety-flag-button"),
  safetyFlagPanel: document.getElementById("safety-flag-panel"),
  safetyFlagTopic: document.getElementById("safety-flag-topic"),
  safetyFlagCancel: document.getElementById("safety-flag-cancel"),
  safetyFlagSend: document.getElementById("safety-flag-send"),
  inputForm: document.getElementById("input-form"),
  inputText: document.getElementById("input-text"),
  inputSend: document.getElementById("input-send"),
};

el.joinUrl.value = defaultWsUrl();
el.joinButton.addEventListener("click", onJoinClick);
el.safetyFlagButton.addEventListener("click", openSafetyFlagPanel);
el.safetyFlagCancel.addEventListener("click", closeSafetyFlagPanel);
el.safetyFlagSend.addEventListener("click", onSafetyFlagSend);
el.inputForm.addEventListener("submit", onInputSubmit);

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

  let ws;
  try {
    ws = new WebSocket(url);
  } catch (err) {
    showJoinError("Could not open WebSocket: " + err.message);
    el.joinButton.disabled = false;
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
    setStatus("disconnected", "error");
  });

  ws.addEventListener("error", () => {
    if (!state.joined) {
      showJoinError("Could not reach that WebSocket URL.");
      el.joinButton.disabled = false;
    }
  });
}

function setStatus(text, cssClass) {
  el.chatStatus.textContent = text;
  el.chatStatus.className = "status " + (cssClass || "");
}

// --- Inbound message routing ---

function handleMessage(msg) {
  switch (msg.type) {
    case "system.session_state":
      if (msg.payload && msg.payload.state === "joined") onJoined();
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
  requestHistory(0);
}

function onSystemError(msg) {
  const message = (msg.payload && msg.payload.message) || "An error occurred.";
  appendErrorNote(message);
  const inReplyTo = msg.payload && msg.payload.in_reply_to_message_id;
  if (inReplyTo && inReplyTo === state.pendingInputMessageId) {
    clearPendingBubble();
  }
}

function onNarrativeBubble(msg) {
  const payload = msg.payload || {};
  if (payload.character_id === state.characterId) {
    clearPendingBubble();
  }
  appendBubble(payload.character_id, payload.text);
}

// --- History paging ---
//
// log.history_request only supports forward pagination (after_sequence),
// not "give me the most recent N" or "load earlier" — see
// docs/design.md §10 and protocol/asyncapi.yaml. So on join this walks
// every page from the beginning automatically rather than offering a
// "load more" control whose direction (older vs. newer) would be
// confusing given what the backend actually supports.
function requestHistory(afterSequence) {
  send({
    ...newEnvelope("log.history_request"),
    payload: { after_sequence: afterSequence || 0, limit: 50 },
  });
}

function onHistoryResponse(payload) {
  const events = (payload && payload.events) || [];
  for (const raw of events) renderHistoryEvent(raw);

  if (payload && payload.has_more) {
    requestHistory(payload.next_after_sequence);
  } else {
    el.log.scrollTop = el.log.scrollHeight;
  }
}

function renderHistoryEvent(raw) {
  switch (raw.type) {
    case "narrative.player_bubble":
      appendBubble(raw.payload.character_id, raw.payload.text);
      break;
    case "safety.flag_broadcast":
      appendSafetyBanner(raw.payload ? raw.payload.topic : "");
      break;
    default:
      // system.connect / system.session_state / narrative.player_input /
      // safety.flag / log.history_* are handshake and audit-trail
      // entries, not things a player wants in their scrollback —
      // skipped deliberately, not an oversight.
      break;
  }
}

// --- Sending player input ---

function onInputSubmit(event) {
  event.preventDefault();
  const text = el.inputText.value.trim();
  if (!text) return;

  const envelope = newEnvelope("narrative.player_input");
  state.pendingInputMessageId = envelope.message_id;
  send({
    ...envelope,
    payload: { character_id: state.characterId, text, source: "typed" },
  });

  el.inputText.value = "";
  showPendingBubble();
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

// --- Rendering ---

function appendBubble(characterId, text) {
  const bubble = document.createElement("div");
  bubble.className = "bubble";

  const who = document.createElement("span");
  who.className = "who";
  who.textContent = characterId || "DM";

  const body = document.createElement("span");
  body.className = "text";
  body.textContent = text;

  bubble.append(who, body);
  el.log.appendChild(bubble);
  el.log.scrollTop = el.log.scrollHeight;
}

function appendSafetyBanner(topic) {
  const banner = document.createElement("div");
  banner.className = "safety-banner";
  banner.textContent = topic ? `⚑ Safety flag raised — topic: ${topic}` : "⚑ Safety flag raised";
  el.log.appendChild(banner);
  el.log.scrollTop = el.log.scrollHeight;
}

function appendErrorNote(text) {
  const note = document.createElement("div");
  note.className = "note error-note";
  note.textContent = text;
  el.log.appendChild(note);
  el.log.scrollTop = el.log.scrollHeight;
}

function showPendingBubble() {
  clearPendingBubble();
  const bubble = document.createElement("div");
  bubble.className = "bubble pending";
  bubble.id = "pending-bubble";
  const who = document.createElement("span");
  who.className = "who";
  who.textContent = "DM";
  const body = document.createElement("span");
  body.className = "text";
  body.textContent = "…";
  bubble.append(who, body);
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
