// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/server"
	"github.com/jamesplotts/layforge/master/internal/session"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// newTestServerForCreation builds a Server wired for character-creation
// tests: a real in-memory store (characters + pregens), and whichever
// fake system engine client the test provides (nil disables it, the
// same nil-means-unconfigured pattern every optional dependency here
// uses).
func newTestServerForCreation(t *testing.T, fakeEngine *fakeSystemEngineClient) (*httptest.Server, *store.SQLiteEventStore) {
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
	ts := httptest.NewServer(server.New(logger, st, nil, "", nil, systemEngineClient, st, nil, nil, st, st, st, nil, st, session.NewHub()).Handler())
	return ts, st
}

func sendCreationStart(ctx context.Context, conn *websocket.Conn, campaignID, sender string) error {
	msg := protocol.CharacterCreationStartMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       sender + "-creation-start",
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeCharacterCreationStart,
		},
	}
	return wsjson.Write(ctx, conn, msg)
}

func sendCreationAnswer(ctx context.Context, conn *websocket.Conn, campaignID, sender, messageID, sessionID, answer string) error {
	msg := protocol.CharacterCreationAnswerMessage{
		Envelope: protocol.Envelope{
			ProtocolVersion: protocol.CurrentProtocolVersion,
			MessageID:       messageID,
			Timestamp:       time.Now().UTC(),
			SenderID:        sender,
			CampaignID:      campaignID,
			Type:            protocol.MessageTypeCharacterCreationAnswer,
		},
		Payload: protocol.CharacterCreationAnswerPayload{SessionID: sessionID, Answer: answer},
	}
	return wsjson.Write(ctx, conn, msg)
}

func TestServe_CreationStart_SendsTopLevelPromptWithFourChoices(t *testing.T) {
	ts, _ := newTestServerForCreation(t, nil)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	var prompt protocol.CharacterCreationPromptMessage
	if err := wsjson.Read(ctx, conn, &prompt); err != nil {
		t.Fatalf("Read(character.creation_prompt) error = %v", err)
	}
	if prompt.Payload.SessionID == "" {
		t.Error("Payload.SessionID is empty, want a generated id")
	}
	want := map[string]bool{"import": true, "quick_roll": true, "detailed_roll": true, "pregen": true}
	if len(prompt.Payload.Choices) != len(want) {
		t.Fatalf("Payload.Choices = %v, want exactly %v", prompt.Payload.Choices, want)
	}
	for _, c := range prompt.Payload.Choices {
		if !want[c] {
			t.Errorf("unexpected choice %q", c)
		}
	}
	if prompt.Payload.AcceptsFileUpload {
		t.Error("top-level prompt AcceptsFileUpload = true, want false — only the import sub-flow's own paste-JSON prompt should set this")
	}
}

