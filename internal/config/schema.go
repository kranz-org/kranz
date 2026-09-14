// Package config defines, loads, merges, and validates Kranz configurations.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// Config is the normalized root structure for every supported source format.
type Config struct {
	Project                string                      `yaml:"project"`
	Version                string                      `yaml:"version,omitempty"`
	Runtime                RuntimeConfig               `yaml:"runtime,omitempty"`
	UI                     UIConfig                    `yaml:"ui,omitempty"`
	Defaults               Defaults                    `yaml:"defaults,omitempty"`
	Services               map[string]Service          `yaml:"services,omitempty"`
	ActionGroups           map[string]ActionGroup      `yaml:"action_groups,omitempty"`
	Include                []IncludeSpec               `yaml:"include,omitempty"`
	Overrides              []string                    `yaml:"overrides,omitempty"`
	Protected              map[string]any              `yaml:"protected,omitempty"`
	ServiceOrder           []string                    `yaml:"-"`
	ActionGroupOrder       []string                    `yaml:"-"`
	Source                 SourceFormat                `yaml:"-"`
	Diagnostics            []string                    `yaml:"-"`
	Paths                  []string                    `yaml:"-"`
	WatchPaths             []string                    `yaml:"-"`
	Sources                []ConfigSource              `yaml:"-" json:"sources,omitempty"`
	ServiceMetadata        map[string]EffectiveService `yaml:"-" json:"service_metadata,omitempty"`
	Provenance             []FieldProvenance           `yaml:"-" json:"provenance,omitempty"`
	CompositionDiagnostics []CompositionDiagnostic     `yaml:"-" json:"composition_diagnostics,omitempty"`
	DiscoveryScopes        []DiscoveryScope            `yaml:"-" json:"discovery_scopes,omitempty"`
	dotenvEnv              map[string]string           `yaml:"-"`
	explicitEnv            map[string]string           `yaml:"-"`
}

// IncludeSpec selects one or more autonomous local configuration sources.
// Exactly one of Path, Glob, or Discover must be set.
type IncludeSpec struct {
	Path     string         `yaml:"path,omitempty" json:"path,omitempty"`
	Glob     string         `yaml:"glob,omitempty" json:"glob,omitempty"`
	Discover *DiscoverySpec `yaml:"discover,omitempty" json:"discover,omitempty"`
	MaxDepth *int           `yaml:"max_depth,omitempty" json:"max_depth,omitempty"`
}

// DiscoverySpec controls a bounded filesystem search for autonomous configs.
type DiscoverySpec struct {
	Root           string `yaml:"root,omitempty" json:"root,omitempty"`
	MaxDepth       *int   `yaml:"max_depth,omitempty" json:"max_depth,omitempty"`
	FollowSymlinks bool   `yaml:"follow_symlinks,omitempty" json:"follow_symlinks,omitempty"`
}

type ConfigSourceKind string

const (
	SourceExplicit      ConfigSourceKind = "explicit"
	SourceGlob          ConfigSourceKind = "glob"
	SourceDiscovery     ConfigSourceKind = "discovery"
	SourceNestedInclude ConfigSourceKind = "nested_include"
	SourceVirtualRoot   ConfigSourceKind = "virtual_root"
	SourceOverride      ConfigSourceKind = "override"
)

// Label returns the human-readable name of a source kind. It is defined once
// so every delivery surface names the same kind identically.
func (k ConfigSourceKind) Label() string {
	switch k {
	case SourceExplicit:
		return "explicit"
	case SourceGlob:
		return "glob"
	case SourceDiscovery:
		return "discovered"
	case SourceNestedInclude:
		return "include"
	case SourceVirtualRoot:
		return "virtual root"
	case SourceOverride:
		return "override"
	default:
		if k == "" {
			return "explicit"
		}
		return string(k)
	}
}

// HasVirtualRoot reports whether a resolved source list came from discovery
// with no root file, so the sources hang off a synthetic virtual root rather
// than a real file. It is the one rule behind the CLI's discovery request and
// the TUI's session paths.
func HasVirtualRoot(sources []ConfigSource) bool {
	for _, source := range sources {
		if source.IsVirtualRoot() {
			return true
		}
	}
	return false
}

// IsVirtualRoot reports whether the source is the synthetic root discovery
// creates when no root file exists.
func (s ConfigSource) IsVirtualRoot() bool { return s.Kind == SourceVirtualRoot }

