// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/llm"
	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

func TestServe_NarrativePlayerInput_SlowPass_CreateNPC_PersistsCharacterAndReturnsID(t *testing.T) {
	npcData, err := structpb.NewStruct(map[string]any{"name": "Goblin Raider", "team": "Monster"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		fromJsonResp: &systemenginepb.FromJsonResponse{
			Actor: &systemenginepb.Actor{ActorId: "engine-assigned-id", CharacterData: npcData, SchemaVersion: "opencombatengine-v1"},
		},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "A goblin raider bursts from the treeline."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "create_npc",
				Arguments: json.RawMessage(`{"character_json":"{\"name\":\"Goblin Raider\",\"team\":\"Monster\"}"}`),
			}}},
			{Text: "The goblin raider snarls, weapon drawn."},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-npc", "player-a")

	conn := dialAndJoin(t, ts, "campaign-npc", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-npc", "player-a", "char-a", "A goblin attacks!"); err != nil {
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
	if toolResult.Payload.ToolName != "create_npc" || !toolResult.Payload.Success {
		t.Fatalf("tool.result = %+v, want a successful create_npc", toolResult.Payload)
	}

	secondSlowPassCall := fakeLLM.callAt(t, 2)
	lastMsg := secondSlowPassCall.Messages[len(secondSlowPassCall.Messages)-1]
	if lastMsg.Role != llm.RoleTool || lastMsg.ToolCallID != "call_1" {
		t.Fatalf("last message before final narration = %+v, want a RoleTool reply to call_1", lastMsg)
	}
	var result struct {
		CharacterID string `json:"character_id"`
	}
	if err := json.Unmarshal([]byte(lastMsg.Content), &result); err != nil {
		t.Fatalf("unmarshaling create_npc tool result error = %v", err)
	}
	if result.CharacterID == "" {
		t.Fatal("create_npc tool result character_id is empty")
	}

	persisted, err := st.GetCharacter(ctx, result.CharacterID)
	if err != nil {
		t.Fatalf("GetCharacter(%q) error = %v, want the NPC to have been persisted", result.CharacterID, err)
	}
	if persisted.CampaignID != "campaign-npc" {
		t.Errorf("persisted.CampaignID = %q, want %q", persisted.CampaignID, "campaign-npc")
	}
	if persisted.OwnerID != "master" {
		t.Errorf("persisted.OwnerID = %q, want %q (an NPC has no player owner)", persisted.OwnerID, "master")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_CreateNPC_ValidationError_ReturnsFailure(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		fromJsonResp: &systemenginepb.FromJsonResponse{
			Warnings: []*systemenginepb.ValidationWarning{
				{FieldPath: "/name", Message: "name is required", Severity: "error"},
			},
		},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "Something stirs in the dark."},
			{ToolCalls: []llm.ToolCall{{
				ID: "call_1", Name: "create_npc",
				Arguments: json.RawMessage(`{"character_json":"{}"}`),
			}}},
			{Text: "The shape retreats into shadow, unresolved."},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-npc-bad", "player-a")

	conn := dialAndJoin(t, ts, "campaign-npc-bad", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-npc-bad", "player-a", "char-a", "Something lurks."); err != nil {
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
	if toolResult.Payload.Success {
		t.Fatal("tool.result Success = true, want false (FromJson reported an error-severity warning)")
	}
	if toolResult.Payload.ReasonCode != "invalid_character" {
		t.Errorf("tool.result ReasonCode = %q, want %q", toolResult.Payload.ReasonCode, "invalid_character")
	}
}

func TestServe_NarrativePlayerInput_SlowPass_GetCharacterSchema_ReturnsSchemaToModel(t *testing.T) {
	fakeEngine := &fakeSystemEngineClient{
		getCharacterSchemaResp: &systemenginepb.GetCharacterSchemaResponse{
			SchemaVersion: "opencombatengine-v1",
			JsonSchema:    `{"type":"object","properties":{"name":{"type":"string"}}}`,
		},
	}
	fakeLLM := &fakeLLMProvider{
		responses: []llm.CompletionResponse{
			{Text: "The DM considers what shape this creature should take."},
			{ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "get_character_schema", Arguments: json.RawMessage(`{}`)}}},
			{Text: "A shadow coalesces into something with claws."},
		},
	}
	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-schema", "player-a")

	conn := dialAndJoin(t, ts, "campaign-schema", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-schema", "player-a", "char-a", "Something forms in the dark."); err != nil {
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
		t.Fatalf("tool.result Success = false, want true: %+v", toolResult.Payload)
	}

	secondSlowPassCall := fakeLLM.callAt(t, 2)
	lastMsg := secondSlowPassCall.Messages[len(secondSlowPassCall.Messages)-1]
	// json_schema is a nested JSON object in the tool result (not a
	// JSON-encoded string) — more directly readable for the model than a
	// string-within-a-string, unlike character.schema_response's wire
	// payload to browser clients, which keeps it as a string specifically
	// so the schema's own JSON Schema keywords round-trip exactly.
	var result struct {
		SchemaVersion string          `json:"schema_version"`
		JSONSchema    json.RawMessage `json:"json_schema"`
	}
	if err := json.Unmarshal([]byte(lastMsg.Content), &result); err != nil {
		t.Fatalf("unmarshaling get_character_schema tool result error = %v", err)
	}
	if result.SchemaVersion != "opencombatengine-v1" {
		t.Errorf("SchemaVersion = %q, want %q", result.SchemaVersion, "opencombatengine-v1")
	}
	if len(result.JSONSchema) == 0 {
		t.Error("JSONSchema is empty, want the fake engine's schema content")
	}
}

