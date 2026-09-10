// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
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
	ts := httptest.NewServer(server.New(logger, st, nil, "", nil, systemEngineClient, st, nil, nil, st, st, st, nil, st, nil, nil, session.NewHub()).Handler())
	return ts, st
}

func sendCreationStart(ctx context.Context, conn *websocket.Conn, campaignID, sender string) error {
	return sendCreationStartNamed(ctx, conn, campaignID, sender, "")
}

func sendCreationStartNamed(ctx context.Context, conn *websocket.Conn, campaignID, sender, characterName string) error {
	msg := protocol.CharacterCreationStartMessage{
		Envelope: creationEnv(protocol.MessageTypeCharacterCreationStart, sender+"-creation-start", sender, campaignID),
		Payload:  protocol.CharacterCreationStartPayload{CharacterName: characterName},
	}
	return wsjson.Write(ctx, conn, msg)
}

func creationEnv(msgType protocol.MessageType, messageID, sender, campaignID string) protocol.Envelope {
	return protocol.Envelope{
		ProtocolVersion: protocol.CurrentProtocolVersion,
		MessageID:       messageID,
		Timestamp:       time.Now().UTC(),
		SenderID:        sender,
		CampaignID:      campaignID,
		Type:            msgType,
	}
}

// creationPrompt is the next question the creation flow sent — a
// client.query (free text) or a client.choice (buttons) — normalized so
// the tests that walk the conversation don't care which.
type creationPrompt struct {
	promptID          string
	text              string
	choiceValues      []string
	isChoice          bool
	acceptsFileUpload bool
}

// readCreationPrompt reads the next frame on conn and asserts it is a
// client.query or client.choice, returning its normalized form.
func readCreationPrompt(t *testing.T, ctx context.Context, conn *websocket.Conn) creationPrompt {
	t.Helper()
	var raw struct {
		Type    protocol.MessageType `json:"type"`
		Payload json.RawMessage      `json:"payload"`
	}
	if err := wsjson.Read(ctx, conn, &raw); err != nil {
		t.Fatalf("Read(creation prompt) error = %v", err)
	}
	switch raw.Type {
	case protocol.MessageTypeClientQuery:
		var p protocol.ClientQueryPayload
		if err := json.Unmarshal(raw.Payload, &p); err != nil {
			t.Fatalf("Unmarshal(client.query payload) error = %v", err)
		}
		return creationPrompt{promptID: p.PromptID, text: p.PromptText, acceptsFileUpload: p.AcceptsFileUpload}
	case protocol.MessageTypeClientChoice:
		var p protocol.ClientChoicePayload
		if err := json.Unmarshal(raw.Payload, &p); err != nil {
			t.Fatalf("Unmarshal(client.choice payload) error = %v", err)
		}
		values := make([]string, len(p.Options))
		for i, o := range p.Options {
			values[i] = o.Value
		}
		return creationPrompt{promptID: p.PromptID, text: p.PromptText, choiceValues: values, isChoice: true}
	default:
		t.Fatalf("expected a client.query or client.choice, got %q", raw.Type)
		return creationPrompt{}
	}
}

// answerCreationPrompt sends the response matching p's kind —
// client.choice_response for a choice, client.query_response otherwise.
func answerCreationPrompt(t *testing.T, ctx context.Context, conn *websocket.Conn, campaignID, sender string, p creationPrompt, answer string) {
	t.Helper()
	mid := sender + "-resp-" + p.promptID
	var msg any
	if p.isChoice {
		msg = protocol.ClientChoiceResponseMessage{
			Envelope: creationEnv(protocol.MessageTypeClientChoiceResponse, mid, sender, campaignID),
			Payload:  protocol.ClientChoiceResponsePayload{PromptID: p.promptID, Value: answer},
		}
	} else {
		msg = protocol.ClientQueryResponseMessage{
			Envelope: creationEnv(protocol.MessageTypeClientQueryResponse, mid, sender, campaignID),
			Payload:  protocol.ClientQueryResponsePayload{PromptID: p.promptID, Text: answer},
		}
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		t.Fatalf("answerCreationPrompt(%q) write error = %v", answer, err)
	}
}

