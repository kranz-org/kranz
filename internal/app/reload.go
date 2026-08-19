package app

import (
	"fmt"
	"os"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// Configuration hot reload. This is the same stamping and debounce pipeline
// that used to live in the TUI (internal/ui/model_config.go): watch a set of
// paths by mtime and size, and only re-parse when one of them actually
// changed. An invalid file leaves the last known good runtime untouched.

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

	next, err := config.LoadFiles(paths)
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
	l.cfg = next
	l.watchPaths = watchedConfigPaths(l.configPaths, next.WatchPaths)
	l.generation++
	l.loadedAt = time.Now()
	l.lastReloadErr = ""
	l.cfgMu.Unlock()
	if stamps, err := readConfigStamps(l.watchPathsSnapshot()); err == nil {
		l.recordReloadStamps(stamps)
	}
	return result, nil
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
			return result, fmt.Errorf("stat %s: %w", path, err)
		}
		result[path] = configStamp{Modified: info.ModTime().UnixNano(), Size: info.Size()}
	}
	return result, nil
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