// ConfigSource describes one canonical input without exposing its absolute
// path through normal user-facing serialization.
type ConfigSource struct {
	ID             string           `json:"id"`
	CanonicalPath  string           `json:"-"`
	DisplayPath    string           `json:"display_path"`
	Kind           ConfigSourceKind `json:"kind"`
	ParentID       string           `json:"parent_id,omitempty"`
	Depth          int              `json:"depth"`
	DiscoveryDepth *int             `json:"discovery_depth,omitempty"`
	Order          int              `json:"order"`
	Truncated      bool             `json:"truncated,omitempty"`
}

// DiscoveryScope is watched as a directory so additions and removals trigger
// the same effective-graph rebuild as edits to already loaded files.
type DiscoveryScope struct {
	Path           string `json:"-"`
	DisplayPath    string `json:"display_path"`
	MaxDepth       *int   `json:"max_depth,omitempty"`
	FollowSymlinks bool   `json:"follow_symlinks"`
}

type ProvenanceStage string

const (
	StageExplicit  ProvenanceStage = "explicit"
	StageDefault   ProvenanceStage = "default"
	StageOverride  ProvenanceStage = "override"
	StageProtected ProvenanceStage = "protected"
	StageBuiltIn   ProvenanceStage = "built_in"
)

// FieldProvenance records the winning source and, for protected values, the
// rejected value without bypassing the shared redaction policy.
type FieldProvenance struct {
	ServiceID          string          `json:"service_id,omitempty"`
	FieldPath          string          `json:"field_path"`
	ValueSourceID      string          `json:"value_source_id"`
	Stage              ProvenanceStage `json:"stage"`
	ReplacedSourceID   string          `json:"replaced_source_id,omitempty"`
	ProtectedRejection bool            `json:"protected_rejection,omitempty"`
	OriginalValue      any             `json:"original_value,omitempty"`
	EffectiveValue     any             `json:"effective_value,omitempty"`
}

// EffectiveService separates immutable source identity from its display name.
type EffectiveService struct {
	ID          string `json:"id"`
	SourceID    string `json:"source_id"`
	SourceName  string `json:"source_name"`
	DisplayName string `json:"display_name"`
	ResolvedDir string `json:"-"`
}

type CompositionDiagnostic struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	SourceID string `json:"source_id,omitempty"`
}

// RuntimeConfig controls the discoverable local runtime identity.
type RuntimeConfig struct {
	Name string `yaml:"name,omitempty"`
}

// UIConfig defines project-specific presentation defaults.
type UIConfig struct {
	Theme      string `yaml:"theme,omitempty"`
	Accent     string `yaml:"accent,omitempty"`
	Background string `yaml:"background,omitempty"`
	ColorMode  string `yaml:"color_mode,omitempty"`
}

// Supported UI appearance sources and palette modes.
const (
	UIBackgroundTerminal = "terminal"
	UIBackgroundTheme    = "theme"
	UIColorModeAuto      = "auto"
	UIColorModeDark      = "dark"
	UIColorModeLight     = "light"
)

// SourceFormat identifies the configuration dialect loaded by Kranz.
type SourceFormat string

const (
	SourceKranz          SourceFormat = "kranz"
	SourceProcessCompose SourceFormat = "process-compose"
	SourceProcfile       SourceFormat = "procfile"
)

// Defaults contains values inherited by every service that omits them.
type Defaults struct {
	Dir      string            `yaml:"dir,omitempty"`
	Shell    string            `yaml:"shell,omitempty"`
	Env      map[string]string `yaml:"env,omitempty"`
	EnvFiles []string          `yaml:"env_files,omitempty"`
}