// TestServe_NarrativePlayerInput_SlowPass_CreateNPCThenStartCombat_IncludesRealNPCInInitiative
// is the integration test motivating create_npc's existence: before it,
// start_combat could only ever include a character a player had already
// uploaded, since a narrated monster had no real character_id — see
// turn_order.go and dm_tools.go's doc comments. Here the DM creates a
// goblin NPC, then references its real (server-generated) character_id
// — extracted from create_npc's own tool result, exactly as the DM
// model itself has to — in a start_combat call alongside the player's
// character, and both must appear in the resulting initiative order.
func TestServe_NarrativePlayerInput_SlowPass_CreateNPCThenStartCombat_IncludesRealNPCInInitiative(t *testing.T) {
	npcData, err := structpb.NewStruct(map[string]any{"name": "Goblin Raider", "team": "Monster"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	fakeEngine := &fakeSystemEngineClient{
		fromJsonResp: &systemenginepb.FromJsonResponse{
			Actor: &systemenginepb.Actor{ActorId: "engine-assigned-id", CharacterData: npcData, SchemaVersion: "opencombatengine-v1"},
		},
		getCharacterStatusResp: &systemenginepb.GetCharacterStatusResponse{Status: systemenginepb.CharacterStatus_CHARACTER_STATUS_ACTIVE},
		startTurnResp:          &systemenginepb.StartTurnResponse{Success: true},
		resolveCheckFunc: func(req *systemenginepb.ResolveCheckRequest) (*systemenginepb.ResolveCheckResponse, error) {
			total := int32(8)
			if req.Actor.ActorId != "char-a" {
				total = 17 // the NPC rolls higher, so it leads initiative
			}
			return &systemenginepb.ResolveCheckResponse{
				Success: true,
				Outcome: &systemenginepb.Outcome{Total: total, ResultSummary: "resolved", Rolls: []*systemenginepb.DieRoll{{Sides: 20, Result: total, Label: "d20"}}},
			}, nil
		},
	}

	var npcCharacterID string
	fakeLLM := &fakeLLMProvider{
		respondFunc: func(callIndex int, req llm.CompletionRequest) (llm.CompletionResponse, error) {
			switch callIndex {
			case 0:
				return llm.CompletionResponse{Text: "A goblin raider leaps out to attack."}, nil
			case 1:
				return llm.CompletionResponse{ToolCalls: []llm.ToolCall{{
					ID: "call_1", Name: "create_npc",
					Arguments: json.RawMessage(`{"character_json":"{\"name\":\"Goblin Raider\",\"team\":\"Monster\"}"}`),
				}}}, nil
			case 2:
				lastMsg := req.Messages[len(req.Messages)-1]
				var result struct {
					CharacterID string `json:"character_id"`
				}
				if err := json.Unmarshal([]byte(lastMsg.Content), &result); err != nil {
					t.Fatalf("unmarshaling create_npc tool result in respondFunc: %v", err)
				}
				npcCharacterID = result.CharacterID
				args, err := json.Marshal(map[string]any{"character_ids": []string{"char-a", npcCharacterID}})
				if err != nil {
					t.Fatalf("marshaling start_combat args: %v", err)
				}
				return llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "call_2", Name: "start_combat", Arguments: args}}}, nil
			default:
				return llm.CompletionResponse{Text: "Roll for initiative — the goblin strikes first!"}, nil
			}
		},
	}

	ts, st := newTestServerWithLLMAndSystemEngine(t, fakeLLM, fakeEngine)
	defer ts.Close()
	seedCharacter(t, st, "char-a", "campaign-npc-combat", "player-a")

	conn := dialAndJoin(t, ts, "campaign-npc-combat", "player-a")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sendPlayerInput(ctx, conn, "campaign-npc-combat", "player-a", "char-a", "A goblin attacks!"); err != nil {
		t.Fatalf("sendPlayerInput() error = %v", err)
	}
	var bubble protocol.NarrativePlayerBubbleMessage
	if err := wsjson.Read(ctx, conn, &bubble); err != nil {
		t.Fatalf("Read(narrative.player_bubble) error = %v", err)
	}

	var state protocol.TurnStatePayload
	found := false
	for i := 0; i < 20; i++ {
		typ, data, err := readEnvelopeType(ctx, conn)
		if err != nil {
			t.Fatalf("reading message %d: %v", i, err)
		}
		if typ != protocol.MessageTypeTurnState {
			continue
		}
		var msg protocol.TurnStateMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshaling turn.state: %v", err)
		}
		state = msg.Payload
		found = true
		break
	}
	if !found {
		t.Fatal("no turn.state message arrived")
	}

	if npcCharacterID == "" {
		t.Fatal("respondFunc never captured a real NPC character_id — create_npc must not have succeeded")
	}
	if !state.Active {
		t.Fatal("turn.state Active = false, want true")
	}
	if len(state.Order) != 2 {
		t.Fatalf("turn.state Order = %v, want 2 entries (char-a and the NPC)", state.Order)
	}
	if state.Order[0] != npcCharacterID {
		t.Errorf("turn.state Order[0] = %q, want the NPC %q (it rolled higher)", state.Order[0], npcCharacterID)
	}
	if state.CurrentCharacterID != npcCharacterID {
		t.Errorf("turn.state CurrentCharacterID = %q, want %q", state.CurrentCharacterID, npcCharacterID)
	}
}
