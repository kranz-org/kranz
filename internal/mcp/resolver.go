package mcp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kranz-org/kranz/internal/app"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

// Registry is the subset of the runtime registry the resolver reads. Narrowing
// it keeps the resolution rules testable without a filesystem registry.
type Registry interface {
	List(ctx context.Context, clientVersion string) ([]kranzruntime.SessionRecord, error)
	Resolve(ctx context.Context, reference, clientVersion string) (kranzruntime.SessionRecord, error)
}

// Dialer opens an app.API against one runtime record.
type Dialer func(ctx context.Context, record kranzruntime.SessionRecord) (app.API, func() error, error)

// Launcher starts a runtime for a project directory and returns the record it
// published. It is the only way an MCP process may bring a runtime into
// existence, and it is wired to a detached child process rather than to an
// in-process supervisor: this process is a client of runtimes, never one. The
// bool reports whether this call actually created the returned runtime; finding
// an already-running one must not grant ownership to the MCP process.
type Launcher func(ctx context.Context, directory string) (kranzruntime.SessionRecord, bool, error)

// Resolver turns the optional runtime argument of a tool call into a live
// client. One MCP process serves any number of runtimes, so the binding that
// used to be made once at launch is made per call here.
type Resolver struct {
	version      string
	registry     Registry
	dial         Dialer
	launch       Launcher
	pin          string
	pinDirectory string
	directory    func() (string, error)
	// projectDirectory is the directory this MCP process was started in, used
	// as the default target of up. It is a path, not a runtime: nothing here
	// assumes a runtime exists for it.
	projectDirectory string

	mu      sync.Mutex
	clients map[string]*runtimeScope
	created map[string]bool
}

// runtimeScope is one resolved runtime: the client that answers for it and the
// identity every envelope served through it is stamped with.
type runtimeScope struct {
	api     app.API
	session SessionIdentity
	close   func() error
}

type ResolverOptions struct {
	Version  string
	Registry Registry
	Dial     Dialer
	Launch   Launcher
	// Pin is the runtime reference the process was launched with through -C or
	// -p. Empty means unbound: the call chooses.
	Pin string
	// PinDirectory is the explicitly selected project path for -C, -f, or
	// --override. It prevents same-name runtimes in another directory from
	// satisfying the pin.
	PinDirectory string
	// Directory resolves the MCP process working directory to a runtime name,
	// the same way the CLI resolves it without -p. It never creates anything.
	Directory func() (string, error)
	// ProjectDirectory is that same working directory as a path.
	ProjectDirectory string
}

func NewResolver(options ResolverOptions) *Resolver {
	return &Resolver{
		version: options.Version, registry: options.Registry, dial: options.Dial, launch: options.Launch,
		pin: options.Pin, pinDirectory: options.PinDirectory, directory: options.Directory, projectDirectory: options.ProjectDirectory,
		clients: map[string]*runtimeScope{}, created: map[string]bool{},
	}
}

// StaticResolver serves exactly one already-connected runtime. It is what a
// pinned launch and the tests both need: the address is settled, and the pool
// has nothing to discover.
func StaticResolver(api app.API, session SessionIdentity) *Resolver {
	session.Pinned = true
	resolver := NewResolver(ResolverOptions{Version: session.KranzVersion, Pin: session.ID})
	resolver.clients[session.ID] = &runtimeScope{api: api, session: session}
	return resolver
}

func (r *Resolver) Pinned() bool { return r.pin != "" }

// Close releases every pooled client. Runtimes themselves are untouched: this
// process owns none of them, including the ones it started through up.
func (r *Resolver) Close() error {
	r.mu.Lock()
	scopes := make([]*runtimeScope, 0, len(r.clients))
	for _, scope := range r.clients {
		scopes = append(scopes, scope)
	}
	r.clients = map[string]*runtimeScope{}
	r.mu.Unlock()
	var errs []error
	for _, scope := range scopes {
		if scope.close != nil {
			errs = append(errs, scope.close())
		}
	}
	return errors.Join(errs...)
}

