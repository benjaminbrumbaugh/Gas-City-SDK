package acp

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/runtime"
)

// nudgePostWriteDrainTimeout caps the wait for sc.done after a Nudge stdin
// write fails. Sized to match terminateProcess's SIGTERM grace period so a
// Nudge racing with Stop still converges to the best-effort nil contract
// rather than surfacing a spurious error before SIGKILL lands.
const nudgePostWriteDrainTimeout = 5 * time.Second

// Config holds ACP provider settings.
type Config struct {
	HandshakeTimeout  time.Duration // default 30s
	NudgeBusyTimeout  time.Duration // default 60s
	OutputBufferLines int           // default 1000
}

func (c *Config) handshakeTimeout() time.Duration {
	if c.HandshakeTimeout <= 0 {
		return 30 * time.Second
	}
	return c.HandshakeTimeout
}

func (c *Config) nudgeBusyTimeout() time.Duration {
	if c.NudgeBusyTimeout <= 0 {
		return 60 * time.Second
	}
	return c.NudgeBusyTimeout
}

func (c *Config) outputBufferLines() int {
	if c.OutputBufferLines <= 0 {
		return defaultOutputBufferLines
	}
	return c.OutputBufferLines
}

// Provider manages agent sessions using the Agent Client Protocol.
type Provider struct {
	mu            sync.Mutex
	dir           string                  // socket/meta file directory
	conns         map[string]*sessionConn // in-process tracking
	workDirs      map[string]string       // session name → workDir (for CopyTo)
	cfg           Config
	activityWrite func(path string, data []byte) error                    // test seam
	socketProbe   func(name, command string, timeout time.Duration) error // test seam
}

// Compile-time check.
var (
	_ runtime.Provider                         = (*Provider)(nil)
	_ runtime.InteractionProvider              = (*Provider)(nil)
	_ runtime.TransportCapabilityProvider      = (*Provider)(nil)
	_ runtime.DefinitiveSessionAbsenceProvider = (*Provider)(nil)
)

// NewProvider returns an ACP [Provider] that stores socket files in
// a default temporary directory.
func NewProvider(cfg Config) *Provider {
	return NewProviderWithDir(defaultProviderDir(), cfg)
}

// defaultProviderDir is the city-less state directory: one per user, because
// the path is otherwise identical for everyone on the host and [os.MkdirAll]
// succeeds on a directory someone else created first. The euid does not make
// the directory private on its own — [runtime.EnsurePrivateDir] validates
// ownership — but it keeps two legitimate users off one path so that validation
// is a real check rather than a permanent outage for whoever logs in second.
func defaultProviderDir() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("gc-acp-%d", os.Geteuid()))
}

// NewProviderWithDir returns an ACP [Provider] that stores socket files
// in the given directory. Useful for tests that need isolated state.
func NewProviderWithDir(dir string, cfg Config) *Provider {
	// Best-effort here and verified at the write path: a constructor cannot
	// report a squatted directory, and failing silently at construction would
	// hand back a Provider that writes anyway.
	_ = runtime.EnsurePrivateDir(dir)
	return &Provider{
		dir:      dir,
		conns:    make(map[string]*sessionConn),
		workDirs: make(map[string]string),
		cfg:      cfg,
	}
}

// SupportsTransport reports whether this provider can host the requested
// session transport.
func (p *Provider) SupportsTransport(transport string) bool {
	return transport == "acp"
}

