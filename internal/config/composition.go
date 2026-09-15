package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrConfigNotFound reports that neither an explicit source nor discovery
// produced any configuration to compose. Callers use errors.Is to tell an
// empty discovery, which may fall back to the supervisor picker, apart from a
// found but invalid configuration, which must surface as its own error.
var ErrConfigNotFound = errors.New("config_not_found")

// LoadOptions selects the inputs used to build one effective configuration.
// Sources are autonomous configuration files (or glob expressions); Overrides
// are ordered patches over the resulting effective names.
type LoadOptions struct {
	Directory      string
	Sources        []string
	Overrides      []string
	FollowSymlinks bool
	Cache          *SourceCache
}

type sourceRequest struct {
	path           string
	kind           ConfigSourceKind
	parentID       string
	depth          int
	discoveryDepth *int
	remaining      *int
}

type sourcedService struct {
	source   ConfigSource
	name     string
	value    Service
	raw      Service
	defaults Defaults
}

type sourcedGroup struct {
	source ConfigSource
	name   string
	value  ActionGroup
}

type protectedLayer struct {
	source ConfigSource
	values map[string]any
}

// truncatedSource remembers a file that was exported with its child includes
// cut off by an include depth limit. A later edge may reach the same file with
// a larger budget and must be able to expand the subtree the first edge could
// not, without exporting the file's own services a second time.
type truncatedSource struct {
	source ConfigSource
	cfg    *Config
}

type provenancePatch struct {
	source ConfigSource
	node   *yaml.Node
}

type composer struct {
	root               string
	followSymlinks     bool
	cache              *SourceCache
	visited            map[string]bool
	truncated          map[string]truncatedSource
	active             map[string]int
	includeCache       map[string]includeMatchesResult
	stack              []ConfigSource
	sources            []ConfigSource
	diagnostics        []CompositionDiagnostic
	scopes             []DiscoveryScope
	services           []sourcedService
	groups             []sourcedGroup
	protected          []protectedLayer
	overrideProvenance map[string][]provenancePatch
	watchPaths         []string
	rootConfig         *Config
	rootCanonical      string
	localServiceNames  map[string]map[string]string
	rawServiceNames    map[string][]string
	localGroupNames    map[string]map[string]string
	rawGroupNames      map[string][]string
}

// includeMatchesResult is one resolved include selector. The filesystem does
// not change during a single build, so the export traversal and the boundary
// cycle check share this result instead of walking the same discovery or glob
// subtree twice.
type includeMatchesResult struct {
	matches   []string
	kind      ConfigSourceKind
	discovery *DiscoverySpec
	root      string
	err       error
}

// newComposer builds the per-build state. It is separate from Compose so a
// test can exercise the include resolver directly without running a full
// composition.
func newComposer(root string, followSymlinks bool, cache *SourceCache) *composer {
	return &composer{
		root:               root,
		followSymlinks:     followSymlinks,
		cache:              cache,
		visited:            make(map[string]bool),
		truncated:          make(map[string]truncatedSource),
		active:             make(map[string]int),
		includeCache:       make(map[string]includeMatchesResult),
		overrideProvenance: make(map[string][]provenancePatch),
	}
}

// Compose is the shared loader used by every delivery surface. It resolves all
// sources before returning, so CLI, TUI, and MCP cannot disagree about names,
// paths, identity, or diagnostics.
func Compose(options LoadOptions) (*Config, error) {
	cfg, err := compose(options)
	if err != nil {
		return nil, sanitizeCompositionError(options.Directory, err)
	}
	return cfg, nil
}

func compose(options LoadOptions) (*Config, error) {
	root := options.Directory
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve composition root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	if canonicalRoot, canonicalErr := filepath.EvalSymlinks(absRoot); canonicalErr == nil {
		absRoot = canonicalRoot
	}
	cache := options.Cache
	if cache == nil {
		cache = NewSourceCache()
	}
	c := newComposer(absRoot, options.FollowSymlinks, cache)

	requests, virtual, err := c.topLevelRequests(options)
	if err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return nil, fmt.Errorf("%w: no configuration was found directly or through discovery", ErrConfigNotFound)
	}
	if virtual {
		virtualSource := ConfigSource{ID: sourceID(c.root + "\x00virtual"), DisplayPath: ".", Kind: SourceVirtualRoot, Order: 0}
		c.sources = append(c.sources, virtualSource)
		for index := range requests {
			requests[index].parentID = virtualSource.ID
			requests[index].depth = 1
		}
	}
	for _, request := range requests {
		if err := c.visit(request); err != nil {
			return nil, err
		}
	}
	effective, err := c.assemble(virtual)
	if err != nil {
		return nil, err
	}
	effective.Sources = append([]ConfigSource(nil), c.sources...)
	effective.CompositionDiagnostics = append([]CompositionDiagnostic(nil), c.diagnostics...)
	for index, path := range options.Overrides {
		if err := applyExternalOverride(effective, c.root, path, index+1); err != nil {
			return nil, err
		}
	}
	if err := c.applyProtected(effective); err != nil {
		return nil, err
	}
	effective.Diagnostics = make([]string, 0, len(effective.CompositionDiagnostics))
	for _, diagnostic := range effective.CompositionDiagnostics {
		effective.Diagnostics = append(effective.Diagnostics, diagnostic.Code+": "+diagnostic.Message)
	}
	effective.DiscoveryScopes = append([]DiscoveryScope(nil), c.scopes...)
	for _, scope := range c.scopes {
		effective.WatchPaths = appendUniqueString(effective.WatchPaths, scope.Path)
	}
	if err := Validate(effective); err != nil {
		return nil, fmt.Errorf("validate effective config: %w", err)
	}
	return effective, nil
}

func (c *composer) topLevelRequests(options LoadOptions) ([]sourceRequest, bool, error) {
	if len(options.Sources) > 0 {
		var requests []sourceRequest
		for _, expression := range options.Sources {
			matches, kind, err := resolveExpression(c.root, c.root, expression)
			if err != nil {
				return nil, false, err
			}
			for _, match := range matches {
				requests = append(requests, sourceRequest{path: match, kind: kind})
			}
		}
		return requests, false, nil
	}
	if primary, err := Discover(c.root); err == nil {
		return []sourceRequest{{path: primary, kind: SourceExplicit}}, false, nil
	}
	var maxDepth *int
	matches, err := discoverConfigs(c.root, maxDepth, options.FollowSymlinks, c.root)
	if err != nil {
		return nil, true, err
	}
	c.scopes = append(c.scopes, DiscoveryScope{Path: c.root, DisplayPath: ".", FollowSymlinks: options.FollowSymlinks})
	requests := make([]sourceRequest, 0, len(matches))
	for _, match := range matches {
		depth := pathDepth(c.root, filepath.Dir(match))
		requests = append(requests, sourceRequest{path: match, kind: SourceDiscovery, discoveryDepth: intPointer(depth)})
	}
	return requests, true, nil
}

