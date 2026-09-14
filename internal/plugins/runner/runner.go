package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	tether "github.com/hollis-labs/go-tether-client"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
	"github.com/hollis-labs/tangent/internal/pluginhost"
)

// ActiveSession represents one running agent session under supervision.
type ActiveSession struct {
	mu           sync.RWMutex
	ID           string
	AgentID      string
	AgentLabel   string
	Mode         Mode
	Lifecycle    Lifecycle
	Cmd          *exec.Cmd
	StdinPipe    io.WriteCloser
	Sieve        *StreamSieve
	LiveState    string // "idle", "processing", "stopped"
	TurnID       string
	TurnsCount   int
	StartedAt    time.Time
	Correlations map[string]any
	cancel       context.CancelFunc
}

func (s *ActiveSession) SetLiveState(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LiveState = state
}

func (s *ActiveSession) StateSnapshot() (liveState string, turnID string, turnsCount int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LiveState, s.TurnID, s.TurnsCount
}

func (s *ActiveSession) RecordTurnResponse() (turnID string, turnsCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LiveState = "processing"
	s.TurnsCount++
	s.TurnID = fmt.Sprintf("tturn-%s-%d", s.ID, s.TurnsCount)
	return s.TurnID, s.TurnsCount
}

// Engine manages both embedded subprocesses and delegated Tether sessions.
type Engine struct {
	mu           sync.RWMutex
	sessions     map[string]*ActiveSession
	tetherClient *tether.Client
	toolCaller   pluginhost.ToolCaller
	logger       *slog.Logger
}

// NewEngine constructs a runner Engine.
func NewEngine(logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		sessions: make(map[string]*ActiveSession),
		logger:   logger,
	}
}

// SetToolCaller assigns the in-process ToolCaller for turn enqueuing.
func (e *Engine) SetToolCaller(caller pluginhost.ToolCaller) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.toolCaller = caller
}

// SetTetherClient configures the client for delegated Tether execution.
func (e *Engine) SetTetherClient(c *tether.Client) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tetherClient = c
}

// Launch starts an agent session (either embedded subprocess or delegated via Tether).
func (e *Engine) Launch(ctx context.Context, params LaunchParams) (LaunchResult, error) {
	if params.AgentID == "" {
		return LaunchResult{}, errors.New("runner: agent_id is required")
	}
	if params.Prompt == "" {
		return LaunchResult{}, errors.New("runner: prompt is required")
	}

	sessionID := params.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("sess-run-%d", time.Now().UnixNano())
	}

	mode := params.Mode
	if mode == "" {
		mode = ModeEmbedded
	}

	lifecycle := params.Lifecycle
	if lifecycle == "" {
		lifecycle = LifecycleStreamingStdio
	}

	// 1. Delegated Tether execution mode
	if mode == ModeTether {
		return e.launchTether(ctx, params)
	}

	// 2. Embedded Subprocess execution mode
	return e.launchEmbedded(ctx, sessionID, lifecycle, params)
}

func (e *Engine) launchTether(ctx context.Context, params LaunchParams) (LaunchResult, error) {
	e.mu.RLock()
	client := e.tetherClient
	e.mu.RUnlock()

	if client == nil {
		// Attempt local default unix socket connection
		var err error
		client, err = tether.New("")
		if err != nil {
			return LaunchResult{}, fmt.Errorf("runner: tether client unavailable: %w", err)
		}
	}

	launchID := params.AgentID
	if launchID == "" {
		launchID = "default"
	}

	req := tether.LaunchRequest{
		Launch:     launchID,
		BootPrompt: params.Prompt,
	}

	sess, err := client.LaunchWithInput(ctx, req)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("runner: create tether session: %w", err)
	}

	e.mu.Lock()
	e.sessions[sess.ID] = &ActiveSession{
		ID:           sess.ID,
		AgentID:      params.AgentID,
		AgentLabel:   params.AgentLabel,
		Mode:         ModeTether,
		Lifecycle:    params.Lifecycle,
		LiveState:    "processing",
		TurnID:       "turn-1",
		TurnsCount:   1,
		StartedAt:    time.Now(),
		Correlations: params.Correlations,
	}
	e.mu.Unlock()

	return LaunchResult{
		SessionID: sess.ID,
		AgentID:   params.AgentID,
		Mode:      ModeTether,
		Lifecycle: params.Lifecycle,
		StartedAt: time.Now(),
		Status:    "running",
	}, nil
}