// Service describes one managed process and its lifecycle policy.
type Service struct {
	Command              string                      `yaml:"command,omitempty"`
	Description          string                      `yaml:"description,omitempty"`
	Supervision          SupervisionMode             `yaml:"supervision,omitempty"`
	StopOnExit           *bool                       `yaml:"stop_on_exit,omitempty"`
	Lifecycle            LifecycleConfig             `yaml:"lifecycle,omitempty"`
	Dir                  string                      `yaml:"dir,omitempty"`
	Shell                string                      `yaml:"shell,omitempty"`
	Ports                []int                       `yaml:"ports,omitempty"`
	DetectPorts          *bool                       `yaml:"detect_ports,omitempty"`
	Tags                 []string                    `yaml:"tags,omitempty"`
	DependsOn            []string                    `yaml:"depends_on,omitempty"`
	DependencyConditions map[string]DependencyConfig `yaml:"dependency_conditions,omitempty"`
	Env                  map[string]string           `yaml:"env,omitempty"`
	EnvFiles             []string                    `yaml:"env_files,omitempty"`
	HealthCheck          *HealthCheckConfig          `yaml:"healthcheck,omitempty"`
	ReadyLogLine         string                      `yaml:"ready_log_line,omitempty"`
	Availability         AvailabilityConfig          `yaml:"availability,omitempty"`
	Shutdown             ShutdownConfig              `yaml:"shutdown,omitempty"`
	SuccessExitCodes     []int                       `yaml:"success_exit_codes,omitempty"`
	Disabled             bool                        `yaml:"disabled,omitempty"`
	DisableDotenv        bool                        `yaml:"is_dotenv_disabled,omitempty"`
	Actions              map[string]Action           `yaml:"actions,omitempty"`
	ActionOrder          []string                    `yaml:"-"`
	BeforeStart          []Prerequisite              `yaml:"before_start,omitempty"`
	disabledSet          bool                        `yaml:"-"`
}

// SupervisionMode identifies the source of truth for a service lifecycle.
type SupervisionMode string

const (
	SupervisionProcess  SupervisionMode = "process"
	SupervisionDetached SupervisionMode = "detached"
)

// LifecycleConfig contains the explicit form of service lifecycle operations.
// Command is shorthand for Start and is normalized before layered merging.
type LifecycleConfig struct {
	Start  *Action                `yaml:"start,omitempty"`
	Stop   *Action                `yaml:"stop,omitempty"`
	Status *LifecycleStatusConfig `yaml:"status,omitempty"`
	Logs   *Action                `yaml:"logs,omitempty"`
}

// LifecycleStatusConfig observes whether a detached resource exists. It uses
// the common check shape without conflating existence with service health.
type LifecycleStatusConfig struct {
	CheckConfig      `yaml:",inline"`
	StoppedInterval  time.Duration `yaml:"stopped_interval,omitempty"`
	RunningExitCodes []int         `yaml:"running_exit_codes,omitempty"`
	StoppedExitCodes []int         `yaml:"stopped_exit_codes,omitempty"`
}

// SupervisionMode resolves the backwards-compatible default.
func (s Service) SupervisionMode() SupervisionMode {
	if s.Supervision == "" {
		return SupervisionProcess
	}
	return s.Supervision
}

// IsDetached reports whether lifecycle state is external to the start process.
func (s Service) IsDetached() bool { return s.SupervisionMode() == SupervisionDetached }

// StartAction returns the canonical start operation, with a fallback for
// programmatically constructed configurations that have not passed the loader.
func (s Service) StartAction() *Action {
	if s.Lifecycle.Start != nil {
		return s.Lifecycle.Start
	}
	if s.Command == "" {
		return nil
	}
	return &Action{
		Command:  s.Command,
		Dir:      s.Dir,
		Shell:    s.Shell,
		Env:      s.Env,
		EnvFiles: s.EnvFiles,
	}
}

// StopOnExitEnabled resolves supervision-specific shutdown ownership.
func (s Service) StopOnExitEnabled() bool {
	if s.StopOnExit != nil {
		return *s.StopOnExit
	}
	return !s.IsDetached()
}

// PrerequisiteRun controls how often a prerequisite runs within one session.
type PrerequisiteRun string

const (
	// PrerequisiteOnce runs a prerequisite until it first succeeds, then treats
	// it as satisfied for the rest of the session, including restarts.
	PrerequisiteOnce PrerequisiteRun = "once"
	// PrerequisiteAlways runs a prerequisite before every start and restart.
	PrerequisiteAlways PrerequisiteRun = "always"
)

// Prerequisite references an action that must succeed before a service starts.
// It is a structured reference rather than an inline command, so prerequisites
// stay visible, runnable, and inspectable as ordinary actions on their own.
type Prerequisite struct {
	// Service names the service owning the action. Empty means the service
	// declaring the prerequisite.
	Service string `yaml:"service,omitempty"`
	// Group names the action group owning the action. Mutually exclusive with
	// Service.
	Group  string          `yaml:"group,omitempty"`
	Action string          `yaml:"action"`
	Run    PrerequisiteRun `yaml:"run,omitempty"`
}

// RunPolicy resolves the optional run frequency.
func (p Prerequisite) RunPolicy() PrerequisiteRun {
	if p.Run == "" {
		return PrerequisiteOnce
	}
	return p.Run
}

