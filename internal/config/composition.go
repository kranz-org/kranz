package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

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

type provenancePatch struct {
	source ConfigSource
	node   *yaml.Node
}

type composer struct {
	root               string
	followSymlinks     bool
	cache              *SourceCache
	visited            map[string]bool
	active             map[string]int
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
	c := &composer{root: absRoot, followSymlinks: options.FollowSymlinks, cache: cache, visited: make(map[string]bool), active: make(map[string]int), overrideProvenance: make(map[string][]provenancePatch)}

	requests, virtual, err := c.topLevelRequests(options)
	if err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return nil, fmt.Errorf("config_not_found: no configuration was found directly or through discovery")
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
			matches, kind, err := resolveExpression(c.root, expression)
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
	maxDepth := (*int)(nil)
	matches, err := discoverConfigs(c.root, maxDepth, options.FollowSymlinks)
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

func resolveExpression(base, expression string) ([]string, ConfigSourceKind, error) {
	if err := rejectRemote(expression); err != nil {
		return nil, "", err
	}
	path := expression
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	if !hasGlobMeta(expression) {
		if _, err := os.Lstat(path); err != nil {
			return nil, "", fmt.Errorf("explicit config %q: %w", expression, err)
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
			return nil, "", fmt.Errorf("inspect glob match %q: %w", expression, statErr)
		}
		if info.IsDir() {
			found, discoverErr := Discover(match)
			if discoverErr != nil {
				return nil, "", fmt.Errorf("glob directory %q has no root config: %w", displayBase(base, match), discoverErr)
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
		return fmt.Errorf("resolve config %s: %w", displayBase(c.root, request.path), err)
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
		return fmt.Errorf("source %s: %w", source.DisplayPath, err)
	}
	for _, path := range cfg.WatchPaths {
		c.watchPaths = appendUniqueString(c.watchPaths, path)
	}

	c.visited[canonical] = true
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

	if request.remaining != nil && *request.remaining == 0 {
		if len(cfg.Include) > 0 {
			for index := range c.sources {
				if c.sources[index].ID == source.ID {
					c.sources[index].Truncated = true
					break
				}
			}
			c.diagnostics = append(c.diagnostics, CompositionDiagnostic{Code: "include_depth_truncated", Message: source.DisplayPath, SourceID: source.ID})
			if err := c.checkBoundaryCycles(cfg, source); err != nil {
				return err
			}
		}
		return nil
	}
	for _, include := range cfg.Include {
		children, err := c.resolveInclude(include, source)
		if err != nil {
			return err
		}
		for _, child := range children {
			remaining := decremented(request.remaining)
			if include.MaxDepth != nil {
				value := *include.MaxDepth
				remaining = &value
			}
			child.remaining = remaining
			if err := c.visit(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *composer) resolveInclude(spec IncludeSpec, parent ConfigSource) ([]sourceRequest, error) {
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
		return nil, fmt.Errorf("source %s: include must set exactly one of path, glob, or discover", parent.DisplayPath)
	}
	if spec.MaxDepth != nil && *spec.MaxDepth < 0 {
		return nil, fmt.Errorf("source %s: include.max_depth cannot be negative", parent.DisplayPath)
	}
	base := filepath.Dir(parent.CanonicalPath)
	if spec.Discover != nil {
		root := spec.Discover.Root
		if root == "" {
			root = "."
		}
		if err := rejectRemote(root); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(base, root)
		}
		followSymlinks := spec.Discover.FollowSymlinks || c.followSymlinks
		matches, err := discoverConfigs(root, spec.Discover.MaxDepth, followSymlinks)
		if err != nil {
			return nil, fmt.Errorf("source %s discovery: %w", parent.DisplayPath, err)
		}
		c.scopes = append(c.scopes, DiscoveryScope{Path: root, DisplayPath: displayBase(c.root, root), MaxDepth: spec.Discover.MaxDepth, FollowSymlinks: followSymlinks})
		requests := make([]sourceRequest, 0, len(matches))
		for _, match := range matches {
			depth := pathDepth(root, filepath.Dir(match))
			requests = append(requests, sourceRequest{path: match, kind: SourceDiscovery, parentID: parent.ID, depth: parent.Depth + 1, discoveryDepth: intPointer(depth)})
		}
		return requests, nil
	}
	expression := spec.Path
	if expression == "" {
		expression = spec.Glob
	}
	matches, kind, err := resolveExpression(base, expression)
	if err != nil {
		return nil, fmt.Errorf("source %s include: %w", parent.DisplayPath, err)
	}
	if spec.Glob != "" {
		kind = SourceGlob
	} else {
		kind = SourceNestedInclude
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
// truncates exported services, not validation of the graph the user declared.
func (c *composer) inspectCycleSubtree(path string, active map[string]int, chain []string, done map[string]bool) error {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
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
		return nil, fmt.Errorf("source %s: include must set exactly one of path, glob, or discover", displayBase(c.root, parentPath))
	}
	base := filepath.Dir(parentPath)
	if spec.Discover != nil {
		root := spec.Discover.Root
		if root == "" {
			root = "."
		}
		if err := rejectRemote(root); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(base, root)
		}
		return discoverConfigs(root, spec.Discover.MaxDepth, spec.Discover.FollowSymlinks || c.followSymlinks)
	}
	expression := spec.Path
	if expression == "" {
		expression = spec.Glob
	}
	matches, _, err := resolveExpression(base, expression)
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
			return fmt.Errorf("source %s override %d: %w", source.DisplayPath, index+1, err)
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		overrideSource := ConfigSource{ID: sourceID(canonical), CanonicalPath: canonical, DisplayPath: displayBase(c.root, canonical), Kind: SourceOverride, ParentID: source.ID, Depth: source.Depth, Order: len(c.sources)}
		c.sources = append(c.sources, overrideSource)
		nodes, nodesErr := patchServiceNodes(path)
		if nodesErr != nil {
			return nodesErr
		}
		for name, node := range nodes {
			key := source.ID + "\x00" + name
			c.overrideProvenance[key] = append(c.overrideProvenance[key], provenancePatch{source: overrideSource, node: node})
		}
		cfg.WatchPaths = appendUniqueString(cfg.WatchPaths, path)
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
			Message: fmt.Sprintf("%s -> %s; repeated -f composes autonomous sources, use --override for legacy layering", name, strings.Join(rawNames[name], ", ")),
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
	for index, item := range c.services {
		display := serviceNames[index]
		service := item.value
		mapReference := func(name string) (string, error) {
			if local := localNames[item.source.ID][name]; local != "" {
				return local, nil
			}
			matches := rawNames[name]
			if len(matches) == 1 {
				return matches[0], nil
			}
			if len(matches) > 1 {
				return "", fmt.Errorf("service %q in %s: reference %q is ambiguous; choose one of %s", item.name, item.source.DisplayPath, name, strings.Join(matches, ", "))
			}
			return name, nil
		}
		for depIndex, dependency := range service.DependsOn {
			mapped, err := mapReference(dependency)
			if err != nil {
				return nil, err
			}
			service.DependsOn[depIndex] = mapped
		}
		for prerequisiteIndex, prerequisite := range service.BeforeStart {
			if prerequisite.Service != "" {
				mapped, err := mapReference(prerequisite.Service)
				if err != nil {
					return nil, err
				}
				prerequisite.Service = mapped
			}
			if prerequisite.Group != "" {
				if local := localGroupNames[item.source.ID][prerequisite.Group]; local != "" {
					prerequisite.Group = local
				} else if matches := rawGroupNames[prerequisite.Group]; len(matches) == 1 {
					prerequisite.Group = matches[0]
				} else if len(matches) > 1 {
					return nil, fmt.Errorf("service %q in %s: action group reference %q is ambiguous; choose one of %s", item.name, item.source.DisplayPath, prerequisite.Group, strings.Join(matches, ", "))
				}
			}
			service.BeforeStart[prerequisiteIndex] = prerequisite
		}
		if len(service.DependencyConditions) > 0 {
			mappedConditions := make(map[string]DependencyConfig, len(service.DependencyConditions))
			for dependency, condition := range service.DependencyConditions {
				mapped, err := mapReference(dependency)
				if err != nil {
					return nil, err
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
	result := append([]string(nil), raw...)
	byName := make(map[string][]int)
	for index, name := range raw {
		byName[name] = append(byName[name], index)
	}
	for name, indexes := range byName {
		if len(indexes) == 1 {
			continue
		}
		segments := make(map[int][]string, len(indexes))
		for _, index := range indexes {
			rel := displayBase(root, dirs[index])
			parts := strings.FieldsFunc(filepath.ToSlash(rel), func(r rune) bool { return r == '/' })
			if len(parts) == 0 || rel == "." {
				parts = []string{filepath.Base(dirs[index])}
			}
			if len(parts) > 0 && parts[len(parts)-1] == name {
				parts = parts[:len(parts)-1]
				if len(parts) == 0 {
					parts = []string{filepath.Base(filepath.Dir(dirs[index]))}
				}
			}
			segments[index] = parts
		}
		for width := 1; ; width++ {
			seen, unique := make(map[string]bool), true
			maxSegments := 0
			for _, index := range indexes {
				parts := segments[index]
				if len(parts) > maxSegments {
					maxSegments = len(parts)
				}
				start := len(parts) - width
				if start < 0 {
					start = 0
				}
				candidate := strings.Join(parts[start:], "/") + "/" + name
				if seen[candidate] {
					unique = false
				}
				seen[candidate] = true
				result[index] = candidate
			}
			if unique {
				break
			}
			if width >= maxSegments {
				// Two supported configs can live in the same directory. A directory
				// prefix cannot distinguish them, so retain deterministic source
				// identity with a final numeric suffix instead of looping forever.
				for order, index := range indexes {
					result[index] = result[index] + fmt.Sprintf("@%d", order+1)
				}
				break
			}
		}
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
		return fmt.Errorf("override layer %d: %w", order, err)
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
		return fmt.Errorf("override layer %d: %w", order, err)
	}
	cfg.Defaults = previousDefaults
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
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
				protectedServices.Content[index].Value = target
			}
		}
		layer.Services = remappedServices
		for _, field := range []string{"include", "overrides", "protected"} {
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
			patchNode := mappingValue(protectedServices, name)
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

func discoverConfigs(root string, maxDepth *int, followSymlinks bool) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
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
			return err
		}
		if seenDirs[canonical] {
			return nil
		}
		seenDirs[canonical] = true
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		candidates := make(map[string]string)
		for _, entry := range entries {
			path := filepath.Join(directory, entry.Name())
			entryInfo, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if entryInfo.Mode()&os.ModeSymlink != 0 {
				if !followSymlinks {
					continue
				}
				target, err := filepath.EvalSymlinks(path)
				if err != nil {
					return fmt.Errorf("broken symlink %s: %w", displayBase(root, path), err)
				}
				targetInfo, err := os.Stat(target)
				if err != nil {
					return err
				}
				if targetInfo.IsDir() {
					if maxDepth == nil || depth < *maxDepth {
						if err := walk(target, depth+1); err != nil {
							return err
						}
					}
					continue
				}
				if supportedConfigName(entry.Name()) {
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
			if supportedConfigName(entry.Name()) {
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

func supportedConfigName(name string) bool {
	for _, supported := range supportedConfigNames() {
		if name == supported {
			return true
		}
	}
	return false
}

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
	return fmt.Errorf("%s", message)
}

func sourceError(display, canonical string, err error) error {
	message := strings.ReplaceAll(err.Error(), filepath.Clean(canonical), display)
	message = strings.ReplaceAll(message, filepath.Dir(filepath.Clean(canonical)), filepath.Dir(display))
	return fmt.Errorf("source %s: %s", display, message)
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
