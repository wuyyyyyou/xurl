package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/xdevplatform/xurl/api"
	"github.com/xdevplatform/xurl/auth"
	"github.com/xdevplatform/xurl/cli"
	"github.com/xdevplatform/xurl/config"
	"github.com/xdevplatform/xurl/version"
)

const (
	internalCLICommand   = "__xurl_cli"
	tokenFileCredential  = "X_OAUTH2_TOKEN_FILE"
	bearerCredentialName = "X_BEARER_TOKEN"
	userTokenEnvKey      = "XURL_EXECUTA_OAUTH2_ACCESS_TOKEN"
	bearerTokenEnvKey    = "XURL_EXECUTA_BEARER_TOKEN"
	defaultFilePerms     = 0o600
	tempFileSuffix       = ".tmp"
	authModeNone         = "none"
	authModeUser         = "user"
	authModeApp          = "app"
	executaName          = "xurl-executa"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

var manifest = map[string]any{
	"name":         executaName,
	"display_name": executaName,
	"version":      "1.0.0",
	"description":  "Run xurl commands from ANNA with a user OAuth2 token file and optional app-only bearer token.",
	"author":       "xdevplatform + ANNA",
	"credentials": []map[string]any{
		{
			"name":         tokenFileCredential,
			"display_name": "X OAuth2 Token File",
			"description":  "Path to a JSON file with Client ID, Client Secret, Refresh Token, and optional Access Token.",
			"required":     true,
			"sensitive":    false,
		},
		{
			"name":         bearerCredentialName,
			"display_name": "X App-Only Bearer Token",
			"description":  "App-only bearer token. Used automatically for search --scope all and other app-only search endpoints.",
			"required":     true,
			"sensitive":    true,
		},
	},
	"tools": []map[string]any{
		{
			"name":        "run_xurl",
			"description": "Run a non-streaming xurl command and write the full result to a JSON file in cwd.",
			"parameters": []map[string]any{
				{
					"name":        "args",
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "xurl command arguments, for example [\"search\", \"golang\", \"-n\", \"20\"].",
					"required":    true,
				},
				{
					"name":        "cwd",
					"type":        "string",
					"description": "Working directory and output directory. Defaults to the plugin binary directory.",
					"required":    false,
				},
			},
		},
	},
	"runtime": map[string]any{
		"type":        "binary",
		"min_version": "1.0.0",
	},
}

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
	ID      any            `json:"id"`
}

type rpcResponse struct {
	JSONRPC  string  `json:"jsonrpc"`
	ID       any     `json:"id"`
	Result   any     `json:"result,omitempty"`
	Error    *rpcErr `json:"error,omitempty"`
	FilePath string  `json:"-"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type fileTransportPointer struct {
	JSONRPC       string `json:"jsonrpc"`
	ID            any    `json:"id"`
	FileTransport string `json:"__file_transport"`
}

type commandResult struct {
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	Cwd            string   `json:"cwd"`
	ExecutedAt     string   `json:"executed_at"`
	FinishedAt     string   `json:"finished_at"`
	DurationMS     int64    `json:"duration_ms"`
	ExitCode       int      `json:"exit_code"`
	CommandSuccess bool     `json:"command_success"`
	Stdout         string   `json:"stdout"`
	Stderr         string   `json:"stderr"`
	ParsedJSON     any      `json:"parsed_json,omitempty"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == internalCLICommand {
		runEmbeddedCLI(os.Args[2:])
		return
	}
	runPlugin()
}

func runEmbeddedCLI(args []string) {
	cfg := config.NewConfig()
	a := auth.NewAuth(cfg)
	rootCmd := cli.CreateRootCommand(cfg, a)
	rootCmd.SetArgs(args)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runPlugin() {
	fmt.Fprintf(os.Stderr, "xurl executa plugin started on %s/%s\n", runtime.GOOS, runtime.GOARCH)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			sendResponse(rpcResponse{
				JSONRPC: "2.0",
				Error: &rpcErr{
					Code:    -32700,
					Message: "Parse error",
					Data:    err.Error(),
				},
			}, false)
			continue
		}

		sendResponse(handleRequest(req), req.Method == "invoke")
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "stdin scanner error: %v\n", err)
	}
}

