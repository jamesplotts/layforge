// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/store"
)

func newTestStore(t *testing.T) *store.SQLiteEventStore {
	t.Helper()
	s, err := store.OpenSQLiteEventStore(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLiteEventStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return s
}

func testEvent(campaignID, messageID string) store.Event {
	return store.Event{
		CampaignID:  campaignID,
		MessageID:   messageID,
		MessageType: "system.connect",
		SenderID:    "sender-1",
		OccurredAt:  time.Now().UTC(),
		Raw:         json.RawMessage(`{"hello":"world"}`),
	}
}

func TestSQLiteEventStore_AppendEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   store.Event
		wantErr error // nil means "expect success"
	}{
		{
			name:    "ValidEvent_ReturnsNil",
			event:   testEvent("campaign-1", "msg-1"),
			wantErr: nil,
		},
		{
			name: "MissingCampaignID_ReturnsCampaignIDRequired",
			event: func() store.Event {
				e := testEvent("", "msg-1")
				return e
			}(),
			wantErr: store.ErrCampaignIDRequired,
		},
		{
			name: "MissingMessageID_ReturnsMessageIDRequired",
			event: func() store.Event {
				e := testEvent("campaign-1", "")
				return e
			}(),
			wantErr: store.ErrMessageIDRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			err := s.AppendEvent(context.Background(), tt.event)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("AppendEvent() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("AppendEvent() error = %v, want error wrapping %v", err, tt.wantErr)
			}
		})
	}
}

func TestSQLiteEventStore_AppendEvent_DuplicateMessageID_ReturnsErrDuplicateMessage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first := testEvent("campaign-1", "msg-1")
	if err := s.AppendEvent(ctx, first); err != nil {
		t.Fatalf("first AppendEvent() error = %v", err)
	}

	second := testEvent("campaign-1", "msg-1")
	err := s.AppendEvent(ctx, second)
	if !errors.Is(err, store.ErrDuplicateMessage) {
		t.Errorf("second AppendEvent() error = %v, want error wrapping ErrDuplicateMessage", err)
	}
}

func TestSQLiteEventStore_AppendEvent_SameMessageIDDifferentCampaign_Succeeds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.AppendEvent(ctx, testEvent("campaign-1", "msg-1")); err != nil {
		t.Fatalf("AppendEvent(campaign-1) error = %v", err)
	}
	// message_id uniqueness is scoped per campaign (design doc §5:
	// message_id disambiguates within a campaign's stream, not globally).
	if err := s.AppendEvent(ctx, testEvent("campaign-2", "msg-1")); err != nil {
		t.Errorf("AppendEvent(campaign-2) error = %v, want nil", err)
	}
}

func TestSQLiteEventStore_ListEvents_MissingCampaignID_ReturnsCampaignIDRequired(t *testing.T) {
	s := newTestStore(t)
	_, err := s.ListEvents(context.Background(), "", store.ListEventsOptions{})
	if !errors.Is(err, store.ErrCampaignIDRequired) {
		t.Errorf("ListEvents() error = %v, want error wrapping ErrCampaignIDRequired", err)
	}
}

func TestSQLiteEventStore_ListEvents_ReturnsInSequenceOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"msg-1", "msg-2", "msg-3"} {
		if err := s.AppendEvent(ctx, testEvent("campaign-1", id)); err != nil {
			t.Fatalf("AppendEvent(%s) error = %v", id, err)
		}
	}

	got, err := s.ListEvents(ctx, "campaign-1", store.ListEventsOptions{})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}

	wantOrder := []string{"msg-1", "msg-2", "msg-3"}
	for i, want := range wantOrder {
		if got[i].MessageID != want {
			t.Errorf("got[%d].MessageID = %q, want %q", i, got[i].MessageID, want)
		}
		if got[i].Sequence <= 0 {
			t.Errorf("got[%d].Sequence = %d, want > 0", i, got[i].Sequence)
		}
		if i > 0 && got[i].Sequence <= got[i-1].Sequence {
			t.Errorf("got[%d].Sequence = %d is not greater than got[%d].Sequence = %d", i, got[i].Sequence, i-1, got[i-1].Sequence)
		}
	}
}

func TestSQLiteEventStore_ListEvents_ScopedToCampaign(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.AppendEvent(ctx, testEvent("campaign-1", "msg-1")); err != nil {
		t.Fatalf("AppendEvent(campaign-1) error = %v", err)
	}
	if err := s.AppendEvent(ctx, testEvent("campaign-2", "msg-1")); err != nil {
		t.Fatalf("AppendEvent(campaign-2) error = %v", err)
	}

	got, err := s.ListEvents(ctx, "campaign-1", store.ListEventsOptions{})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].CampaignID != "campaign-1" {
		t.Errorf("got[0].CampaignID = %q, want %q", got[0].CampaignID, "campaign-1")
	}
}

func TestSQLiteEventStore_ListEvents_AfterSequence_ReturnsOnlyLaterEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"msg-1", "msg-2", "msg-3"} {
		if err := s.AppendEvent(ctx, testEvent("campaign-1", id)); err != nil {
			t.Fatalf("AppendEvent(%s) error = %v", id, err)
		}
	}

	all, err := s.ListEvents(ctx, "campaign-1", store.ListEventsOptions{})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	firstSequence := all[0].Sequence

	got, err := s.ListEvents(ctx, "campaign-1", store.ListEventsOptions{AfterSequence: firstSequence})
	if err != nil {
		t.Fatalf("ListEvents(AfterSequence) error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].MessageID != "msg-2" || got[1].MessageID != "msg-3" {
		t.Errorf("got = %+v, want [msg-2, msg-3]", got)
	}
}

func TestSQLiteEventStore_ListEvents_LimitCapping(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := s.AppendEvent(ctx, testEvent("campaign-1", "msg-"+string(rune('a'+i)))); err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}

	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "ZeroLimit_UsesDefault", limit: 0, want: 5},
		{name: "ExplicitLimitBelowCount", limit: 2, want: 2},
		{name: "NegativeLimit_UsesDefault", limit: -1, want: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.ListEvents(ctx, "campaign-1", store.ListEventsOptions{Limit: tt.limit})
			if err != nil {
				t.Fatalf("ListEvents() error = %v", err)
			}
			if len(got) != tt.want {
				t.Errorf("len(got) = %d, want %d", len(got), tt.want)
			}
		})
	}
}

func TestSQLiteEventStore_AppendThenList_RawPayloadRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	event := testEvent("campaign-1", "msg-1")
	event.Raw = json.RawMessage(`{"client_kind":"player_web_v1","auth_token":"secret"}`)
	if err := s.AppendEvent(ctx, event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	got, err := s.ListEvents(ctx, "campaign-1", store.ListEventsOptions{})
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}

	var decoded map[string]string
	if err := json.Unmarshal(got[0].Raw, &decoded); err != nil {
		t.Fatalf("Unmarshal(Raw) error = %v", err)
	}
	if decoded["client_kind"] != "player_web_v1" {
		t.Errorf("decoded[client_kind] = %q, want %q", decoded["client_kind"], "player_web_v1")
	}

	// OccurredAt should round-trip to within driver precision (RFC3339Nano
	// on disk), not necessarily bit-identical to the in-memory time.Time.
	if got[0].OccurredAt.Sub(event.OccurredAt).Abs() > time.Second {
		t.Errorf("OccurredAt = %v, want close to %v", got[0].OccurredAt, event.OccurredAt)
	}
}