// Start spawns an ACP agent process, performs the JSON-RPC handshake, and
// optionally sends the initial nudge. Returns an error if a session with
// that name already exists or the handshake fails.
func (p *Provider) Start(ctx context.Context, name string, cfg runtime.Config) error {
	p.mu.Lock()

	// Check in-memory tracking first.
	if existing, ok := p.conns[name]; ok {
		if existing.alive() {
			p.mu.Unlock()
			return fmt.Errorf("%w: session %q", runtime.ErrSessionExists, name)
		}
		delete(p.conns, name)
	}

	// Check socket for cross-process case.
	if p.socketAlive(name) {
		p.mu.Unlock()
		return fmt.Errorf("%w: session %q", runtime.ErrSessionExists, name)
	}

	// Reserve the name with a sentinel so concurrent Start calls for the
	// same name are rejected while we perform the slow handshake outside
	// the lock. The sentinel's done channel is open (not closed), so
	// alive() returns true and duplicate checks above will reject.
	// The cancel func lets Stop abort an in-progress handshake immediately.
	hsCtx, hsCancel := context.WithCancel(ctx)
	sentinel := &sessionConn{done: make(chan struct{}), cancel: hsCancel, pending: make(map[int64]chan JSONRPCMessage)}
	p.conns[name] = sentinel

	// Store workDir for CopyTo.
	if cfg.WorkDir != "" {
		p.workDirs[name] = cfg.WorkDir
	}

	p.mu.Unlock()

	// clearSentinel removes the reservation on failure.
	clearSentinel := func() {
		p.mu.Lock()
		if p.conns[name] == sentinel {
			delete(p.conns, name)
			delete(p.workDirs, name)
		}
		p.mu.Unlock()
	}

	if err := runtime.StageSessionWorkDir(cfg); err != nil {
		clearSentinel()
		return fmt.Errorf("staging workdir for %q: %w", name, err)
	}

	command := cfg.Command
	if cfg.PromptSuffix != "" {
		if cfg.PromptFlag != "" {
			command = command + " " + cfg.PromptFlag + " " + cfg.PromptSuffix
		} else {
			command = command + " " + cfg.PromptSuffix
		}
	}
	if command == "" {
		clearSentinel()
		return fmt.Errorf("acp provider requires a command")
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Bound os/exec's pipe cleanup when descendants inherit stdout. Without a
	// WaitDelay, cmd.Wait can retain a reader goroutine indefinitely after the
	// process group has been killed.
	cmd.WaitDelay = runtime.ManagedProcessReapGrace
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}

	// Build environment: inherit parent env + apply overrides. Empty overrides
	// withhold inherited variables, as they do for the other session runtimes.
	env := os.Environ()
	if len(cfg.Env) > 0 {
		keys := make([]string, 0, len(cfg.Env))
		for k := range cfg.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env = envWithoutKey(env, k)
			if cfg.Env[k] == "" {
				continue
			}
			env = append(env, k+"="+cfg.Env[k])
		}
	}
	cmd.Env = env

	// Set up stdio pipes for JSON-RPC.
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		clearSentinel()
		return fmt.Errorf("creating stdin pipe for %q: %w", name, err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		clearSentinel()
		return fmt.Errorf("creating stdout pipe for %q: %w", name, err)
	}
	// Capture stderr to a bounded buffer for diagnostics. We use our
	// own pipe + goroutine (not cmd.Stderr) so that cmd.Wait() does not
	// block waiting for the stderr copy to finish after process kill.
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		clearSentinel()
		return fmt.Errorf("creating stderr pipe for %q: %w", name, err)
	}
	cmd.Stderr = stderrW
	var stderrBuf limitedWriter
	stderrBuf.max = 4096
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := stderrR.Read(buf)
			if n > 0 {
				stderrBuf.Write(buf[:n]) //nolint:errcheck
			}
			if readErr != nil {
				break
			}
		}
		stderrR.Close() //nolint:errcheck
	}()

	// Publish an exclusive name reservation before the process exists. Together
	// with the control socket, this closes the cross-process startup gap so an
	// absence proof cannot overlook a same-name ACP process between cmd.Start and
	// listener creation.
	reservationToken, err := p.reserveSessionName(name)
	if err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		stderrW.Close() //nolint:errcheck
		stderrR.Close() //nolint:errcheck
		clearSentinel()
		return err
	}
	if err := p.inheritSessionNameLease(cmd, name, reservationToken); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		stderrW.Close() //nolint:errcheck
		stderrR.Close() //nolint:errcheck
		p.releaseSessionName(name, reservationToken)
		clearSentinel()
		return fmt.Errorf("inheriting ACP session %q reservation lease: %w", name, err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		stderrW.Close() //nolint:errcheck
		stderrR.Close() //nolint:errcheck
		p.releaseSessionName(name, reservationToken)
		clearSentinel()
		return fmt.Errorf("starting session %q: %w", name, err)
	}
	if err := p.transferSessionNameOwner(name, reservationToken, cmd.Process.Pid); err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		stderrW.Close() //nolint:errcheck
		stderrR.Close() //nolint:errcheck
		p.releaseSessionName(name, reservationToken)
		clearSentinel()
		return fmt.Errorf("transferring ACP session %q reservation to child: %w", name, err)
	}
	p.closeParentSessionNameLease(name, reservationToken)
	// Close the write end — child inherits it; we only read.
	stderrW.Close() //nolint:errcheck

	// Create control socket for cross-process discovery.
	processDone := make(chan struct{})
	lis, err := p.startControlSocket(name, reservationToken, cmd, processDone)
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		_ = stderrR.Close()
		p.releaseSessionName(name, reservationToken)
		clearSentinel()
		return fmt.Errorf("creating control socket for %q: %w", name, err)
	}

	sc := newSessionConn(cmd, stdinPipe, lis, p.cfg.outputBufferLines(), processDone)

	// Start readLoop before handshake so we can receive responses.
	go sc.readLoop(stdoutPipe)

	// Monitor process exit — clean up pending state, socket, and listener.
	// Socket cleanup happens BEFORE close(done) so that callers waiting
	// on sc.done (e.g., terminateProcess) can rely on the socket being
	// gone when done fires. Without this ordering, IsRunning can race:
	// Stop deletes the conn from the map, terminateProcess waits on done,
	// done closes, Stop returns — but the socket is still alive, so
	// IsRunning falls through to socketAlive and returns true.
	go func() {
		_ = cmd.Wait()
		// stderr is an explicit os.Pipe, so os/exec cannot close it for us.
		// Descendants may retain the write end after the direct child exits;
		// close the read end to release the drain goroutine.
		_ = stderrR.Close()
		// Order the read loop's exit ahead of the publisher's final flush so a
		// session/update the loop did dispatch cannot race publication
		// shutdown. This is ordering, not a drain guarantee: cmd.Wait closes
		// the stdout read end itself, so bytes still unread at that point are
		// not guaranteed to be dispatched.
		<-sc.readDone
		sc.drainPending()
		sc.closeActivityPublisher()
		p.cleanupSessionGeneration(name, reservationToken, lis)
		close(processDone)
	}()

	// Perform ACP handshake with a deadline. hsCtx (created above with
	// WithCancelCause) is already cancellable by Stop. Add a timeout
	// child so handshake_timeout applies even when the parent has a
	// longer deadline.
	hsTimeoutCtx, hsTimeoutCancel := context.WithTimeout(hsCtx, p.cfg.handshakeTimeout())
	defer hsTimeoutCancel()

	if err := p.handshake(hsTimeoutCtx, sc, cfg.WorkDir, cfg.MCPServers); err != nil {
		// Handshake failed — kill the process. The monitor goroutine
		// handles listener/socket cleanup when the process exits.
		_ = stdinPipe.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-sc.done
		clearSentinel()
		// Include stderr tail in the error for diagnostics.
		if stderr := stderrBuf.String(); stderr != "" {
			return fmt.Errorf("acp handshake for %q: %w\nagent stderr:\n%s", name, err, stderr)
		}
		return fmt.Errorf("acp handshake for %q: %w", name, err)
	}

	// Before committing the real conn, check whether Stop was called
	// during the handshake (which cancels hsCtx). If so, kill the process
	// and clean up — the caller of Stop expects the session to be gone.
	if err := hsCtx.Err(); err != nil {
		_ = stdinPipe.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-sc.done
		clearSentinel()
		return fmt.Errorf("session %q was stopped during startup", name)
	}

	// Seed the sidecar synchronously at handshake completion. Start must not
	// advertise a cross-process activity-capable session until the first
	// durable value exists. Later updates use the non-blocking publisher.
	seed := time.Now()
	if err := p.publishActivity(name, seed); err != nil {
		_ = stdinPipe.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-sc.done
		p.mu.Lock()
		if p.conns[name] == sentinel {
			delete(p.conns, name)
			delete(p.workDirs, name)
			p.cleanupMeta(name)
		}
		p.mu.Unlock()
		return fmt.Errorf("publishing initial activity for %q: %w", name, err)
	}
	publisher := newActivityPublisher(
		activityPublishInterval,
		time.Now(),
		func(stamp time.Time) error { return p.publishActivity(name, stamp) },
		func(err error) {
			fmt.Fprintf(os.Stderr, "acp: publishing activity for %q: %v\n", name, err)
		},
	)
	if err := sc.installActivityPublisher(publisher, seed); err != nil {
		_ = stdinPipe.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-sc.done
		p.mu.Lock()
		if p.conns[name] == sentinel {
			delete(p.conns, name)
			delete(p.workDirs, name)
			p.cleanupMeta(name)
		}
		p.mu.Unlock()
		return fmt.Errorf("starting activity publication for %q: %w", name, err)
	}

	// Commit the real connection only if the startup sentinel still owns the
	// name. Stop may have removed it while the initial atomic write was in
	// progress.
	p.mu.Lock()
	if p.conns[name] != sentinel {
		p.mu.Unlock()
		_ = stdinPipe.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-sc.done
		p.mu.Lock()
		if _, replaced := p.conns[name]; !replaced {
			p.cleanupMeta(name)
		}
		p.mu.Unlock()
		return fmt.Errorf("session %q was stopped during startup", name)
	}
	p.conns[name] = sc
	p.mu.Unlock()

	// Send initial nudge if configured (best-effort, outside lock).
	if cfg.Nudge != "" {
		_ = p.Nudge(name, runtime.TextContent(cfg.Nudge))
	}

	return nil
}

