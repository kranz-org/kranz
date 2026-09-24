package app

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// Configuration reload uses watched paths to avoid reparsing unchanged files.
// An invalid file leaves the last known good runtime untouched.

// configStamp is one watch target's cheap change signature. Modified and Size
// have a different meaning per target kind, and the double meaning is
// deliberate: a file records its modification time and size, while a directory
// (a discovery scope) cannot answer either, so Modified holds a hash of its
// relevant config entries and Size holds how many were seen. Both are only ever
// compared to the same path's previous stamp, never to another target's.
type configStamp struct {
	Modified int64
	Size     int64
}

type discoveryWatchPolicy struct {
	followSymlinks bool
	maxDepth       int // -1 means unlimited
}

// ConfigChanged checks the same watch set as Reload without changing the
// runtime or acknowledging the new stamps.
func (l *Local) ConfigChanged() (bool, error) {
	l.cfgMu.RLock()
	paths := append([]string(nil), l.watchPaths...)
	policies := cloneDiscoveryWatchPolicies(l.watchPolicies)
	previous := cloneConfigStamps(l.stamps)
	l.cfgMu.RUnlock()
	if len(paths) == 0 {
		return false, nil
	}
	current, err := readConfigStampsWithPolicies(paths, policies)
	if err != nil {
		return false, err
	}
	return !equalConfigStamps(previous, current), nil
}

// Reload re-reads the configuration if a watched path changed, and applies
// it to the running services. A concurrent request reports that the reload is
// already in progress, so an explicit caller cannot mistake a skipped apply
// for success.
func (l *Local) Reload(force bool) (ReloadResult, error) {
	l.cfgMu.Lock()
	if len(l.configPaths) == 0 && l.loadOptions == nil {
		l.cfgMu.Unlock()
		return ReloadResult{}, nil
	}
	if l.reloadBusy {
		l.cfgMu.Unlock()
		return ReloadResult{}, errors.New("configuration reload already in progress")
	}
	l.reloadBusy = true
	paths := append([]string(nil), l.configPaths...)
	loadOptions := cloneLoadOptions(l.loadOptions)
	watchPaths := append([]string(nil), l.watchPaths...)
	watchPolicies := cloneDiscoveryWatchPolicies(l.watchPolicies)
	previousStamps := cloneConfigStamps(l.stamps)
	l.cfgMu.Unlock()

	defer func() {
		l.cfgMu.Lock()
		l.reloadBusy = false
		l.cfgMu.Unlock()
	}()

	stamps, err := readConfigStampsWithPolicies(watchPaths, watchPolicies)
	if err != nil {
		return ReloadResult{}, err
	}
	changed := force || !equalConfigStamps(previousStamps, stamps)
	if !changed {
		return ReloadResult{}, nil
	}

	var next *config.Config
	if loadOptions != nil {
		next, err = config.Compose(*loadOptions)
	} else {
		next, err = config.LoadFiles(paths)
	}
	if err != nil {
		l.recordReloadError(err)
		return ReloadResult{}, err
	}

	result, err := l.manager.ApplyConfig(next)
	if err != nil {
		l.recordReloadError(err)
		return result, err
	}

	l.cfgMu.Lock()
	// The manager may retain the last accepted definition for running services
	// while the newly composed definition is pending. Every surface must expose
	// that accepted runtime graph, not a different desired graph.
	l.cfg = l.manager.Config()
	nextWatchPaths := watchedConfigPaths(next.Paths, next.WatchPaths)
	nextWatchPolicies := discoveryWatchPolicies(next)
	l.watchPaths = nextWatchPaths
	l.watchPolicies = nextWatchPolicies
	l.generation++
	generation := l.generation
	l.loadedAt = time.Now()
	l.lastReloadErr = ""
	// Confirmations are bound to the accepted configuration generation. Clear
	// them only once a new configuration has been applied; polling, debounce,
	// and failed reload attempts leave the accepted plan unchanged.
	l.invalidateConfirmations()
	l.cfgMu.Unlock()
	l.manager.RecordConfigReload(generation)
	l.recordReloadTransition(generation, result)
	// The stamps read before composing are still current when the watch scope
	// did not change, which is the ordinary case, so the second scan the reload
	// used to run on every success is gone. A changed scope alone needs a fresh
	// scan, and replacing the whole map keeps no stale path to compare against.
	if !slices.Equal(watchPaths, nextWatchPaths) || !equalDiscoveryWatchPolicies(watchPolicies, nextWatchPolicies) {
		if fresh, err := readConfigStampsWithPolicies(nextWatchPaths, nextWatchPolicies); err == nil {
			l.recordReloadStamps(fresh)
		}
	} else {
		l.recordReloadStamps(stamps)
	}
	return result, nil
}