func (e *Engine) launchEmbedded(
	ctx context.Context,
	sessionID string,
	lifecycle Lifecycle,
	params LaunchParams,
) (LaunchResult, error) {
	command := params.Command
	if command == "" {
		command = "cat" // Safe fallback command that echoes stdin
	}

	cmdCtx, cancel := context.WithCancel(ctx)
	// #nosec G204 -- launching agent subprocess as requested by caller
	cmd := exec.CommandContext(cmdCtx, command, params.Args...)

	if params.WorkingDir != "" {
		cmd.Dir = params.WorkingDir
	}

	if len(params.Env) > 0 {
		cmd.Env = append(os.Environ(), params.Env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return LaunchResult{}, fmt.Errorf("runner: pipe stdin: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return LaunchResult{}, fmt.Errorf("runner: pipe stdout: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return LaunchResult{}, fmt.Errorf("runner: pipe stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return LaunchResult{}, fmt.Errorf("runner: start process: %w", err)
	}

	sieve := NewStreamSieve(sessionID)

	active := &ActiveSession{
		ID:           sessionID,
		AgentID:      params.AgentID,
		AgentLabel:   params.AgentLabel,
		Mode:         ModeEmbedded,
		Lifecycle:    lifecycle,
		Cmd:          cmd,
		StdinPipe:    stdin,
		Sieve:        sieve,
		LiveState:    "processing",
		TurnID:       fmt.Sprintf("tturn-%s-1", sessionID),
		TurnsCount:   1,
		StartedAt:    time.Now(),
		Correlations: params.Correlations,
		cancel:       cancel,
	}

	e.mu.Lock()
	e.sessions[sessionID] = active
	e.mu.Unlock()

	// Launch async reader loop for combined stdout and stderr
	go e.superviseOutput(active, stdout, stderr)

	// Write the initial prompt
	if lifecycle == LifecycleACP || lifecycle == LifecycleJSONRPCStdio {
		req := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "session/prompt",
			"params": map[string]any{
				"prompt": params.Prompt,
			},
		}
		raw, _ := json.Marshal(req)
		_, _ = stdin.Write(append(raw, '\n'))
	} else {
		_, _ = stdin.Write([]byte(params.Prompt + "\n"))
	}

	return LaunchResult{
		SessionID: sessionID,
		AgentID:   params.AgentID,
		Mode:      ModeEmbedded,
		Lifecycle: lifecycle,
		PID:       cmd.Process.Pid,
		StartedAt: active.StartedAt,
		Status:    "running",
	}, nil
}

func (e *Engine) superviseOutput(s *ActiveSession, stdout, stderr io.Reader) {
	buf := make([]byte, 4096)
	combined := io.MultiReader(stdout, stderr)

	// Inactivity timer to detect LiveStateIdle
	idleTimer := time.NewTimer(300 * time.Millisecond)
	defer idleTimer.Stop()

	chunkCh := make(chan []byte, 100)
	doneCh := make(chan struct{})

	go func() {
		defer close(doneCh)
		for {
			n, err := combined.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				chunkCh <- cp
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case chunk, ok := <-chunkCh:
			if !ok {
				e.markSessionStopped(s.ID)
				return
			}
			s.Sieve.WriteChunk(chunk)
			idleTimer.Reset(300 * time.Millisecond)

		case <-idleTimer.C:
			// Process transitioned to idle; extract conversational turn
			e.onSessionIdle(s)

		case <-doneCh:
			e.markSessionStopped(s.ID)
			return
		}
	}
}

func (e *Engine) onSessionIdle(s *ActiveSession) {
	s.SetLiveState("idle")
	_, turnID, turnsCount := s.StateSnapshot()
	extracted := s.Sieve.ExtractTurn(turnID)
	if extracted == nil || extracted.Prose == "" {
		return
	}

	e.logger.Info("runner: conversational turn extracted at LiveStateIdle",
		"session_id", s.ID,
		"kind", extracted.Kind,
		"title", extracted.Title,
	)

	// Automatically enqueue into Tangent turns inbox if ToolCaller is available
	e.mu.RLock()
	caller := e.toolCaller
	e.mu.RUnlock()

	if caller != nil {
		req := map[string]any{
			"contract_version": extensions.AgentTurnContractVersion,
			"turn_id":          extracted.TurnID,
			"session_id":       s.ID,
			"idempotency_key":  fmt.Sprintf("runner:%s:%s", s.ID, extracted.TurnID),
			"kind":             extracted.Kind,
			"source": map[string]any{
				"agent_id":       s.AgentID,
				"agent_label":    s.AgentLabel,
				"application_id": "runner",
			},
			"title":   extracted.Title,
			"content": extracted.Prose,
			"correlations": map[string]any{
				"runner_session_id": s.ID,
				"turns_count":       turnsCount,
			},
		}
		raw, _ := json.Marshal(req)

		// Call tangent.turns_enqueue
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := caller.CallTool(ctx, "tangent.turns_enqueue", map[string]any{
			"request": string(raw),
		})
		if err != nil {
			e.logger.Warn("runner: auto-enqueue turn to tangent failed",
				"session_id", s.ID,
				"err", err,
			)
		}
	}
}

func (e *Engine) markSessionStopped(sessionID string) {
	e.mu.RLock()
	s, ok := e.sessions[sessionID]
	e.mu.RUnlock()
	if ok {
		s.SetLiveState("stopped")
	}
}

// SendTurn delivers an operator response back to the agent session.
func (e *Engine) SendTurn(ctx context.Context, params SendTurnParams) (SendTurnResult, error) {
	e.mu.RLock()
	sess, ok := e.sessions[params.SessionID]
	e.mu.RUnlock()

	if !ok {
		return SendTurnResult{}, fmt.Errorf("runner: session %s not found", params.SessionID)
	}

	// 1. Delegated Tether delivery
	if sess.Mode == ModeTether {
		e.mu.RLock()
		client := e.tetherClient
		e.mu.RUnlock()
		if client == nil {
			return SendTurnResult{}, errors.New("runner: tether client not configured")
		}
		if err := client.SendTurn(ctx, sess.ID, params.ResponseText); err != nil {
			return SendTurnResult{}, fmt.Errorf("runner: tether send turn: %w", err)
		}
		sess.RecordTurnResponse()
		return SendTurnResult{
			SessionID:   sess.ID,
			Delivered:   true,
			DeliveredAt: time.Now(),
			LiveState:   "processing",
		}, nil
	}

	// 2. Embedded Subprocess delivery
	sess.mu.RLock()
	stdin := sess.StdinPipe
	sess.mu.RUnlock()
	if stdin == nil {
		return SendTurnResult{}, errors.New("runner: stdin closed or unavailable")
	}

	_, turnsCount := sess.RecordTurnResponse()

	var payload []byte
	if sess.Lifecycle == LifecycleACP || sess.Lifecycle == LifecycleJSONRPCStdio {
		req := map[string]any{
			"jsonrpc": "2.0",
			"id":      turnsCount,
			"method":  "session/prompt",
			"params": map[string]any{
				"prompt":          params.ResponseText,
				"action":          params.Action,
				"selected_option": params.SelectedOption,
			},
		}
		raw, _ := json.Marshal(req)
		payload = append(raw, '\n')
	} else {
		payload = []byte(params.ResponseText + "\n")
	}

	_, err := stdin.Write(payload)
	if err != nil {
		return SendTurnResult{}, fmt.Errorf("runner: write stdin: %w", err)
	}

	return SendTurnResult{
		SessionID:   sess.ID,
		Delivered:   true,
		DeliveredAt: time.Now(),
		LiveState:   "processing",
	}, nil
}

// Health inspects live session status.
func (e *Engine) Health(ctx context.Context, sessionID string) (SessionHealthResult, error) {
	e.mu.RLock()
	sess, ok := e.sessions[sessionID]
	e.mu.RUnlock()

	if !ok {
		return SessionHealthResult{}, fmt.Errorf("runner: session %s not found", sessionID)
	}

	liveState, turnID, turnsCount := sess.StateSnapshot()

	if sess.Mode == ModeTether {
		e.mu.RLock()
		client := e.tetherClient
		e.mu.RUnlock()
		if client != nil {
			h, err := client.SessionHealth(ctx, sessionID)
			if err == nil {
				return SessionHealthResult{
					SessionID:  sessionID,
					Alive:      h.Alive,
					LiveState:  h.LiveState,
					TurnID:     h.TurnID,
					TurnsCount: turnsCount,
				}, nil
			}
		}
	}

	return SessionHealthResult{
		SessionID:  sessionID,
		Alive:      liveState != "stopped",
		LiveState:  liveState,
		TurnID:     turnID,
		TurnsCount: turnsCount,
	}, nil
}

// Stop terminates an active session.
func (e *Engine) Stop(sessionID string) error {
	e.mu.RLock()
	sess, ok := e.sessions[sessionID]
	e.mu.RUnlock()

	if !ok {
		return fmt.Errorf("runner: session %s not found", sessionID)
	}

	sess.SetLiveState("stopped")
	sess.mu.Lock()
	cancel := sess.cancel
	stdin := sess.StdinPipe
	sess.StdinPipe = nil
	sess.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if stdin != nil {
		_ = stdin.Close()
	}
	return nil
}
