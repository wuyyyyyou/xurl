package main

import (
	"bufio"
	"bytes"
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

	"github.com/xdevplatform/xurl/api"
	"github.com/xdevplatform/xurl/auth"
	"github.com/xdevplatform/xurl/cli"
	"github.com/xdevplatform/xurl/config"
	"github.com/xdevplatform/xurl/version"
)

const (
	internalCLICommand = "__xurl_cli"
	credentialName     = "X_OAUTH2_ACCESS_TOKEN"
	tokenEnvKey        = "XURL_EXECUTA_OAUTH2_ACCESS_TOKEN"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

var manifest = map[string]any{
	"name":         "xurl-executa",
	"display_name": "xurl Executa",
	"version":      version.Version,
	"description":  "Run xurl commands from ANNA with an injected OAuth 2.0 access token.",
	"author":       "xdevplatform + ANNA",
	"credentials": []map[string]any{
		{
			"name":         credentialName,
			"display_name": "X OAuth2 Access Token",
			"description":  "OAuth 2.0 Bearer access token used for all xurl requests.",
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
	JSONRPC string  `json:"jsonrpc"`
	ID      any     `json:"id"`
	Result  any     `json:"result,omitempty"`
	Error   *rpcErr `json:"error,omitempty"`
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
	if toolName != "run_xurl" {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcErr{
				Code:    -32601,
				Message: fmt.Sprintf("Unknown tool: %s", toolName),
				Data:    map[string]any{"available_tools": []string{"run_xurl"}},
			},
		}
	}

	arguments, _ := req.Params["arguments"].(map[string]any)
	if arguments == nil {
		arguments = map[string]any{}
	}

	args, err := getArgs(arguments)
	if err != nil {
		return invalidParamsResponse(req.ID, err.Error())
	}

	if err := validateArgs(args); err != nil {
		return invalidParamsResponse(req.ID, err.Error())
	}

	cwd, err := resolveCWD(arguments)
	if err != nil {
		return invalidParamsResponse(req.ID, err.Error())
	}

	token := resolveAccessToken(req.Params)
	if token == "" {
		return invalidParamsResponse(req.ID, fmt.Sprintf("missing credential %q", credentialName))
	}

	outputPath, result, err := runXURLCommand(args, cwd, token)
	if err != nil {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcErr{
				Code:    -32603,
				Message: "Failed to execute xurl command",
				Data: map[string]any{
					"error": err.Error(),
				},
			},
		}
	}

	return rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"success": true,
			"tool":    toolName,
			"data": map[string]any{
				"output_file":     outputPath,
				"command_success": result.CommandSuccess,
				"exit_code":       result.ExitCode,
				"cwd":             cwd,
				"args":            args,
			},
		},
	}
}

func invalidParamsResponse(id any, message string) rpcResponse {
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcErr{
			Code:    -32602,
			Message: message,
		},
	}
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

func resolveAccessToken(params map[string]any) string {
	context, _ := params["context"].(map[string]any)
	if context != nil {
		credentials, _ := context["credentials"].(map[string]any)
		if credentials != nil {
			if token, ok := credentials[credentialName].(string); ok {
				return strings.TrimSpace(token)
			}
		}
	}
	return strings.TrimSpace(os.Getenv(credentialName))
}

func runXURLCommand(args []string, cwd string, token string) (string, commandResult, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", commandResult{}, fmt.Errorf("failed to resolve plugin executable: %w", err)
	}

	cmdArgs := append([]string{internalCLICommand}, args...)
	cmd := exec.Command(executable, cmdArgs...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), tokenEnvKey+"="+token)

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

	cleanStdout := redactSecret(stripANSI(stdoutBuf.String()), token)
	cleanStderr := redactSecret(stripANSI(stderrBuf.String()), token)

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

	outputPath := filepath.Join(cwd, fmt.Sprintf("xurl-output-%d.json", time.Now().UTC().UnixNano()))
	if err := writeJSONFile(outputPath, result); err != nil {
		return "", commandResult{}, fmt.Errorf("failed to write output file: %w", err)
	}

	return outputPath, result, nil
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

func redactSecret(input string, secret string) string {
	if secret == "" {
		return input
	}
	return strings.ReplaceAll(input, secret, "[REDACTED]")
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
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

	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("executa-resp-%d.json", time.Now().UnixNano()))
	if err := writeJSONFile(tmpPath, resp); err != nil {
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

	pointer := fileTransportPointer{
		JSONRPC:       "2.0",
		ID:            resp.ID,
		FileTransport: tmpPath,
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