func resolveExpression(base, displayRoot, expression string) ([]string, ConfigSourceKind, error) {
	if err := rejectRemote(expression); err != nil {
		return nil, "", err
	}
	path := expression
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	if !hasGlobMeta(expression) {
		if _, err := os.Lstat(path); err != nil {
			return nil, "", fmt.Errorf("explicit config %q: %w", expression, sanitizePathError(displayRoot, path, err))
		}
		return []string{path}, SourceExplicit, nil
	}
	matches, err := filepath.Glob(path)
	if err != nil {
		return nil, "", fmt.Errorf("invalid config glob %q: %w", expression, err)
	}
	if len(matches) == 0 {
		return nil, "", fmt.Errorf("config_glob_empty: pattern %q matched no files", expression)
	}
	var files []string
	for _, match := range matches {
		info, statErr := os.Stat(match)
		if statErr != nil {
			return nil, "", fmt.Errorf("inspect glob match %q: %w", expression, sanitizePathError(displayRoot, match, statErr))
		}
		if info.IsDir() {
			found, discoverErr := Discover(match)
			if discoverErr != nil {
				return nil, "", fmt.Errorf("glob directory %q has no root config: %w", displayBase(displayRoot, match), sanitizePathError(displayRoot, match, discoverErr))
			}
			files = append(files, found)
		} else {
			files = append(files, match)
		}
	}
	sort.Strings(files)
	return files, SourceGlob, nil
}

func (c *composer) visit(request sourceRequest) error {
	canonical, err := filepath.EvalSymlinks(request.path)
	if err != nil {
		return fmt.Errorf("resolve config %s: %w", displayBase(c.root, request.path), sanitizePathError(c.root, request.path, err))
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return err
	}
	canonical = filepath.Clean(canonical)
	if index, cycling := c.active[canonical]; cycling {
		chain := make([]string, 0, len(c.stack)-index+1)
		for _, source := range c.stack[index:] {
			chain = append(chain, source.DisplayPath)
		}
		chain = append(chain, displayBase(c.root, canonical))
		return fmt.Errorf("config_include_cycle: %s", strings.Join(chain, " -> "))
	}
	if c.visited[canonical] {
		c.diagnostics = append(c.diagnostics, CompositionDiagnostic{Code: "source_deduplicated", Message: displayBase(c.root, canonical)})
		return nil
	}
	if truncated, known := c.truncated[canonical]; known {
		if request.remaining == nil || *request.remaining > 0 {
			// The file's own services were already exported; only its child
			// includes were cut off. Expand them now, under the larger budget of
			// this edge, and keep the file on the active stack so a child that
			// points back to it is still reported as a cycle.
			delete(c.truncated, canonical)
			c.visited[canonical] = true
			c.discardTruncation(truncated.source.ID)
			return c.expandUnderActive(canonical, truncated.source, truncated.cfg, request.remaining)
		}
		// Another edge reached the same file with no budget to spare. The
		// truncation diagnostic was already recorded when the file was first
		// exported; recording a source_deduplicated here would hide the cut-off
		// subtree behind an ordinary duplication.
		return nil
	}

	source := ConfigSource{ID: sourceID(canonical), CanonicalPath: canonical, DisplayPath: displayBase(c.root, request.path), Kind: request.kind, ParentID: request.parentID, Depth: request.depth, DiscoveryDepth: request.discoveryDepth, Order: len(c.sources)}
	c.active[canonical] = len(c.stack)
	c.stack = append(c.stack, source)
	defer func() {
		delete(c.active, canonical)
		c.stack = c.stack[:len(c.stack)-1]
	}()

	cfg, err := c.cache.load(canonical, canonical)
	if err != nil {
		return sourceError(source.DisplayPath, canonical, err)
	}
	c.sources = append(c.sources, source)
	if cfg.Source == SourceProcessCompose {
		extension := filepath.Ext(canonical)
		conventional := filepath.Join(filepath.Dir(canonical), "process-compose.override"+extension)
		if canonical != conventional {
			if info, statErr := os.Stat(conventional); statErr == nil && !info.IsDir() {
				layer, loadErr := c.cache.load(conventional, canonical)
				if loadErr != nil {
					return fmt.Errorf("source %s conventional override: %w", source.DisplayPath, loadErr)
				}
				if mergeErr := mergeConfig(cfg, layer); mergeErr != nil {
					return mergeErr
				}
				overrideSource := ConfigSource{ID: sourceID(conventional), CanonicalPath: conventional, DisplayPath: displayBase(c.root, conventional), Kind: SourceOverride, ParentID: source.ID, Depth: source.Depth, Order: len(c.sources)}
				c.sources = append(c.sources, overrideSource)
				cfg.WatchPaths = appendUniqueString(cfg.WatchPaths, conventional)
			}
		}
	}
	if request.depth == 0 && c.rootConfig == nil {
		c.rootConfig = cfg
		c.rootCanonical = canonical
	}
	// Adjacent dotenv values are part of this autonomous source's defaults,
	// exactly as in the single-file loader, and must be merged before the
	// source is normalized or exported.
	cfg.Defaults.Env = mergeStringMap(cfg.dotenvEnv, cfg.Defaults.Env)
	rawServices := make(map[string]Service, len(cfg.Services))
	for name, service := range cfg.Services {
		rawServices[name] = service
	}
	localDefaults := cfg.Defaults
	if err := c.applyLocalOverrides(cfg, source); err != nil {
		return err
	}
	resolveConfigPaths(cfg, filepath.Dir(canonical))
	if err := applyDefaults(cfg); err != nil {
		return sourceError(source.DisplayPath, canonical, err)
	}
	for _, path := range cfg.WatchPaths {
		c.watchPaths = appendUniqueString(c.watchPaths, path)
	}

	for _, name := range cfg.ServiceNames() {
		service := cfg.Services[name]
		service.EnvFiles = resolvedServiceEnvFiles(cfg, service)
		c.services = append(c.services, sourcedService{source: source, name: name, value: service, raw: rawServices[name], defaults: localDefaults})
	}
	for _, name := range cfg.ActionGroupNames() {
		c.groups = append(c.groups, sourcedGroup{source: source, name: name, value: cfg.ActionGroups[name]})
	}
	if len(cfg.Protected) > 0 {
		c.protected = append(c.protected, protectedLayer{source: source, values: cfg.Protected})
	}

	if request.remaining != nil && *request.remaining == 0 && len(cfg.Include) > 0 {
		c.truncated[canonical] = truncatedSource{source: source, cfg: cfg}
		c.markTruncated(source.ID)
		c.diagnostics = append(c.diagnostics, CompositionDiagnostic{Code: "include_depth_truncated", Message: source.DisplayPath, SourceID: source.ID})
		if err := c.checkBoundaryCycles(cfg, source); err != nil {
			return err
		}
		return nil
	}
	c.visited[canonical] = true
	return c.expandIncludes(cfg, source, request.remaining)
}

// expandIncludes visits the child includes of an already exported source. The
// remaining budget counts graph levels below the source; an explicit
// include.max_depth replaces it for that declaration.
func (c *composer) expandIncludes(cfg *Config, source ConfigSource, remaining *int) error {
	for _, include := range cfg.Include {
		children, err := c.resolveInclude(include, source)
		if err != nil {
			return err
		}
		for _, child := range children {
			next := decremented(remaining)
			if include.MaxDepth != nil {
				value := *include.MaxDepth
				next = &value
			}
			child.remaining = next
			if err := c.visit(child); err != nil {
				return err
			}
		}
	}
	return nil
}