func envWithoutKey(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return out
}

// handshake performs the ACP initialize → initialized → session/new sequence.
func (p *Provider) handshake(ctx context.Context, sc *sessionConn, workDir string, mcpServers []runtime.MCPServerConfig) error {
	// Step 1: Send "initialize" request.
	initReq, _ := newInitializeRequest()
	ch, err := sc.sendRequest(initReq)
	if err != nil {
		return fmt.Errorf("sending initialize: %w", err)
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return fmt.Errorf("connection closed during initialize")
		}
		if resp.Error != nil {
			return fmt.Errorf("initialize error: %s", resp.Error.Message)
		}
	case <-ctx.Done():
		return fmt.Errorf("initialize timeout: %w", ctx.Err())
	case <-sc.done:
		return fmt.Errorf("process exited during initialize")
	}

	// Step 2: Send "initialized" notification.
	if err := sc.sendNotification(newInitializedNotification()); err != nil {
		return fmt.Errorf("sending initialized: %w", err)
	}

	// Step 3: Send "session/new" request.
	newReq, _ := newSessionNewRequest(workDir, mcpServers)
	ch, err = sc.sendRequest(newReq)
	if err != nil {
		return fmt.Errorf("sending session/new: %w", err)
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return fmt.Errorf("connection closed during session/new")
		}
		if resp.Error != nil {
			return fmt.Errorf("session/new error: %s", resp.Error.Message)
		}
		var result SessionNewResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return fmt.Errorf("decoding session/new result: %w", err)
		}
		sc.mu.Lock()
		sc.sessionID = result.SessionID
		sc.mu.Unlock()
	case <-ctx.Done():
		return fmt.Errorf("session/new timeout: %w", ctx.Err())
	case <-sc.done:
		return fmt.Errorf("process exited during session/new")
	}

	return nil
}

// Stop terminates the named session. Returns nil if it doesn't exist
// (idempotent). Sends SIGTERM first, then SIGKILL after a grace period.
func (p *Provider) Stop(name string) error {
	p.mu.Lock()
	sc, ok := p.conns[name]
	if ok {
		delete(p.conns, name)
	}
	p.mu.Unlock()

	if ok {
		if !sc.alive() {
			p.cleanupMeta(name)
			return nil
		}
		// Guard against sentinel sessionConn (nil cmd/stdin during handshake).
		// Signal the in-progress handshake to abort via the cancel func.
		if sc.cmd == nil {
			if sc.cancel != nil {
				sc.cancel()
			}
			return nil
		}
		_ = sc.stdin.Close()
		err := terminateProcess(sc)
		if err == nil || runtime.IsSessionGone(err) {
			p.cleanupMeta(name)
			return nil
		}
		return err
	}

	// Fall back to socket (cross-process case).
	err := p.stopBySocket(name)
	if err == nil || runtime.IsSessionGone(err) {
		p.cleanupMeta(name)
		return nil
	}
	return err
}