func TestServe_CreationAnswer_UnknownSession_ReturnsSystemError(t *testing.T) {
	ts, _ := newTestServerForCreation(t, nil)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-unknown", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationAnswer(ctx, conn, "campaign-creation-unknown", "player-a", "answer-1", "no-such-session", "import"); err != nil {
		t.Fatalf("sendCreationAnswer() error = %v", err)
	}
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_CreationAnswer_WrongSender_ReturnsSystemError(t *testing.T) {
	ts, _ := newTestServerForCreation(t, nil)
	defer ts.Close()

	connA := dialAndJoin(t, ts, "campaign-creation-wrong-sender", "player-a")
	defer connA.CloseNow()
	connB := dialAndJoin(t, ts, "campaign-creation-wrong-sender", "player-b")
	defer connB.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, connA, "campaign-creation-wrong-sender", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	var prompt protocol.CharacterCreationPromptMessage
	if err := wsjson.Read(ctx, connA, &prompt); err != nil {
		t.Fatalf("Read(character.creation_prompt) error = %v", err)
	}

	// player-b tries to answer player-a's own session.
	if err := sendCreationAnswer(ctx, connB, "campaign-creation-wrong-sender", "player-b", "answer-1", prompt.Payload.SessionID, "import"); err != nil {
		t.Fatalf("sendCreationAnswer() error = %v", err)
	}
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, connB, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_CreationImport_FullFlow_SavesAndRespondsWithValidationResult(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{"name": "Kestrel"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fake := &fakeSystemEngineClient{
		fromJsonResp: &systemenginepb.FromJsonResponse{
			Actor: &systemenginepb.Actor{ActorId: "engine-actor-1", CharacterData: characterData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	ts, st := newTestServerForCreation(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-import", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation-import", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	var prompt protocol.CharacterCreationPromptMessage
	if err := wsjson.Read(ctx, conn, &prompt); err != nil {
		t.Fatalf("Read(character.creation_prompt) error = %v", err)
	}

	if err := sendCreationAnswer(ctx, conn, "campaign-creation-import", "player-a", "answer-1", prompt.Payload.SessionID, "import"); err != nil {
		t.Fatalf("sendCreationAnswer(import) error = %v", err)
	}
	var pastePrompt protocol.CharacterCreationPromptMessage
	if err := wsjson.Read(ctx, conn, &pastePrompt); err != nil {
		t.Fatalf("Read(character.creation_prompt) (paste JSON) error = %v", err)
	}
	if len(pastePrompt.Payload.Choices) != 0 {
		t.Errorf("paste-JSON prompt Choices = %v, want empty (free text)", pastePrompt.Payload.Choices)
	}
	if !pastePrompt.Payload.AcceptsFileUpload {
		t.Error("paste-JSON prompt AcceptsFileUpload = false, want true — this is where the client should offer a file picker")
	}

	if err := sendCreationAnswer(ctx, conn, "campaign-creation-import", "player-a", "answer-2", pastePrompt.Payload.SessionID, `{"name":"Kestrel"}`); err != nil {
		t.Fatalf("sendCreationAnswer(json) error = %v", err)
	}
	var validation protocol.CharacterValidationResultMessage
	if err := wsjson.Read(ctx, conn, &validation); err != nil {
		t.Fatalf("Read(character.validation_result) error = %v", err)
	}
	if validation.Payload.CharacterID == "" {
		t.Fatal("Payload.CharacterID is empty, want a generated id")
	}

	saved, err := st.GetCharacter(ctx, validation.Payload.CharacterID)
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	if saved.OwnerID != "player-a" {
		t.Errorf("saved.OwnerID = %q, want player-a", saved.OwnerID)
	}
}

func TestServe_CreationPregen_NotConfigured_ReturnsSystemError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	defer st.Close()
	// pregens deliberately left nil.
	ts := httptest.NewServer(server.New(logger, st, nil, "", nil, nil, st, nil, nil, st, st, st, nil, nil, session.NewHub()).Handler())
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-pregen-unconfigured", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation-pregen-unconfigured", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	var prompt protocol.CharacterCreationPromptMessage
	if err := wsjson.Read(ctx, conn, &prompt); err != nil {
		t.Fatalf("Read(character.creation_prompt) error = %v", err)
	}
	if err := sendCreationAnswer(ctx, conn, "campaign-creation-pregen-unconfigured", "player-a", "answer-1", prompt.Payload.SessionID, "pregen"); err != nil {
		t.Fatalf("sendCreationAnswer(pregen) error = %v", err)
	}
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_CreationPregen_FullFlow_ClaimsIndependentCharacterPerPlayer(t *testing.T) {
	ts, st := newTestServerForCreation(t, nil)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := st.SavePregen(ctx, store.Pregen{
		ID:            "bram-fighter",
		CampaignID:    "campaign-creation-pregen",
		Name:          "Bram the Bold",
		Description:   "A stalwart level-1 fighter.",
		SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Bram"}`),
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SavePregen() error = %v", err)
	}

	claim := func(sender string) string {
		conn := dialAndJoin(t, ts, "campaign-creation-pregen", sender)
		defer conn.CloseNow()

		if err := sendCreationStart(ctx, conn, "campaign-creation-pregen", sender); err != nil {
			t.Fatalf("sendCreationStart() error = %v", err)
		}
		var prompt protocol.CharacterCreationPromptMessage
		if err := wsjson.Read(ctx, conn, &prompt); err != nil {
			t.Fatalf("Read(character.creation_prompt) error = %v", err)
		}
		if err := sendCreationAnswer(ctx, conn, "campaign-creation-pregen", sender, "answer-1", prompt.Payload.SessionID, "pregen"); err != nil {
			t.Fatalf("sendCreationAnswer(pregen) error = %v", err)
		}
		var pregenPrompt protocol.CharacterCreationPromptMessage
		if err := wsjson.Read(ctx, conn, &pregenPrompt); err != nil {
			t.Fatalf("Read(character.creation_prompt) (pregen list) error = %v", err)
		}
		if len(pregenPrompt.Payload.Choices) != 1 || pregenPrompt.Payload.Choices[0] != "bram-fighter" {
			t.Fatalf("pregen list Choices = %v, want [bram-fighter]", pregenPrompt.Payload.Choices)
		}
		if err := sendCreationAnswer(ctx, conn, "campaign-creation-pregen", sender, "answer-2", pregenPrompt.Payload.SessionID, "bram-fighter"); err != nil {
			t.Fatalf("sendCreationAnswer(bram-fighter) error = %v", err)
		}
		var validation protocol.CharacterValidationResultMessage
		if err := wsjson.Read(ctx, conn, &validation); err != nil {
			t.Fatalf("Read(character.validation_result) error = %v", err)
		}
		if validation.Payload.CharacterID == "" {
			t.Fatal("Payload.CharacterID is empty, want a generated id")
		}
		return validation.Payload.CharacterID
	}

	idA := claim("player-a")
	idB := claim("player-b")
	if idA == idB {
		t.Fatalf("both players claimed the same character ID %q, want two independent characters", idA)
	}

	charA, err := st.GetCharacter(ctx, idA)
	if err != nil {
		t.Fatalf("GetCharacter(a) error = %v", err)
	}
	charB, err := st.GetCharacter(ctx, idB)
	if err != nil {
		t.Fatalf("GetCharacter(b) error = %v", err)
	}
	if charA.OwnerID != "player-a" {
		t.Errorf("charA.OwnerID = %q, want player-a", charA.OwnerID)
	}
	if charB.OwnerID != "player-b" {
		t.Errorf("charB.OwnerID = %q, want player-b", charB.OwnerID)
	}
	if charA.Status != store.CharacterStatusApproved || charB.Status != store.CharacterStatusApproved {
		t.Errorf("claimed pregens should be Approved (pre-vetted by the Host): a=%q b=%q", charA.Status, charB.Status)
	}

	// The template row itself must be untouched by either claim.
	pregen, err := st.GetPregen(ctx, "bram-fighter")
	if err != nil {
		t.Fatalf("GetPregen() error = %v", err)
	}
	if pregen.Name != "Bram the Bold" {
		t.Errorf("template pregen was mutated: Name = %q", pregen.Name)
	}
}

func TestServe_CreationRoll_NoSystemEngine_ReturnsSystemError(t *testing.T) {
	ts, _ := newTestServerForCreation(t, nil)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-roll-noengine", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation-roll-noengine", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	var prompt protocol.CharacterCreationPromptMessage
	if err := wsjson.Read(ctx, conn, &prompt); err != nil {
		t.Fatalf("Read(character.creation_prompt) error = %v", err)
	}
	if err := sendCreationAnswer(ctx, conn, "campaign-creation-roll-noengine", "player-a", "answer-1", prompt.Payload.SessionID, "quick_roll"); err != nil {
		t.Fatalf("sendCreationAnswer(quick_roll) error = %v", err)
	}
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_CreationRoll_RelaysEnginePromptsUntilDoneAndSavesCharacter(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{"name": "player-a", "gender": "nonbinary"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fake := &fakeSystemEngineClient{
		startCharacterCreationFunc: func(req *systemenginepb.StartCharacterCreationRequest) (*systemenginepb.CharacterCreationPromptResponse, error) {
			if req.Mode != systemenginepb.CharacterCreationMode_CHARACTER_CREATION_MODE_DETAILED {
				t.Errorf("StartCharacterCreation Mode = %v, want DETAILED", req.Mode)
			}
			if req.CharacterName != "player-a" {
				t.Errorf("StartCharacterCreation CharacterName = %q, want player-a (the sender's own display name)", req.CharacterName)
			}
			return &systemenginepb.CharacterCreationPromptResponse{
				Success: true, PromptText: "Choose your race.", Choices: []string{"Human", "Elf", "Dwarf", "Halfling"},
			}, nil
		},
		answerCharacterCreationPromptFunc: func(req *systemenginepb.AnswerCharacterCreationPromptRequest) (*systemenginepb.CharacterCreationPromptResponse, error) {
			if req.Answer == "Human" {
				return &systemenginepb.CharacterCreationPromptResponse{
					Success: true, PromptText: "Choose your class.", Choices: []string{"Fighter", "Wizard", "Cleric", "Rogue"},
				}, nil
			}
			return &systemenginepb.CharacterCreationPromptResponse{
				Success: true, Done: true,
				Actor: &systemenginepb.Actor{ActorId: "engine-actor-1", CharacterData: characterData, SchemaVersion: "opencombatengine-v1"},
			}, nil
		},
	}
	ts, st := newTestServerForCreation(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-roll", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation-roll", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	var topPrompt protocol.CharacterCreationPromptMessage
	if err := wsjson.Read(ctx, conn, &topPrompt); err != nil {
		t.Fatalf("Read(character.creation_prompt) (top-level) error = %v", err)
	}
	if err := sendCreationAnswer(ctx, conn, "campaign-creation-roll", "player-a", "answer-1", topPrompt.Payload.SessionID, "detailed_roll"); err != nil {
		t.Fatalf("sendCreationAnswer(detailed_roll) error = %v", err)
	}
	var racePrompt protocol.CharacterCreationPromptMessage
	if err := wsjson.Read(ctx, conn, &racePrompt); err != nil {
		t.Fatalf("Read(character.creation_prompt) (race) error = %v", err)
	}
	if racePrompt.Payload.PromptText != "Choose your race." {
		t.Errorf("PromptText = %q, want %q", racePrompt.Payload.PromptText, "Choose your race.")
	}
	if err := sendCreationAnswer(ctx, conn, "campaign-creation-roll", "player-a", "answer-2", racePrompt.Payload.SessionID, "Human"); err != nil {
		t.Fatalf("sendCreationAnswer(Human) error = %v", err)
	}
	var classPrompt protocol.CharacterCreationPromptMessage
	if err := wsjson.Read(ctx, conn, &classPrompt); err != nil {
		t.Fatalf("Read(character.creation_prompt) (class) error = %v", err)
	}
	if err := sendCreationAnswer(ctx, conn, "campaign-creation-roll", "player-a", "answer-3", classPrompt.Payload.SessionID, "Fighter"); err != nil {
		t.Fatalf("sendCreationAnswer(Fighter) error = %v", err)
	}
	var validation protocol.CharacterValidationResultMessage
	if err := wsjson.Read(ctx, conn, &validation); err != nil {
		t.Fatalf("Read(character.validation_result) error = %v", err)
	}
	if validation.Payload.CharacterID == "" {
		t.Fatal("Payload.CharacterID is empty, want a generated id")
	}

	saved, err := st.GetCharacter(ctx, validation.Payload.CharacterID)
	if err != nil {
		t.Fatalf("GetCharacter() error = %v", err)
	}
	if saved.OwnerID != "player-a" {
		t.Errorf("saved.OwnerID = %q, want player-a", saved.OwnerID)
	}
	if saved.Status != store.CharacterStatusApproved {
		t.Errorf("saved.Status = %q, want Approved (engine-generated, nothing for a human reviewer to check)", saved.Status)
	}
}

func TestServe_CreationRoll_EngineReportsFailure_ReturnsSystemError(t *testing.T) {
	fake := &fakeSystemEngineClient{
		startCharacterCreationResp: &systemenginepb.CharacterCreationPromptResponse{Success: false, Error: "no races configured"},
	}
	ts, _ := newTestServerForCreation(t, fake)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-roll-fail", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation-roll-fail", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	var prompt protocol.CharacterCreationPromptMessage
	if err := wsjson.Read(ctx, conn, &prompt); err != nil {
		t.Fatalf("Read(character.creation_prompt) error = %v", err)
	}
	if err := sendCreationAnswer(ctx, conn, "campaign-creation-roll-fail", "player-a", "answer-1", prompt.Payload.SessionID, "quick_roll"); err != nil {
		t.Fatalf("sendCreationAnswer(quick_roll) error = %v", err)
	}
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_CreationTopLevel_InvalidAnswer_ReturnsSystemError(t *testing.T) {
	ts, _ := newTestServerForCreation(t, nil)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-invalid", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation-invalid", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	var prompt protocol.CharacterCreationPromptMessage
	if err := wsjson.Read(ctx, conn, &prompt); err != nil {
		t.Fatalf("Read(character.creation_prompt) error = %v", err)
	}
	if err := sendCreationAnswer(ctx, conn, "campaign-creation-invalid", "player-a", "answer-1", prompt.Payload.SessionID, "not-a-real-choice"); err != nil {
		t.Fatalf("sendCreationAnswer() error = %v", err)
	}
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}