// ActionID resolves the referenced action's identity. owner is the service that
// declared the prerequisite and supplies the default scope.
func (p Prerequisite) ActionID(owner string) ActionID {
	if p.Group != "" {
		return ActionID{OwnerKind: ActionOwnerGroup, Owner: p.Group, Name: p.Action}
	}
	service := p.Service
	if service == "" {
		service = owner
	}
	return ActionID{OwnerKind: ActionOwnerService, Owner: service, Name: p.Action}
}

// String renders the reference for logs, confirmations, and error messages.
// The owning scope is named only when it differs from the declaring service,
// so the common case reads as plain prose instead of a qualified path.
func (p Prerequisite) String(owner string) string {
	id := p.ActionID(owner)
	switch {
	case id.OwnerKind == ActionOwnerGroup:
		return fmt.Sprintf("group %q action %q", id.Owner, id.Name)
	case id.Owner != owner:
		return fmt.Sprintf("service %q action %q", id.Owner, id.Name)
	default:
		return fmt.Sprintf("action %q", id.Name)
	}
}

// Action describes one explicitly configured command that runs to completion.
// Its owner supplies any omitted execution context.
type Action struct {
	Command     string            `yaml:"command"`
	Description string            `yaml:"description,omitempty"`
	Dir         string            `yaml:"dir,omitempty"`
	Shell       string            `yaml:"shell,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	EnvFiles    []string          `yaml:"env_files,omitempty"`
	Timeout     time.Duration     `yaml:"timeout,omitempty"`
	Confirm     *bool             `yaml:"confirm,omitempty"`
	Interactive *bool             `yaml:"interactive,omitempty"`
}

// ConfirmationRequired resolves the optional confirmation flag.
func (a Action) ConfirmationRequired() bool {
	return a.Confirm != nil && *a.Confirm
}

// InteractiveEnabled resolves the optional terminal handoff flag.
func (a Action) InteractiveEnabled() bool {
	return a.Interactive != nil && *a.Interactive
}

// ActionGroup owns project-level actions that do not belong to a managed
// service, while providing shared execution context for those actions.
type ActionGroup struct {
	Description string            `yaml:"description,omitempty"`
	Dir         string            `yaml:"dir,omitempty"`
	Shell       string            `yaml:"shell,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	EnvFiles    []string          `yaml:"env_files,omitempty"`
	Actions     map[string]Action `yaml:"actions"`
	ActionOrder []string          `yaml:"-"`
}

// ActionOwnerKind distinguishes service-scoped and project-level actions.
type ActionOwnerKind string

const (
	ActionOwnerService   ActionOwnerKind = "service"
	ActionOwnerGroup     ActionOwnerKind = "group"
	ActionOwnerLifecycle ActionOwnerKind = "lifecycle"
)

// ActionID is a comparable, unambiguous runtime identity. Names remain opaque,
// so natural keys such as build:launcher do not require delimiter parsing.
type ActionID struct {
	OwnerKind ActionOwnerKind `json:"owner_kind"`
	Owner     string          `json:"owner"`
	Name      string          `json:"name"`
}

// ResolveAction returns the normalized action identified by id.
func (c *Config) ResolveAction(id ActionID) (Action, bool) {
	if c == nil {
		return Action{}, false
	}
	switch id.OwnerKind {
	case ActionOwnerService:
		service, exists := c.Services[id.Owner]
		if !exists {
			return Action{}, false
		}
		action, exists := service.Actions[id.Name]
		return action, exists
	case ActionOwnerGroup:
		group, exists := c.ActionGroups[id.Owner]
		if !exists {
			return Action{}, false
		}
		action, exists := group.Actions[id.Name]
		return action, exists
	default:
		return Action{}, false
	}
}

// ActionIDs returns every configured action in deterministic owner/name order.
func (c *Config) ActionIDs() []ActionID {
	if c == nil {
		return nil
	}
	ids := make([]ActionID, 0)
	for _, owner := range c.ServiceNames() {
		for _, name := range c.Services[owner].ActionNames() {
			ids = append(ids, ActionID{OwnerKind: ActionOwnerService, Owner: owner, Name: name})
		}
	}
	for _, owner := range c.ActionGroupNames() {
		for _, name := range c.ActionGroups[owner].ActionNames() {
			ids = append(ids, ActionID{OwnerKind: ActionOwnerGroup, Owner: owner, Name: name})
		}
	}
	return ids
}

// ActionGroupNames returns action group names in declaration order.
func (c *Config) ActionGroupNames() []string {
	return orderedNames(c.ActionGroupOrder, c.ActionGroups)
}