// Interrupt sends SIGINT to the named session's process.
// Best-effort: returns nil if the session doesn't exist.
func (p *Provider) Interrupt(name string) error {
	p.mu.Lock()
	sc, ok := p.conns[name]
	p.mu.Unlock()
	if ok {
		// Guard against sentinel sessionConn (nil cmd during handshake).
		if sc.cmd == nil {
			return nil
		}
		return syscall.Kill(-sc.cmd.Process.Pid, syscall.SIGINT)
	}

	// Fall back to socket (cross-process case).
	_ = p.sendSocketCommand(name, "interrupt", 2*time.Second)
	return nil
}

// IsRunning reports whether the named session has a live process.
func (p *Provider) IsRunning(name string) bool {
	p.mu.Lock()
	sc, ok := p.conns[name]
	p.mu.Unlock()

	if ok {
		return sc.alive()
	}
	return p.socketAlive(name)
}

// SessionDefinitelyAbsent proves that no ACP runtime or startup reservation
// currently owns name. The reservation is created before cmd.Start, so checking
// it before the socket closes the otherwise-unobservable cross-process startup
// window. Any filesystem or socket uncertainty fails closed.
func (p *Provider) SessionDefinitelyAbsent(name string) (bool, error) {
	p.mu.Lock()
	sc, ok := p.conns[name]
	p.mu.Unlock()
	if ok && sc.alive() {
		return false, nil
	}

	reserved, err := p.sessionNameReserved(name)
	if err != nil {
		return false, fmt.Errorf("checking ACP session reservation for %q: %w", name, err)
	}
	if reserved {
		return false, nil
	}
	probe := p.sendSocketCommand
	if p.socketProbe != nil {
		probe = p.socketProbe
	}
	if err := probe(name, "ping", 500*time.Millisecond); err == nil {
		return false, nil
	} else if !isUnavailableSocketError(err) {
		return false, fmt.Errorf("probing ACP session %q: %w", name, err)
	}
	reserved, err = p.sessionNameReserved(name)
	if err != nil {
		return false, fmt.Errorf("rechecking ACP session reservation for %q: %w", name, err)
	}
	if reserved {
		return false, nil
	}
	return true, nil
}

// IsAttached always returns false — ACP sessions have no terminal.
func (p *Provider) IsAttached(_ string) bool { return false }

// Attach is not supported by the ACP provider.
func (p *Provider) Attach(_ string) error {
	return fmt.Errorf("acp provider does not support attach")
}

// ProcessAlive delegates to IsRunning. Returns true when processNames is
// empty (per the Provider contract).
func (p *Provider) ProcessAlive(name string, processNames []string) bool {
	if len(processNames) == 0 {
		return true
	}
	return p.IsRunning(name)
}

// Nudge sends a session/prompt to the named session. Waits for the agent to
// become idle before sending. Returns ErrSessionNotFound when this provider
// instance does not own the in-memory ACP connection. Returns nil if the
// agent process exits during the send (best-effort).
func (p *Provider) Nudge(name string, content []runtime.ContentBlock) error {
	p.mu.Lock()
	sc, ok := p.conns[name]
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: ACP provider does not own session %q", runtime.ErrSessionNotFound, name)
	}
	if !sc.alive() {
		return nil
	}

	// Serialize nudges per-session so that waitIdle → setActivePrompt →
	// sendRequest is atomic with respect to other concurrent Nudge calls.
	sc.nudgeMu.Lock()
	defer sc.nudgeMu.Unlock()

	// Re-check liveness under the lock. If an earlier Nudge observed the
	// process exit and returned nil while we were queued on nudgeMu, skip
	// the marshal+write work instead of tripping through the recovery path.
	if !sc.alive() {
		return nil
	}

	// Wait for agent to become idle.
	if !sc.waitIdle(p.cfg.nudgeBusyTimeout()) {
		return fmt.Errorf("agent %q busy, timed out waiting for idle", name)
	}

	sc.mu.Lock()
	sessID := sc.sessionID
	sc.mu.Unlock()
	if sessID == "" {
		return fmt.Errorf("session %q has no ACP session ID", name)
	}

	msg, id := newSessionPromptRequest(sessID, content)

	// Set busy state BEFORE sendRequest so that dispatch can match the
	// response ID and clear it. If we set it after, a fast agent could
	// respond before setActivePrompt runs, leaving busy set permanently.
	sc.setActivePrompt(id)

	ch, err := sc.sendRequest(msg)
	if err != nil {
		sc.clearActivePrompt(id)
		// Non-pipe failures (e.g., marshal errors) have nothing to do with
		// the agent lifecycle, so surface them immediately rather than
		// stalling the caller on sc.done.
		if !isPipeWriteError(err) {
			return fmt.Errorf("sending prompt to %q: %w", name, err)
		}
		// Pipe write failed — the agent process is exiting (e.g., a prior
		// Interrupt delivered SIGINT and the agent died, or Stop closed
		// our stdin end between the alive() check and the write).
		// Sync on the existing lifecycle event: cmd.Wait() → drainPending →
		// close(sc.done). Once that fires, this is identical to the
		// !sc.alive() case above, so honor the best-effort contract by
		// returning nil. The bound matches terminateProcess's SIGTERM grace
		// period; the common path returns in microseconds.
		select {
		case <-sc.done:
			// A chronically flapping agent would otherwise be silent here;
			// a single stderr line lets ops distinguish "nothing happened"
			// from "agent died mid-write."
			fmt.Fprintf(os.Stderr, "acp: nudge to %q skipped (agent exiting): %v\n", name, err)
			return nil
		case <-time.After(nudgePostWriteDrainTimeout):
			return fmt.Errorf("sending prompt to %q: %w", name, err)
		}
	}

	// Drain the response channel in the background. If the agent
	// returns a JSON-RPC error, log it rather than silently dropping.
	go func() {
		resp, ok := <-ch
		if !ok {
			return // connection closed, drainPending already cleaned up
		}
		if resp.Error != nil {
			// Best we can do: log via stderr. The prompt was sent, so
			// the error is informational, not actionable by the caller.
			fmt.Fprintf(os.Stderr, "acp: prompt error for %q: %s\n", name, resp.Error.Message)
		}
	}()

	return nil
}

