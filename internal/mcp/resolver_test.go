package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	kranzruntime "github.com/kranz-org/kranz/internal/runtime"
)

type fakeRegistry struct {
	records []kranzruntime.SessionRecord
}

func (r fakeRegistry) List(context.Context, string) ([]kranzruntime.SessionRecord, error) {
	return r.records, nil
}

func (r fakeRegistry) Resolve(_ context.Context, reference, _ string) (kranzruntime.SessionRecord, error) {
	for _, record := range r.records {
		if record.Name == reference || record.ID == reference {
			return record, nil
		}
	}
	return kranzruntime.SessionRecord{}, &kranzruntime.SessionNotFoundError{Reference: reference}
}

func fakeRecord(id, name string) kranzruntime.SessionRecord {
	return kranzruntime.SessionRecord{
		SessionMetadata: kranzruntime.SessionMetadata{ID: id, Name: name, Project: name, Directory: "/tmp/" + name},
		State:           kranzruntime.SessionRunning,
	}
}

func fakeLocal(t *testing.T, project string) *app.Local {
	t.Helper()
	cfg := &config.Config{Project: project, Services: map[string]config.Service{"api": {Command: "exit 0"}}, ServiceOrder: []string{"api"}}
	local := app.NewLocal(cfg, nil, app.Options{SessionID: project})
	t.Cleanup(func() { _ = local.Shutdown() })
	return local
}

// resolverServer builds a server over fake runtimes so the addressing rules can
// be exercised without a registry on disk.
func resolverServer(t *testing.T, options ResolverOptions, records ...kranzruntime.SessionRecord) *Server {
	t.Helper()
	options.Version = "test"
	options.Registry = fakeRegistry{records: records}
	if options.Dial == nil {
		options.Dial = func(_ context.Context, record kranzruntime.SessionRecord) (app.API, func() error, error) {
			return fakeLocal(t, record.Name), func() error { return nil }, nil
		}
	}
	return NewServer(NewResolver(options), "test", strings.NewReader(""), nil, nil)
}

func TestAddressingPrefersTheExplicitRuntime(t *testing.T) {
	server := resolverServer(t, ResolverOptions{Directory: func() (string, error) { return "alpha", nil }},
		fakeRecord("id-alpha", "alpha"), fakeRecord("id-beta", "beta"))

	if envelope := testTool(t, server, "status", `{}`); envelope.Session == nil || envelope.Session.Name != "alpha" {
		t.Fatalf("directory default = %#v", envelope.Session)
	}
	envelope := testTool(t, server, "status", `{"runtime":"beta"}`)
	if envelope.Error != nil || envelope.Session == nil || envelope.Session.Name != "beta" {
		t.Fatalf("explicit runtime = %#v %#v", envelope.Session, envelope.Error)
	}
}

func TestUnaddressedCallOutsideAProjectCarriesCandidates(t *testing.T) {
	server := resolverServer(t, ResolverOptions{
		Directory: func() (string, error) { return "", errors.New("no Kranz configuration was found in this directory") },
	}, fakeRecord("id-alpha", "alpha"), fakeRecord("id-beta", "beta"))

	envelope := testTool(t, server, "logs", `{}`)
	if envelope.Error == nil || envelope.Error.Code != "runtime_required" {
		t.Fatalf("unaddressed call = %#v", envelope)
	}
	payload, _ := json.Marshal(envelope.Error.Details)
	if !strings.Contains(string(payload), `"runtime":"alpha"`) || !strings.Contains(string(payload), `"runtime":"beta"`) {
		t.Fatalf("candidates = %s", payload)
	}
	if envelope.Session != nil {
		t.Fatalf("a failure that resolved no runtime claimed one answered: %#v", envelope.Session)
	}
}

func TestMissingRuntimePointsAtTheToolThatWouldFixIt(t *testing.T) {
	server := resolverServer(t, ResolverOptions{Directory: func() (string, error) { return "gamma", nil }},
		fakeRecord("id-alpha", "alpha"))

	envelope := testTool(t, server, "status", `{}`)
	if envelope.Error == nil || envelope.Error.Code != "runtime_not_found" {
		t.Fatalf("stopped project = %#v", envelope)
	}
	if !strings.Contains(envelope.Error.Hint, "up") || !strings.Contains(envelope.Error.Message, "gamma") {
		t.Fatalf("error = %#v", envelope.Error)
	}
}

