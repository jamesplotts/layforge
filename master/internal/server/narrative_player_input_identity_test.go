// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/auth"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/server"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// fixedIdentityAuthProvider is a minimal auth.Provider test double: it
// authorizes every join and always asserts the same fixed account
// identity, regardless of what credentials the client actually presents.
// It exists to exercise the authenticated-connection path
// (connState.identity.Authenticated()) that dialAndJoin's plain,
// unauthenticated test connections never reach — see actingSender's own
// doc comment (terms.go) for why that path matters: an authenticated
// connection must act as its verified account, never as a client-claimed
// sender_id.
type fixedIdentityAuthProvider struct {
	accountID string
}

func (p fixedIdentityAuthProvider) Authorize(_ context.Context, _ string, _ auth.Credentials) (auth.Result, error) {
	return auth.Result{OK: true, Identity: auth.Identity{AccountID: p.accountID, DisplayName: "Test Player"}}, nil
}

// newTestServerWithLLMSystemEngineAndAuth is newTestServerWithLLMAndSystemEngine's
// authenticated-connection counterpart: identical wiring, but with
// authProvider plugged in so a handshake actually populates
// connState.identity instead of leaving it zero.
func newTestServerWithLLMSystemEngineAndAuth(t *testing.T, llmProvider *fakeLLMProvider, fakeEngine *fakeSystemEngineClient, authProvider auth.Provider) (*httptest.Server, *store.SQLiteEventStore) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var systemEngineClient systemenginepb.SystemEngineClient
	if fakeEngine != nil {
		systemEngineClient = fakeEngine
	}
	ts := httptest.NewServer(server.New(logger, st, llmProvider, "test-model", authProvider, systemEngineClient, st, nil, nil, st, st, st, nil, nil, nil, nil, session.NewHub()).Handler())
	return ts, st
}

// dialAndJoinAuthenticated is dialAndJoin's authenticated-connection
// counterpart: the returned connection's server-side connState carries a
// real auth.Identity (asserted by authProvider), rather than the zero
// Identity every dialAndJoin connection gets. declaredSenderID is the
// client's own, unverified sender_id claim on the handshake — deliberately
// allowed to differ from the account authProvider actually asserts, the
// same way a forged or simply stale locally-typed sender_id would in
// production.
func dialAndJoinAuthenticated(t *testing.T, ts *httptest.Server, campaignID, declaredSenderID string) *websocket.Conn {
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
			MessageID:       declaredSenderID + "-connect",
			Timestamp:       time.Now().UTC(),
			SenderID:        declaredSenderID,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeSystemConnect,
		},
		// AuthToken's actual value doesn't matter to fixedIdentityAuthProvider
		// (it authorizes unconditionally), but a non-empty token here
		// mirrors what a real Discord-authenticated client sends.
		Payload: protocol.SystemConnectPayload{ClientKind: "test_client", AuthToken: "test-token"},
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
	return conn
}

// TestServe_NarrativePlayerInput_SlowPass_SpendCurrency_AuthenticatedConnection_SelfOwnedNotGatedByForgedSenderID
// is a regression test for a real, live-observed bug: server.go's
// narrative.player_input dispatch case used the raw envelope.SenderID
// directly instead of resolving it through actingSender(cs, ...) first
// (every sibling case in that dispatch switch already did). For an
// authenticated (Discord-logged-in) connection whose client-declared
// sender_id doesn't happen to equal its own account id — the normal case,
// since the web client's sender_id is just a locally-typed value, not the
// authenticated account — pvpGateBlocked's "source.OwnerID ==
// actingSenderID" self-owned exemption could never match, so a player
// spending or giving away their OWN character's own gold was wrongly
// rejected as PvP-blocked every time.
//
// Confirmed live via /tmp/layforge-master.log:
//
//	reason_code=pvp_blocked tool=spend_currency
//	arguments="{\"character_id\":\"...\",\"gold\":3}"
//
// for a player narrating "Rog gives Sister Miriam three gold" — spending
// Rog's own gold, with no other player involved at all.
func TestServe_NarrativePlayerInput_SlowPass_SpendCurrency_AuthenticatedConnection_SelfOwnedNotGatedByForgedSenderID(t *testing.T) {
	actorData, err := structpb.NewStruct(map[string]any{"name": "Rog"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		// pvpGateBlocked's non-exempt branch calls characterIsDead, which
		// calls GetCharacterStatus — populated so that branch, if the fix
		// regresses and this test ends up exercising it, reports a live
		// (not dead) character rather than dereferencing a nil response.
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
		removeCurrencyResp: &systemenginepb.RemoveCurrencyResponse{
			Success: true, Actor: &systemenginepb.Actor{ActorId: "char-rog", CharacterData: actorData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := toolCallLLM("spend_currency", `{"character_id":"char-rog","gold":3}`)

	authProvider := fixedIdentityAuthProvider{accountID: "discord:199042467872374784"}
	ts, st := newTestServerWithLLMSystemEngineAndAuth(t, fakeLLM, fakeEngine, authProvider)
	defer ts.Close()

	// The character is owned by the authenticated Discord account — not
	// by whatever raw sender_id string the client happens to send on the
	// handshake or on individual messages.
	seedCharacter(t, st, "char-rog", "campaign-spend-auth", authProvider.accountID)

	// The client's own declared sender_id deliberately does NOT match the
	// authenticated account, reproducing the live report: the web
	// client's sender_id is just a locally-typed value, never the
	// Discord account id.
	const declaredSenderID = "some-locally-typed-value"
	conn := dialAndJoinAuthenticated(t, ts, "campaign-spend-auth", declaredSenderID)
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-spend-auth", declaredSenderID, "char-rog", "Rog gives Sister Miriam three gold."); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}
	var toolResult protocol.ToolResultMessage
	if err := wsjson.Read(ctx, conn, &toolResult); err != nil {
		t.Fatalf("Read(tool.result) error = %v", err)
	}
	if !toolResult.Payload.Success {
		t.Fatalf("tool.result Success = false, reason_code=%q, want true — spending one's own currency must not be PvP-gated just because the client's declared sender_id differs from the authenticated account", toolResult.Payload.ReasonCode)
	}
	if fakeEngine.lastRemoveCurrencyRequest == nil || fakeEngine.lastRemoveCurrencyRequest.Gold != 3 {
		t.Errorf("RemoveCurrency called with = %+v, want Gold=3", fakeEngine.lastRemoveCurrencyRequest)
	}
}