// expandUnderActive expands a previously truncated source while holding it on
// the active stack, so a child include that points back to it is reported as a
// cycle rather than silently deduplicated.
func (c *composer) expandUnderActive(canonical string, source ConfigSource, cfg *Config, remaining *int) error {
	c.active[canonical] = len(c.stack)
	c.stack = append(c.stack, source)
	defer func() {
		delete(c.active, canonical)
		c.stack = c.stack[:len(c.stack)-1]
	}()
	return c.expandIncludes(cfg, source, remaining)
}

// markTruncated records that a source's child includes were cut off.
func (c *composer) markTruncated(sourceIDValue string) {
	for index := range c.sources {
		if c.sources[index].ID == sourceIDValue {
			c.sources[index].Truncated = true
			return
		}
	}
}

// discardTruncation clears the truncation marker once a later edge has expanded
// the subtree, so the final graph no longer claims the source is truncated.
func (c *composer) discardTruncation(sourceIDValue string) {
	for index := range c.sources {
		if c.sources[index].ID == sourceIDValue {
			c.sources[index].Truncated = false
			break
		}
	}
	filtered := c.diagnostics[:0]
	for _, diagnostic := range c.diagnostics {
		if diagnostic.Code == "include_depth_truncated" && diagnostic.SourceID == sourceIDValue {
			continue
		}
		filtered = append(filtered, diagnostic)
	}
	c.diagnostics = filtered
}

// validateIncludeSpec enforces the "exactly one selector" rule shared by the
// export traversal and the boundary cycle check.
func validateIncludeSpec(spec IncludeSpec, parentDisplay string) error {
	count := 0
	if spec.Path != "" {
		count++
	}
	if spec.Glob != "" {
		count++
	}
	if spec.Discover != nil {
		count++
	}
	if count != 1 {
		return fmt.Errorf("source %s: include must set exactly one of path, glob, or discover", parentDisplay)
	}
	if spec.MaxDepth != nil && *spec.MaxDepth < 0 {
		return fmt.Errorf("source %s: include.max_depth cannot be negative", parentDisplay)
	}
	if spec.Discover != nil && spec.Discover.MaxDepth != nil && *spec.Discover.MaxDepth < 0 {
		return fmt.Errorf("source %s: discover.max_depth cannot be negative", parentDisplay)
	}
	return nil
}

// includeMatches resolves the concrete files one include selects. The export
// traversal and the boundary cycle check both call it, so the two passes cannot
// disagree about what an include covers or where a discovery root points. The
// discovery spec and its resolved root are returned so callers can record the
// watched scope. Results are cached per selector and base directory for the
// duration of one build: the boundary pass deliberately ignores include
// max_depth, so without the cache it would repeat every discovery and glob the
// export pass already resolved for the truncated subtree.
func (c *composer) includeMatches(spec IncludeSpec, base string) ([]string, ConfigSourceKind, *DiscoverySpec, string, error) {
	key := includeCacheKey(spec, base)
	if cached, ok := c.includeCache[key]; ok {
		return cached.matches, cached.kind, cached.discovery, cached.root, cached.err
	}
	result := c.resolveIncludeMatches(spec, base)
	c.includeCache[key] = result
	return result.matches, result.kind, result.discovery, result.root, result.err
}

// includeCacheKey identifies one selector against one base directory. The
// include-level MaxDepth is intentionally absent: it limits how deep the graph
// is exported, not which files a selector matches, so two includes that differ
// only in include.max_depth share one resolved match set. discover.max_depth is
// part of the key because it does change the set of matched files.
func includeCacheKey(spec IncludeSpec, base string) string {
	var builder strings.Builder
	builder.WriteString(base)
	builder.WriteByte(0)
	builder.WriteString(spec.Path)
	builder.WriteByte(0)
	builder.WriteString(spec.Glob)
	if spec.Discover != nil {
		builder.WriteByte(0)
		builder.WriteString(spec.Discover.Root)
		builder.WriteByte(0)
		builder.WriteString(strconv.FormatBool(spec.Discover.FollowSymlinks))
		if spec.Discover.MaxDepth != nil {
			builder.WriteByte(0)
			builder.WriteString(strconv.Itoa(*spec.Discover.MaxDepth))
		}
	}
	return builder.String()
}

func (c *composer) resolveIncludeMatches(spec IncludeSpec, base string) includeMatchesResult {
	if spec.Discover != nil {
		root := spec.Discover.Root
		if root == "" {
			root = "."
		}
		if err := rejectRemote(root); err != nil {
			return includeMatchesResult{discovery: spec.Discover, err: err}
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(base, root)
		}
		followSymlinks := spec.Discover.FollowSymlinks || c.followSymlinks
		matches, err := discoverConfigs(root, spec.Discover.MaxDepth, followSymlinks, c.root)
		if err != nil {
			return includeMatchesResult{discovery: spec.Discover, root: root, err: err}
		}
		return includeMatchesResult{matches: matches, kind: SourceDiscovery, discovery: spec.Discover, root: root}
	}
	expression := spec.Path
	if expression == "" {
		expression = spec.Glob
	}
	matches, _, err := resolveExpression(base, c.root, expression)
	if err != nil {
		return includeMatchesResult{err: err}
	}
	if spec.Glob != "" {
		return includeMatchesResult{matches: matches, kind: SourceGlob}
	}
	return includeMatchesResult{matches: matches, kind: SourceNestedInclude}
}

func (c *composer) resolveInclude(spec IncludeSpec, parent ConfigSource) ([]sourceRequest, error) {
	if err := validateIncludeSpec(spec, parent.DisplayPath); err != nil {
		return nil, err
	}
	base := filepath.Dir(parent.CanonicalPath)
	matches, kind, discovery, root, err := c.includeMatches(spec, base)
	if err != nil {
		if discovery != nil {
			return nil, fmt.Errorf("source %s discovery: %w", parent.DisplayPath, err)
		}
		return nil, fmt.Errorf("source %s include: %w", parent.DisplayPath, err)
	}
	if discovery != nil {
		c.scopes = append(c.scopes, DiscoveryScope{Path: root, DisplayPath: displayBase(c.root, root), MaxDepth: discovery.MaxDepth, FollowSymlinks: discovery.FollowSymlinks || c.followSymlinks})
		requests := make([]sourceRequest, 0, len(matches))
		for _, match := range matches {
			depth := pathDepth(root, filepath.Dir(match))
			requests = append(requests, sourceRequest{path: match, kind: kind, parentID: parent.ID, depth: parent.Depth + 1, discoveryDepth: intPointer(depth)})
		}
		return requests, nil
	}
	requests := make([]sourceRequest, 0, len(matches))
	for _, match := range matches {
		requests = append(requests, sourceRequest{path: match, kind: kind, parentID: parent.ID, depth: parent.Depth + 1})
	}
	return requests, nil
}