// answerCreationChoiceID sends a client.choice_response with an explicit
// prompt_id — for the tests that deliberately answer an unknown or
// someone else's prompt.
func answerCreationChoiceID(ctx context.Context, conn *websocket.Conn, campaignID, sender, promptID, value string) error {
	return wsjson.Write(ctx, conn, protocol.ClientChoiceResponseMessage{
		Envelope: creationEnv(protocol.MessageTypeClientChoiceResponse, sender+"-resp", sender, campaignID),
		Payload:  protocol.ClientChoiceResponsePayload{PromptID: promptID, Value: value},
	})
}

func TestServe_CreationStart_WithEngine_OffersImportAndRoll(t *testing.T) {
	ts, _ := newTestServerForCreation(t, &fakeSystemEngineClient{})
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	prompt := readCreationPrompt(t, ctx, conn)
	if prompt.promptID == "" {
		t.Error("promptID is empty, want a generated id")
	}
	if !prompt.isChoice {
		t.Fatalf("top-level prompt is a query, want a choice")
	}
	// No pregens authored, so pregen is not offered; the three
	// engine-backed choices are.
	want := map[string]bool{"import": true, "quick_roll": true, "detailed_roll": true}
	if len(prompt.choiceValues) != len(want) {
		t.Fatalf("choice values = %v, want exactly %v", prompt.choiceValues, want)
	}
	for _, c := range prompt.choiceValues {
		if !want[c] {
			t.Errorf("unexpected choice %q", c)
		}
	}
}

func TestServe_CreationStart_NoEngineNoPregens_ClearError(t *testing.T) {
	ts, _ := newTestServerForCreation(t, nil)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-nada", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation-nada", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if !strings.Contains(errMsg.Payload.Message, "system engine") {
		t.Errorf("message = %q, want it to explain the missing system engine", errMsg.Payload.Message)
	}
}