// Pending reports structured pending interactions. ACP only tracks whether an
// outbound prompt is in flight; that busy state is not a user-facing blocking
// interaction, so the provider intentionally reports this capability as
// unsupported.
func (p *Provider) Pending(_ string) (*runtime.PendingInteraction, error) {
	return nil, runtime.ErrInteractionUnsupported
}

// Respond resolves a pending structured interaction. ACP does not currently
// expose those interactions over the protocol, so responses are unsupported.
func (p *Provider) Respond(_ string, _ runtime.InteractionResponse) error {
	return runtime.ErrInteractionUnsupported
}

// SendKeys is a no-op for ACP sessions (no terminal).
func (p *Provider) SendKeys(_ string, _ ...string) error {
	return nil
}

// RunLive is a no-op for ACP sessions.
func (p *Provider) RunLive(_ string, _ runtime.Config) error {
	return nil
}

// Peek returns the last N lines of captured output from session/update
// notifications.
func (p *Provider) Peek(name string, lines int) (string, error) {
	p.mu.Lock()
	sc, ok := p.conns[name]
	p.mu.Unlock()
	if !ok {
		return "", nil
	}
	return sc.peekLines(lines), nil
}

// SetMeta stores a key-value pair for the named session in a sidecar file.
//
// The sidecar carries session identity and drain state, which a reader can use
// to impersonate the session and a writer can use to forge a drain
// acknowledgement, so it is owner-only. The directory is re-checked on every
// write rather than trusted from construction: a squatted directory is not
// something a constructor can report.
func (p *Provider) SetMeta(name, key, value string) error {
	if err := runtime.EnsurePrivateDir(p.dir); err != nil {
		return err
	}
	return runtime.WritePrivateFile(p.metaPath(name, key), []byte(value))
}

// GetMeta retrieves a metadata value from a sidecar file.
// Returns ("", nil) if the key is not set.
func (p *Provider) GetMeta(name, key string) (string, error) {
	data, err := os.ReadFile(p.metaPath(name, key))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// RemoveMeta removes a metadata sidecar file.
func (p *Provider) RemoveMeta(name, key string) error {
	err := os.Remove(p.metaPath(name, key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// lastActivityMetaKey names the sidecar holding the durable last-activity
// stamp. Keeping it in the meta namespace means Stop's cleanupMeta already
// removes it along with the rest of the session's sidecar state.
const lastActivityMetaKey = "gc_last_activity"

// publishActivity atomically replaces the durable last-activity stamp. Atomic
// replacement prevents cross-process readers from observing a truncated or
// partially-written timestamp.
func (p *Provider) publishActivity(name string, t time.Time) error {
	path := p.metaPath(name, lastActivityMetaKey)
	data := []byte(t.UTC().Format(time.RFC3339Nano))
	var err error
	if p.activityWrite != nil {
		err = p.activityWrite(path, data)
	} else {
		err = fsys.WriteFileAtomic(fsys.OSFS{}, path, data, 0o600)
	}
	if err != nil {
		return fmt.Errorf("writing activity sidecar: %w", err)
	}
	return nil
}

// GetLastActivity returns the time of the last observed session/update, or the
// Start-time seed if none has been observed.
//
// It reads the in-process connection when this process owns it, and otherwise
// falls back to the durable stamp on disk — the same
// in-memory-then-cross-process shape that Stop, Interrupt and IsRunning
// already use for the control socket.
//
// The connection and in-memory stamp live only in the process that ran Start.
// The sidecar gives other processes the same last-observed protocol timestamp.
func (p *Provider) GetLastActivity(name string) (time.Time, error) {
	p.mu.Lock()
	sc, ok := p.conns[name]
	p.mu.Unlock()
	if ok {
		if t := sc.getLastActivity(); !t.IsZero() {
			return t, nil
		}
	}
	return p.persistedActivity(name)
}

// persistedActivity reads the durable last-activity stamp.
//
// A missing stamp is "unknown" (zero, nil) — the pre-existing contract for a
// session this provider knows nothing about. An unreadable or malformed stamp
// is an error rather than a silent zero.
func (p *Provider) persistedActivity(name string) (time.Time, error) {
	raw, err := p.GetMeta(name, lastActivityMetaKey)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading last activity for %q: %w", name, err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing last activity for %q: %w", name, err)
	}
	return t, nil
}

// ClearScrollback clears the output buffer.
func (p *Provider) ClearScrollback(name string) error {
	p.mu.Lock()
	sc, ok := p.conns[name]
	p.mu.Unlock()
	if ok {
		sc.clearOutput()
	}
	return nil
}

// CopyTo copies src into the named session's working directory at relDst.
// Best-effort: returns nil if session unknown or src missing.
func (p *Provider) CopyTo(name, src, relDst string) error {
	p.mu.Lock()
	wd := p.workDirs[name]
	p.mu.Unlock()
	if wd == "" {
		return nil
	}
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	dst := wd
	if relDst != "" {
		dst = filepath.Join(wd, relDst)
	}
	return runtime.StagePath(src, dst)
}

// ListRunning returns the names of all running sessions whose names
// match the given prefix, discovered via socket files.
func (p *Provider) ListRunning(prefix string) ([]string, error) {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".sock") {
			continue
		}
		sn := p.socketNameForEntry(strings.TrimSuffix(n, ".sock"))
		if !strings.HasPrefix(sn, prefix) {
			continue
		}
		if p.socketAlive(sn) {
			names = append(names, sn)
		}
	}
	return names, nil
}