func TestPinnedServerRefusesAnotherAddress(t *testing.T) {
	server := resolverServer(t, ResolverOptions{Pin: "alpha"},
		fakeRecord("id-alpha", "alpha"), fakeRecord("id-beta", "beta"))

	if envelope := testTool(t, server, "status", `{}`); envelope.Session == nil || envelope.Session.Name != "alpha" {
		t.Fatalf("pinned default = %#v", envelope.Session)
	}
	envelope := testTool(t, server, "status", `{"runtime":"beta"}`)
	if envelope.Error == nil || envelope.Error.Code != "runtime_pinned" {
		t.Fatalf("pinned server reached another runtime: %#v", envelope)
	}
}

func TestPinnedUpRejectsAnotherDirectoryBeforeLaunch(t *testing.T) {
	launched := false
	server := resolverServer(t, ResolverOptions{Pin: "alpha", Launch: func(context.Context, string) (kranzruntime.SessionRecord, bool, error) {
		launched = true
		return fakeRecord("id-beta", "beta"), true, nil
	}}, fakeRecord("id-alpha", "alpha"))
	envelope := testTool(t, server, "up", `{"directory":"/tmp/beta","confirm":true}`)
	if envelope.Error == nil || envelope.Error.Code != "runtime_pinned" || launched {
		t.Fatalf("pinned up crossed projects: error=%#v launched=%v", envelope.Error, launched)
	}
}

func TestDirectoryPinRejectsSameNameRuntimeElsewhere(t *testing.T) {
	server := resolverServer(t, ResolverOptions{Pin: "alpha", PinDirectory: "/workspace/shop"}, fakeRecord("id-alpha", "alpha"))
	envelope := testTool(t, server, "status", `{}`)
	if envelope.Error == nil || envelope.Error.Code != "runtime_pinned" {
		t.Fatalf("same-name runtime bypassed directory pin: %#v", envelope.Error)
	}
}

func TestStoppedNamePinCanLaunchItsProjectDirectory(t *testing.T) {
	server := resolverServer(t, ResolverOptions{Pin: "custom", ProjectDirectory: "/workspace/shop",
		Launch: func(_ context.Context, directory string) (kranzruntime.SessionRecord, bool, error) {
			record := fakeRecord("id-custom", "custom")
			record.Directory = directory
			return record, true, nil
		}},
	)
	envelope := testTool(t, server, "up", `{"directory":"/workspace/shop","confirm":true}`)
	if envelope.Error != nil {
		t.Fatalf("stopped name pin rejected its project: %#v", envelope.Error)
	}
}

func TestUnavailablePinStillRefusesAnotherAddress(t *testing.T) {
	server := resolverServer(t, ResolverOptions{Pin: "alpha"}, fakeRecord("id-beta", "beta"))

	envelope := testTool(t, server, "status", `{"runtime":"beta"}`)
	if envelope.Error == nil || envelope.Error.Code != "runtime_pinned" {
		t.Fatalf("unavailable pin allowed another runtime: %#v", envelope)
	}
	pinned := testTool(t, server, "status", `{"runtime":"alpha"}`)
	if pinned.Error == nil || pinned.Error.Code != "runtime_not_found" {
		t.Fatalf("the pinned address itself did not reach normal resolution: %#v", pinned)
	}
}

// An incompatible runtime is one runtime's problem. The process serving three
// others must keep serving them.
func TestVersionMismatchFailsOneCallAndNotTheProcess(t *testing.T) {
	options := ResolverOptions{Dial: func(_ context.Context, record kranzruntime.SessionRecord) (app.API, func() error, error) {
		if record.Name == "beta" {
			return nil, nil, &kranzruntime.VersionMismatchError{ServerProtocol: 1, ServerVersion: "v0.9.0"}
		}
		return fakeLocal(t, record.Name), func() error { return nil }, nil
	}}
	server := resolverServer(t, options, fakeRecord("id-alpha", "alpha"), fakeRecord("id-beta", "beta"))

	envelope := testTool(t, server, "status", `{"runtime":"beta"}`)
	if envelope.Error == nil || envelope.Error.Code != "runtime_version_mismatch" {
		t.Fatalf("incompatible runtime = %#v", envelope)
	}
	if healthy := testTool(t, server, "status", `{"runtime":"alpha"}`); healthy.Error != nil {
		t.Fatalf("a compatible runtime stopped answering: %#v", healthy.Error)
	}
}