// Resolve applies the addressing order: an explicit argument, then the pin,
// then the working directory as a lookup, then failure carrying the candidates
// that would have worked.
func (r *Resolver) Resolve(ctx context.Context, requested string) (*runtimeScope, *CausalError) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		if causal := r.checkPin(ctx, requested); causal != nil {
			return nil, causal
		}
		return r.connectReference(ctx, requested, addressExplicit)
	}
	if r.pin != "" {
		return r.connectReference(ctx, r.pin, addressPinned)
	}
	name, err := r.directoryReference()
	if err != nil {
		return nil, r.runtimeRequired(ctx, err)
	}
	return r.connectReference(ctx, name, addressDirectory)
}

type addressSource int

const (
	addressExplicit addressSource = iota
	addressPinned
	addressDirectory
)

func (r *Resolver) directoryReference() (string, error) {
	if r.directory == nil {
		return "", errors.New("this MCP server was started outside a Kranz project")
	}
	return r.directory()
}

// checkPin refuses an address that disagrees with the launch pin. A pinned
// connection is an explicit statement that this client speaks for one project,
// and an argument must not be able to talk it out of that.
func (r *Resolver) checkPin(ctx context.Context, requested string) *CausalError {
	if r.pin == "" {
		return nil
	}
	pinned, err := r.resolveRecord(ctx, r.pin)
	if err != nil {
		// A missing pin still constrains the server. Only the same literal
		// reference may continue to the connect path and report that the pinned
		// runtime is not running; another address must not turn an unavailable
		// pin into an unbound server.
		if strings.EqualFold(r.pin, requested) {
			return nil
		}
		return &CausalError{
			Code:    "runtime_pinned",
			Message: fmt.Sprintf("this MCP server is pinned to runtime %q and cannot address %q", r.pin, requested),
			Hint:    "Register a second MCP server without -C/-p to address runtimes per call, or drop the runtime argument.",
			Details: map[string]any{"pinned_runtime": r.pin, "requested": requested},
		}
	}
	if matchesRecord(pinned, requested) {
		return nil
	}
	return &CausalError{
		Code:    "runtime_pinned",
		Message: fmt.Sprintf("this MCP server is pinned to runtime %q and cannot address %q", pinned.Name, requested),
		Hint:    "Register a second MCP server without -C/-p to address runtimes per call, or drop the runtime argument.",
		Details: map[string]any{"pinned_runtime": pinned.Name, "pinned_id": pinned.ID, "requested": requested},
	}
}

func matchesRecord(record kranzruntime.SessionRecord, reference string) bool {
	if strings.EqualFold(record.Name, reference) || record.ID == reference {
		return true
	}
	return len(reference) >= 8 && strings.HasPrefix(record.ID, reference)
}

func (r *Resolver) resolveRecord(ctx context.Context, reference string) (kranzruntime.SessionRecord, error) {
	if r.registry == nil {
		return kranzruntime.SessionRecord{}, &kranzruntime.SessionNotFoundError{Reference: reference}
	}
	return r.registry.Resolve(ctx, reference, r.version)
}

func (r *Resolver) connectReference(ctx context.Context, reference string, source addressSource) (*runtimeScope, *CausalError) {
	record, err := r.resolveRecord(ctx, reference)
	if err != nil {
		// A pinned or static resolver may hold a live client for a reference no
		// registry can answer for, which is the case every in-process test and
		// every attached launch is in.
		if scope := r.pooledByReference(reference); scope != nil {
			return scope, nil
		}
		return nil, r.resolveFailure(ctx, reference, source, err)
	}
	if r.pinDirectory != "" && !sameProjectDirectory(r.pinDirectory, record.Directory) {
		return nil, &CausalError{Code: "runtime_pinned", Message: "the selected runtime name belongs to another project directory",
			Hint: "Start the selected project under a distinct runtime name."}
	}
	return r.connectRecord(ctx, record)
}

func (r *Resolver) pooledByReference(reference string) *runtimeScope {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, scope := range r.clients {
		if id == reference || strings.EqualFold(scope.session.Name, reference) {
			return scope
		}
	}
	return nil
}

