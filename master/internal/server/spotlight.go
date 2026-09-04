// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jamesplotts/layforge/master/internal/protocol"
	"github.com/jamesplotts/layforge/master/internal/store"
)

// spotlightWindowLimit bounds how many recent campaign events
// spotlightContextText scans to reconstruct "who has spoken lately"
// (design doc §9.6). This is a soft signal, not a hard gate — a
// campaign with more than this many events since a quiet character's
// last turn just reads as "no turns in recent history" rather than a
// precise (and increasingly expensive to compute) exact count, which is
// exactly the fidelity a DM nudge needs, not perfect bookkeeping back to
// session one.
const spotlightWindowLimit = 500

// spotlightReportMax caps how many characters spotlightContextText
// flags per turn — the point is a short, actionable nudge, not a full
// roster dump the model has to wade through every single turn.
const spotlightReportMax = 3

// spotlightContextText returns a best-effort DM-context section
// (design doc §9.6, §8: "tracked from tool-use log data ... surfaced to
// the DM's context") naming the campaign's quietest player characters —
// the ones with the most turns since their own last narrative.
// player_input — or "" if there's nothing useful to say (no
// characters/events store configured, fewer than two real player
// characters to balance between, or every player has been heard from
// recently). Deliberately reads the same durable event log every other
// log-derived feature already reads (see store.EventStore's own doc
// comment) rather than maintaining separate bookkeeping — this is
// exactly the "DM has perfect bookkeeping a human GM would have to rely
// on memory for" design doc names, not a new source of truth.
//
// NPCs (characters create_npc saved under masterSenderID) are excluded
// — design doc §9.6 is about proactively prompting quieter *players*,
// not monsters/NPCs the DM itself controls.
func (s *Server) spotlightContextText(ctx context.Context, campaignID string) string {
	if s.characters == nil || s.events == nil {
		return ""
	}

	characters, err := s.characters.ListCharacters(ctx, campaignID)
	if err != nil {
		return ""
	}
	players := make([]store.Character, 0, len(characters))
	for _, c := range characters {
		if c.OwnerID != "" && c.OwnerID != masterSenderID {
			players = append(players, c)
		}
	}
	if len(players) < 2 {
		return ""
	}

	events, _, err := s.events.ListEvents(ctx, campaignID, store.ListEventsOptions{Limit: spotlightWindowLimit})
	if err != nil {
		return ""
	}

	var turns []string // character_id per narrative.player_input, oldest-first
	for _, e := range events {
		if e.MessageType != string(protocol.MessageTypeNarrativePlayerInput) {
			continue
		}
		var envelope struct {
			Payload struct {
				CharacterID string `json:"character_id"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(e.Raw, &envelope); err != nil || envelope.Payload.CharacterID == "" {
			continue
		}
		turns = append(turns, envelope.Payload.CharacterID)
	}
	if len(turns) == 0 {
		return ""
	}

	type quietPlayer struct {
		characterID  string
		turnsSince   int
		everAppeared bool
	}
	var report []quietPlayer
	for _, p := range players {
		lastIndex := -1
		for i, id := range turns {
			if id == p.ID {
				lastIndex = i
			}
		}
		if lastIndex == -1 {
			report = append(report, quietPlayer{characterID: p.ID, turnsSince: len(turns)})
			continue
		}
		since := len(turns) - 1 - lastIndex
		if since == 0 {
			continue // just went — nothing to flag
		}
		report = append(report, quietPlayer{characterID: p.ID, turnsSince: since, everAppeared: true})
	}
	if len(report) == 0 {
		return ""
	}

	sort.SliceStable(report, func(i, j int) bool { return report[i].turnsSince > report[j].turnsSince })
	if len(report) > spotlightReportMax {
		report = report[:spotlightReportMax]
	}

	var b strings.Builder
	b.WriteString("Spotlight balance (soft signal, not a rule — look for a natural moment to draw a quieter character in; never force it):\n")
	for _, r := range report {
		if r.everAppeared {
			fmt.Fprintf(&b, "- %s: %d turn(s) since their last turn\n", r.characterID, r.turnsSince)
		} else {
			fmt.Fprintf(&b, "- %s: no turns in recent history\n", r.characterID)
		}
	}
	return b.String()
}
