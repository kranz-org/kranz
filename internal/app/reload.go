package app

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// Configuration hot reload. This is the same stamping and debounce pipeline
// that used to live in the TUI (internal/ui/model_config.go): watch a set of
// paths by mtime and size, and only re-parse when one of them actually
// changed. An invalid file leaves the last known good runtime untouched.

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

// Reload debounces to at most once per second unless force is true, matching
// the interval the TUI's polling tick used to enforce on its own.
const reloadDebounce = time.Second

// Reload re-reads the configuration if a watched path changed, and applies
// it to the running services. A concurrent Reload call while one is already
// in flight is a no-op, reported as (ReloadResult{}, nil).
func (l *Local) Reload(force bool) (ReloadResult, error) {
	l.cfgMu.Lock()
	if len(l.configPaths) == 0 && l.loadOptions == nil {
		l.cfgMu.Unlock()
		return ReloadResult{}, nil
	}
	if l.reloadBusy {
		l.cfgMu.Unlock()
		return ReloadResult{}, nil
	}
	if !force && time.Since(l.lastConfigScan) < reloadDebounce {
		l.cfgMu.Unlock()
		return ReloadResult{}, nil
	}
	l.lastConfigScan = time.Now()
	l.reloadBusy = true
	paths := append([]string(nil), l.configPaths...)
	loadOptions := cloneLoadOptions(l.loadOptions)
	watchPaths := append([]string(nil), l.watchPaths...)
	previousStamps := cloneConfigStamps(l.stamps)
	l.cfgMu.Unlock()

	defer func() {
		l.cfgMu.Lock()
		l.reloadBusy = false
		l.cfgMu.Unlock()
	}()

	stamps, err := readConfigStamps(watchPaths)
	if err != nil {
		l.recordReloadStamps(stamps)
		return ReloadResult{}, err
	}
	changed := force || !equalConfigStamps(previousStamps, stamps)
	l.recordReloadStamps(stamps)
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
	l.watchPaths = nextWatchPaths
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
	if !slices.Equal(watchPaths, nextWatchPaths) {
		if fresh, err := readConfigStamps(nextWatchPaths); err == nil {
			l.recordReloadStamps(fresh)
		}
	}
	return result, nil
}

// AcknowledgeExternalWrite implements API.AcknowledgeExternalWrite.
func (l *Local) AcknowledgeExternalWrite() {
	if stamps, err := readConfigStamps(l.watchPathsSnapshot()); err == nil {
		l.recordReloadStamps(stamps)
	}
}

func (l *Local) watchPathsSnapshot() []string {
	l.cfgMu.RLock()
	defer l.cfgMu.RUnlock()
	return append([]string(nil), l.watchPaths...)
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
	result := make(map[string]configStamp, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			result[path] = configStamp{}
			continue
		}
		if err != nil {
			return result, fmt.Errorf("stat watched path %s: %w", filepath.Base(path), redactPath(err, path))
		}
		if !info.IsDir() {
			result[path] = configStamp{Modified: info.ModTime().UnixNano(), Size: info.Size()}
			continue
		}
		hash, entries, err := stampDiscoveryScope(path)
		if err != nil {
			return result, fmt.Errorf("scan discovery scope %s: %w", filepath.Base(path), redactPath(err, path))
		}
		result[path] = configStamp{Modified: hash, Size: entries}
	}
	return result, nil
}

// stampDiscoveryScope hashes only the files discovery can actually load under a
// scope. Nested directories are still walked, but an entry that is not a
// supported config name is skipped before any stat, so a directory holding
// thousands of unrelated files costs one directory read instead of one hash per
// file.
func stampDiscoveryScope(path string) (int64, int64, error) {
	hash := fnv.New64a()
	var entries int64
	err := filepath.WalkDir(path, func(entryPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !config.IsConfigFileName(entry.Name()) {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		relative, _ := filepath.Rel(path, entryPath)
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", relative, info.ModTime().UnixNano(), info.Size())
		entries++
		return nil
	})
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