func handleRequest(req rpcRequest) rpcResponse {
	switch req.Method {
	case "describe":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: manifest}
	case "health":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"status":      "healthy",
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
			"version":     version.Version,
			"tools_count": len(manifest["tools"].([]map[string]any)),
		}}
	case "invoke":
		return handleInvoke(req)
	default:
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcErr{
				Code:    -32601,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
		}
	}
}

func handleInvoke(req rpcRequest) rpcResponse {
	toolName, _ := req.Params["tool"].(string)
	response := rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}
	if toolName != "run_xurl" {
		response.Error = &rpcErr{
			Code:    -32601,
			Message: fmt.Sprintf("Unknown tool: %s", toolName),
			Data:    map[string]any{"available_tools": []string{"run_xurl"}},
		}
		return response
	}

	arguments, _ := req.Params["arguments"].(map[string]any)
	if arguments == nil {
		arguments = map[string]any{}
	}

	cwd, cwdErr := resolveCWD(arguments)
	if cwdErr == nil {
		response.FilePath = buildInvokeResponsePath(cwd)
	}

	args, err := getArgs(arguments)
	if err != nil {
		response.Error = &rpcErr{
			Code:    -32602,
			Message: err.Error(),
		}
		return response
	}

	if err := validateArgs(args); err != nil {
		response.Error = &rpcErr{
			Code:    -32602,
			Message: err.Error(),
		}
		return response
	}

	if cwdErr != nil {
		response.Error = &rpcErr{
			Code:    -32602,
			Message: cwdErr.Error(),
		}
		return response
	}

	authSelection, err := resolveAuthSelection(args, req.Params)
	if err != nil {
		response.Error = &rpcErr{
			Code:    -32602,
			Message: err.Error(),
		}
		return response
	}

	result, err := runXURLCommand(args, cwd, authSelection)
	if err != nil {
		response.Error = &rpcErr{
			Code:    -32603,
			Message: "Failed to execute xurl command",
			Data: map[string]any{
				"error": err.Error(),
			},
		}
		return response
	}

	if !result.CommandSuccess {
		response.Error = &rpcErr{
			Code:    -32001,
			Message: "xurl command failed",
			Data:    result,
		}
		return response
	}

	response.Result = map[string]any{
		"success": true,
		"tool":    toolName,
		"data":    result,
	}
	return response
}

func buildInvokeResponsePath(cwd string) string {
	return filepath.Join(cwd, fmt.Sprintf("executa-response-%d.json", time.Now().UTC().UnixNano()))
}

func getArgs(arguments map[string]any) ([]string, error) {
	rawArgs, ok := arguments["args"].([]any)
	if !ok || len(rawArgs) == 0 {
		return nil, fmt.Errorf("arguments.args must be a non-empty string array")
	}

	args := make([]string, 0, len(rawArgs))
	for _, raw := range rawArgs {
		value, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("arguments.args must contain only strings")
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("arguments.args cannot contain empty strings")
		}
		args = append(args, value)
	}
	return args, nil
}

func validateArgs(args []string) error {
	for _, arg := range args {
		if arg == "--verbose" || arg == "-v" {
			return fmt.Errorf("verbose mode is not supported because it may expose credentials")
		}
		if arg == "--stream" || arg == "-s" {
			return fmt.Errorf("streaming commands are not supported")
		}
		if strings.HasPrefix(arg, "/") || strings.HasPrefix(strings.ToLower(arg), "http://") || strings.HasPrefix(strings.ToLower(arg), "https://") {
			if api.IsStreamingEndpoint(arg) {
				return fmt.Errorf("streaming endpoints are not supported")
			}
		}
	}

	switch args[0] {
	case "auth":
		return fmt.Errorf("auth commands are not supported in the plugin")
	case "webhook":
		return fmt.Errorf("webhook commands are not supported in the plugin")
	}

	return nil
}

func resolveCWD(arguments map[string]any) (string, error) {
	if raw, ok := arguments["cwd"].(string); ok && strings.TrimSpace(raw) != "" {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return "", fmt.Errorf("failed to resolve cwd: %w", err)
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", fmt.Errorf("failed to create cwd: %w", err)
		}
		return abs, nil
	}

	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to resolve executable path: %w", err)
	}
	return filepath.Dir(execPath), nil
}

type authSelection struct {
	mode          string
	token         string
	tokenFilePath string
}

type oauth2TokenFile struct {
	ClientID     string `json:"Client ID"`
	ClientSecret string `json:"Client Secret"`
	AccessToken  string `json:"Access Token,omitempty"`
	RefreshToken string `json:"Refresh Token"`
}

