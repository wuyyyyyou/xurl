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
	internalCLICommand         = "__xurl_cli"
	tokenFileCredential        = "X_OAUTH2_TOKEN_FILE"
	oauth1TokenFileCredential  = "X_OAUTH1_TOKEN_FILE"
	bearerCredentialName       = "X_BEARER_TOKEN"
	userTokenEnvKey            = "XURL_EXECUTA_OAUTH2_ACCESS_TOKEN"
	bearerTokenEnvKey          = "XURL_EXECUTA_BEARER_TOKEN"
	oauth1ConsumerKeyEnvKey    = "XURL_EXECUTA_OAUTH1_CONSUMER_KEY"
	oauth1ConsumerSecretEnvKey = "XURL_EXECUTA_OAUTH1_CONSUMER_SECRET"
	oauth1AccessTokenEnvKey    = "XURL_EXECUTA_OAUTH1_ACCESS_TOKEN"
	oauth1TokenSecretEnvKey    = "XURL_EXECUTA_OAUTH1_TOKEN_SECRET"
	adsAPIBaseURL              = "https://ads-api.x.com"
	defaultFilePerms           = 0o600
	tempFileSuffix             = ".tmp"
	authModeNone               = "none"
	authModeUser               = "user"
	authModeApp                = "app"
	authModeOAuth1             = "oauth1"
	executaName                = "tool-lightvoss_5433-xurl-executa-6rbgfeke"
	executaVersion             = "1.0.0"
	outputJSONPathArgument     = "output_json_path"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