func TestAddressedResourceReadsTheNamedRuntime(t *testing.T) {
	server := resolverServer(t, ResolverOptions{Pin: "alpha"}, fakeRecord("id-alpha", "alpha"))
	definition, ok := server.resources["kranz://session"]
	if !ok {
		t.Fatal("session resource is missing")
	}
	runtimeRef, short, scoped := runtimeScopedURI("kranz://runtimes/alpha/session")
	if !scoped || runtimeRef != "alpha" || short != "kranz://session" {
		t.Fatalf("template parse = %q %q %v", runtimeRef, short, scoped)
	}
	envelope := server.readResourceDefinition(context.Background(), definition, runtimeRef)
	if envelope.Error != nil || envelope.Session == nil || envelope.Session.Name != "alpha" {
		t.Fatalf("addressed read = %#v %#v", envelope.Session, envelope.Error)
	}
}

func TestUpCreatesNothingWithoutConfirmationAndDownRefusesForeignRuntimes(t *testing.T) {
	launched := 0
	options := ResolverOptions{
		ProjectDirectory: "/tmp/alpha",
		Launch: func(context.Context, string) (kranzruntime.SessionRecord, bool, error) {
			launched++
			return fakeRecord("id-alpha", "alpha"), true, nil
		},
	}
	server := resolverServer(t, options, fakeRecord("id-beta", "beta"))

	envelope := testTool(t, server, "up", `{}`)
	if envelope.Error == nil || envelope.Error.Code != "confirmation_required" {
		t.Fatalf("up without confirmation = %#v", envelope)
	}
	if launched != 0 {
		t.Fatal("up started a runtime before it was confirmed")
	}
	if envelope := testTool(t, server, "up", `{"confirm":true}`); envelope.Error != nil {
		t.Fatalf("up = %#v", envelope.Error)
	} else if envelope.Session != nil || envelope.Generation != 0 {
		t.Fatalf("global up claimed a runtime served it: %#v", envelope)
	}
	if launched != 1 {
		t.Fatalf("launches = %d", launched)
	}

	// beta was found, not created here, so it is not this session's to stop.
	if envelope := testTool(t, server, "down", `{"runtime":"beta","confirm":true}`); envelope.Error == nil || envelope.Error.Code != "not_owned" {
		t.Fatalf("down on a foreign runtime = %#v", envelope)
	}
}

func TestUpDoesNotClaimAnExistingRuntime(t *testing.T) {
	alpha := fakeRecord("id-alpha", "alpha")
	options := ResolverOptions{
		ProjectDirectory: "/tmp/alpha",
		Launch: func(context.Context, string) (kranzruntime.SessionRecord, bool, error) {
			return alpha, false, nil
		},
	}
	server := resolverServer(t, options, alpha)

	up := testTool(t, server, "up", `{"confirm":true}`)
	if up.Error != nil {
		t.Fatalf("up against an existing runtime = %#v", up.Error)
	}
	data := up.Data.(map[string]any)
	if created, _ := data["created"].(bool); created {
		t.Fatalf("up claimed it created an existing runtime: %#v", up.Data)
	}
	if down := testTool(t, server, "down", `{"runtime":"alpha","confirm":true}`); down.Error == nil || down.Error.Code != "not_owned" {
		t.Fatalf("up granted ownership of an existing runtime: %#v", down)
	}
}

func TestGlobalToolsDeclareNoRuntimeArgument(t *testing.T) {
	server := resolverServer(t, ResolverOptions{}, fakeRecord("id-alpha", "alpha"))
	for _, name := range []string{"runtimes", "up"} {
		properties := server.tools[name].InputSchema["properties"].(map[string]any)
		if _, ok := properties["runtime"]; ok {
			t.Fatalf("%s declares a runtime argument", name)
		}
	}
	for name, definition := range server.tools {
		if definition.scoped == nil {
			continue
		}
		properties := definition.InputSchema["properties"].(map[string]any)
		if _, ok := properties["runtime"]; !ok {
			t.Fatalf("%s is runtime-scoped but cannot be addressed", name)
		}
	}
}
