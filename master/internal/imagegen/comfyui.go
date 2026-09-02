// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package imagegen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// promptPlaceholder is the token ComfyUIProvider substitutes with the
// real scene prompt inside an operator-supplied workflow template. It's
// deliberately unlikely to collide with real prompt text a workflow
// author would type — see NewComfyUIProvider's doc comment for the
// substitution contract.
const promptPlaceholder = "%%LAYFORGE_PROMPT%%"

// ComfyUIProvider implements Provider (design doc §6.3) against a
// self-hosted ComfyUI instance's own REST API — no wrapper service, no
// x402 payment handling (see this package's doc comment for why).
//
// ComfyUI's workflow graph is entirely operator-defined (which
// checkpoint/model, sampler settings, resolution, LoRAs, etc.) —
// there's no single "the" text-to-image workflow every install shares,
// so Master cannot construct one itself the way it constructs, say, an
// ability-check request against the System Engine's known contract.
// Instead the operator exports their own working workflow from
// ComfyUI's UI ("Save (API Format)") with the literal token
// %%LAYFORGE_PROMPT%% in place of whichever node's prompt text field
// should receive the generated scene description, and passes that file
// to NewComfyUIProvider. This keeps Master's own code entirely
// workflow-agnostic — the same principle as never assuming a specific
// system engine's shape outside its own adapter (CLAUDE.md) — while
// still being a real, working reference implementation once an operator
// supplies their own real workflow.
type ComfyUIProvider struct {
	baseURL          string
	workflowTemplate string
	httpClient       *http.Client
	pollInterval     time.Duration
}

var _ Provider = (*ComfyUIProvider)(nil)

// NewComfyUIProvider creates a ComfyUIProvider that submits jobs to the
// ComfyUI instance at baseURL (e.g. "http://localhost:8188"; no trailing
// slash). workflowTemplate is the full contents of an API-format
// workflow JSON file exported from ComfyUI's own UI, containing the
// literal string "%%LAYFORGE_PROMPT%%" in place of the positive-prompt
// node's text value — e.g. a CLIPTextEncode node's
// `"inputs": {"text": "%%LAYFORGE_PROMPT%%", ...}`. Returns an error if
// the placeholder is missing, since a template that can never receive a
// real prompt would silently generate the same image every time.
func NewComfyUIProvider(baseURL, workflowTemplate string) (*ComfyUIProvider, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return nil, errors.New("imagegen: baseURL is required")
	}
	if !strings.Contains(workflowTemplate, promptPlaceholder) {
		return nil, fmt.Errorf("imagegen: workflow template does not contain the required placeholder %s — the generated image would never reflect the actual scene prompt", promptPlaceholder)
	}
	return &ComfyUIProvider{
		baseURL:          baseURL,
		workflowTemplate: workflowTemplate,
		httpClient:       &http.Client{Timeout: 30 * time.Second},
		pollInterval:     time.Second,
	}, nil
}

type comfyUIPromptResponse struct {
	PromptID   string                     `json:"prompt_id"`
	Number     int                        `json:"number"`
	NodeErrors map[string]json.RawMessage `json:"node_errors"`
	Error      string                     `json:"error"`
}

type comfyUIHistoryEntry struct {
	Outputs map[string]comfyUINodeOutput `json:"outputs"`
	Status  struct {
		Completed bool   `json:"completed"`
		StatusStr string `json:"status_str"`
	} `json:"status"`
}

type comfyUINodeOutput struct {
	Images []comfyUIImageRef `json:"images"`
}

type comfyUIImageRef struct {
	Filename  string `json:"filename"`
	Subfolder string `json:"subfolder"`
	Type      string `json:"type"`
}

// GenerateSceneImage implements Provider. It substitutes the finished
// prompt into the workflow template, submits it to ComfyUI's /prompt
// endpoint, polls /history/{prompt_id} until an output image appears (or
// ctx is done), and returns a /view URL for the first image found across
// any node's outputs — ComfyUI workflows commonly have a single
// SaveImage node, but nothing in the contract guarantees which node ID
// that is, so this doesn't assume one.
func (p *ComfyUIProvider) GenerateSceneImage(ctx context.Context, prompt, maturityTierPrompt string) (string, error) {
	fullPrompt := prompt
	if maturityTierPrompt != "" {
		fullPrompt = prompt + "\n\nContent guidance: " + maturityTierPrompt
	}

	workflowJSON := strings.ReplaceAll(p.workflowTemplate, promptPlaceholder, jsonEscapeForTemplate(fullPrompt))

	var workflow json.RawMessage
	if err := json.Unmarshal([]byte(workflowJSON), &workflow); err != nil {
		return "", fmt.Errorf("imagegen: workflow template is not valid JSON after substituting the prompt: %w", err)
	}

	promptID, err := p.submit(ctx, workflow)
	if err != nil {
		return "", err
	}

	return p.pollForImage(ctx, promptID)
}

