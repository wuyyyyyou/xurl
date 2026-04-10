package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOAuth2TokenFileRequiresFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "token.json")
	content := `{
  "Client ID": "",
  "Client Secret": "secret",
  "Refresh Token": "refresh-token"
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	_, err := loadOAuth2TokenFile(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if got := err.Error(); got == "" || !containsAll(got, "Client ID", "missing required field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPersistOAuth2TokenFileUpdatesAccessToken(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "token.json")
	tokenFile := oauth2TokenFile{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RefreshToken: "refresh-token",
		AccessToken:  "access-token",
	}

	if err := persistOAuth2TokenFile(path, tokenFile); err != nil {
		t.Fatalf("persist token file: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}

	var decoded map[string]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal token file: %v", err)
	}

	if decoded["Client ID"] != "client-id" {
		t.Fatalf("unexpected client id: %q", decoded["Client ID"])
	}
	if decoded["Client Secret"] != "client-secret" {
		t.Fatalf("unexpected client secret: %q", decoded["Client Secret"])
	}
	if decoded["Refresh Token"] != "refresh-token" {
		t.Fatalf("unexpected refresh token: %q", decoded["Refresh Token"])
	}
	if decoded["Access Token"] != "access-token" {
		t.Fatalf("unexpected access token: %q", decoded["Access Token"])
	}
}

func TestShouldRefreshUserToken(t *testing.T) {
	t.Parallel()

	authFailure := commandResult{
		CommandSuccess: false,
		ParsedJSON: map[string]any{
			"status": float64(401),
		},
	}
	if !shouldRefreshUserToken(authFailure) {
		t.Fatal("expected 401 failure to trigger refresh")
	}

	unsupportedAuth := commandResult{
		CommandSuccess: false,
		ParsedJSON: map[string]any{
			"status": float64(403),
			"title":  "Unsupported Authentication",
		},
		Stdout: `{"status":403,"title":"Unsupported Authentication"}`,
	}
	if shouldRefreshUserToken(unsupportedAuth) {
		t.Fatal("did not expect 403 unsupported authentication to trigger refresh")
	}
}

func TestResolveAuthSelectionUsesTokenFileForUserCommands(t *testing.T) {
	t.Parallel()

	selection, err := resolveAuthSelection([]string{"timeline", "-n", "20"}, map[string]any{
		"context": map[string]any{
			"credentials": map[string]any{
				tokenFileCredential: "/tmp/token.json",
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve auth selection: %v", err)
	}

	if selection.mode != authModeUser {
		t.Fatalf("unexpected auth mode: %s", selection.mode)
	}
	if selection.tokenFilePath != "/tmp/token.json" {
		t.Fatalf("unexpected token file path: %q", selection.tokenFilePath)
	}
}

func TestResolveAuthSelectionSkipsAuthForVersion(t *testing.T) {
	t.Parallel()

	selection, err := resolveAuthSelection([]string{"version"}, map[string]any{})
	if err != nil {
		t.Fatalf("resolve auth selection: %v", err)
	}

	if selection.mode != authModeNone {
		t.Fatalf("unexpected auth mode: %s", selection.mode)
	}
}

func containsAll(input string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(input, part) {
			return false
		}
	}
	return true
}
