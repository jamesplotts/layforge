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

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/server"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/terms"
)

// newTermsGateTestServer builds a Server with a real in-memory SQLite
// store wired as adminSettings — everything else nil, since this test
// file is only concerned with dispatch's terms gate (terms.go), not any
// particular feature it might otherwise block.
func newTermsGateTestServer(t *testing.T) (*httptest.Server, *store.SQLiteEventStore) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := server.New(logger, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, st, nil, session.NewHub())
	return httptest.NewServer(srv.Handler()), st
}

func acceptOperatorTerms(t *testing.T, st *store.SQLiteEventStore) {
	t.Helper()
	if err := st.SaveSystemSettings(context.Background(), map[string]string{
		"terms_accepted_version": terms.Version,
	}); err != nil {
		t.Fatalf("SaveSystemSettings() error = %v", err)
	}
}

func sendTermsAccept(ctx context.Context, conn *websocket.Conn, campaignID, sender, version string) error {
	msg := protocol.TermsAcceptMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "terms-" + sender,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeTermsAccept,
		},
		Payload: protocol.TermsAcceptPayload{Version: version},
	}
	return wsjson.Write(ctx, conn, msg)
}

func sendSafetyFlag(ctx context.Context, conn *websocket.Conn, campaignID, sender string) error {
	msg := protocol.SafetyFlagMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       "flag-" + sender,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeSafetyFlag,
		},
		Payload: protocol.SafetyFlagPayload{Topic: "pause"},
	}
	return wsjson.Write(ctx, conn, msg)
}

func TestDispatch_TermsGate_OperatorNotAccepted_BlocksRealMessages(t *testing.T) {
	ts, _ := newTermsGateTestServer(t)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-terms", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendSafetyFlag(ctx, conn, "campaign-terms", "player-a"); err != nil {
		t.Fatalf("sendSafetyFlag() error = %v", err)
	}
	var got protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if got.Payload.Code != "operator_terms_not_accepted" {
		t.Errorf("Code = %q, want operator_terms_not_accepted (payload: %+v)", got.Payload.Code, got.Payload)
	}
}

func TestDispatch_TermsGate_TermsAccept_AllowedEvenBeforeOperatorAccepts(t *testing.T) {
	ts, _ := newTermsGateTestServer(t)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-terms", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendTermsAccept(ctx, conn, "campaign-terms", "player-a", terms.Version); err != nil {
		t.Fatalf("sendTermsAccept() error = %v", err)
	}
	// terms.accept itself never gets a reply either way (success is the
	// absence of an error) — prove that by sending a real message next
	// and confirming its rejection is the OPERATOR reason, not some
	// leftover response to terms.accept.
	if err := sendSafetyFlag(ctx, conn, "campaign-terms", "player-a"); err != nil {
		t.Fatalf("sendSafetyFlag() error = %v", err)
	}
	var got protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if got.Payload.Code != "operator_terms_not_accepted" {
		t.Errorf("Code = %q, want operator_terms_not_accepted (the player's own acceptance shouldn't matter here)", got.Payload.Code)
	}
}

func TestDispatch_TermsGate_OperatorAccepted_PlayerNotYet_Blocked(t *testing.T) {
	ts, st := newTermsGateTestServer(t)
	defer ts.Close()
	acceptOperatorTerms(t, st)

	conn := dialAndJoin(t, ts, "campaign-terms", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendSafetyFlag(ctx, conn, "campaign-terms", "player-a"); err != nil {
		t.Fatalf("sendSafetyFlag() error = %v", err)
	}
	var got protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if got.Payload.Code != "player_terms_not_accepted" {
		t.Errorf("Code = %q, want player_terms_not_accepted (payload: %+v)", got.Payload.Code, got.Payload)
	}
}

func TestDispatch_TermsGate_BothAccepted_MessageProcessedNormally(t *testing.T) {
	ts, st := newTermsGateTestServer(t)
	defer ts.Close()
	acceptOperatorTerms(t, st)

	conn := dialAndJoin(t, ts, "campaign-terms", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendTermsAccept(ctx, conn, "campaign-terms", "player-a", terms.Version); err != nil {
		t.Fatalf("sendTermsAccept() error = %v", err)
	}
	if err := sendSafetyFlag(ctx, conn, "campaign-terms", "player-a"); err != nil {
		t.Fatalf("sendSafetyFlag() error = %v", err)
	}
	var got protocol.SafetyFlagBroadcastMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(safety.flag_broadcast) error = %v (want the real broadcast, not a terms rejection)", err)
	}
	if got.Payload.Topic != "pause" {
		t.Errorf("Payload.Topic = %q, want pause", got.Payload.Topic)
	}
}

func TestDispatch_TermsGate_TermsAcceptWithStaleVersion_ReturnsMismatchAndDoesNotSetFlag(t *testing.T) {
	ts, st := newTermsGateTestServer(t)
	defer ts.Close()
	acceptOperatorTerms(t, st)

	conn := dialAndJoin(t, ts, "campaign-terms", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendTermsAccept(ctx, conn, "campaign-terms", "player-a", "stale-version"); err != nil {
		t.Fatalf("sendTermsAccept() error = %v", err)
	}
	var mismatch protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &mismatch); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if mismatch.Payload.Code != "terms_version_mismatch" {
		t.Errorf("Code = %q, want terms_version_mismatch", mismatch.Payload.Code)
	}

	// The stale accept must not have set the connection's flag.
	if err := sendSafetyFlag(ctx, conn, "campaign-terms", "player-a"); err != nil {
		t.Fatalf("sendSafetyFlag() error = %v", err)
	}
	var got protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if got.Payload.Code != "player_terms_not_accepted" {
		t.Errorf("Code = %q, want player_terms_not_accepted (a stale-version accept must not count)", got.Payload.Code)
	}
}