func (c *composer) checkBoundaryCycles(cfg *Config, parent ConfigSource) error {
	active := make(map[string]int, len(c.active))
	for path, index := range c.active {
		active[path] = index
	}
	chain := make([]string, len(c.stack))
	for index, source := range c.stack {
		chain[index] = source.DisplayPath
	}
	done := make(map[string]bool)
	for _, include := range cfg.Include {
		children, err := c.resolveCycleInclude(include, parent.CanonicalPath)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := c.inspectCycleSubtree(child, active, chain, done); err != nil {
				return err
			}
		}
	}
	return nil
}

// inspectCycleSubtree deliberately ignores include.max_depth. A depth limit
// truncates which child includes are exported, not the file's own services or
// the graph the user declared, so cycle validation must still walk every edge.
func (c *composer) inspectCycleSubtree(path string, active map[string]int, chain []string, done map[string]bool) error {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return sanitizePathError(c.root, path, err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return err
	}
	canonical = filepath.Clean(canonical)
	display := displayBase(c.root, canonical)
	if index, exists := active[canonical]; exists {
		return fmt.Errorf("config_include_cycle: %s", strings.Join(append(append([]string(nil), chain[index:]...), display), " -> "))
	}
	if done[canonical] {
		return nil
	}
	loaded, err := c.cache.load(canonical, canonical)
	if err != nil {
		return sourceError(display, canonical, err)
	}
	active[canonical] = len(chain)
	chain = append(chain, display)
	defer delete(active, canonical)
	for _, include := range loaded.Include {
		children, err := c.resolveCycleInclude(include, canonical)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := c.inspectCycleSubtree(child, active, chain, done); err != nil {
				return err
			}
		}
	}
	done[canonical] = true
	return nil
}

func (c *composer) resolveCycleInclude(spec IncludeSpec, parentPath string) ([]string, error) {
	if err := validateIncludeSpec(spec, displayBase(c.root, parentPath)); err != nil {
		return nil, err
	}
	matches, _, _, _, err := c.includeMatches(spec, filepath.Dir(parentPath))
	return matches, err
}

func (c *composer) applyLocalOverrides(cfg *Config, source ConfigSource) error {
	paths := append([]string(nil), cfg.Overrides...)
	cfg.Overrides = nil
	for index, path := range paths {
		if err := rejectRemote(path); err != nil {
			return err
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(source.CanonicalPath), path)
		}
		if err := applyConfigPatch(cfg, path); err != nil {
			return fmt.Errorf("source %s override %d: %w", source.DisplayPath, index+1, sanitizePathError(c.root, path, err))
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return sanitizePathError(c.root, path, err)
		}
		overrideSource := ConfigSource{ID: sourceID(canonical), CanonicalPath: canonical, DisplayPath: displayBase(c.root, canonical), Kind: SourceOverride, ParentID: source.ID, Depth: source.Depth, Order: len(c.sources)}
		c.sources = append(c.sources, overrideSource)
		nodes, nodesErr := patchServiceNodes(path)
		if nodesErr != nil {
			return sanitizePathError(c.root, path, nodesErr)
		}
		for name, node := range nodes {
			key := source.ID + "\x00" + name
			c.overrideProvenance[key] = append(c.overrideProvenance[key], provenancePatch{source: overrideSource, node: node})
		}
		cfg.WatchPaths = appendUniqueString(cfg.WatchPaths, path)
		cfg.WatchPaths = appendUniqueString(cfg.WatchPaths, filepath.Join(filepath.Dir(path), ".env"))
		c.diagnostics = append(c.diagnostics, CompositionDiagnostic{Code: "override_applied", Message: fmt.Sprintf("%s layer %d", displayBase(c.root, path), index+1), SourceID: overrideSource.ID})
	}
	return nil
}

func (c *composer) assemble(virtual bool) (*Config, error) {
	effective := &Config{Source: SourceKranz, Services: make(map[string]Service), ActionGroups: make(map[string]ActionGroup), ServiceMetadata: make(map[string]EffectiveService)}
	if c.rootConfig != nil && !virtual {
		effective.Project = c.rootConfig.Project
		effective.Version = c.rootConfig.Version
		effective.Runtime = c.rootConfig.Runtime
		effective.UI = c.rootConfig.UI
		effective.Source = c.rootConfig.Source
	} else {
		effective.Project = filepath.Base(c.root)
		effective.UI = UIConfig{Background: UIBackgroundTerminal, ColorMode: UIColorModeAuto}
	}

	serviceNames := allocateServiceNames(c.root, c.services)
	localNames := make(map[string]map[string]string)
	rawNames := make(map[string][]string)
	for index, item := range c.services {
		display := serviceNames[index]
		if localNames[item.source.ID] == nil {
			localNames[item.source.ID] = make(map[string]string)
		}
		localNames[item.source.ID][item.name] = display
		rawNames[item.name] = append(rawNames[item.name], display)
	}
	var collidingNames []string
	for name, displays := range rawNames {
		if len(displays) > 1 {
			collidingNames = append(collidingNames, name)
		}
	}
	sort.Strings(collidingNames)
	for _, name := range collidingNames {
		c.diagnostics = append(c.diagnostics, CompositionDiagnostic{
			Code:    "display_name_qualified",
			Message: fmt.Sprintf("%s -> %s; colliding autonomous services are qualified by relative source path", name, strings.Join(rawNames[name], ", ")),
		})
	}
	// A raw name that occurs once can still be renamed: display names must be
	// unique across the whole composition, so a literal name may collide with a
	// qualified name derived from another source. Report those renames too, or
	// the effective graph silently drops the author's name.
	for index, item := range c.services {
		display := serviceNames[index]
		if display == item.name || len(rawNames[item.name]) > 1 {
			continue
		}
		c.diagnostics = append(c.diagnostics, CompositionDiagnostic{
			Code:    "display_name_qualified",
			Message: fmt.Sprintf("%s -> %s; service name was qualified to stay unique across sources", item.name, display),
		})
	}
	groupNames := allocateGroupNames(c.root, c.groups)
	localGroupNames := make(map[string]map[string]string)
	rawGroupNames := make(map[string][]string)
	for index, item := range c.groups {
		display := groupNames[index]
		if localGroupNames[item.source.ID] == nil {
			localGroupNames[item.source.ID] = make(map[string]string)
		}
		localGroupNames[item.source.ID][item.name] = display
		rawGroupNames[item.name] = append(rawGroupNames[item.name], display)
	}
	// Keep the local-to-display mapping available to protected application,
	// which must resolve the same service and group references.
	c.localServiceNames, c.rawServiceNames = localNames, rawNames
	c.localGroupNames, c.rawGroupNames = localGroupNames, rawGroupNames
	for index, item := range c.services {
		display := serviceNames[index]
		service := item.value
		for depIndex, dependency := range service.DependsOn {
			mapped, err := c.mapServiceReference(item.source.ID, dependency)
			if err != nil {
				return nil, fmt.Errorf("service %q in %s: %w", item.name, item.source.DisplayPath, err)
			}
			service.DependsOn[depIndex] = mapped
		}
		for prerequisiteIndex, prerequisite := range service.BeforeStart {
			if prerequisite.Service != "" {
				mapped, err := c.mapServiceReference(item.source.ID, prerequisite.Service)
				if err != nil {
					return nil, fmt.Errorf("service %q in %s: %w", item.name, item.source.DisplayPath, err)
				}
				prerequisite.Service = mapped
			}
			if prerequisite.Group != "" {
				mapped, err := c.mapGroupReference(item.source.ID, prerequisite.Group)
				if err != nil {
					return nil, fmt.Errorf("service %q in %s: %w", item.name, item.source.DisplayPath, err)
				}
				prerequisite.Group = mapped
			}
			service.BeforeStart[prerequisiteIndex] = prerequisite
		}
		if len(service.DependencyConditions) > 0 {
			mappedConditions := make(map[string]DependencyConfig, len(service.DependencyConditions))
			for dependency, condition := range service.DependencyConditions {
				mapped, err := c.mapServiceReference(item.source.ID, dependency)
				if err != nil {
					return nil, fmt.Errorf("service %q in %s: %w", item.name, item.source.DisplayPath, err)
				}
				mappedConditions[mapped] = condition
			}
			service.DependencyConditions = mappedConditions
		}
		id := serviceID(item.source.CanonicalPath, item.name)
		effective.Services[display] = service
		effective.ServiceOrder = append(effective.ServiceOrder, display)
		effective.ServiceMetadata[display] = EffectiveService{ID: id, SourceID: item.source.ID, SourceName: item.name, DisplayName: display, ResolvedDir: service.Dir}
		recordProvenance(effective, display, id, item.source.ID, StageExplicit, item.raw)
		if item.raw.Dir != "" && item.raw.Dir != service.Dir {
			effective.Provenance = append(effective.Provenance, FieldProvenance{ServiceID: id, FieldPath: "services." + display + ".dir", ValueSourceID: item.source.ID, Stage: StageExplicit, ReplacedSourceID: item.source.ID, OriginalValue: item.raw.Dir, EffectiveValue: service.Dir})
		}
		recordDefaultProvenance(effective, display, id, item.source.ID, item.raw, item.defaults, service)
		for _, patch := range c.overrideProvenance[item.source.ID+"\x00"+item.name] {
			recordNodeProvenance(effective, patch.node, []string{"services", display}, id, patch.source.ID, StageOverride)
		}
	}

	for index, item := range c.groups {
		name := groupNames[index]
		if _, collision := effective.Services[name]; collision {
			return nil, fmt.Errorf("action group %q from %s collides with an effective service name", name, item.source.DisplayPath)
		}
		effective.ActionGroups[name] = item.value
		effective.ActionGroupOrder = append(effective.ActionGroupOrder, name)
	}
	for _, source := range c.sources {
		if source.CanonicalPath != "" && source.Kind != SourceOverride {
			effective.Paths = append(effective.Paths, source.CanonicalPath)
		}
	}
	for _, item := range c.services {
		for _, path := range item.value.EnvFiles {
			effective.WatchPaths = appendUniqueString(effective.WatchPaths, path)
		}
	}
	for _, source := range c.sources {
		if source.CanonicalPath != "" {
			effective.WatchPaths = appendUniqueString(effective.WatchPaths, source.CanonicalPath)
		}
	}
	for _, path := range c.watchPaths {
		effective.WatchPaths = appendUniqueString(effective.WatchPaths, path)
	}
	return effective, nil
}