func (r *Resolver) connectRecord(ctx context.Context, record kranzruntime.SessionRecord) (*runtimeScope, *CausalError) {
	r.mu.Lock()
	if scope, ok := r.clients[record.ID]; ok {
		r.mu.Unlock()
		return scope, nil
	}
	r.mu.Unlock()
	if r.dial == nil {
		return nil, &CausalError{Code: "runtime_unavailable", Message: fmt.Sprintf("runtime %q cannot be dialled from this process", record.Name)}
	}
	api, closer, err := r.dial(ctx, record)
	if err != nil {
		return nil, dialFailure(record, err)
	}
	scope := &runtimeScope{api: api, session: r.identity(record), close: closer}
	r.mu.Lock()
	// Another call may have dialled the same runtime while this one was in
	// flight; keep the first and close the duplicate rather than leaking it.
	if existing, ok := r.clients[record.ID]; ok {
		r.mu.Unlock()
		if closer != nil {
			_ = closer()
		}
		return existing, nil
	}
	r.clients[record.ID] = scope
	r.mu.Unlock()
	return scope, nil
}

func (r *Resolver) identity(record kranzruntime.SessionRecord) SessionIdentity {
	identity := SessionFromRecord(record)
	identity.Pinned = r.pin != "" && matchesRecord(record, r.pin)
	r.mu.Lock()
	if r.created[record.ID] {
		identity.CreatedBy = "mcp"
	}
	r.mu.Unlock()
	return identity
}

// dialFailure keeps an incompatible or unreachable runtime local to the call
// that named it. One bad runtime must not take the process serving the others
// down with it.
func dialFailure(record kranzruntime.SessionRecord, err error) *CausalError {
	var mismatch *kranzruntime.VersionMismatchError
	if errors.As(err, &mismatch) {
		return &CausalError{
			Code:    "runtime_version_mismatch",
			Message: fmt.Sprintf("runtime %q speaks protocol %d from Kranz %s, which this client cannot read", record.Name, mismatch.ServerProtocol, mismatch.ServerVersion),
			Hint:    "Restart that runtime with the current Kranz build, or address a runtime built from the same version.",
			Details: map[string]any{"runtime": record.Name, "id": record.ID, "runtime_kranz_version": mismatch.ServerVersion, "runtime_protocol_version": mismatch.ServerProtocol},
		}
	}
	return &CausalError{
		Code:    "runtime_unavailable",
		Message: fmt.Sprintf("runtime %q is registered but did not answer: %v", record.Name, err),
		Hint:    "Read kranz://runtimes to see current state, or restart the project.",
		Details: map[string]any{"runtime": record.Name, "id": record.ID},
	}
}

func (r *Resolver) resolveFailure(ctx context.Context, reference string, source addressSource, err error) *CausalError {
	var ambiguous *kranzruntime.AmbiguousSessionError
	if errors.As(err, &ambiguous) {
		return &CausalError{Code: "ambiguous_runtime", Message: ambiguous.Error(),
			Hint:    "Address the runtime by its full id from kranz://runtimes.",
			Details: map[string]any{"requested": reference, "candidates": r.candidates(ctx)}}
	}
	var notFound *kranzruntime.SessionNotFoundError
	if !errors.As(err, &notFound) {
		return &CausalError{Code: "runtime_unavailable", Message: err.Error(), Details: map[string]any{"requested": reference}}
	}
	message := fmt.Sprintf("no runtime named %q is running", reference)
	hint := "Start it with the up tool, or address a running runtime from kranz://runtimes."
	if source == addressDirectory {
		message = fmt.Sprintf("the project in this directory has no runtime running (it would be called %q)", reference)
	}
	if source == addressPinned {
		hint = "This MCP server is pinned to that project. Start its runtime with the up tool."
	}
	return &CausalError{Code: "runtime_not_found", Message: message, Hint: hint,
		Details: map[string]any{"requested": reference, "candidates": r.candidates(ctx)}}
}

