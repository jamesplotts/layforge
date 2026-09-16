// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// connectAndReadSessionState dials ts and completes the handshake for
// campaignID/sender, returning the joined system.session_state's own
// payload (not just the open connection dialAndJoin hands back) — this
// file's tests all assert on ExistingCharacterID, which dialAndJoin's
// return value doesn't expose.
func connectAndReadSessionState(t *testing.T, ts *httptest.Server, campaignID, sender string) (*websocket.Conn, protocol.SystemSessionStatePayload) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(ts.URL), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	connect := protocol.SystemConnectMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       sender + "-connect",
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeSystemConnect,
		},
		Payload: protocol.SystemConnectPayload{ClientKind: "test_client"},
	}
	if err := wsjson.Write(ctx, conn, connect); err != nil {
		t.Fatalf("Write(connect) error = %v", err)
	}

	var joined protocol.SystemSessionStateMessage
	if err := wsjson.Read(ctx, conn, &joined); err != nil {
		t.Fatalf("Read(session_state) error = %v", err)
	}
	if joined.Payload.State != protocol.SessionStateJoined {
		t.Fatalf("Payload.State = %q, want %q", joined.Payload.State, protocol.SessionStateJoined)
	}
	return conn, joined.Payload
}

// TestServe_Join_ExistingCharacterStatus_DeterminesSessionStateResumeHint
// is the regression test for a live-reported bug: a browser back-button
// navigation to the join screen, followed by logging back in, restarted
// character creation from scratch even though the account already had a
// finished character — the client had no way to tell "I already have
// one" apart from "I'm a new player" on its own, since a fresh page load
// looks identical either way. Master now resolves this once, at join
// time (findOwnedCharacter, character_creation.go), and echoes the
// answer on the very system.session_state that triggers the client's
// join-vs-reconnect branching (ExistingCharacterID).
func TestServe_Join_ExistingCharacterStatus_DeterminesSessionStateResumeHint(t *testing.T) {
	tests := []struct {
		name           string
		seedStatus     store.CharacterStatus // "" means seed no character at all
		wantResumeHint bool
	}{
		{name: "NoCharacterYet_NoResumeHint_GenuinelyNewPlayer", seedStatus: "", wantResumeHint: false},
		{name: "Approved_ResumesIt", seedStatus: store.CharacterStatusApproved, wantResumeHint: true},
		{name: "PendingReview_StillResumesIt_NotARestart", seedStatus: store.CharacterStatusPendingReview, wantResumeHint: true},
		{name: "Rejected_NoResumeHint_LetsThemTryAgain", seedStatus: store.CharacterStatusRejected, wantResumeHint: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, st := newTestServerForCreation(t, &fakeSystemEngineClient{})
			defer ts.Close()

			campaignID := "campaign-rejoin-" + tt.name
			var seededID string
			if tt.seedStatus != "" {
				seededID = "char-" + tt.name
				seedCharacterWithStatus(t, st, seededID, campaignID, "player-a", tt.seedStatus)
			}

			conn, payload := connectAndReadSessionState(t, ts, campaignID, "player-a")
			defer conn.CloseNow()

			if tt.wantResumeHint {
				if payload.ExistingCharacterID != seededID {
					t.Errorf("Payload.ExistingCharacterID = %q, want %q", payload.ExistingCharacterID, seededID)
				}
			} else if payload.ExistingCharacterID != "" {
				t.Errorf("Payload.ExistingCharacterID = %q, want empty", payload.ExistingCharacterID)
			}
		})
	}
}

// TestServe_Join_ExistingCharacter_IsolatedPerOwner confirms
// findOwnedCharacter never leaks one player's character to a different
// sender_id/account joining the same campaign — the resume hint is keyed
// on ownership, not merely "does this campaign have any character."
func TestServe_Join_ExistingCharacter_IsolatedPerOwner(t *testing.T) {
	ts, st := newTestServerForCreation(t, &fakeSystemEngineClient{})
	defer ts.Close()

	seedCharacterWithStatus(t, st, "char-a", "campaign-rejoin-isolation", "player-a", store.CharacterStatusApproved)

	conn, payload := connectAndReadSessionState(t, ts, "campaign-rejoin-isolation", "player-b")
	defer conn.CloseNow()

	if payload.ExistingCharacterID != "" {
		t.Errorf("Payload.ExistingCharacterID = %q, want empty — player-a's character must not resolve for player-b", payload.ExistingCharacterID)
	}
}

// TestServe_CreationImport_ThenRejoin_ResumesTheSameCharacterEndToEnd is
// the full end-to-end version of the bug report: a real character.upload
// import flow to completion, then a brand-new connection under the same
// sender — exactly what a browser back-button navigation followed by
// logging back in produces — asserting the reconnect's own
// system.session_state already carries the character this sender just
// finished, proving the whole join -> creation -> disconnect -> rejoin
// path, not just findOwnedCharacter in isolation.
func TestServe_CreationImport_ThenRejoin_ResumesTheSameCharacterEndToEnd(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fake := &fakeSystemEngineClient{
		fromJsonResp: &systemenginepb.FromJsonResponse{
			Actor: &systemenginepb.Actor{ActorId: "engine-actor-1", CharacterData: characterData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	ts, _ := newTestServerForCreation(t, fake)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := dialAndJoin(t, ts, "campaign-rejoin-e2e", "player-a")
	if err := sendCreationStart(ctx, conn, "campaign-rejoin-e2e", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	topPrompt := readCreationPrompt(t, ctx, conn)
	answerCreationPrompt(t, ctx, conn, "campaign-rejoin-e2e", "player-a", topPrompt, "import")
	pastePrompt := readCreationPrompt(t, ctx, conn)
	answerCreationPrompt(t, ctx, conn, "campaign-rejoin-e2e", "player-a", pastePrompt, `{"name":"Kestrel"}`)

	var validation protocol.CharacterValidationResultMessage
	if err := wsjson.Read(ctx, conn, &validation); err != nil {
		t.Fatalf("Read(character.validation_result) error = %v", err)
	}
	if validation.Payload.CharacterID == "" {
		t.Fatal("Payload.CharacterID is empty, want a generated id")
	}
	conn.CloseNow()

	rejoin, payload := connectAndReadSessionState(t, ts, "campaign-rejoin-e2e", "player-a")
	defer rejoin.CloseNow()

	if payload.ExistingCharacterID != validation.Payload.CharacterID {
		t.Errorf("rejoin Payload.ExistingCharacterID = %q, want %q (the character player-a already finished creating)", payload.ExistingCharacterID, validation.Payload.CharacterID)
	}
}