func TestServe_CreationAnswer_UnknownPrompt_ReturnsSystemError(t *testing.T) {
	ts, _ := newTestServerForCreation(t, nil)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-unknown", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := answerCreationChoiceID(ctx, conn, "campaign-creation-unknown", "player-a", "no-such-prompt", "import"); err != nil {
		t.Fatalf("answerCreationChoiceID() error = %v", err)
	}
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_CreationAnswer_WrongSender_ReturnsSystemError(t *testing.T) {
	ts, _ := newTestServerForCreation(t, &fakeSystemEngineClient{})
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
	prompt := readCreationPrompt(t, ctx, connA)

	// player-b tries to answer player-a's own prompt.
	if err := answerCreationChoiceID(ctx, connB, "campaign-creation-wrong-sender", "player-b", prompt.promptID, "import"); err != nil {
		t.Fatalf("answerCreationChoiceID() error = %v", err)
	}
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, connB, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if !strings.Contains(errMsg.Payload.Message, "not addressed to you") {
		t.Errorf("message = %q, want it to say the prompt was not addressed to this sender", errMsg.Payload.Message)
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
	topPrompt := readCreationPrompt(t, ctx, conn)
	answerCreationPrompt(t, ctx, conn, "campaign-creation-import", "player-a", topPrompt, "import")

	pastePrompt := readCreationPrompt(t, ctx, conn)
	if pastePrompt.isChoice {
		t.Errorf("paste-JSON prompt is a choice, want a free-text query")
	}
	if !pastePrompt.acceptsFileUpload {
		t.Error("paste-JSON prompt acceptsFileUpload = false, want true — this is where the client should offer a file picker")
	}

	answerCreationPrompt(t, ctx, conn, "campaign-creation-import", "player-a", pastePrompt, `{"name":"Kestrel"}`)
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
	// pregens deliberately left nil, but the engine is up so "pregen"
	// stays reachable as a top-level choice to exercise this path.
	ts := httptest.NewServer(server.New(logger, st, nil, "", nil, &fakeSystemEngineClient{}, st, nil, nil, st, st, st, nil, nil, nil, nil, session.NewHub()).Handler())
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-pregen-unconfigured", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation-pregen-unconfigured", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	prompt := readCreationPrompt(t, ctx, conn)
	// The engine is up but pregens are nil, so "pregen" is not among the
	// offered choices; answering it anyway must still be rejected cleanly.
	answerCreationPrompt(t, ctx, conn, "campaign-creation-pregen-unconfigured", "player-a", prompt, "pregen")
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
		topPrompt := readCreationPrompt(t, ctx, conn)
		answerCreationPrompt(t, ctx, conn, "campaign-creation-pregen", sender, topPrompt, "pregen")

		pregenPrompt := readCreationPrompt(t, ctx, conn)
		if !pregenPrompt.isChoice || len(pregenPrompt.choiceValues) != 1 || pregenPrompt.choiceValues[0] != "bram-fighter" {
			t.Fatalf("pregen list choice values = %v, want [bram-fighter]", pregenPrompt.choiceValues)
		}
		answerCreationPrompt(t, ctx, conn, "campaign-creation-pregen", sender, pregenPrompt, "bram-fighter")

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
	// The engine is up for the top-level prompt so "quick_roll" is
	// offered, then removed before the answer is processed to exercise
	// the mid-flow "no engine" guard.
	ts, _ := newTestServerForCreation(t, nil)
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-roll-noengine", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation-roll-noengine", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	// No engine: the top-level prompt itself is the "creation unavailable"
	// system.error.
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_CreationRoll_RelaysEnginePromptsUntilDoneAndSavesCharacter(t *testing.T) {
	characterData, err := structpb.NewStruct(map[string]any{"name": "player-a", "gender": "Male"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fake := &fakeSystemEngineClient{
		startCharacterCreationFunc: func(req *systemenginepb.StartCharacterCreationRequest) (*systemenginepb.CharacterCreationPromptResponse, error) {
			if req.Mode != systemenginepb.CharacterCreationMode_CHARACTER_CREATION_MODE_DETAILED {
				t.Errorf("StartCharacterCreation Mode = %v, want DETAILED", req.Mode)
			}
			if req.CharacterName != "Bram the Bold" {
				t.Errorf("StartCharacterCreation CharacterName = %q, want the name from character.creation_start", req.CharacterName)
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

	if err := sendCreationStartNamed(ctx, conn, "campaign-creation-roll", "player-a", "Bram the Bold"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	topPrompt := readCreationPrompt(t, ctx, conn)
	answerCreationPrompt(t, ctx, conn, "campaign-creation-roll", "player-a", topPrompt, "detailed_roll")

	racePrompt := readCreationPrompt(t, ctx, conn)
	if racePrompt.text != "Choose your race." {
		t.Errorf("text = %q, want %q", racePrompt.text, "Choose your race.")
	}
	answerCreationPrompt(t, ctx, conn, "campaign-creation-roll", "player-a", racePrompt, "Human")

	classPrompt := readCreationPrompt(t, ctx, conn)
	answerCreationPrompt(t, ctx, conn, "campaign-creation-roll", "player-a", classPrompt, "Fighter")

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
	prompt := readCreationPrompt(t, ctx, conn)
	answerCreationPrompt(t, ctx, conn, "campaign-creation-roll-fail", "player-a", prompt, "quick_roll")
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}

func TestServe_CreationPregen_ChoiceCarriesReadableLabels(t *testing.T) {
	ts, st := newTestServerForCreation(t, nil)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := st.SavePregen(ctx, store.Pregen{
		ID:            "bram-fighter",
		CampaignID:    "campaign-pregen-labels",
		Name:          "Bram the Bold",
		Description:   "A stalwart level-1 fighter.",
		SchemaVersion: "opencombatengine-v1",
		CharacterData: json.RawMessage(`{"name":"Bram"}`),
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SavePregen() error = %v", err)
	}

	conn := dialAndJoin(t, ts, "campaign-pregen-labels", "player-a")
	defer conn.CloseNow()

	if err := sendCreationStart(ctx, conn, "campaign-pregen-labels", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	top := readCreationPrompt(t, ctx, conn)
	answerCreationPrompt(t, ctx, conn, "campaign-pregen-labels", "player-a", top, "pregen")

	// Read the raw client.choice so both value and label can be checked.
	var raw struct {
		Type    protocol.MessageType         `json:"type"`
		Payload protocol.ClientChoicePayload `json:"payload"`
	}
	if err := wsjson.Read(ctx, conn, &raw); err != nil {
		t.Fatalf("Read(client.choice) error = %v", err)
	}
	if raw.Type != protocol.MessageTypeClientChoice || len(raw.Payload.Options) != 1 {
		t.Fatalf("got %q with options %+v", raw.Type, raw.Payload.Options)
	}
	opt := raw.Payload.Options[0]
	if opt.Value != "bram-fighter" {
		t.Errorf("option value = %q, want the pregen id", opt.Value)
	}
	if !strings.Contains(opt.Label, "Bram the Bold") || !strings.Contains(opt.Label, "stalwart") {
		t.Errorf("option label = %q, want the readable name and description", opt.Label)
	}
}

func TestServe_CreationAnswer_WrongResponseKind_ReturnsSystemError(t *testing.T) {
	ts, _ := newTestServerForCreation(t, &fakeSystemEngineClient{})
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-wrongkind", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation-wrongkind", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	prompt := readCreationPrompt(t, ctx, conn)
	if !prompt.isChoice {
		t.Fatalf("expected the top-level prompt to be a choice")
	}
	// Answer a client.choice prompt with a client.query_response.
	if err := wsjson.Write(ctx, conn, protocol.ClientQueryResponseMessage{
		Envelope: creationEnv(protocol.MessageTypeClientQueryResponse, "player-a-wrongkind", "player-a", "campaign-creation-wrongkind"),
		Payload:  protocol.ClientQueryResponsePayload{PromptID: prompt.promptID, Text: "import"},
	}); err != nil {
		t.Fatalf("write client.query_response error = %v", err)
	}
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
	if !strings.Contains(errMsg.Payload.Message, "wrong response type") {
		t.Errorf("message = %q, want it to name the wrong response type", errMsg.Payload.Message)
	}

	// The prompt must still be answerable the correct way afterward.
	answerCreationPrompt(t, ctx, conn, "campaign-creation-wrongkind", "player-a", prompt, "import")
	next := readCreationPrompt(t, ctx, conn)
	if next.isChoice {
		t.Errorf("expected the paste-JSON query after answering, got a choice")
	}
}

func TestServe_CreationTopLevel_InvalidAnswer_ReturnsSystemError(t *testing.T) {
	ts, _ := newTestServerForCreation(t, &fakeSystemEngineClient{})
	defer ts.Close()

	conn := dialAndJoin(t, ts, "campaign-creation-invalid", "player-a")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sendCreationStart(ctx, conn, "campaign-creation-invalid", "player-a"); err != nil {
		t.Fatalf("sendCreationStart() error = %v", err)
	}
	prompt := readCreationPrompt(t, ctx, conn)
	answerCreationPrompt(t, ctx, conn, "campaign-creation-invalid", "player-a", prompt, "not-a-real-choice")
	var errMsg protocol.SystemErrorMessage
	if err := wsjson.Read(ctx, conn, &errMsg); err != nil {
		t.Fatalf("Read(system.error) error = %v", err)
	}
}