func resolveTokenFilePath(params map[string]any) string {
	context, _ := params["context"].(map[string]any)
	if context != nil {
		credentials, _ := context["credentials"].(map[string]any)
		if credentials != nil {
			if tokenFilePath, ok := credentials[tokenFileCredential].(string); ok {
				return strings.TrimSpace(tokenFilePath)
			}
		}
	}
	return strings.TrimSpace(os.Getenv(tokenFileCredential))
}

func resolveBearerToken(params map[string]any) string {
	context, _ := params["context"].(map[string]any)
	if context != nil {
		credentials, _ := context["credentials"].(map[string]any)
		if credentials != nil {
			if token, ok := credentials[bearerCredentialName].(string); ok {
				return strings.TrimSpace(token)
			}
		}
	}
	return strings.TrimSpace(os.Getenv(bearerCredentialName))
}

func resolveAuthSelection(args []string, params map[string]any) (authSelection, error) {
	if !requiresAuth(args) {
		return authSelection{mode: authModeNone}, nil
	}

	if requiresAppOnlyAuth(args) {
		token := resolveBearerToken(params)
		if token == "" {
			return authSelection{}, fmt.Errorf("missing credential %q for app-only search", bearerCredentialName)
		}
		return authSelection{mode: authModeApp, token: token}, nil
	}

	tokenFilePath := resolveTokenFilePath(params)
	if tokenFilePath == "" {
		return authSelection{}, fmt.Errorf("missing credential %q", tokenFileCredential)
	}
	return authSelection{mode: authModeUser, tokenFilePath: tokenFilePath}, nil
}

func requiresAppOnlyAuth(args []string) bool {
	if len(args) == 0 {
		return false
	}

	if args[0] == "search" {
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "--scope" && i+1 < len(args) && args[i+1] == "all" {
				return true
			}
			if arg == "--scope=all" {
				return true
			}
		}
	}

	if args[0] == "trends" {
		if len(args) > 1 {
			target := strings.ToLower(args[1])
			return target != "personal" && target != "personalized"
		}
		return true
	}

	if args[0] == "news" {
		return true
	}

	for _, arg := range args {
		if strings.Contains(arg, "/2/tweets/search/all") ||
			strings.Contains(arg, "/2/trends/by/woeid/") ||
			strings.Contains(arg, "/2/news/search") {
			return true
		}
	}

	return false
}

func requiresAuth(args []string) bool {
	if len(args) == 0 {
		return false
	}

	switch args[0] {
	case "version", "help", "completion":
		return false
	}

	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return false
		}
	}

	return true
}

func runXURLCommand(args []string, cwd string, auth authSelection) (commandResult, error) {
	var (
		result commandResult
		err    error
	)

	switch auth.mode {
	case authModeNone:
		result, err = executeXURLCommand(args, cwd, nil, nil)
	case authModeApp:
		result, err = executeXURLCommand(args, cwd, map[string]string{bearerTokenEnvKey: auth.token}, []string{auth.token})
	case authModeUser:
		result, err = executeUserXURLCommand(args, cwd, auth.tokenFilePath)
	default:
		err = fmt.Errorf("unsupported auth mode: %s", auth.mode)
	}
	if err != nil {
		return commandResult{}, err
	}

	return result, nil
}

func executeUserXURLCommand(args []string, cwd string, tokenFilePath string) (commandResult, error) {
	tokenFile, err := loadOAuth2TokenFile(tokenFilePath)
	if err != nil {
		return commandResult{}, err
	}

	if tokenFile.AccessToken == "" {
		tokenFile, err = refreshOAuth2TokenFile(tokenFilePath, tokenFile)
		if err != nil {
			return commandResult{}, fmt.Errorf("failed to refresh access token: %w", err)
		}
	}

	result, err := executeXURLCommand(
		args,
		cwd,
		map[string]string{userTokenEnvKey: tokenFile.AccessToken},
		[]string{tokenFile.AccessToken, tokenFile.RefreshToken, tokenFile.ClientSecret},
	)
	if err != nil {
		return commandResult{}, err
	}

	if !shouldRefreshUserToken(result) {
		return result, nil
	}

	refreshedTokenFile, refreshErr := refreshOAuth2TokenFile(tokenFilePath, tokenFile)
	if refreshErr != nil {
		result.Stderr = appendDiagnostic(result.Stderr, fmt.Sprintf("plugin refresh attempt failed: %v", refreshErr))
		return result, nil
	}

	retriedResult, retryErr := executeXURLCommand(
		args,
		cwd,
		map[string]string{userTokenEnvKey: refreshedTokenFile.AccessToken},
		[]string{refreshedTokenFile.AccessToken, refreshedTokenFile.RefreshToken, refreshedTokenFile.ClientSecret},
	)
	if retryErr != nil {
		return commandResult{}, retryErr
	}
	if !retriedResult.CommandSuccess && shouldRefreshUserToken(retriedResult) {
		retriedResult.Stderr = appendDiagnostic(retriedResult.Stderr, "plugin refreshed the OAuth2 access token and retried once, but the X API still returned an authorization error")
	}
	return retriedResult, nil
}

