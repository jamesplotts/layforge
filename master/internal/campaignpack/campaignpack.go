// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package campaignpack loads a directory-based campaign pack (design
// doc §6.4) — campaign.md, locations/*.md, npcs/*.md, encounters/*.md,
// each markdown + YAML front matter — into structured data. It
// deliberately does not load state.json: that file documents the shape
// of a campaign's *mutable* session state (party location, discovered
// locations, stashed possessions, land holdings), but Master tracks
// that in its own SQLite store instead (package store's
// campaign_pack/party_location/location_state/stashed_items/
// stashed_currency tables), matching how every other piece of live
// campaign state already persists (design doc §10) rather than a
// running server rewriting a file tracked in the pack's own git
// history.
package campaignpack

// Pack is one campaign pack's static content, loaded from a directory
// by LoadPack.
type Pack struct {
	// ID, Title, LevelRange, Tone, PvPPolicy, MaturityTier,
	// SharedKnowledge, Lines, Veils, Author, and ContentWarnings come
	// from campaign.md's front matter (design doc §6.4). PvPPolicy is
	// one of policy.PvPPolicy's string values, unvalidated here — see
	// package policy's campaignpack-backed Provider for where that's
	// checked. MaturityTier is a reference to a maturity_tiers/<id>.md
	// file (design doc §6.5) — not itself prompt text, and not resolved
	// by this package; no such loader exists yet.
	ID              string
	Title           string
	LevelRange      string
	Tone            []string
	PvPPolicy       string
	MaturityTier    string
	SharedKnowledge string
	Lines           []string
	Veils           []string
	Author          string
	ContentWarnings []string
	// Overview is campaign.md's markdown body.
	Overview string

	Locations  []Location
	NPCs       []NPC
	Encounters []Encounter
}

// Location is one locations/*.md file.
type Location struct {
	ID string
	// Connections lists the IDs of locations directly reachable from
	// this one — the real graph package server's travel_to DM tool
	// gates movement against (design doc's own committed content
	// already authors this graph; nothing here invents it).
	Connections []string
	// Body is the location's markdown description.
	Body string
}

// NPC is one npcs/*.md file.
type NPC struct {
	ID           string
	Location     string
	StatBlockRef string
	Voice        string
	// Body is the NPC's markdown personality/background description.
	Body string
}

// Encounter is one encounters/*.md file.
type Encounter struct {
	ID       string
	Location string
	Involves []string
	// Body is the encounter's markdown description.
	Body string
}