func allocateServiceNames(root string, items []sourcedService) []string {
	dirs, names := make([]string, len(items)), make([]string, len(items))
	for index, item := range items {
		dirs[index] = filepath.Dir(item.source.CanonicalPath)
		names[index] = item.name
	}
	return allocateNames(root, dirs, names)
}

func allocateGroupNames(root string, items []sourcedGroup) []string {
	dirs, names := make([]string, len(items)), make([]string, len(items))
	for index, item := range items {
		dirs[index] = filepath.Dir(item.source.CanonicalPath)
		names[index] = item.name
	}
	return allocateNames(root, dirs, names)
}

func allocateNames(root string, dirs, raw []string) []string {
	// Relative directory segments per item, with a trailing segment equal to the
	// service's own name removed so a name that repeats its directory is not
	// shown twice and because the name is appended separately.
	segments := make([][]string, len(raw))
	for index := range raw {
		// Only the composition-relative path may become a prefix. At the
		// composition root the relative path is ".", which contributes no
		// segment; falling back to the absolute directory name would make the
		// same workspace produce different display names in another directory.
		rel := displayBase(root, dirs[index])
		parts := strings.FieldsFunc(filepath.ToSlash(rel), func(r rune) bool { return r == '/' })
		if rel == "." {
			parts = nil
		}
		if len(parts) > 0 && parts[len(parts)-1] == raw[index] {
			parts = parts[:len(parts)-1]
		}
		segments[index] = parts
	}
	counts := make(map[string]int, len(raw))
	for _, name := range raw {
		counts[name]++
	}
	candidateAt := func(index, width int) string {
		parts := segments[index]
		if width <= 0 || len(parts) == 0 {
			return raw[index]
		}
		start := len(parts) - width
		if start < 0 {
			start = 0
		}
		return strings.Join(parts[start:], "/") + "/" + raw[index]
	}

	// Greedy global allocation: a display name must be unique across the whole
	// composition, not only within one raw name. Two different raw names can
	// otherwise resolve to the same candidate, for example a literal service
	// named "catalog/api" and `api` under repositories/catalog. Each item takes
	// its shortest still-free candidate, widening the directory prefix until it
	// is free; a numeric suffix is the deterministic last resort.
	used := make(map[string]bool, len(raw))
	result := make([]string, len(raw))
	for index := range raw {
		first := 0
		if counts[raw[index]] > 1 {
			first = 1
		}
		chosen := ""
		for width := first; width <= len(segments[index]); width++ {
			candidate := candidateAt(index, width)
			if !used[candidate] {
				chosen = candidate
				break
			}
		}
		if chosen == "" {
			base := candidateAt(index, len(segments[index]))
			for suffix := 1; ; suffix++ {
				candidate := fmt.Sprintf("%s@%d", base, suffix)
				if !used[candidate] {
					chosen = candidate
					break
				}
			}
		}
		used[chosen] = true
		result[index] = chosen
	}
	return result
}

