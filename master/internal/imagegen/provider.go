// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

// Package imagegen implements design doc §6.3's image-gen provider
// contract — generate_scene_image(prompt, context, maturity_tier) ->
// image_url — as a pluggable interface, the same shape as package llm's
// Provider (design doc §3.1) and package auth's Provider (§6.6): Master
// depends only on this interface, never on a specific backend, so a
// self-hoster can substitute a different image-gen service without
// touching package server.
//
// ComfyUIProvider (comfyui.go) is the reference implementation design
// doc §6.3 names, calling a self-hosted ComfyUI instance's own REST API
// directly. It does NOT implement x402 payment negotiation — design doc
// §6.3 describes "existing x402/ComfyUI setup" as the operator's own
// deployment shape, not a contract Master's code needs to speak itself.
// Real payment settlement, even micropayments, is out of scope for
// Master to perform autonomously; a self-hoster whose ComfyUI endpoint
// sits behind an x402 paywall needs a transparent proxy in front of it
// that handles payment and forwards ComfyUI's plain REST API through —
// ComfyUIProvider then talks to that proxy exactly as it would talk to
// ComfyUI directly.
package imagegen

import "context"

// Provider generates a scene illustration from a text description
// (design doc §6.3). Implementations are expected to be slow (real
// image generation, seconds at minimum) — callers should not block a
// connection's read loop on this the same way runSlowPass launches DM
// narration in its own goroutine (design doc §7).
type Provider interface {
	// GenerateSceneImage generates an image from prompt and returns a
	// URL the client can load it from. maturityTierPrompt, when
	// non-empty, is appended as content guidance the same way package
	// server's withMaturityConstraint appends it to narrative
	// generation — design doc §6.3 says an image-gen tier "may be
	// configured stricter than the text tier for a campaign, but never
	// more permissive by default"; callers should pass a
	// policy.CampaignPolicy's image-specific tier when the operator set
	// one, falling back to its text tier otherwise, never an empty
	// string when a text tier is configured.
	GenerateSceneImage(ctx context.Context, prompt, maturityTierPrompt string) (imageURL string, err error)
}