func executeXURLCommand(args []string, cwd string, envVars map[string]string, secrets []string) (commandResult, error) {
	executable, err := os.Executable()
	if err != nil {
		return commandResult{}, fmt.Errorf("failed to resolve plugin executable: %w", err)
	}

	cmdArgs := append([]string{internalCLICommand}, args...)
	cmd := exec.Command(executable, cmdArgs...)
	cmd.Dir = cwd
	cmd.Env = append([]string{}, os.Environ()...)
	for key, value := range envVars {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	started := time.Now().UTC()
	runErr := cmd.Run()
	finished := time.Now().UTC()

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	cleanStdout := redactSecrets(stripANSI(stdoutBuf.String()), secrets...)
	cleanStderr := redactSecrets(stripANSI(stderrBuf.String()), secrets...)

	result := commandResult{
		Command:        strings.Join(args, " "),
		Args:           args,
		Cwd:            cwd,
		ExecutedAt:     started.Format(time.RFC3339),
		FinishedAt:     finished.Format(time.RFC3339),
		DurationMS:     finished.Sub(started).Milliseconds(),
		ExitCode:       exitCode,
		CommandSuccess: runErr == nil,
		Stdout:         cleanStdout,
		Stderr:         cleanStderr,
	}

	if parsed := parseJSON(cleanStdout); parsed != nil {
		result.ParsedJSON = parsed
	}

	return result, nil
}

func parseJSON(input string) any {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	var parsed any
	if err := json.Unmarshal([]byte(input), &parsed); err != nil {
		return nil
	}
	return parsed
}

func stripANSI(input string) string {
	return ansiPattern.ReplaceAllString(input, "")
}

func redactSecrets(input string, secrets ...string) string {
	output := input
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		output = strings.ReplaceAll(output, secret, "[REDACTED]")
	}
	return output
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func loadOAuth2TokenFile(path string) (oauth2TokenFile, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return oauth2TokenFile{}, fmt.Errorf("failed to resolve token file path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return oauth2TokenFile{}, fmt.Errorf("failed to read token file %q: %w", absPath, err)
	}

	var tokenFile oauth2TokenFile
	if err := json.Unmarshal(data, &tokenFile); err != nil {
		return oauth2TokenFile{}, fmt.Errorf("failed to parse token file %q: %w", absPath, err)
	}

	tokenFile.ClientID = strings.TrimSpace(tokenFile.ClientID)
	tokenFile.ClientSecret = strings.TrimSpace(tokenFile.ClientSecret)
	tokenFile.AccessToken = strings.TrimSpace(tokenFile.AccessToken)
	tokenFile.RefreshToken = strings.TrimSpace(tokenFile.RefreshToken)

	switch {
	case tokenFile.ClientID == "":
		return oauth2TokenFile{}, fmt.Errorf("token file %q is missing required field %q", absPath, "Client ID")
	case tokenFile.ClientSecret == "":
		return oauth2TokenFile{}, fmt.Errorf("token file %q is missing required field %q", absPath, "Client Secret")
	case tokenFile.RefreshToken == "":
		return oauth2TokenFile{}, fmt.Errorf("token file %q is missing required field %q", absPath, "Refresh Token")
	}

	return tokenFile, nil
}

func refreshOAuth2TokenFile(path string, tokenFile oauth2TokenFile) (oauth2TokenFile, error) {
	cfg := config.NewConfig()
	oauthCfg := &oauth2.Config{
		ClientID:     tokenFile.ClientID,
		ClientSecret: tokenFile.ClientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL: cfg.TokenURL,
		},
	}

	// Force a refresh-token exchange. The token file does not persist expiry
	// metadata, so reusing the current access token here can prevent refresh.
	tokenSource := oauthCfg.TokenSource(context.Background(), expiredOAuth2Token(tokenFile))

	refreshedToken, err := tokenSource.Token()
	if err != nil {
		return oauth2TokenFile{}, fmt.Errorf("refresh token exchange failed: %w", err)
	}
	if strings.TrimSpace(refreshedToken.AccessToken) == "" {
		return oauth2TokenFile{}, fmt.Errorf("refresh token exchange returned an empty access token")
	}

	tokenFile.AccessToken = strings.TrimSpace(refreshedToken.AccessToken)
	if refreshedRefreshToken := strings.TrimSpace(refreshedToken.RefreshToken); refreshedRefreshToken != "" {
		tokenFile.RefreshToken = refreshedRefreshToken
	}

	if err := persistOAuth2TokenFile(path, tokenFile); err != nil {
		return oauth2TokenFile{}, err
	}

	return tokenFile, nil
}