func applyExternalOverride(cfg *Config, root, path string, order int) error {
	if err := rejectRemote(path); err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	affected, err := patchServiceNodes(path)
	if err != nil {
		return fmt.Errorf("override layer %d: %w", order, sanitizePathError(root, path, err))
	}
	previousMetadata := make(map[string]EffectiveService, len(affected))
	for name := range affected {
		previousMetadata[name] = cfg.ServiceMetadata[name]
	}
	if err := applyConfigPatch(cfg, path); err != nil {
		return fmt.Errorf("override layer %d (%s): %w", order, displayBase(root, path), sourceError(displayBase(root, path), path, err))
	}
	previousDefaults := cfg.Defaults
	if cfg.Defaults.Dir == "" {
		cfg.Defaults.Dir = root
	}
	if err := applyDefaults(cfg); err != nil {
		return fmt.Errorf("override layer %d: %w", order, sanitizePathError(root, path, err))
	}
	cfg.Defaults = previousDefaults
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return sanitizePathError(root, path, err)
	}
	overrideSource := ConfigSource{ID: sourceID(canonical), CanonicalPath: canonical, DisplayPath: displayBase(root, canonical), Kind: SourceOverride, Order: len(cfg.Sources)}
	cfg.Sources = append(cfg.Sources, overrideSource)
	for _, name := range sortedYAMLNodeNames(affected) {
		node := affected[name]
		if _, exists := cfg.Services[name]; !exists {
			metadata := previousMetadata[name]
			recordNodeProvenance(cfg, node, []string{"services", name}, metadata.ID, overrideSource.ID, StageOverride)
			continue
		}
		metadata, exists := cfg.ServiceMetadata[name]
		if !exists {
			metadata = EffectiveService{ID: serviceID(canonical, name), SourceID: overrideSource.ID, SourceName: name, DisplayName: name, ResolvedDir: cfg.Services[name].Dir}
			cfg.ServiceMetadata[name] = metadata
		}
		recordNodeProvenance(cfg, node, []string{"services", name}, metadata.ID, overrideSource.ID, StageOverride)
	}
	cfg.WatchPaths = appendUniqueString(cfg.WatchPaths, path)
	cfg.WatchPaths = appendUniqueString(cfg.WatchPaths, filepath.Join(filepath.Dir(path), ".env"))
	cfg.CompositionDiagnostics = append(cfg.CompositionDiagnostics, CompositionDiagnostic{Code: "override_applied", Message: fmt.Sprintf("layer %d: %s", order, displayBase(root, path))})
	return nil
}

func sortedYAMLNodeNames(nodes map[string]*yaml.Node) []string {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *composer) applyProtected(cfg *Config) error {
	sort.SliceStable(c.protected, func(i, j int) bool { return c.protected[i].source.Depth > c.protected[j].source.Depth })
	for _, protected := range c.protected {
		payload, err := yaml.Marshal(protected.values)
		if err != nil {
			return fmt.Errorf("source %s protected: %w", protected.source.DisplayPath, err)
		}
		var layer Config
		var protectedDocument yaml.Node
		if err := yaml.Unmarshal(payload, &protectedDocument); err != nil {
			return err
		}
		protectedRoot, err := yamlMappingRoot(&protectedDocument)
		if err != nil {
			return err
		}
		resolvePatchPaths(protectedRoot, filepath.Dir(protected.source.CanonicalPath))
		if protected.source.CanonicalPath != c.rootCanonical {
			retainMappingKeys(protectedRoot, map[string]bool{"services": true, "action_groups": true})
		}
		payload, err = yaml.Marshal(&protectedDocument)
		if err != nil {
			return err
		}
		protectedServices := mappingValue(protectedRoot, "services")
		decoder := yaml.NewDecoder(strings.NewReader(string(payload)))
		decoder.KnownFields(true)
		if err := decoder.Decode(&layer); err != nil {
			return fmt.Errorf("source %s protected has incompatible type: %w", protected.source.DisplayPath, err)
		}
		remappedServices := make(map[string]Service, len(layer.Services))
		// The protected mapping key is rewritten to the effective display name
		// below, so provenance cannot look the node up by its raw name again.
		// Keep the node captured while the key is still the raw name.
		protectedNodes := make(map[string]*yaml.Node, len(layer.Services))
		layerNames := sortedServiceNames(layer.Services)
		for _, name := range layerNames {
			protectedService := layer.Services[name]
			target := name
			if _, exists := cfg.Services[target]; !exists {
				for display, metadata := range cfg.ServiceMetadata {
					if metadata.SourceID == protected.source.ID && metadata.SourceName == name {
						target = display
						break
					}
				}
			}
			if _, exists := cfg.Services[target]; !exists {
				return fmt.Errorf("source %s protected refers to missing service %q", protected.source.DisplayPath, name)
			}
			remappedServices[target] = protectedService
			if index := mappingIndex(protectedServices, name); index >= 0 {
				serviceNode := protectedServices.Content[index+1]
				if err := c.remapProtectedServiceReferences(protected.source.ID, serviceNode); err != nil {
					return fmt.Errorf("source %s protected service %q: %w", protected.source.DisplayPath, name, err)
				}
				protectedNodes[target] = serviceNode
				protectedServices.Content[index].Value = target
			}
		}
		if groups := mappingValue(protectedRoot, "action_groups"); groups != nil && groups.Kind == yaml.MappingNode {
			for index := 0; index+1 < len(groups.Content); index += 2 {
				mapped, err := c.mapGroupReference(protected.source.ID, groups.Content[index].Value)
				if err != nil {
					return fmt.Errorf("source %s protected: %w", protected.source.DisplayPath, err)
				}
				groups.Content[index].Value = mapped
			}
		}
		layer.Services = remappedServices
		// A protected layer pins service values, so the composition
		// directives that only make sense while the graph is being built are
		// dropped. defaults belongs to that set: it is applied to a file's
		// services before protected runs, and after assembly applyDefaults is
		// never called again, so keeping it would advertise a defaults section
		// that no service ever sees. A nested protected layer already keeps
		// only services and action_groups; the root drops the same directives
		// explicitly so its effective config cannot carry an inert defaults.
		for _, field := range []string{"defaults", "include", "overrides", "protected"} {
			deleteMappingValue(protectedRoot, field)
		}
		before := make(map[string]Service, len(layer.Services))
		for _, name := range sortedServiceNames(layer.Services) {
			before[name] = cfg.Services[name]
		}
		if err := applyConfigPatchNode(cfg, protectedRoot); err != nil {
			return fmt.Errorf("source %s protected: %w", protected.source.DisplayPath, err)
		}
		for _, name := range sortedServiceNames(layer.Services) {
			metadata := cfg.ServiceMetadata[name]
			patchNode := protectedNodes[name]
			beforeNode := yamlValueNode(before[name])
			start := len(cfg.Provenance)
			recordNodeProvenance(cfg, patchNode, []string{"services", name}, metadata.ID, protected.source.ID, StageProtected)
			for index := start; index < len(cfg.Provenance); index++ {
				entry := &cfg.Provenance[index]
				relative := strings.TrimPrefix(entry.FieldPath, "services."+name+".")
				fields := strings.Split(relative, ".")
				oldValue, found := yamlPathValue(beforeNode, fields)
				newValue, _ := yamlPathValue(patchNode, fields)
				entry.ProtectedRejection = found && !reflect.DeepEqual(oldValue, newValue)
				if found && len(fields) >= 2 && fields[0] == "env" {
					if redacted, changed := redactEnvironmentValue(fields[1], fmt.Sprint(oldValue)); changed {
						oldValue = redacted
					}
				}
				entry.OriginalValue = oldValue
			}
		}
	}
	return nil
}

// mapServiceReference resolves a raw service name written in one source against
// the effective display names, preferring the source's own local mapping and
// falling back to a globally unique raw name. An unresolvable name is returned
// unchanged so the later validation reports it against its own source.
func (c *composer) mapServiceReference(sourceIDValue, name string) (string, error) {
	if local := c.localServiceNames[sourceIDValue][name]; local != "" {
		return local, nil
	}
	matches := c.rawServiceNames[name]
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return name, nil
	default:
		return "", fmt.Errorf("reference %q is ambiguous; choose one of %s", name, strings.Join(matches, ", "))
	}
}