// ActionNames returns the service action names in declaration order.
func (s Service) ActionNames() []string {
	return orderedNames(s.ActionOrder, s.Actions)
}

// ActionNames returns the group action names in declaration order.
func (g ActionGroup) ActionNames() []string {
	return orderedNames(g.ActionOrder, g.Actions)
}

// PortDiscoveryEnabled resolves the service-level tri-state. Services without
// configured port hints use runtime discovery by default.
func (s Service) PortDiscoveryEnabled() bool {
	if s.DetectPorts != nil {
		return *s.DetectPorts
	}
	if s.IsDetached() {
		return false
	}
	return len(s.Ports) == 0
}

// DependencyConfig defines the condition required from one dependency.
type DependencyConfig struct {
	Condition DependencyCondition `yaml:"condition,omitempty"`
}

// DependencyCondition identifies when a dependent service may start.
type DependencyCondition string

const (
	DependencyStarted               DependencyCondition = "process_started"
	DependencyHealthy               DependencyCondition = "process_healthy"
	DependencyCompleted             DependencyCondition = "process_completed"
	DependencyCompletedSuccessfully DependencyCondition = "process_completed_successfully"
	DependencyLogReady              DependencyCondition = "process_log_ready"
)

// AvailabilityConfig controls restart and project-exit behavior after completion.
type AvailabilityConfig struct {
	Restart       string        `yaml:"restart,omitempty"`
	Backoff       time.Duration `yaml:"backoff,omitempty"`
	MaxRestarts   int           `yaml:"max_restarts,omitempty"`
	ExitOnEnd     bool          `yaml:"exit_on_end,omitempty"`
	ExitOnSkipped bool          `yaml:"exit_on_skipped,omitempty"`
}

// ShutdownConfig customizes graceful termination for one service.
type ShutdownConfig struct {
	Command    string        `yaml:"command,omitempty"`
	Timeout    time.Duration `yaml:"timeout,omitempty"`
	Signal     int           `yaml:"signal,omitempty"`
	ParentOnly bool          `yaml:"parent_only,omitempty"`
}

// HealthCheckConfig defines independent readiness and liveness probes.
type HealthCheckConfig struct {
	Readiness *CheckConfig `yaml:"readiness,omitempty"`
	Liveness  *CheckConfig `yaml:"liveness,omitempty"`
}

// CheckConfig describes one HTTP, TCP, or command probe.
type CheckConfig struct {
	Type              CheckType         `yaml:"type"`
	URL               string            `yaml:"url,omitempty"`
	Port              int               `yaml:"port,omitempty"`
	PortFrom          string            `yaml:"port_from,omitempty"`
	DetectedPortIndex *int              `yaml:"detected_port_index,omitempty"`
	Command           string            `yaml:"command,omitempty"`
	Headers           map[string]string `yaml:"headers,omitempty"`
	StatusCode        int               `yaml:"status_code,omitempty"`
	InitialDelay      time.Duration     `yaml:"initial_delay,omitempty"`
	Interval          time.Duration     `yaml:"interval,omitempty"`
	Timeout           time.Duration     `yaml:"timeout,omitempty"`
	FailureThreshold  int               `yaml:"failure_threshold,omitempty"`
}

// UsesDetectedPort reports whether the probe resolves its port from the
// service's runtime listeners. Omitting a TCP port or an HTTP URL port is the
// concise form of port_from: detected; static HTTP defaults stay expressible as
// explicit :80 or :443 URLs.
func (c *CheckConfig) UsesDetectedPort() bool {
	if c == nil {
		return false
	}
	if c.PortFrom == PortFromDetected {
		return true
	}
	if c.PortFrom != "" {
		return false
	}
	if c.Type == CheckTCP {
		return c.Port == 0
	}
	if c.Type != CheckHTTP || c.URL == "" {
		return false
	}
	parsed, err := url.Parse(c.URL)
	return err == nil && parsed.Hostname() != "" && parsed.Port() == ""
}

// CheckType identifies the transport used by a health probe.
type CheckType string

const (
	CheckHTTP        CheckType = "http"
	CheckTCP         CheckType = "tcp"
	CheckCommand     CheckType = "command"
	PortFromDetected           = "detected"
)