func (p *Provider) metaPath(name, key string) string {
	return filepath.Join(p.dir, metaFilePrefix(name)+".meta."+metaFileKey(key))
}

// cleanupMeta removes all sidecar meta files for the named session.
func (p *Provider) cleanupMeta(name string) {
	matches, _ := filepath.Glob(filepath.Join(p.dir, metaFilePrefix(name)+".meta.*"))
	for _, m := range matches {
		os.Remove(m) //nolint:errcheck
	}
}

func metaFilePrefix(name string) string {
	return "m" + metaFileKey(name)
}

func metaFileKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// --- Unix socket helpers (same as subprocess) ---

func (p *Provider) legacySockPath(name string) string {
	return filepath.Join(p.dir, name+".sock")
}

func (p *Provider) sockKey(name string) string {
	sum := sha256.Sum256([]byte(name))
	return "s" + hex.EncodeToString(sum[:4])
}

func (p *Provider) sockPath(name string) string {
	return filepath.Join(p.dir, p.sockKey(name)+".sock")
}

func (p *Provider) sockNamePath(name string) string {
	return filepath.Join(p.dir, p.sockKey(name)+".name")
}

type sessionNameReservation struct {
	Version    int    `json:"version"`
	Name       string `json:"name"`
	OwnerPID   int    `json:"owner_pid"`
	OwnerStart string `json:"owner_start"`
	Token      string `json:"token"`
	Lease      string `json:"lease"`
}

var (
	sessionNameProcessLocks sync.Map
	sessionNameLeaseFiles   sync.Map
)

func (p *Provider) validSessionNameReservation(name string, record sessionNameReservation) bool {
	return record.Version == 2 && record.Name == name && record.OwnerPID > 0 && record.OwnerStart != "" && record.Token != "" &&
		record.Lease == p.sockKey(name)+".lease-"+record.Token
}

func (p *Provider) sessionNameReserved(name string) (bool, error) {
	if err := runtime.EnsurePrivateDir(p.dir); err != nil {
		return false, err
	}
	var reserved bool
	err := p.withSessionNameLock(name, func() error {
		var err error
		reserved, err = p.sessionNameReservedLocked(name)
		return err
	})
	return reserved, err
}