// mapGroupReference is mapServiceReference for action groups.
func (c *composer) mapGroupReference(sourceIDValue, name string) (string, error) {
	if local := c.localGroupNames[sourceIDValue][name]; local != "" {
		return local, nil
	}
	matches := c.rawGroupNames[name]
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return name, nil
	default:
		return "", fmt.Errorf("action group reference %q is ambiguous; choose one of %s", name, strings.Join(matches, ", "))
	}
}

// remapProtectedServiceReferences rewrites the service and group references a
// protected service declares, so a protected layer cannot reintroduce a raw
// name after the collision allocator has already qualified it.
func (c *composer) remapProtectedServiceReferences(sourceIDValue string, node *yaml.Node) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	if dependencies := mappingValue(node, "depends_on"); dependencies != nil && dependencies.Kind == yaml.SequenceNode {
		for _, item := range dependencies.Content {
			if item.Kind != yaml.ScalarNode {
				continue
			}
			mapped, err := c.mapServiceReference(sourceIDValue, item.Value)
			if err != nil {
				return err
			}
			item.Value = mapped
		}
	}
	if conditions := mappingValue(node, "dependency_conditions"); conditions != nil && conditions.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(conditions.Content); index += 2 {
			mapped, err := c.mapServiceReference(sourceIDValue, conditions.Content[index].Value)
			if err != nil {
				return err
			}
			conditions.Content[index].Value = mapped
		}
	}
	if prerequisites := mappingValue(node, "before_start"); prerequisites != nil && prerequisites.Kind == yaml.SequenceNode {
		for _, item := range prerequisites.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			if service := mappingValue(item, "service"); service != nil && service.Kind == yaml.ScalarNode && service.Value != "" {
				mapped, err := c.mapServiceReference(sourceIDValue, service.Value)
				if err != nil {
					return err
				}
				service.Value = mapped
			}
			if group := mappingValue(item, "group"); group != nil && group.Kind == yaml.ScalarNode && group.Value != "" {
				mapped, err := c.mapGroupReference(sourceIDValue, group.Value)
				if err != nil {
					return err
				}
				group.Value = mapped
			}
		}
	}
	return nil
}

func sortedServiceNames(services map[string]Service) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func resolveConfigPaths(cfg *Config, base string) {
	resolve := func(path string) string {
		if path == "" || filepath.IsAbs(path) {
			return path
		}
		return filepath.Clean(filepath.Join(base, path))
	}
	// An autonomous source's omitted directory belongs to that source, not the
	// composition root. Resolve it before applyDefaults fills service/action dirs.
	if cfg.Defaults.Dir == "" {
		cfg.Defaults.Dir = "."
	}
	cfg.Defaults.Dir = resolve(cfg.Defaults.Dir)
	for name, service := range cfg.Services {
		service.Dir = resolve(service.Dir)
		for actionName, action := range service.Actions {
			action.Dir = resolve(action.Dir)
			service.Actions[actionName] = action
		}
		if service.Lifecycle.Start != nil {
			service.Lifecycle.Start.Dir = resolve(service.Lifecycle.Start.Dir)
		}
		if service.Lifecycle.Stop != nil {
			service.Lifecycle.Stop.Dir = resolve(service.Lifecycle.Stop.Dir)
		}
		if service.Lifecycle.Logs != nil {
			service.Lifecycle.Logs.Dir = resolve(service.Lifecycle.Logs.Dir)
		}
		cfg.Services[name] = service
	}
	for name, group := range cfg.ActionGroups {
		group.Dir = resolve(group.Dir)
		for actionName, action := range group.Actions {
			action.Dir = resolve(action.Dir)
			group.Actions[actionName] = action
		}
		cfg.ActionGroups[name] = group
	}
}

func discoverConfigs(root string, maxDepth *int, followSymlinks bool, displayRoot string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, sanitizePathError(displayRoot, root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("discovery root is not a directory")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var result []string
	seenDirs := make(map[string]bool)
	var walk func(string, int) error
	walk = func(directory string, depth int) error {
		canonical, err := filepath.EvalSymlinks(directory)
		if err != nil {
			return sanitizePathError(displayRoot, directory, err)
		}
		if seenDirs[canonical] {
			return nil
		}
		seenDirs[canonical] = true
		entries, err := os.ReadDir(directory)
		if err != nil {
			return sanitizePathError(displayRoot, directory, err)
		}
		candidates := make(map[string]string)
		for _, entry := range entries {
			path := filepath.Join(directory, entry.Name())
			entryInfo, err := os.Lstat(path)
			if err != nil {
				return sanitizePathError(displayRoot, path, err)
			}
			if entryInfo.Mode()&os.ModeSymlink != 0 {
				if !followSymlinks {
					continue
				}
				target, err := filepath.EvalSymlinks(path)
				if err != nil {
					return fmt.Errorf("broken symlink %s: %w", displayBase(displayRoot, path), sanitizePathError(displayRoot, path, err))
				}
				targetInfo, err := os.Stat(target)
				if err != nil {
					return sanitizePathError(displayRoot, target, err)
				}
				if targetInfo.IsDir() {
					if maxDepth == nil || depth < *maxDepth {
						if err := walk(target, depth+1); err != nil {
							return err
						}
					}
					continue
				}
				if IsConfigFileName(entry.Name()) {
					candidates[entry.Name()] = target
				}
				continue
			}
			if entry.IsDir() {
				if maxDepth == nil || depth < *maxDepth {
					if err := walk(path, depth+1); err != nil {
						return err
					}
				}
				continue
			}
			if IsConfigFileName(entry.Name()) {
				candidates[entry.Name()] = path
			}
		}
		for _, name := range supportedConfigNames() {
			if path := candidates[name]; path != "" {
				result = append(result, path)
				break
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return nil, err
	}
	sort.Strings(result)
	return result, nil
}

// IsConfigFileName reports whether name is a supported configuration file name
// Kranz discovers and loads. External packages must use this predicate instead
// of keeping their own copy of the list, so discovery-scope walking stays in
// sync when a supported name is added.
func IsConfigFileName(name string) bool {
	for _, supported := range supportedConfigNames() {
		if name == supported {
			return true
		}
	}
	return false
}

// supportedConfigNames is the single source of truth for supported config base
// names, in discovery priority order (highest first). Discover, the
// discovery-scope walker, and the exported predicate all derive from it.
func supportedConfigNames() []string {
	return []string{"kranz.yaml", "kranz.yml", "process-compose.yaml", "process-compose.yml", "Procfile.dev", "Procfile"}
}

func rejectRemote(value string) error {
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && (strings.Contains(value, "://") || parsed.Host != "") {
		return fmt.Errorf("remote config sources are not supported: %q", value)
	}
	return nil
}

func hasGlobMeta(value string) bool { return strings.ContainsAny(value, "*?[") }

func decremented(value *int) *int {
	if value == nil {
		return nil
	}
	next := *value - 1
	if next < 0 {
		next = 0
	}
	return &next
}

func intPointer(value int) *int { return &value }

func pathDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(filepath.Clean(rel), string(filepath.Separator)))
}

func displayBase(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(filepath.Clean(rel))
}

// sanitizePathError rewrites an error so the given path and its directory are
// shown relative to the composition root. A source outside the root (a
// legitimate ../shared include) then reports "../shared/x" instead of the
// user's absolute filesystem path.
func sanitizePathError(root, path string, err error) error {
	if err == nil {
		return nil
	}
	clean := filepath.Clean(path)
	message := err.Error()
	if dir := filepath.Dir(clean); dir != clean {
		message = strings.ReplaceAll(message, dir+string(filepath.Separator), displayBase(root, dir)+"/")
	}
	message = strings.ReplaceAll(message, clean, displayBase(root, clean))
	return &sanitizedCompositionError{message: message, cause: err}
}

func sourceID(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return "src_" + hex.EncodeToString(sum[:8])
}
func serviceID(path, name string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path) + "\x00" + name))
	return "svc_" + hex.EncodeToString(sum[:12])
}