// runtimeRequired is the answer when nothing addressed a runtime and nothing
// could stand in for the missing address. It carries the candidates so the
// caller can retry immediately instead of reading documentation.
func (r *Resolver) runtimeRequired(ctx context.Context, cause error) *CausalError {
	candidates := r.candidates(ctx)
	message := "no runtime was addressed and this MCP server has no project of its own"
	hint := "Pass runtime with a name or id from the candidates, or start a project with the up tool."
	if len(candidates) == 0 {
		hint = "No runtime is running. Start one with the up tool, giving it the project directory."
	}
	return &CausalError{Code: "runtime_required", Message: message, Hint: hint,
		Details: map[string]any{"candidates": candidates, "reason": cause.Error()}}
}

func (r *Resolver) candidates(ctx context.Context) []map[string]any {
	records, err := r.records(ctx)
	if err != nil {
		return []map[string]any{}
	}
	candidates := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if record.State != kranzruntime.SessionRunning {
			continue
		}
		candidates = append(candidates, map[string]any{
			"runtime": record.Name, "id": record.ID, "project": record.Project, "directory": record.Directory,
		})
	}
	return candidates
}

func (r *Resolver) records(ctx context.Context) ([]kranzruntime.SessionRecord, error) {
	if r.registry == nil {
		return nil, errors.New("no runtime registry is available in this process")
	}
	return r.registry.List(ctx, r.version)
}

// Launch starts a runtime for a project directory. It is reached only from the
// up tool, and it records the result so down can tell a runtime this process
// created from one it merely found.
func (r *Resolver) Launch(ctx context.Context, directory string) (*runtimeScope, bool, *CausalError) {
	if causal := r.checkLaunchPin(ctx, directory); causal != nil {
		return nil, false, causal
	}
	if r.launch == nil {
		return nil, false, &CausalError{Code: "unsupported", Message: "this MCP server cannot start runtimes"}
	}
	record, created, err := r.launch(ctx, directory)
	if err != nil {
		var conflict *kranzruntime.SessionConflictError
		if errors.As(err, &conflict) {
			return nil, false, &CausalError{Code: "runtime_conflict", Message: conflict.Error(),
				Details: map[string]any{"directory": directory}}
		}
		return nil, false, &CausalError{Code: "runtime_start_failed", Message: err.Error(),
			Hint: "Run doctor in that project, or start it from a terminal to read the failure.", Details: map[string]any{"directory": directory}}
	}
	if r.pinDirectory != "" && !sameProjectDirectory(r.pinDirectory, record.Directory) {
		return nil, created, &CausalError{Code: "runtime_pinned", Message: "launched runtime belongs to another project directory"}
	}
	r.mu.Lock()
	if created {
		r.created[record.ID] = true
	}
	createdHere := r.created[record.ID]
	r.mu.Unlock()
	scope, causal := r.connectRecord(ctx, record)
	if causal != nil {
		return nil, created, causal
	}
	if createdHere {
		scope.session.CreatedBy = "mcp"
	}
	return scope, created, nil
}

func (r *Resolver) checkLaunchPin(ctx context.Context, directory string) *CausalError {
	if r.pin == "" {
		return nil
	}
	allowed := ""
	if r.pinDirectory != "" {
		allowed = r.pinDirectory
	} else if record, err := r.resolveRecord(ctx, r.pin); err == nil {
		allowed = record.Directory
	} else {
		allowed = r.projectDirectory
	}
	if allowed != "" && sameProjectDirectory(allowed, directory) {
		return nil
	}
	return &CausalError{Code: "runtime_pinned", Message: "this MCP server is pinned to another project",
		Hint: "Use the pinned project directory or a separate unpinned MCP server."}
}

func sameProjectDirectory(left, right string) bool {
	canonical := func(path string) string {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return filepath.Clean(path)
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			return resolved
		}
		return absolute
	}
	return canonical(left) == canonical(right)
}

// CreatedHere reports whether this process started the runtime through up. It
// is what limits down to runtimes nobody else is relying on.
func (r *Resolver) CreatedHere(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.created[id]
}

func (r *Resolver) forget(id string) {
	r.mu.Lock()
	scope := r.clients[id]
	delete(r.clients, id)
	delete(r.created, id)
	r.mu.Unlock()
	if scope != nil && scope.close != nil {
		_ = scope.close()
	}
}