var manifest = map[string]any{
	"name":         executaName,
	"display_name": "xurl-executa",
	"version":      executaVersion,
	"description":  "Run xurl commands from ANNA with OAuth2, app-only bearer, and OAuth1 token files.",
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
			"name":         oauth1TokenFileCredential,
			"display_name": "X OAuth1 Token File",
			"description":  "Path to a JSON file with Consumer Key, Consumer Key Secret, Access Token, and Access Token Secret. Used automatically for Ads API requests.",
			"required":     false,
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
			"description": "Run a non-streaming xurl command. Large default responses use file transport; optional output_json_path writes result data to a JSON file.",
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
				{
					"name":        outputJSONPathArgument,
					"type":        "string",
					"description": "Optional JSON file path for result.data. When set, the file is overwritten and stdout returns only a JSON-RPC summary with the output path.",
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
	JSONRPC          string  `json:"jsonrpc"`
	ID               any     `json:"id"`
	Result           any     `json:"result,omitempty"`
	Error            *rpcErr `json:"error,omitempty"`
	FilePath         string  `json:"-"`
	UseFileTransport bool    `json:"-"`
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
	Diagnostic     any      `json:"diagnostic,omitempty"`
}

type commandDiagnostic struct {
	Kind              string   `json:"kind"`
	Message           string   `json:"message"`
	InvalidCommand    string   `json:"invalid_command,omitempty"`
	SuggestedCommands []string `json:"suggested_commands,omitempty"`
	UsageHint         string   `json:"usage_hint,omitempty"`
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
			})
			continue
		}

		sendResponse(handleRequest(req))
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
		JSONRPC:          "2.0",
		ID:               req.ID,
		UseFileTransport: true,
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
	if hasOutputJSONPath(arguments) {
		response.UseFileTransport = false
	}

	cwd, cwdErr := resolveCWD(arguments)
	outputJSONPath := ""
	if cwdErr == nil {
		response.FilePath = buildInvokeResponsePath(cwd)
		var outputPathErr error
		outputJSONPath, outputPathErr = resolveOutputJSONPath(arguments, cwd)
		if outputPathErr != nil {
			response.Error = &rpcErr{
				Code:    -32602,
				Message: outputPathErr.Error(),
			}
			return response
		}
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

	if diagnostic := diagnoseArgs(args); diagnostic != nil {
		response.Error = &rpcErr{
			Code:    -32602,
			Message: diagnostic.Message,
			Data: map[string]any{
				"command":    strings.Join(args, " "),
				"args":       args,
				"diagnostic": diagnostic,
			},
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

	responseData := any(result)
	if outputJSONPath != "" {
		if err := writeJSONFile(outputJSONPath, result); err != nil {
			response.Error = &rpcErr{
				Code:    -32603,
				Message: "Failed to write output JSON file",
				Data: map[string]any{
					"output_json_path": outputJSONPath,
					"error":            err.Error(),
				},
			}
			return response
		}
		responseData = map[string]any{
			"output_json_path": outputJSONPath,
		}
	}

	response.Result = map[string]any{
		"success": true,
		"tool":    toolName,
		"data":    responseData,
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

func hasOutputJSONPath(arguments map[string]any) bool {
	raw, ok := arguments[outputJSONPathArgument].(string)
	return ok && strings.TrimSpace(raw) != ""
}

func resolveOutputJSONPath(arguments map[string]any, cwd string) (string, error) {
	raw, ok := arguments[outputJSONPathArgument].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return "", nil
	}

	outputPath := strings.TrimSpace(raw)
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(cwd, outputPath)
	}
	abs, err := filepath.Abs(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %s: %w", outputJSONPathArgument, err)
	}

	parent := filepath.Dir(abs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("failed to create %s parent directory: %w", outputJSONPathArgument, err)
	}

	return abs, nil
}

type authSelection struct {
	mode            string
	token           string
	tokenFilePath   string
	oauth1TokenFile oauth1TokenFile
}

type oauth2TokenFile struct {
	ClientID     string `json:"Client ID"`
	ClientSecret string `json:"Client Secret"`
	AccessToken  string `json:"Access Token,omitempty"`
	RefreshToken string `json:"Refresh Token"`
}

type oauth1TokenFile struct {
	ConsumerKey       string `json:"Consumer Key"`
	ConsumerKeySecret string `json:"Consumer Key Secret"`
	AccessToken       string `json:"Access Token"`
	AccessTokenSecret string `json:"Access Token Secret"`
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

func resolveOAuth1TokenFilePath(params map[string]any) string {
	context, _ := params["context"].(map[string]any)
	if context != nil {
		credentials, _ := context["credentials"].(map[string]any)
		if credentials != nil {
			if tokenFilePath, ok := credentials[oauth1TokenFileCredential].(string); ok {
				return strings.TrimSpace(tokenFilePath)
			}
		}
	}
	return strings.TrimSpace(os.Getenv(oauth1TokenFileCredential))
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

	if requiresOAuth1Auth(args) {
		tokenFilePath := resolveOAuth1TokenFilePath(params)
		if tokenFilePath == "" {
			return authSelection{}, fmt.Errorf("missing credential %q for Ads API", oauth1TokenFileCredential)
		}
		tokenFile, err := loadOAuth1TokenFile(tokenFilePath)
		if err != nil {
			return authSelection{}, err
		}
		return authSelection{mode: authModeOAuth1, tokenFilePath: tokenFilePath, oauth1TokenFile: tokenFile}, nil
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

func requiresOAuth1Auth(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(strings.TrimSpace(arg))
		if strings.Contains(lower, "ads-api.x.com") || strings.Contains(lower, "ads-api.twitter.com") {
			return true
		}
		if isAdsAPIRelativePath(lower) {
			return true
		}
	}
	return false
}

func isAdsAPIRelativePath(arg string) bool {
	if !strings.HasPrefix(arg, "/") {
		return false
	}

	version, _, _ := strings.Cut(strings.TrimPrefix(arg, "/"), "/")
	if version == "" || version == "2" {
		return false
	}
	for _, ch := range version {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
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
			strings.Contains(arg, "/2/tweets/counts/recent") ||
			strings.Contains(arg, "/2/tweets/counts/all") ||
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
	case authModeOAuth1:
		result, err = executeXURLCommand(args, cwd, oauth1EnvVars(auth.oauth1TokenFile), oauth1Secrets(auth.oauth1TokenFile))
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

func oauth1EnvVars(tokenFile oauth1TokenFile) map[string]string {
	return map[string]string{
		oauth1ConsumerKeyEnvKey:    tokenFile.ConsumerKey,
		oauth1ConsumerSecretEnvKey: tokenFile.ConsumerKeySecret,
		oauth1AccessTokenEnvKey:    tokenFile.AccessToken,
		oauth1TokenSecretEnvKey:    tokenFile.AccessTokenSecret,
		"API_BASE_URL":             adsAPIBaseURL,
	}
}

func oauth1Secrets(tokenFile oauth1TokenFile) []string {
	return []string{
		tokenFile.ConsumerKey,
		tokenFile.ConsumerKeySecret,
		tokenFile.AccessToken,
		tokenFile.AccessTokenSecret,
	}
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

	if !result.CommandSuccess {
		result.Diagnostic = diagnoseFailedCommand(result)
	}

	return result, nil
}

func diagnoseArgs(args []string) *commandDiagnostic {
	if len(args) == 0 {
		return nil
	}

	first := args[0]
	if isRawEndpoint(first) || strings.HasPrefix(first, "-") || isKnownCommand(first) {
		return nil
	}

	return &commandDiagnostic{
		Kind:              "invalid_xurl_command",
		Message:           fmt.Sprintf("%q is not a supported xurl shortcut command; use a known shortcut command or a raw X API endpoint", first),
		InvalidCommand:    strings.Join(args, " "),
		SuggestedCommands: suggestCommands(first),
		UsageHint:         "Examples: [\"whoami\"], [\"user\", \"twitterdev\"], [\"/2/users/me\"], or [\"/2/users/by/username/twitterdev\"]. Use [\"help\"] to list commands.",
	}
}

func diagnoseFailedCommand(result commandResult) *commandDiagnostic {
	if diagnostic := diagnoseArgs(result.Args); diagnostic != nil {
		return diagnostic
	}

	output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	if output == "" {
		return nil
	}

	if strings.Contains(output, "Error: request failed") {
		return &commandDiagnostic{
			Kind:      "x_api_request_failed",
			Message:   "xurl reached the X API, but the API request failed; inspect parsed_json/stdout for HTTP status and X API error details",
			UsageHint: "If the first argument was intended as a shortcut command, use one of: " + strings.Join(knownCommands(), ", ") + ". Raw endpoints should start with /2/ or https://.",
		}
	}

	return nil
}

func isRawEndpoint(arg string) bool {
	lower := strings.ToLower(arg)
	return strings.HasPrefix(arg, "/") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func isKnownCommand(arg string) bool {
	for _, command := range knownCommands() {
		if arg == command {
			return true
		}
	}
	return false
}

func knownCommands() []string {
	return []string{
		"auth", "bookmark", "bookmarks", "block", "completion", "delete", "dm", "dms", "follow", "followers", "following", "help", "like", "likes", "media", "mentions", "mute", "news", "post", "quote", "read", "reply", "repost", "search", "timeline", "trends", "unblock", "unbookmark", "unfollow", "unlike", "unmute", "unrepost", "user", "version", "webhook", "whoami",
	}
}

func suggestCommands(input string) []string {
	suggestions := make([]string, 0, 3)
	lower := strings.ToLower(input)
	for _, command := range knownCommands() {
		if strings.HasPrefix(command, lower) || strings.HasPrefix(lower, command) {
			suggestions = append(suggestions, command)
			if len(suggestions) == 3 {
				return suggestions
			}
		}
	}

	if lower == "users" {
		return []string{"user", "whoami"}
	}
	return suggestions
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

func loadOAuth1TokenFile(path string) (oauth1TokenFile, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return oauth1TokenFile{}, fmt.Errorf("failed to resolve OAuth1 token file path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return oauth1TokenFile{}, fmt.Errorf("failed to read OAuth1 token file %q: %w", absPath, err)
	}

	var tokenFile oauth1TokenFile
	if err := json.Unmarshal(data, &tokenFile); err != nil {
		return oauth1TokenFile{}, fmt.Errorf("failed to parse OAuth1 token file %q: %w", absPath, err)
	}

	tokenFile.ConsumerKey = strings.TrimSpace(tokenFile.ConsumerKey)
	tokenFile.ConsumerKeySecret = strings.TrimSpace(tokenFile.ConsumerKeySecret)
	tokenFile.AccessToken = strings.TrimSpace(tokenFile.AccessToken)
	tokenFile.AccessTokenSecret = strings.TrimSpace(tokenFile.AccessTokenSecret)

	switch {
	case tokenFile.ConsumerKey == "":
		return oauth1TokenFile{}, fmt.Errorf("OAuth1 token file %q is missing required field %q", absPath, "Consumer Key")
	case tokenFile.ConsumerKeySecret == "":
		return oauth1TokenFile{}, fmt.Errorf("OAuth1 token file %q is missing required field %q", absPath, "Consumer Key Secret")
	case tokenFile.AccessToken == "":
		return oauth1TokenFile{}, fmt.Errorf("OAuth1 token file %q is missing required field %q", absPath, "Access Token")
	case tokenFile.AccessTokenSecret == "":
		return oauth1TokenFile{}, fmt.Errorf("OAuth1 token file %q is missing required field %q", absPath, "Access Token Secret")
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

func sendResponse(resp rpcResponse) {
	if !resp.UseFileTransport {
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
