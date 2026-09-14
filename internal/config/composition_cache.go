package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// SourceCache retains parsed autonomous files between live-reload builds.
// Composition still resolves discovery on every changed scope; only unchanged
// file parsing and format conversion are reused.
type SourceCache struct {
	mu      sync.Mutex
	entries map[string]cachedSource
	hits    uint64
}

type cachedSource struct {
	stamp  sourceCacheStamp
	config *Config
}

type sourceCacheStamp struct {
	modified       int64
	size           int64
	digest         [32]byte
	dotenvModified int64
	dotenvSize     int64
	dotenvDigest   [32]byte
	dotenvExists   bool
}

func NewSourceCache() *SourceCache {
	return &SourceCache{entries: make(map[string]cachedSource)}
}

// load returns the parsed configuration at path, reusing a previous parse when
// the file and its companion dotenv are unchanged. basePath is the file whose
// directory supplies the adjacent .env that loadFile expands the source with;
// its stamp is part of the invalidation key so editing that dotenv alone still
// rebuilds even when the source file itself is untouched.
func (cache *SourceCache) load(path, basePath string) (*Config, error) {
	if cache == nil {
		return loadFile(path, basePath)
	}
	stamp, err := readSourceCacheStamp(path, basePath)
	if err != nil {
		return nil, err
	}
	key := filepath.Clean(path) + "\x00" + filepath.Clean(basePath)
	cache.mu.Lock()
	entry, found := cache.entries[key]
	cache.mu.Unlock()
	if found && entry.stamp == stamp {
		cache.mu.Lock()
		cache.hits++
		cache.mu.Unlock()
		return cloneAutonomousConfig(entry.config)
	}
	loaded, err := loadFile(path, basePath)
	if err != nil {
		return nil, err
	}
	stored, err := cloneAutonomousConfig(loaded)
	if err != nil {
		return nil, err
	}
	cache.mu.Lock()
	cache.entries[key] = cachedSource{stamp: stamp, config: stored}
	cache.mu.Unlock()
	return cloneAutonomousConfig(stored)
}

func readSourceCacheStamp(path, basePath string) (sourceCacheStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return sourceCacheStamp{}, err
	}
	stamp := sourceCacheStamp{modified: info.ModTime().UnixNano(), size: info.Size()}
	contents, err := os.ReadFile(path)
	if err != nil {
		return sourceCacheStamp{}, err
	}
	stamp.digest = sha256.Sum256(contents)
	dotenv := filepath.Join(filepath.Dir(basePath), ".env")
	if dotenvInfo, dotenvErr := os.Stat(dotenv); dotenvErr == nil {
		stamp.dotenvExists = true
		stamp.dotenvModified = dotenvInfo.ModTime().UnixNano()
		stamp.dotenvSize = dotenvInfo.Size()
		dotenvContents, readErr := os.ReadFile(dotenv)
		if readErr != nil {
			return sourceCacheStamp{}, readErr
		}
		stamp.dotenvDigest = sha256.Sum256(dotenvContents)
	} else if !os.IsNotExist(dotenvErr) {
		return sourceCacheStamp{}, dotenvErr
	}
	return stamp, nil
}

func cloneAutonomousConfig(source *Config) (*Config, error) {
	payload, err := yaml.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("cache configuration: %w", err)
	}
	var clone Config
	if err := yaml.Unmarshal(payload, &clone); err != nil {
		return nil, fmt.Errorf("restore cached configuration: %w", err)
	}
	clone.Source = source.Source
	clone.ServiceOrder = append([]string(nil), source.ServiceOrder...)
	clone.ActionGroupOrder = append([]string(nil), source.ActionGroupOrder...)
	clone.Paths = append([]string(nil), source.Paths...)
	clone.WatchPaths = append([]string(nil), source.WatchPaths...)
	clone.Diagnostics = append([]string(nil), source.Diagnostics...)
	clone.dotenvEnv = cloneStringMap(source.dotenvEnv)
	clone.explicitEnv = cloneStringMap(source.explicitEnv)
	for name, service := range clone.Services {
		service.ActionOrder = append([]string(nil), source.Services[name].ActionOrder...)
		clone.Services[name] = service
	}
	for name, group := range clone.ActionGroups {
		group.ActionOrder = append([]string(nil), source.ActionGroups[name].ActionOrder...)
		clone.ActionGroups[name] = group
	}
	return &clone, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