func expiredOAuth2Token(tokenFile oauth2TokenFile) *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  tokenFile.AccessToken,
		RefreshToken: tokenFile.RefreshToken,
		Expiry:       time.Now().Add(-1 * time.Hour),
	}
}

func persistOAuth2TokenFile(path string, tokenFile oauth2TokenFile) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve token file path: %w", err)
	}

	data, err := json.MarshalIndent(tokenFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize token file %q: %w", absPath, err)
	}

	tempPath := absPath + tempFileSuffix
	if err := os.WriteFile(tempPath, append(data, '\n'), defaultFilePerms); err != nil {
		return fmt.Errorf("failed to write token file %q: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, absPath); err != nil {
		return fmt.Errorf("failed to replace token file %q: %w", absPath, err)
	}

	return nil
}

func shouldRefreshUserToken(result commandResult) bool {
	if result.CommandSuccess {
		return false
	}

	if status, ok := extractStatusCode(result.ParsedJSON); ok && status == 401 {
		return true
	}

	combined := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(combined, "expired token") ||
		strings.Contains(combined, "invalid or expired") ||
		strings.Contains(combined, "unauthorized")
}

func extractStatusCode(parsed any) (int, bool) {
	payload, ok := parsed.(map[string]any)
	if !ok {
		return 0, false
	}

	switch value := payload["status"].(type) {
	case float64:
		return int(value), true
	case int:
		return value, true
	case json.Number:
		number, err := value.Int64()
		if err != nil {
			return 0, false
		}
		return int(number), true
	default:
		return 0, false
	}
}

func appendDiagnostic(input string, message string) string {
	if strings.TrimSpace(input) == "" {
		return message
	}
	return input + "\n" + message
}

func sendResponse(resp rpcResponse, useFileTransport bool) {
	if !useFileTransport {
		payload, err := json.Marshal(resp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to marshal response: %v\n", err)
			return
		}
		writer := bufio.NewWriter(os.Stdout)
		if _, err := writer.Write(append(payload, '\n')); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write response: %v\n", err)
			return
		}
		if err := writer.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to flush stdout: %v\n", err)
		}
		return
	}

	targetPath := strings.TrimSpace(resp.FilePath)
	if targetPath == "" {
		targetPath = filepath.Join(os.TempDir(), fmt.Sprintf("executa-resp-%d.json", time.Now().UnixNano()))
	}

	if err := writeJSONFile(targetPath, resp); err != nil {
		fallback := rpcResponse{
			JSONRPC: "2.0",
			ID:      resp.ID,
			Error: &rpcErr{
				Code:    -32603,
				Message: "Failed to write file transport response",
				Data:    err.Error(),
			},
		}
		if payload, marshalErr := json.Marshal(fallback); marshalErr == nil {
			fmt.Fprintln(os.Stdout, string(payload))
		}
		return
	}

	writeFileTransportPointer(resp.ID, targetPath)
}

func writeFileTransportPointer(id any, path string) {
	pointer := fileTransportPointer{
		JSONRPC:       "2.0",
		ID:            id,
		FileTransport: path,
	}
	payload, err := json.Marshal(pointer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal file transport pointer: %v\n", err)
		return
	}

	writer := bufio.NewWriter(os.Stdout)
	if _, err := writer.Write(append(payload, '\n')); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write file transport pointer: %v\n", err)
		return
	}
	if err := writer.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to flush stdout: %v\n", err)
	}
}