// submit posts workflow to ComfyUI's /prompt endpoint and returns the
// resulting prompt_id.
func (p *ComfyUIProvider) submit(ctx context.Context, workflow json.RawMessage) (string, error) {
	body, err := json.Marshal(map[string]any{"prompt": workflow})
	if err != nil {
		return "", fmt.Errorf("imagegen: building /prompt request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/prompt", strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("imagegen: building /prompt request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("imagegen: calling ComfyUI /prompt (is it running at %s?): %w", p.baseURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("imagegen: reading /prompt response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("imagegen: ComfyUI /prompt returned %s: %s", resp.Status, string(respBody))
	}

	var parsed comfyUIPromptResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("imagegen: parsing /prompt response: %w", err)
	}
	if parsed.Error != "" {
		return "", fmt.Errorf("imagegen: ComfyUI rejected the workflow: %s", parsed.Error)
	}
	if len(parsed.NodeErrors) > 0 {
		return "", fmt.Errorf("imagegen: ComfyUI reported node errors in the workflow: %s", string(respBody))
	}
	if parsed.PromptID == "" {
		return "", fmt.Errorf("imagegen: ComfyUI /prompt response had no prompt_id: %s", string(respBody))
	}
	return parsed.PromptID, nil
}

// pollForImage polls /history/{promptID} until an output image appears,
// ctx is done, or ComfyUI reports the job failed.
func (p *ComfyUIProvider) pollForImage(ctx context.Context, promptID string) (string, error) {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		ref, done, err := p.checkHistory(ctx, promptID)
		if err != nil {
			return "", err
		}
		if done {
			return p.viewURL(ref), nil
		}

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("imagegen: waiting for ComfyUI to finish prompt %s: %w", promptID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (p *ComfyUIProvider) checkHistory(ctx context.Context, promptID string) (ref comfyUIImageRef, done bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/history/"+url.PathEscape(promptID), nil)
	if err != nil {
		return ref, false, fmt.Errorf("imagegen: building /history request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return ref, false, fmt.Errorf("imagegen: calling ComfyUI /history: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ref, false, fmt.Errorf("imagegen: reading /history response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return ref, false, fmt.Errorf("imagegen: ComfyUI /history returned %s: %s", resp.Status, string(body))
	}

	var history map[string]comfyUIHistoryEntry
	if err := json.Unmarshal(body, &history); err != nil {
		return ref, false, fmt.Errorf("imagegen: parsing /history response: %w", err)
	}

	entry, ok := history[promptID]
	if !ok {
		return ref, false, nil // not finished yet
	}
	for _, output := range entry.Outputs {
		if len(output.Images) > 0 {
			return output.Images[0], true, nil
		}
	}
	// The job completed but produced no image output — a workflow whose
	// final node isn't a SaveImage-shaped node, most likely.
	if entry.Status.Completed {
		return ref, false, fmt.Errorf("imagegen: ComfyUI finished prompt %s but no node produced an image output — check that the workflow template ends in a SaveImage-shaped node", promptID)
	}
	return ref, false, nil
}

func (p *ComfyUIProvider) viewURL(ref comfyUIImageRef) string {
	v := url.Values{}
	v.Set("filename", ref.Filename)
	v.Set("subfolder", ref.Subfolder)
	v.Set("type", ref.Type)
	return p.baseURL + "/view?" + v.Encode()
}

// jsonEscapeForTemplate returns s encoded as the *contents* of a JSON
// string (no surrounding quotes) — for substituting directly into a
// template where the placeholder already sits between the template's
// own quote characters, e.g. `"text": "%%LAYFORGE_PROMPT%%"`.
func jsonEscapeForTemplate(s string) string {
	quoted, _ := json.Marshal(s) // never errors for a string input
	return strings.TrimSuffix(strings.TrimPrefix(string(quoted), `"`), `"`)
}