func sanitizeCompositionError(directory string, err error) error {
	if err == nil {
		return nil
	}
	root := directory
	if root == "" {
		root = "."
	}
	absRoot, absErr := filepath.Abs(root)
	if absErr != nil {
		return err
	}
	if canonical, canonicalErr := filepath.EvalSymlinks(absRoot); canonicalErr == nil {
		absRoot = canonical
	}
	message := strings.ReplaceAll(err.Error(), filepath.Clean(absRoot)+string(filepath.Separator), "")
	message = strings.ReplaceAll(message, filepath.Clean(absRoot), ".")
	return &sanitizedCompositionError{message: message, cause: err}
}

// sanitizedCompositionError presents a path-redacted message while keeping the
// original error reachable through errors.Is/As. Every redaction layer in this
// file returns one, so a sentinel or a *fs.PathError survives the whole chain;
// fmt.Errorf("%s", message) would flatten it to an opaque error string.
type sanitizedCompositionError struct {
	message string
	cause   error
}

func (e *sanitizedCompositionError) Error() string { return e.message }
func (e *sanitizedCompositionError) Unwrap() error { return e.cause }

func sourceError(display, canonical string, err error) error {
	message := strings.ReplaceAll(err.Error(), filepath.Clean(canonical), display)
	message = strings.ReplaceAll(message, filepath.Dir(filepath.Clean(canonical)), filepath.Dir(display))
	return &sanitizedCompositionError{message: fmt.Sprintf("source %s: %s", display, message), cause: err}
}

func recordProvenance(cfg *Config, display, serviceIDValue, sourceIDValue string, stage ProvenanceStage, service Service) {
	payload, err := yaml.Marshal(service)
	if err != nil {
		return
	}
	var node yaml.Node
	if yaml.Unmarshal(payload, &node) != nil || len(node.Content) == 0 {
		return
	}
	recordNodeProvenance(cfg, node.Content[0], []string{"services", display}, serviceIDValue, sourceIDValue, stage)
}

func recordDefaultProvenance(cfg *Config, display, serviceIDValue, sourceIDValue string, raw Service, defaults Defaults, effective Service) {
	appendValue := func(field string, original, value any, stage ProvenanceStage) {
		cfg.Provenance = append(cfg.Provenance, FieldProvenance{ServiceID: serviceIDValue, FieldPath: "services." + display + "." + field, ValueSourceID: sourceIDValue, Stage: stage, ReplacedSourceID: sourceIDValue, OriginalValue: original, EffectiveValue: value})
	}
	if raw.Dir == "" {
		stage := StageBuiltIn
		original := any(".")
		if defaults.Dir != "" {
			stage = StageDefault
			original = defaults.Dir
		}
		appendValue("dir", original, effective.Dir, stage)
	}
	if raw.Shell == "" {
		stage := StageBuiltIn
		if defaults.Shell != "" {
			stage = StageDefault
		}
		appendValue("shell", effective.Shell, effective.Shell, stage)
	}
	if len(raw.EnvFiles) == 0 && len(defaults.EnvFiles) > 0 {
		appendValue("env_files", append([]string(nil), defaults.EnvFiles...), append([]string(nil), effective.EnvFiles...), StageDefault)
	} else if len(raw.EnvFiles) > 0 && !reflect.DeepEqual(raw.EnvFiles, effective.EnvFiles) {
		appendValue("env_files", append([]string(nil), raw.EnvFiles...), append([]string(nil), effective.EnvFiles...), StageExplicit)
	}
	keys := make([]string, 0, len(effective.Env))
	for name := range effective.Env {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		value := effective.Env[name]
		if _, explicit := raw.Env[name]; explicit {
			continue
		}
		if _, inherited := defaults.Env[name]; inherited {
			if redacted, changed := redactEnvironmentValue(name, value); changed {
				value = redacted
			}
			appendValue("env."+name, value, value, StageDefault)
		}
	}
}

func recordNodeProvenance(cfg *Config, node *yaml.Node, path []string, serviceIDValue, sourceIDValue string, stage ProvenanceStage) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		if len(node.Content) == 0 {
			appendNodeProvenance(cfg, node, path, serviceIDValue, sourceIDValue, stage)
			return
		}
		for index := 0; index+1 < len(node.Content); index += 2 {
			recordNodeProvenance(cfg, node.Content[index+1], append(append([]string(nil), path...), node.Content[index].Value), serviceIDValue, sourceIDValue, stage)
		}
		return
	}
	if node.Kind == yaml.SequenceNode {
		appendNodeProvenance(cfg, node, path, serviceIDValue, sourceIDValue, stage)
		return
	}
	appendNodeProvenance(cfg, node, path, serviceIDValue, sourceIDValue, stage)
}

func appendNodeProvenance(cfg *Config, node *yaml.Node, path []string, serviceIDValue, sourceIDValue string, stage ProvenanceStage) {
	var value any
	if err := node.Decode(&value); err != nil {
		value = node.Value
	}
	if len(path) >= 2 && path[len(path)-2] == "env" {
		if redacted, changed := redactEnvironmentValue(path[len(path)-1], fmt.Sprint(value)); changed {
			value = redacted
		}
	}
	fieldPath := strings.Join(path, ".")
	replaced := ""
	if stage != StageExplicit {
		for index := len(cfg.Provenance) - 1; index >= 0; index-- {
			if cfg.Provenance[index].FieldPath == fieldPath {
				replaced = cfg.Provenance[index].ValueSourceID
				break
			}
		}
	}
	cfg.Provenance = append(cfg.Provenance, FieldProvenance{ServiceID: serviceIDValue, FieldPath: fieldPath, ValueSourceID: sourceIDValue, Stage: stage, ReplacedSourceID: replaced, OriginalValue: value, EffectiveValue: value})
}

func yamlValueNode(value any) *yaml.Node {
	payload, err := yaml.Marshal(value)
	if err != nil {
		return nil
	}
	var document yaml.Node
	if yaml.Unmarshal(payload, &document) != nil || len(document.Content) == 0 {
		return nil
	}
	return document.Content[0]
}

func yamlPathValue(root *yaml.Node, fields []string) (any, bool) {
	current := root
	for _, field := range fields {
		if current == nil || current.Kind != yaml.MappingNode {
			return nil, false
		}
		current = mappingValue(current, field)
		if current == nil {
			return nil, false
		}
	}
	var value any
	if err := current.Decode(&value); err != nil {
		return current.Value, true
	}
	return value, true
}
