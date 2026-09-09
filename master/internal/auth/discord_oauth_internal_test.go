// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jamesplotts/layforge/master/internal/store"
)

// memAccountWriter is an in-memory accountWriter for the OAuth flow test.
type memAccountWriter struct {
	accounts map[string]store.Account // by ID
	sessions map[string]string        // token -> accountID
}

func newMemAccountWriter() *memAccountWriter {
	return &memAccountWriter{accounts: map[string]store.Account{}, sessions: map[string]string{}}
}

func (m *memAccountWriter) UpsertAccount(_ context.Context, a store.Account) (store.Account, error) {
	if existing, ok := m.accounts[a.ID]; ok {
		a.CreatedAt = existing.CreatedAt
	} else {
		a.CreatedAt = time.Now()
	}
	a.LastSeenAt = time.Now()
	m.accounts[a.ID] = a
	return a, nil
}

func (m *memAccountWriter) CreateOAuthSession(_ context.Context, token, accountID string, _ time.Time) error {
	m.sessions[token] = accountID
	return nil
}

func (m *memAccountWriter) DeleteOAuthSession(_ context.Context, token string) error {
	delete(m.sessions, token)
	return nil
}

// fakeDiscord stands in for Discord's token + user endpoints.
func fakeDiscord(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("code") == "" || r.FormValue("code_verifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "discord-access-tok", "token_type": "Bearer"})
	})
	mux.HandleFunc("/users/@me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer discord-access-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "80351110224678912", "username": "bram", "global_name": "Bram the Bold", "avatar": "abc",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestHandler(t *testing.T, s accountWriter) (*DiscordOAuthHandler, *httptest.Server) {
	t.Helper()
	discord := fakeDiscord(t)
	h := NewDiscordOAuthHandler("client-id", "client-secret",
		"http://master.test/auth/discord/callback", "http://web.test", s, nil)
	h.baseURLs = discordEndpoints{
		authorize: discord.URL + "/oauth2/authorize",
		token:     discord.URL + "/oauth2/token",
		user:      discord.URL + "/users/@me",
	}
	return h, discord
}

func TestDiscordOAuth_LoginCallback_MintsSession(t *testing.T) {
	mem := newMemAccountWriter()
	h, _ := newTestHandler(t, mem)
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)

	// No-redirect client so we can read Location headers.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	loginResp, err := client.Get(srv.URL + "/auth/discord/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("/login status = %d, want 302", loginResp.StatusCode)
	}
	authURL, _ := url.Parse(loginResp.Header.Get("Location"))
	state := authURL.Query().Get("state")
	if state == "" || authURL.Query().Get("code_challenge") == "" {
		t.Fatalf("/login redirect missing state or PKCE challenge: %s", authURL)
	}

	cbResp, err := client.Get(srv.URL + "/auth/discord/callback?code=the-code&state=" + state)
	if err != nil {
		t.Fatalf("GET /callback: %v", err)
	}
	cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusFound {
		t.Fatalf("/callback status = %d, want 302", cbResp.StatusCode)
	}
	loc := cbResp.Header.Get("Location")
	if !strings.HasPrefix(loc, "http://web.test/#") {
		t.Fatalf("/callback redirect = %q, want the web client with a fragment", loc)
	}
	frag, _ := url.ParseQuery(strings.TrimPrefix(loc, "http://web.test/#"))
	token := frag.Get("lf_token")
	if token == "" || frag.Get("lf_name") != "Bram the Bold" {
		t.Fatalf("/callback fragment = %v, want lf_token + lf_name", frag)
	}
	if mem.sessions[token] != "discord:80351110224678912" {
		t.Fatalf("session %q -> %q, want the discord account id", token, mem.sessions[token])
	}
	acct := mem.accounts["discord:80351110224678912"]
	if acct.DisplayName != "Bram the Bold" || acct.AvatarURL == "" {
		t.Fatalf("stored account = %+v, want display name + avatar", acct)
	}
}

func TestDiscordOAuth_Callback_BadState_400(t *testing.T) {
	h, _ := newTestHandler(t, newMemAccountWriter())
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := client.Get(srv.URL + "/auth/discord/callback?code=x&state=never-issued")
	if err != nil {
		t.Fatalf("GET /callback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("/callback with bogus state: status = %d, want 400", resp.StatusCode)
	}
}

func TestDiscordOAuth_Logout_DeletesSession(t *testing.T) {
	mem := newMemAccountWriter()
	mem.sessions["tok-1"] = "discord:1"
	h, _ := newTestHandler(t, mem)
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/auth/discord/logout", "application/json", strings.NewReader(`{"token":"tok-1"}`))
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/logout status = %d (%s), want 204", resp.StatusCode, body)
	}
	if _, ok := mem.sessions["tok-1"]; ok {
		t.Fatal("/logout did not delete the session")
	}
}

func TestDiscordOAuth_Enabled_NoSecret(t *testing.T) {
	h, _ := newTestHandler(t, newMemAccountWriter())
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/auth/discord/enabled")
	if err != nil {
		t.Fatalf("GET /enabled: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"enabled":true`) {
		t.Fatalf("/enabled = %d %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "client-secret") {
		t.Fatal("/enabled leaked the client secret")
	}
}
