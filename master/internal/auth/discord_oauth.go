// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jamesplotts/layforge/master/internal/store"
)

// Discord OAuth2 / API endpoints (design doc §6.6). Overridable in tests
// via DiscordOAuthHandler.baseURLs.
const (
	discordAuthorizeURL = "https://discord.com/oauth2/authorize"
	discordTokenURL     = "https://discord.com/api/oauth2/token"
	discordUserURL      = "https://discord.com/api/users/@me"
	// discordScope: "identify" gives the account's id, username and
	// avatar — everything store.Account needs, nothing more.
	discordScope = "identify"
)

// sessionTTL is how long a minted login session is valid. Sessions are
// not refreshed — an expired one means the player clicks "Log in with
// Discord" again, one round-trip.
const sessionTTL = 30 * 24 * time.Hour

// oauthStateTTL bounds how long a login may sit between /login and
// /callback before its CSRF state is discarded.
const oauthStateTTL = 10 * time.Minute

// accountWriter is the slice of store.AccountStore the handler needs to
// complete a login. Declared at the point of consumption.
type accountWriter interface {
	UpsertAccount(ctx context.Context, account store.Account) (store.Account, error)
	CreateOAuthSession(ctx context.Context, token, accountID string, expiresAt time.Time) error
	DeleteOAuthSession(ctx context.Context, token string) error
}

// DiscordOAuthHandler serves the browser-facing half of Discord login:
// /auth/discord/login redirects to Discord, /auth/discord/callback
// completes the code exchange and mints a session token, /logout revokes
// one, /enabled is a no-secret probe the web client uses to decide
// whether to show the button.
//
// The client secret lives only in this struct and in the outbound
// token-exchange request body — never in a redirect, a log line, or any
// response body (CLAUDE.md: Master never leaks secrets to clients).
type DiscordOAuthHandler struct {
	clientID     string
	clientSecret string
	redirectURL  string
	// webClientURL is where the browser is sent after login completes —
	// the reference client's origin. The minted token rides back in the
	// URL fragment so it never reaches an access log or a Referer header.
	webClientURL string

	store      accountWriter
	httpClient *http.Client
	logger     *slog.Logger

	// baseURLs lets tests point the token / user calls at an httptest
	// server. Zero value means the real Discord endpoints.
	baseURLs discordEndpoints

	mu    sync.Mutex
	state map[string]oauthState
}

type discordEndpoints struct {
	authorize string
	token     string
	user      string
}

type oauthState struct {
	verifier  string
	createdAt time.Time
}

// NewDiscordOAuthHandler creates a handler. redirectURL must be the exact
// callback URL registered in the Discord developer portal
// (<origin>/auth/discord/callback). webClientURL is where a completed
// login bounces the browser; pass "" to use redirectURL's own origin,
// which is the right default — Discord just redirected the browser there,
// so it is by definition an address the browser can reach (unlike a
// -addr-derived "localhost" URL when the player is on another machine).
func NewDiscordOAuthHandler(clientID, clientSecret, redirectURL, webClientURL string, s accountWriter, logger *slog.Logger) *DiscordOAuthHandler {
	if logger == nil {
		logger = slog.Default()
	}
	if webClientURL == "" {
		webClientURL = originOf(redirectURL)
	}
	return &DiscordOAuthHandler{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		webClientURL: strings.TrimRight(webClientURL, "/"),
		store:        s,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		logger:       logger,
		state:        make(map[string]oauthState),
	}
}

// originOf returns the scheme://host of a URL (no path/query), or "" if
// it doesn't parse into an absolute URL with a host.
func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Routes returns the handler's mux, meant to be mounted at
// /auth/discord/ on Master's client-facing listener.
func (h *DiscordOAuthHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/discord/enabled", h.handleEnabled)
	mux.HandleFunc("GET /auth/discord/login", h.handleLogin)
	mux.HandleFunc("GET /auth/discord/callback", h.handleCallback)
	mux.HandleFunc("POST /auth/discord/logout", h.handleLogout)
	return mux
}

func (h *DiscordOAuthHandler) endpoints() discordEndpoints {
	e := discordEndpoints{authorize: discordAuthorizeURL, token: discordTokenURL, user: discordUserURL}
	if h.baseURLs.authorize != "" {
		e.authorize = h.baseURLs.authorize
	}
	if h.baseURLs.token != "" {
		e.token = h.baseURLs.token
	}
	if h.baseURLs.user != "" {
		e.user = h.baseURLs.user
	}
	return e
}

