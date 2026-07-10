package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/streamjson"
)

type Server struct {
	options     Options
	store       *sessions.Store
	events      *eventBroker
	permissions *permissionBroker
	asks        *askBroker

	runsMu sync.Mutex
	runs   map[string]*activeRun
}

type activeRun struct {
	runID  string
	cancel context.CancelFunc
}

func New(options Options) *Server {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.MaxFileBytes <= 0 {
		options.MaxFileBytes = defaultMaxFileBytes
	}
	if options.MaxRequestBytes <= 0 {
		options.MaxRequestBytes = defaultMaxRequestBytes
	}
	store := options.Store
	if store == nil {
		store = sessions.NewStore(sessions.StoreOptions{})
	}
	return &Server{
		options:     options,
		store:       store,
		events:      newEventBroker(),
		permissions: newPermissionBroker(),
		asks:        newAskBroker(),
		runs:        map[string]*activeRun{},
	}
}

func (server *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, server.options.MaxRequestBytes)
	}
	server.withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.withAuth(http.HandlerFunc(server.route)).ServeHTTP(w, r)
	})).ServeHTTP(w, r)
}

func (server *Server) route(w http.ResponseWriter, r *http.Request) {
	path := cleanPath(r.URL.Path)
	switch path {
	case "/global/health":
		server.requireMethod(w, r, http.MethodGet, server.handleHealth)
	case "/openapi.json":
		server.requireMethod(w, r, http.MethodGet, server.handleOpenAPI)
	case "/doc":
		server.requireMethod(w, r, http.MethodGet, server.handleDoc)
	case "/event":
		server.requireMethod(w, r, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
			serveSSE(w, r, server.events)
		})
	case "/config":
		server.requireMethod(w, r, http.MethodGet, server.handleConfig)
	case "/provider":
		server.requireMethod(w, r, http.MethodGet, server.handleProvider)
	case "/models":
		server.requireMethod(w, r, http.MethodGet, server.handleModels)
	case "/path":
		server.requireMethod(w, r, http.MethodGet, server.handlePath)
	case "/vcs":
		server.requireMethod(w, r, http.MethodGet, server.handleVCS)
	case "/session":
		if r.Method == http.MethodGet {
			server.handleListSessions(w, r)
			return
		}
		if r.Method == http.MethodPost {
			server.handleCreateSession(w, r)
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	case "/file":
		server.requireMethod(w, r, http.MethodGet, server.handleFile)
	case "/file/content":
		server.requireMethod(w, r, http.MethodGet, server.handleFileContent)
	case "/file/status":
		server.requireMethod(w, r, http.MethodGet, server.handleFileStatus)
	case "/find":
		server.requireMethod(w, r, http.MethodGet, server.handleFind)
	case "/find/file":
		server.requireMethod(w, r, http.MethodGet, server.handleFindFile)
	default:
		if strings.HasPrefix(path, "/session/") {
			server.routeSession(w, r, path)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func (server *Server) routeSession(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "session" {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	sessionID := parts[1]
	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet:
			server.handleGetSession(w, r, sessionID)
		case http.MethodPatch:
			server.handleUpdateSession(w, r, sessionID)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
		return
	}
	if len(parts) == 3 {
		switch parts[2] {
		case "event-log":
			server.requireMethod(w, r, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
				server.handleSessionEventLog(w, r, sessionID)
			})
		case "children":
			server.requireMethod(w, r, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
				server.handleSessionChildren(w, r, sessionID)
			})
		case "lineage":
			server.requireMethod(w, r, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
				server.handleSessionLineage(w, r, sessionID)
			})
		case "tree":
			server.requireMethod(w, r, http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
				server.handleSessionTree(w, r, sessionID)
			})
		case "fork":
			server.requireMethod(w, r, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
				server.handleForkSession(w, r, sessionID)
			})
		case "abort":
			server.requireMethod(w, r, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
				server.handleAbortRun(w, r, sessionID)
			})
		case "message":
			server.requireMethod(w, r, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
				server.handlePrompt(w, r, sessionID, false)
			})
		case "prompt_async":
			server.requireMethod(w, r, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
				server.handlePrompt(w, r, sessionID, true)
			})
		default:
			writeError(w, http.StatusNotFound, "not_found", "route not found")
		}
		return
	}
	if len(parts) == 4 && parts[2] == "permissions" {
		server.requireMethod(w, r, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
			server.handlePermissionDecision(w, r, sessionID, parts[3])
		})
		return
	}
	if len(parts) == 4 && parts[2] == "ask" {
		server.requireMethod(w, r, http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
			server.handleAskAnswer(w, r, sessionID, parts[3])
		})
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "route not found")
}