// Default probe timings. These are the single source of truth for every
// documented default: readiness, liveness, and lifecycle status probes all
// resolve unset values through these constants.
const (
	DefaultCheckInterval         = 5 * time.Second
	DefaultCheckTimeout          = 2 * time.Second
	DefaultCheckFailureThreshold = 3
	// DefaultStoppedStatusInterval polls a resource that is stopped or unknown.
	// Such a resource only changes when something outside Kranz acts on it, so
	// it is polled on a flat, predictable schedule rather than a derived one.
	DefaultStoppedStatusInterval = 30 * time.Second
)

// ServiceStatus is the current lifecycle state of a managed service.
type ServiceStatus int

const (
	StatusStopped ServiceStatus = iota
	StatusStarting
	StatusRunning
	StatusUnhealthy
	StatusStopping
	StatusUnknown
)

// String returns the human-readable lifecycle state.
func (s ServiceStatus) String() string {
	switch s {
	case StatusStopped:
		return "stopped"
	case StatusStarting:
		return "starting"
	case StatusRunning:
		return "running"
	case StatusUnhealthy:
		return "unhealthy"
	case StatusStopping:
		return "stopping"
	case StatusUnknown:
		return "unknown"
	default:
		// Unreachable for configured states. Kept distinct from StatusUnknown so
		// a corrupted value is never mistaken for a deliberate observation.
		return fmt.Sprintf("invalid(%d)", int(s))
	}
}

func (s ServiceStatus) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

func (s *ServiceStatus) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	for candidate := StatusStopped; candidate <= StatusUnknown; candidate++ {
		if candidate.String() == value {
			*s = candidate
			return nil
		}
	}
	return fmt.Errorf("unknown service status %q", value)
}

// ServiceState is a concurrency-safe snapshot of mutable service state.
type ServiceState struct {
	Status ServiceStatus `json:"status"`
	// Run numbers this service's executions the way an action's run numbers
	// its invocations. It is the stable address a log query, a transition, and
	// a failure report all use for the same attempt, so "the logs of the last
	// failed start" is one identifier rather than a reconstructed time range.
	Run          uint32    `json:"run,omitempty"`
	PID          int       `json:"pid,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	ReadyAt      time.Time `json:"ready_at,omitempty"`
	LastLiveness time.Time `json:"last_liveness,omitempty"`
	FailedChecks int       `json:"failed_checks,omitempty"`
	NewLogCount  int       `json:"new_log_count,omitempty"`
	Completed    bool      `json:"completed,omitempty"`
	ExitCode     int       `json:"exit_code"`
	ExitError    string    `json:"exit_error,omitempty"`
	RestartCount int       `json:"restart_count,omitempty"`
	// Cause explains a state whose reason is not visible in the state itself.
	// A service that is simply stopped has none; a service that stayed stopped
	// because a prerequisite failed carries the structured fact, so a reader
	// does not have to recover the causal chain from log text.
	Cause *StateCause `json:"cause,omitempty"`
}

// StateCause is the structured reason a service is in its current state.
type StateCause struct {
	// Type is one of prerequisite_failed, port_conflict, dependency_failed,
	// start_failed, or exited.
	Type       string    `json:"type"`
	Message    string    `json:"message,omitempty"`
	At         time.Time `json:"at,omitempty"`
	Action     string    `json:"action,omitempty"`
	ActionRun  uint32    `json:"action_run,omitempty"`
	Dependency string    `json:"dependency,omitempty"`
	Port       int       `json:"port,omitempty"`
	PID        int       `json:"pid,omitempty"`
	Process    string    `json:"process,omitempty"`
	ExitCode   *int      `json:"exit_code,omitempty"`
}

// PortInfo identifies the process listening on a configured port.
type PortInfo struct {
	Port     int    `json:"port"`
	Address  string `json:"address"`
	Protocol string `json:"protocol"`
	PID      int    `json:"pid"`
	Process  string `json:"process,omitempty"`
	Command  string `json:"command,omitempty"`
}

// LogLevel is the semantic severity inferred for a log line.
type LogLevel int

const (
	LogError LogLevel = iota
	LogWarn
	LogInfo
	LogDebug
)

// LogEntry stores one captured log line and its metadata.
type LogEntry struct {
	Sequence  uint64    `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	// Run numbers the execution that produced the line. Services stream
	// continuously and leave it zero; actions number every invocation so a
	// single run stays addressable after later ones append to the same buffer.
	Run   uint32   `json:"run,omitempty"`
	Level LogLevel `json:"level"`
	Text  string   `json:"text"`
	Raw   string   `json:"raw"`
}

// Notification is one entry in the in-memory notification center.
type Notification struct {
	Time    time.Time
	Level   LogLevel
	Service string
	Message string
}
