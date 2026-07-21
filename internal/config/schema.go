// Package config defines, loads, merges, and validates Kranz configurations.
package config

import "time"

// Config is the root structure of a native Kranz configuration.
type Config struct {
	Project     string             `yaml:"project"`
	Version     string             `yaml:"version,omitempty"`
	UI          UIConfig           `yaml:"ui,omitempty"`
	Defaults    Defaults           `yaml:"defaults,omitempty"`
	Services    map[string]Service `yaml:"services"`
	Source      SourceFormat       `yaml:"-"`
	Diagnostics []string           `yaml:"-"`
	Paths       []string           `yaml:"-"`
	WatchPaths  []string           `yaml:"-"`
	dotenvEnv   map[string]string  `yaml:"-"`
	explicitEnv map[string]string  `yaml:"-"`
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
	Command              string                      `yaml:"command"`
	Description          string                      `yaml:"description,omitempty"`
	Dir                  string                      `yaml:"dir,omitempty"`
	Shell                string                      `yaml:"shell,omitempty"`
	Ports                []int                       `yaml:"ports,omitempty"`
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
	disabledSet          bool                        `yaml:"-"`
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
	Type             CheckType         `yaml:"type"`
	URL              string            `yaml:"url,omitempty"`
	Port             int               `yaml:"port,omitempty"`
	Command          string            `yaml:"command,omitempty"`
	Headers          map[string]string `yaml:"headers,omitempty"`
	StatusCode       int               `yaml:"status_code,omitempty"`
	InitialDelay     time.Duration     `yaml:"initial_delay,omitempty"`
	Interval         time.Duration     `yaml:"interval,omitempty"`
	Timeout          time.Duration     `yaml:"timeout,omitempty"`
	FailureThreshold int               `yaml:"failure_threshold,omitempty"`
}

// CheckType identifies the transport used by a health probe.
type CheckType string

const (
	CheckHTTP    CheckType = "http"
	CheckTCP     CheckType = "tcp"
	CheckCommand CheckType = "command"
)

// ServiceStatus is the current lifecycle state of a managed service.
type ServiceStatus int

const (
	StatusStopped ServiceStatus = iota
	StatusStarting
	StatusRunning
	StatusUnhealthy
	StatusStopping
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
	default:
		return "unknown"
	}
}

// ServiceState is a concurrency-safe snapshot of mutable service state.
type ServiceState struct {
	Status       ServiceStatus
	PID          int
	StartedAt    time.Time
	ReadyAt      time.Time
	LastLiveness time.Time
	FailedChecks int
	NewLogCount  int
	Completed    bool
	ExitCode     int
	ExitError    string
	RestartCount int
}

// PortInfo identifies the process listening on a configured port.
type PortInfo struct {
	Port     int
	Address  string
	Protocol string
	PID      int
	Process  string
	Command  string
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
	Timestamp time.Time
	Level     LogLevel
	Text      string
	Raw       string
}

// Notification is one entry in the in-memory notification center.
type Notification struct {
	Time    time.Time
	Level   LogLevel
	Service string
	Message string
}