func (server *Server) requireMethod(w http.ResponseWriter, r *http.Request, method string, handler http.HandlerFunc) {
	if r.Method != method {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	handler(w, r)
}

func (server *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"version": server.options.Version,
	})
}

func (server *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if server.options.ConfigSnapshot == nil {
		writeJSON(w, http.StatusOK, ConfigSnapshot{Version: server.options.Version, Cwd: server.options.Cwd})
		return
	}
	snapshot, err := server.options.ConfigSnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (server *Server) handleProvider(w http.ResponseWriter, r *http.Request) {
	if server.options.Provider == nil {
		writeJSON(w, http.StatusOK, ProviderSnapshot{})
		return
	}
	snapshot, err := server.options.Provider(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "provider_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (server *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if server.options.Models == nil {
		writeJSON(w, http.StatusOK, ModelSnapshot{Models: []any{}})
		return
	}
	snapshot, err := server.options.Models(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "models_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (server *Server) handlePath(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"cwd": server.options.Cwd})
}

func (server *Server) handleVCS(w http.ResponseWriter, r *http.Request) {
	if server.options.VCS == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	result, err := server.options.VCS(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "vcs_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (server *Server) handlePrompt(w http.ResponseWriter, r *http.Request, sessionID string, async bool) {
	session, err := server.store.Get(sessionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if session == nil {
		writeError(w, http.StatusNotFound, "session_not_found", "session not found")
		return
	}
	var body struct {
		Content         string                  `json:"content"`
		Model           string                  `json:"model"`
		ReasoningEffort string                  `json:"reasoningEffort"`
		PermissionMode  string                  `json:"permissionMode"`
		Autonomy        string                  `json:"autonomy"`
		Images          []streamjson.InputImage `json:"images"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	body.Content = strings.TrimSpace(body.Content)
	if body.Content == "" && len(body.Images) == 0 {
		writeError(w, http.StatusBadRequest, "content_required", "content is required")
		return
	}
	runID, err := streamjson.CreateRunID(server.options.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "run_id_error", err.Error())
		return
	}
	request := RunRequest{
		RunID:           runID,
		SessionID:       sessionID,
		Cwd:             firstNonEmpty(server.options.Cwd, session.Cwd),
		Content:         body.Content,
		Model:           strings.TrimSpace(body.Model),
		ReasoningEffort: strings.TrimSpace(body.ReasoningEffort),
		PermissionMode:  strings.TrimSpace(body.PermissionMode),
		Autonomy:        strings.TrimSpace(body.Autonomy),
		Images:          body.Images,
		Async:           async,
		Store:           server.store,
	}
	parentCtx := context.Background()
	if !async {
		parentCtx = r.Context()
	}
	ctx, cancel, ok := server.startRunWithParent(parentCtx, sessionID, runID)
	if !ok {
		writeError(w, http.StatusConflict, "run_active", "session already has an active run")
		return
	}
	if async {
		go func() {
			_, _ = server.executeRunSafely(ctx, cancel, request)
		}()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	result, err := server.executeRunSafely(ctx, cancel, request)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			writeError(w, 499, "cancelled", "run cancelled")
			return
		}
		writeError(w, http.StatusInternalServerError, "run_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (server *Server) executeRunSafely(ctx context.Context, cancel context.CancelFunc, request RunRequest) (result RunResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("run panicked: %v", recovered)
			server.publishRunError(request, "run_panic", err.Error(), 1)
			result = RunResult{RunID: request.RunID, SessionID: request.SessionID, Status: "error", ExitCode: 1}
		}
	}()
	return server.executeRun(ctx, cancel, request)
}

func (server *Server) startRunWithParent(parent context.Context, sessionID string, runID string) (context.Context, context.CancelFunc, bool) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	server.runsMu.Lock()
	defer server.runsMu.Unlock()
	if _, exists := server.runs[sessionID]; exists {
		cancel()
		return nil, nil, false
	}
	server.runs[sessionID] = &activeRun{runID: runID, cancel: cancel}
	return ctx, cancel, true
}

func (server *Server) finishRun(sessionID string) {
	server.runsMu.Lock()
	delete(server.runs, sessionID)
	server.runsMu.Unlock()
}

func (server *Server) executeRun(ctx context.Context, cancel context.CancelFunc, request RunRequest) (RunResult, error) {
	defer cancel()
	defer server.finishRun(request.SessionID)

	server.publish(request, streamjson.Event{
		Type:      streamjson.EventRunStart,
		Cwd:       request.Cwd,
		SessionID: request.SessionID,
	})
	if server.options.Runner == nil {
		err := fmt.Errorf("runner unavailable")
		server.publishRunError(request, "runner_unavailable", err.Error(), 1)
		return RunResult{}, err
	}
	hooks := RunHooks{
		Emit: func(event streamjson.Event) {
			server.publish(request, event)
		},
		OnPermissionRequest: func(ctx context.Context, req agent.PermissionRequest) (agent.PermissionDecision, error) {
			return server.permissions.request(ctx, request.SessionID, req, func(event streamjson.Event) {
				server.publish(request, event)
			}, server.events.ackControl)
		},
		OnAskUser: func(ctx context.Context, req agent.AskUserRequest) (agent.AskUserResponse, error) {
			return server.asks.request(ctx, request.SessionID, req, func(event streamjson.Event) {
				server.publish(request, event)
			}, server.events.ackControl)
		},
	}
	result, err := server.options.Runner.Run(ctx, request, hooks)
	if err != nil {
		code := "run_error"
		status := "error"
		exitCode := 1
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			code = "cancelled"
			status = "cancelled"
			exitCode = 130
		}
		server.publish(request, streamjson.Event{
			Type:        streamjson.EventError,
			Code:        code,
			Message:     err.Error(),
			Recoverable: boolPtr(false),
		})
		server.publish(request, streamjson.Event{
			Type:     streamjson.EventRunEnd,
			Status:   status,
			ExitCode: intPtr(exitCode),
		})
		return RunResult{RunID: request.RunID, SessionID: request.SessionID, Status: status, ExitCode: exitCode}, err
	}
	if result.RunID == "" {
		result.RunID = request.RunID
	}
	if result.SessionID == "" {
		result.SessionID = request.SessionID
	}
	if result.Status == "" {
		result.Status = "success"
	}
	if result.FinalAnswer != "" {
		server.publish(request, streamjson.Event{
			Type: streamjson.EventFinal,
			Text: result.FinalAnswer,
		})
	}
	server.publish(request, streamjson.Event{
		Type:     streamjson.EventRunEnd,
		Status:   result.Status,
		ExitCode: intPtr(result.ExitCode),
	})
	return result, nil
}

func (server *Server) publishRunError(request RunRequest, code string, message string, exitCode int) {
	server.publish(request, streamjson.Event{
		Type:        streamjson.EventError,
		Code:        code,
		Message:     message,
		Recoverable: boolPtr(false),
	})
	server.publish(request, streamjson.Event{
		Type:     streamjson.EventRunEnd,
		Status:   "error",
		ExitCode: intPtr(exitCode),
	})
}

func (server *Server) publish(request RunRequest, event streamjson.Event) {
	if event.SchemaVersion == 0 {
		event.SchemaVersion = streamjson.SchemaVersion
	}
	if event.RunID == "" {
		event.RunID = request.RunID
	}
	if event.SessionID == "" {
		event.SessionID = request.SessionID
	}
	server.events.publish(event)
}

func (server *Server) handleAbortRun(w http.ResponseWriter, r *http.Request, sessionID string) {
	server.runsMu.Lock()
	run, ok := server.runs[sessionID]
	server.runsMu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "run_not_found", "active run not found")
		return
	}
	run.cancel()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runId": run.runID})
}

func (server *Server) handlePermissionDecision(w http.ResponseWriter, r *http.Request, sessionID string, permissionID string) {
	var body struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	decision := agent.PermissionDecision{
		Action: agent.PermissionDecisionAction(strings.TrimSpace(body.Action)),
		Reason: strings.TrimSpace(body.Reason),
	}
	if decision.Action == "" {
		writeError(w, http.StatusBadRequest, "action_required", "action is required")
		return
	}
	if err := server.permissions.respond(sessionID, permissionID, decision); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (server *Server) handleAskAnswer(w http.ResponseWriter, r *http.Request, sessionID string, askID string) {
	var body struct {
		Answers []string `json:"answers"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := server.asks.respond(sessionID, askID, agent.AskUserResponse{Answers: body.Answers}); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func cleanPath(path string) string {
	if path == "" {
		return "/"
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
		if path == "" {
			return "/"
		}
	}
	return path
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	message = redaction.RedactString(message, redaction.Options{})
	writeJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		return domainError{status: http.StatusUnsupportedMediaType, code: "unsupported_media_type", message: "Content-Type must be application/json"}
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return domainError{status: http.StatusUnsupportedMediaType, code: "unsupported_media_type", message: "Content-Type must be application/json"}
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain a single JSON value")
		}
		return err
	}
	return nil
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