// sessionNameReservedLocked reports whether a live, legacy, malformed, or
// otherwise inconclusive reservation owns name. A structured reservation is
// reclaimed only when both its inherited lease and PID/start identity prove
// that its owner is gone.
func (p *Provider) sessionNameReservedLocked(name string) (bool, error) {
	path := p.sockNamePath(name)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var record sessionNameReservation
	if json.Unmarshal(raw, &record) != nil || !p.validSessionNameReservation(name, record) {
		// Legacy and unparseable reservations have no safe ownership proof.
		return true, nil
	}
	lease, err := os.OpenFile(filepath.Join(p.dir, record.Lease), os.O_RDWR, 0o600)
	if err == nil {
		lockErr := syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(lockErr, syscall.EWOULDBLOCK) || errors.Is(lockErr, syscall.EAGAIN) {
			_ = lease.Close()
			return true, nil
		}
		if lockErr != nil {
			_ = lease.Close()
			return false, lockErr
		}
		_ = syscall.Flock(int(lease.Fd()), syscall.LOCK_UN)
		_ = lease.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	currentStart, startErr := pidutil.StartTime(record.OwnerPID)
	if startErr == nil {
		if currentStart == record.OwnerStart {
			return true, nil
		}
		return false, p.removeSessionNameReservationFiles(path, record)
	}
	if pidutil.Alive(record.OwnerPID) {
		// The process exists but its identity could not be read. Never steal it.
		return true, nil
	}
	return false, p.removeSessionNameReservationFiles(path, record)
}

func (p *Provider) reserveSessionName(name string) (string, error) {
	if err := runtime.EnsurePrivateDir(p.dir); err != nil {
		return "", fmt.Errorf("preparing ACP session directory: %w", err)
	}
	ownerStart, err := pidutil.StartTime(os.Getpid())
	if err != nil {
		return "", fmt.Errorf("capturing ACP reservation owner identity: %w", err)
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generating ACP reservation owner token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	leaseName := p.sockKey(name) + ".lease-" + token
	leasePath := filepath.Join(p.dir, leaseName)
	lease, err := os.OpenFile(leasePath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("creating ACP reservation lease: %w", err)
	}
	if err := syscall.Flock(int(lease.Fd()), syscall.LOCK_EX); err != nil {
		_ = lease.Close()
		_ = os.Remove(leasePath)
		return "", fmt.Errorf("locking ACP reservation lease: %w", err)
	}
	record := sessionNameReservation{Version: 2, Name: name, OwnerPID: os.Getpid(), OwnerStart: ownerStart, Token: token, Lease: leaseName}
	raw, err := json.Marshal(record)
	if err != nil {
		_ = lease.Close()
		_ = os.Remove(leasePath)
		return "", fmt.Errorf("encoding ACP session name reservation: %w", err)
	}
	err = p.withSessionNameLock(name, func() error {
		reserved, err := p.sessionNameReservedLocked(name)
		if err != nil {
			return err
		}
		if reserved {
			return fmt.Errorf("%w: session %q has an ACP name reservation", runtime.ErrSessionExists, name)
		}
		return publishReservationNoReplace(p.sockNamePath(name), raw)
	})
	if err != nil {
		_ = lease.Close()
		_ = os.Remove(leasePath)
		return "", fmt.Errorf("reserving ACP session name %q: %w", name, err)
	}
	sessionNameLeaseFiles.Store(p.sessionNameLeaseKey(name, token), lease)
	return token, nil
}

func (p *Provider) releaseSessionName(name, token string) {
	p.closeParentSessionNameLease(name, token)
	_ = p.withSessionNameLock(name, func() error {
		raw, err := os.ReadFile(p.sockNamePath(name))
		if err != nil {
			return nil
		}
		var record sessionNameReservation
		if json.Unmarshal(raw, &record) != nil || !p.validSessionNameReservation(name, record) || record.Token != token {
			return nil
		}
		return p.removeSessionNameReservationFiles(p.sockNamePath(name), record)
	})
}

func (p *Provider) transferSessionNameOwner(name, token string, ownerPID int) error {
	ownerStart, err := pidutil.StartTime(ownerPID)
	if err != nil {
		return fmt.Errorf("capturing child process identity: %w", err)
	}
	return p.withSessionNameLock(name, func() error {
		path := p.sockNamePath(name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var record sessionNameReservation
		if err := json.Unmarshal(raw, &record); err != nil || !p.validSessionNameReservation(name, record) || record.Token != token {
			return errors.New("ACP session reservation ownership changed before child transfer")
		}
		record.OwnerPID = ownerPID
		record.OwnerStart = ownerStart
		raw, err = json.Marshal(record)
		if err != nil {
			return err
		}
		return replaceReservation(path, raw)
	})
}

func (p *Provider) sessionNameLeaseKey(name, token string) string {
	return p.sockNamePath(name) + ":" + token
}

func (p *Provider) inheritSessionNameLease(cmd *exec.Cmd, name, token string) error {
	value, ok := sessionNameLeaseFiles.Load(p.sessionNameLeaseKey(name, token))
	if !ok {
		return errors.New("ACP reservation lease is unavailable")
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, value.(*os.File))
	return nil
}

func (p *Provider) closeParentSessionNameLease(name, token string) {
	value, ok := sessionNameLeaseFiles.LoadAndDelete(p.sessionNameLeaseKey(name, token))
	if ok {
		_ = value.(*os.File).Close()
	}
}

func (p *Provider) removeSessionNameReservationFiles(path string, record sessionNameReservation) error {
	if err := removeReservation(path); err != nil {
		return err
	}
	if record.Lease != "" {
		if err := os.Remove(filepath.Join(p.dir, record.Lease)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (p *Provider) cleanupSessionGeneration(name, token string, lis net.Listener) {
	_ = p.withSessionNameLock(name, func() error {
		_ = lis.Close()
		raw, err := os.ReadFile(p.sockNamePath(name))
		if err != nil {
			return nil
		}
		var record sessionNameReservation
		if json.Unmarshal(raw, &record) != nil || !p.validSessionNameReservation(name, record) || record.Token != token {
			return nil
		}
		if err := os.Remove(p.sockPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return p.removeSessionNameReservationFiles(p.sockNamePath(name), record)
	})
}

func (p *Provider) withSessionNameLock(name string, fn func() error) error {
	lockPath := p.sockNamePath(name) + ".lock"
	processLock, _ := sessionNameProcessLocks.LoadOrStore(lockPath, &sync.Mutex{})
	mu := processLock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close() //nolint:errcheck
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}

func publishReservationNoReplace(path string, raw []byte) error {
	tempPath, err := writeReservationTemp(path, raw)
	if err != nil {
		return err
	}
	defer os.Remove(tempPath) //nolint:errcheck
	return os.Link(tempPath, path)
}

func replaceReservation(path string, raw []byte) error {
	tempPath, err := writeReservationTemp(path, raw)
	if err != nil {
		return err
	}
	defer os.Remove(tempPath) //nolint:errcheck
	return os.Rename(tempPath, path)
}

func writeReservationTemp(path string, raw []byte) (string, error) {
	temp, err := os.CreateTemp(filepath.Dir(path), ".reservation-*")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return "", err
	}
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	return tempPath, nil
}

func removeReservation(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (p *Provider) socketNameForEntry(key string) string {
	data, err := os.ReadFile(filepath.Join(p.dir, key+".name"))
	if err != nil {
		return key
	}
	var record sessionNameReservation
	if json.Unmarshal(data, &record) == nil && p.validSessionNameReservation(record.Name, record) {
		return record.Name
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return key
	}
	return name
}

// startControlSocket creates a unix socket for cross-process commands.
func (p *Provider) startControlSocket(name, token string, cmd *exec.Cmd, done <-chan struct{}) (net.Listener, error) {
	var lis net.Listener
	err := p.withSessionNameLock(name, func() error {
		raw, err := os.ReadFile(p.sockNamePath(name))
		if err != nil {
			return err
		}
		var record sessionNameReservation
		if json.Unmarshal(raw, &record) != nil || !p.validSessionNameReservation(name, record) || record.Token != token {
			return fmt.Errorf("ACP session %q reservation ownership changed before socket publication", name)
		}
		sp := p.sockPath(name)
		if err := os.Remove(sp); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		lis, err = net.Listen("unix", sp)
		if err != nil {
			return err
		}
		if unixListener, ok := lis.(*net.UnixListener); ok {
			unixListener.SetUnlinkOnClose(false)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go handleControlConn(conn, cmd, done)
		}
	}()
	return lis, nil
}

// handleControlConn reads a command from the connection and acts on the process.
func handleControlConn(conn net.Conn, cmd *exec.Cmd, done <-chan struct{}) {
	defer conn.Close()                                     //nolint:errcheck
	conn.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}
	switch scanner.Text() {
	case "stop":
		_ = runtime.TerminateManagedProcess(cmd, done, runtime.ManagedProcessStopGrace)
		conn.Write([]byte("ok\n")) //nolint:errcheck
	case "interrupt":
		_ = runtime.SignalProcessGroup(cmd, syscall.SIGINT)
		conn.Write([]byte("ok\n")) //nolint:errcheck
	case "ping":
		conn.Write([]byte("ok\n")) //nolint:errcheck
	case "pid":
		fmt.Fprintf(conn, "%d\n", cmd.Process.Pid) //nolint:errcheck
	}
}

// socketAlive checks if a session is alive by pinging its control socket.
func (p *Provider) socketAlive(name string) bool {
	for _, sp := range []string{p.sockPath(name), p.legacySockPath(name)} {
		conn, err := net.DialTimeout("unix", sp, 500*time.Millisecond)
		if err != nil {
			continue
		}
		_ = conn.Close()
		if p.sendSocketCommand(name, "ping", 500*time.Millisecond) == nil {
			return true
		}
	}
	return false
}

// sendSocketCommand connects to the session's control socket and sends a command.
func (p *Provider) sendSocketCommand(name, command string, timeout time.Duration) error {
	var (
		lastErr            error
		firstActionableErr error
	)
	for _, sp := range []string{p.sockPath(name), p.legacySockPath(name)} {
		err := func(path string) error {
			conn, err := net.DialTimeout("unix", path, timeout)
			if err != nil {
				return err
			}
			defer conn.Close()                        //nolint:errcheck
			conn.SetDeadline(time.Now().Add(timeout)) //nolint:errcheck
			_, err = fmt.Fprintf(conn, "%s\n", command)
			if err != nil {
				return err
			}
			scanner := bufio.NewScanner(conn)
			if scanner.Scan() && scanner.Text() == "ok" {
				return nil
			}
			if err := scanner.Err(); err != nil {
				return err
			}
			return fmt.Errorf("unexpected response from socket")
		}(sp)
		if err == nil {
			return nil
		}
		if !isUnavailableSocketError(err) && firstActionableErr == nil {
			firstActionableErr = err
		}
		lastErr = err
	}
	if firstActionableErr != nil {
		return firstActionableErr
	}
	return lastErr
}

// stopBySocket connects to a session's control socket and asks it to stop.
func (p *Provider) stopBySocket(name string) error {
	err := p.sendSocketCommand(name, "stop", 7*time.Second)
	if err != nil {
		if isUnavailableSocketError(err) {
			reserved, reserveErr := p.sessionNameReserved(name)
			if reserveErr != nil {
				return fmt.Errorf("checking ACP startup reservation for %q after unavailable control socket: %w", name, reserveErr)
			}
			if reserved {
				return fmt.Errorf("ACP session %q has a startup reservation but no reachable control socket", name)
			}
			return nil
		}
		return err
	}
	return nil
}

func isUnavailableSocketError(err error) bool {
	return errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNREFUSED)
}

// Capabilities reports ACP provider capabilities. ACP sessions are headless,
// so attachment is never reportable — but session/update notifications are a
// real activity signal, durably stamped by GetLastActivity's sidecar so it
// survives the process boundary.
//
// Declaring the capability allows activity-aware policies to use the signal.
// Those policies remain independently configured; activity age alone does not
// diagnose the reason updates stopped.
func (p *Provider) Capabilities() runtime.ProviderCapabilities {
	return runtime.ProviderCapabilities{CanReportActivity: true}
}

// SleepCapability reports that ACP sessions support timed-only idle sleep.
func (p *Provider) SleepCapability(string) runtime.SessionSleepCapability {
	return runtime.SessionSleepCapabilityTimedOnly
}

// isPipeWriteError reports whether err originated from writing to a closed
// stdin pipe — the signal that the agent process exited between our alive()
// check and the write. Other sendRequest failures (marshal errors, etc.) are
// unrelated to lifecycle and should surface immediately.
func isPipeWriteError(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.EPIPE)
}

// terminateProcess sends SIGTERM then SIGKILL to a tracked process group.
func terminateProcess(sc *sessionConn) error {
	return runtime.TerminateManagedProcess(sc.cmd, sc.done, runtime.ManagedProcessStopGrace)
}