func (h *DiscordOAuthHandler) handleEnabled(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"enabled":true}`))
}

func (h *DiscordOAuthHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	stateTok := randToken(16)
	verifier := randToken(32)

	h.mu.Lock()
	h.pruneStateLocked()
	h.state[stateTok] = oauthState{verifier: verifier, createdAt: time.Now()}
	h.mu.Unlock()

	challenge := pkceChallenge(verifier)
	q := url.Values{}
	q.Set("client_id", h.clientID)
	q.Set("redirect_uri", h.redirectURL)
	q.Set("response_type", "code")
	q.Set("scope", discordScope)
	q.Set("state", stateTok)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("prompt", "none")

	http.Redirect(w, r, h.endpoints().authorize+"?"+q.Encode(), http.StatusFound)
}

func (h *DiscordOAuthHandler) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if derr := q.Get("error"); derr != "" {
		// The user declined consent, or Discord refused — not an error
		// on our side.
		h.renderError(w, http.StatusOK, "Discord login was cancelled.")
		return
	}
	code := q.Get("code")
	stateTok := q.Get("state")
	if code == "" || stateTok == "" {
		h.renderError(w, http.StatusBadRequest, "Discord login response was missing its code or state.")
		return
	}

	h.mu.Lock()
	h.pruneStateLocked()
	st, ok := h.state[stateTok]
	if ok {
		delete(h.state, stateTok)
	}
	h.mu.Unlock()
	if !ok {
		h.renderError(w, http.StatusBadRequest, "This login link has expired or was already used. Start again.")
		return
	}

	tok, err := h.exchangeCode(r.Context(), code, st.verifier)
	if err != nil {
		h.logger.Warn("discord token exchange failed", "error", err)
		h.renderError(w, http.StatusBadGateway, "Could not complete the Discord login. Try again.")
		return
	}
	du, err := h.fetchUser(r.Context(), tok)
	if err != nil {
		h.logger.Warn("discord user fetch failed", "error", err)
		h.renderError(w, http.StatusBadGateway, "Could not read your Discord profile. Try again.")
		return
	}
	if du.ID == "" {
		h.renderError(w, http.StatusBadGateway, "Discord returned an incomplete profile. Try again.")
		return
	}

	account := store.Account{
		ID:             "discord:" + du.ID,
		Provider:       store.AuthProviderKindDiscord,
		ProviderUserID: du.ID,
		DisplayName:    du.displayName(),
		AvatarURL:      du.avatarURL(),
	}
	stored, err := h.store.UpsertAccount(r.Context(), account)
	if err != nil {
		h.logger.Error("discord login: upsert account", "error", err)
		h.renderError(w, http.StatusInternalServerError, "Could not save your account. Try again.")
		return
	}

	sessionToken := randToken(32)
	if err := h.store.CreateOAuthSession(r.Context(), sessionToken, stored.ID, time.Now().Add(sessionTTL)); err != nil {
		h.logger.Error("discord login: create session", "error", err)
		h.renderError(w, http.StatusInternalServerError, "Could not start your session. Try again.")
		return
	}
	h.logger.Info("discord login", "account_id", stored.ID, "display_name", stored.DisplayName)

	frag := url.Values{}
	frag.Set("lf_token", sessionToken)
	frag.Set("lf_name", stored.DisplayName)
	http.Redirect(w, r, h.webClientURL+"/#"+frag.Encode(), http.StatusFound)
}

func (h *DiscordOAuthHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil || body.Token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	if err := h.store.DeleteOAuthSession(r.Context(), body.Token); err != nil {
		h.logger.Warn("discord logout: delete session", "error", err)
		http.Error(w, "could not log out", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// exchangeCode swaps an authorization code (+ PKCE verifier) for a
// Discord access token.
func (h *DiscordOAuthHandler) exchangeCode(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{}
	form.Set("client_id", h.clientID)
	form.Set("client_secret", h.clientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", h.redirectURL)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpoints().token, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("token endpoint status %d", resp.StatusCode)
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(payload, &tr); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("token response had no access_token")
	}
	return tr.AccessToken, nil
}

// discordUser is the subset of Discord's /users/@me we read.
type discordUser struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	GlobalName    string `json:"global_name"`
	Discriminator string `json:"discriminator"`
	Avatar        string `json:"avatar"`
}

func (u discordUser) displayName() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

func (u discordUser) avatarURL() string {
	if u.Avatar == "" || u.ID == "" {
		return ""
	}
	return fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png", u.ID, u.Avatar)
}

func (h *DiscordOAuthHandler) fetchUser(ctx context.Context, accessToken string) (discordUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.endpoints().user, nil)
	if err != nil {
		return discordUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return discordUser{}, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return discordUser{}, fmt.Errorf("user endpoint status %d", resp.StatusCode)
	}
	var du discordUser
	if err := json.Unmarshal(payload, &du); err != nil {
		return discordUser{}, fmt.Errorf("decoding user response: %w", err)
	}
	return du, nil
}

// renderError writes a minimal HTML page with a link back to the web
// client. It never includes secret material or raw upstream error
// bodies — msg is always a fixed, caller-chosen string.
func (h *DiscordOAuthHandler) renderError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	back := h.webClientURL
	if back == "" {
		back = "/"
	}
	fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><title>Discord login</title>`+
		`<body style="font:15px system-ui;margin:3rem auto;max-width:32rem;padding:0 1rem">`+
		`<p>%s</p><p><a href="%s">Back to Layforge</a></p>`,
		html.EscapeString(msg), html.EscapeString(back))
}

func (h *DiscordOAuthHandler) pruneStateLocked() {
	cutoff := time.Now().Add(-oauthStateTTL)
	for k, v := range h.state {
		if v.createdAt.Before(cutoff) {
			delete(h.state, k)
		}
	}
}

// randToken returns nBytes of crypto-random data as a hex string.
func randToken(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read only fails on a broken platform RNG; there is
		// no safe fallback for security tokens, so fail loudly.
		panic("auth: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// pkceChallenge is the S256 code challenge for a verifier (RFC 7636):
// base64url(sha256(verifier)), no padding.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