// AcknowledgeExternalWrite implements API.AcknowledgeExternalWrite.
func (l *Local) AcknowledgeExternalWrite() {
	paths, policies := l.watchStateSnapshot()
	if stamps, err := readConfigStampsWithPolicies(paths, policies); err == nil {
		l.recordReloadStamps(stamps)
	}
}

func (l *Local) watchStateSnapshot() ([]string, map[string][]discoveryWatchPolicy) {
	l.cfgMu.RLock()
	defer l.cfgMu.RUnlock()
	return append([]string(nil), l.watchPaths...), cloneDiscoveryWatchPolicies(l.watchPolicies)
}

func discoveryWatchPolicies(cfg *config.Config) map[string][]discoveryWatchPolicy {
	policies := make(map[string][]discoveryWatchPolicy, len(cfg.DiscoveryScopes))
	for _, scope := range cfg.DiscoveryScopes {
		policy := discoveryWatchPolicy{followSymlinks: scope.FollowSymlinks, maxDepth: -1}
		if scope.MaxDepth != nil {
			policy.maxDepth = *scope.MaxDepth
		}
		policies[scope.Path] = append(policies[scope.Path], policy)
	}
	return policies
}

func cloneDiscoveryWatchPolicies(source map[string][]discoveryWatchPolicy) map[string][]discoveryWatchPolicy {
	clone := make(map[string][]discoveryWatchPolicy, len(source))
	for path, policies := range source {
		clone[path] = append([]discoveryWatchPolicy(nil), policies...)
	}
	return clone
}

func equalDiscoveryWatchPolicies(left, right map[string][]discoveryWatchPolicy) bool {
	if len(left) != len(right) {
		return false
	}
	for path, policies := range left {
		if !slices.Equal(policies, right[path]) {
			return false
		}
	}
	return true
}

func (l *Local) recordReloadStamps(stamps map[string]configStamp) {
	l.cfgMu.Lock()
	l.stamps = stamps
	l.cfgMu.Unlock()
}

func (l *Local) recordReloadError(err error) {
	l.cfgMu.Lock()
	l.lastReloadErr = err.Error()
	l.cfgMu.Unlock()
}

func readConfigStamps(paths []string) (map[string]configStamp, error) {
	return readConfigStampsWithPolicies(paths, nil)
}

func readConfigStampsWithPolicies(paths []string, policies map[string][]discoveryWatchPolicy) (map[string]configStamp, error) {
	result := make(map[string]configStamp, len(paths))
	for _, path := range paths {
		if strings.ContainsAny(path, "*?[") {
			stamp, err := stampConfigGlob(path)
			if err != nil {
				return result, fmt.Errorf("scan config glob %s: %w", filepath.Base(path), redactPath(err, path))
			}
			result[path] = stamp
			continue
		}
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			result[path] = configStamp{}
			continue
		}
		if err != nil {
			return result, fmt.Errorf("stat watched path %s: %w", filepath.Base(path), redactPath(err, path))
		}
		if !info.IsDir() {
			stamp, err := stampConfigFile(path, info)
			if err != nil {
				return result, fmt.Errorf("read watched file %s: %w", filepath.Base(path), redactPath(err, path))
			}
			result[path] = stamp
			continue
		}
		hash, entries, err := stampDiscoveryScope(path, policies[path])
		if err != nil {
			return result, fmt.Errorf("scan discovery scope %s: %w", filepath.Base(path), redactPath(err, path))
		}
		result[path] = configStamp{Modified: hash, Size: entries}
	}
	return result, nil
}

func stampConfigFile(path string, info os.FileInfo) (configStamp, error) {
	file, err := os.Open(path)
	if err != nil {
		return configStamp{}, err
	}
	defer func() { _ = file.Close() }()
	hash := fnv.New64a()
	if _, err := io.Copy(hash, file); err != nil {
		return configStamp{}, err
	}
	stamp := configStamp{Modified: int64(hash.Sum64()), Size: info.Size()}
	link, err := os.Lstat(path)
	if err == nil && link.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err == nil {
			linked := fnv.New64a()
			_, _ = fmt.Fprintf(linked, "%s\x00%d\x00%d\x00%d", target, stamp.Modified, stamp.Size, link.ModTime().UnixNano())
			stamp.Modified = int64(linked.Sum64())
		}
	}
	return stamp, nil
}

func stampConfigGlob(pattern string) (configStamp, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return configStamp{}, err
	}
	hash := fnv.New64a()
	var count int64
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			return configStamp{}, err
		}
		file := match
		if info.IsDir() {
			file, err = config.Discover(match)
			if err != nil {
				_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", match, info.ModTime().UnixNano())
				continue
			}
			info, err = os.Stat(file)
			if err != nil {
				return configStamp{}, err
			}
		}
		stamp, err := stampConfigFile(file, info)
		if err != nil {
			return configStamp{}, err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", file, stamp.Modified, stamp.Size)
		count++
	}
	return configStamp{Modified: int64(hash.Sum64()), Size: count}, nil
}

// stampDiscoveryScope hashes only the files discovery can actually load under a
// scope. Nested directories are still walked, but an entry that is not a
// supported config name is skipped before any stat, so a directory holding
// thousands of unrelated files costs one directory read instead of one hash per
// file.
func stampDiscoveryScope(path string, policies []discoveryWatchPolicy) (int64, int64, error) {
	if len(policies) == 0 {
		policies = []discoveryWatchPolicy{{maxDepth: -1}}
	}
	combined := fnv.New64a()
	var total int64
	for _, policy := range policies {
		stamp, count, err := stampOneDiscoveryScope(path, policy)
		if err != nil {
			return 0, 0, err
		}
		_, _ = fmt.Fprintf(combined, "%d:%d\x00", stamp, count)
		total += count
	}
	return int64(combined.Sum64()), total, nil
}

func stampOneDiscoveryScope(path string, policy discoveryWatchPolicy) (int64, int64, error) {
	hash := fnv.New64a()
	var entries int64
	seen := make(map[string]bool)
	var walk func(string, int) error
	walk = func(directory string, depth int) error {
		canonical, err := filepath.EvalSymlinks(directory)
		if err != nil {
			return err
		}
		if seen[canonical] {
			return nil
		}
		seen[canonical] = true
		children, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		candidates := make(map[string]os.FileInfo)
		for _, child := range children {
			if child.Type()&os.ModeSymlink != 0 && !policy.followSymlinks {
				continue
			}
			if policy.maxDepth >= 0 && depth >= policy.maxDepth &&
				(child.IsDir() || !config.IsConfigFileName(child.Name())) {
				continue
			}
			if !child.IsDir() && child.Type()&os.ModeSymlink == 0 && !config.IsConfigFileName(child.Name()) {
				continue
			}
			entryPath := filepath.Join(directory, child.Name())
			info, err := os.Stat(entryPath)
			if err != nil {
				return err
			}
			if info.IsDir() {
				if policy.maxDepth >= 0 && depth >= policy.maxDepth {
					continue
				}
				if child.Type()&os.ModeSymlink != 0 {
					target, err := filepath.EvalSymlinks(entryPath)
					if err != nil {
						return err
					}
					relative, _ := filepath.Rel(path, entryPath)
					_, _ = fmt.Fprintf(hash, "link:%s:%s\x00", relative, target)
				}
				if err := walk(entryPath, depth+1); err != nil {
					return err
				}
				continue
			}
			if !config.IsConfigFileName(child.Name()) {
				continue
			}
			candidates[child.Name()] = info
		}
		names := make([]string, 0, len(candidates))
		for name := range candidates {
			names = append(names, name)
		}
		if preferred := config.PreferredConfigName(names); preferred != "" {
			entryPath := filepath.Join(directory, preferred)
			stamp, err := stampConfigFile(entryPath, candidates[preferred])
			if err != nil {
				return err
			}
			relative, _ := filepath.Rel(path, entryPath)
			_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", relative, stamp.Modified, stamp.Size)
			entries++
		}
		return nil
	}
	err := walk(path, 0)
	if err != nil {
		return 0, 0, err
	}
	return int64(hash.Sum64()), entries, nil
}

// pathError keeps errors.Is working on the original error while presenting a
// message with the watch path reduced to its base name, so a diagnostics line
// never publishes an absolute project location.
type pathError struct {
	message string
	cause   error
}

func (e *pathError) Error() string { return e.message }
func (e *pathError) Unwrap() error { return e.cause }

func redactPath(err error, path string) error {
	if err == nil {
		return nil
	}
	message := strings.ReplaceAll(err.Error(), path, filepath.Base(path))
	return &pathError{message: message, cause: err}
}

func watchedConfigPaths(configPaths, auxiliaryPaths []string) []string {
	result := append([]string(nil), configPaths...)
	seen := make(map[string]bool, len(result)+len(auxiliaryPaths))
	for _, path := range result {
		seen[path] = true
	}
	for _, path := range auxiliaryPaths {
		if path != "" && !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}

func cloneConfigStamps(source map[string]configStamp) map[string]configStamp {
	result := make(map[string]configStamp, len(source))
	for path, stamp := range source {
		result[path] = stamp
	}
	return result
}

func equalConfigStamps(left, right map[string]configStamp) bool {
	if len(left) != len(right) {
		return false
	}
	for path, stamp := range left {
		if right[path] != stamp {
			return false
		}
	}
	return true
}
