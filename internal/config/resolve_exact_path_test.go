package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/shellquote"
)

// Discovery must check an executable's complete literal path before applying
// the legacy first-space command-token fallback, regardless of TMPDIR.
func TestResolveProviderLiteralExecutableWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "provider executables with spaces")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(dir, "account-wrapper")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := "parent"
	providers := map[string]ProviderSpec{
		"direct":    {Command: command},
		"parent":    {Command: command},
		"inherited": {Base: &base},
	}
	for _, name := range []string{"direct", "inherited"} {
		t.Run(name, func(t *testing.T) {
			resolved, err := ResolveProvider(&Agent{Provider: name}, nil, providers, exec.LookPath)
			if err != nil {
				t.Fatalf("ResolveProvider literal executable: %v", err)
			}
			if resolved.Command != command {
				t.Fatalf("Command = %q, want unchanged literal %q", resolved.Command, command)
			}
		})
	}
}

func TestResolveProviderDiscoveryLookupContract(t *testing.T) {
	for _, tt := range []struct {
		name        string
		spec        ProviderSpec
		available   []string
		wantCalls   []string
		wantMissing bool
	}{
		{"plain", ProviderSpec{Command: "agent"}, []string{"agent"}, []string{"agent"}, false},
		{"missing plain checked once", ProviderSpec{Command: "agent"}, nil, []string{"agent"}, true},
		{"literal wins over token", ProviderSpec{Command: "/tools with spaces/agent"}, []string{"/tools with spaces/agent", "/tools"}, []string{"/tools with spaces/agent"}, false},
		{"legacy token fallback", ProviderSpec{Command: "agent --flag"}, []string{"agent"}, []string{"agent --flag", "agent"}, false},
		{"missing literal and token", ProviderSpec{Command: "agent --flag"}, nil, []string{"agent --flag", "agent"}, true},
		{"explicit path check only", ProviderSpec{Command: "agent --flag", PathCheck: "/check with spaces/agent"}, []string{"/check with spaces/agent"}, []string{"/check with spaces/agent"}, false},
		{"missing path check cannot fall back", ProviderSpec{Command: "agent", PathCheck: "missing"}, []string{"agent"}, []string{"missing"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			lookup := func(command string) (string, error) {
				calls = append(calls, command)
				for _, available := range tt.available {
					if command == available {
						return command, nil
					}
				}
				return "", exec.ErrNotFound
			}
			resolved, err := ResolveProvider(&Agent{Provider: "custom"}, nil, map[string]ProviderSpec{"custom": tt.spec}, lookup)
			if tt.wantMissing {
				if !errors.Is(err, ErrProviderNotInPATH) {
					t.Fatalf("error = %v, want ErrProviderNotInPATH", err)
				}
			} else if err != nil || resolved.Command != tt.spec.Command {
				t.Fatalf("resolved = %+v, error = %v", resolved, err)
			}
			if !reflect.DeepEqual(calls, tt.wantCalls) {
				t.Errorf("LookPath calls = %q, want %q", calls, tt.wantCalls)
			}
		})
	}
}

// Discovery does not rewrite Command into shell text. Launch builders preserve
// raw Command (including legacy shell fragments) and quote only Args. A caller
// needing a literal shell executable must supply its own quoting at launch.
func TestProviderLaunchBuilderPreservesRawCommandAndQuotesArgs(t *testing.T) {
	cityPath := t.TempDir()
	for _, command := range []string{"/tools with spaces/agent", "agent --legacy", shellquote.Quote("/tools with spaces/agent")} {
		resolved := &ResolvedProvider{Command: command, Args: []string{"argument with spaces", "$(not-executed)"}}
		want := command + " " + shellquote.Join(resolved.Args)
		for _, transport := range []string{"", SessionTransportTmux, SessionTransportACP} {
			launch, err := BuildProviderLaunchCommand(cityPath, resolved, nil, transport)
			if err != nil || launch.Command != want {
				t.Fatalf("launch %q = %+v, %v; want %q", transport, launch, err, want)
			}
			deferred, err := BuildProviderLaunchCommandWithoutOptions(cityPath, resolved, transport)
			if err != nil || deferred.Command != want {
				t.Fatalf("deferred %q = %+v, %v; want %q", transport, deferred, err, want)
			}
		}
	}
}
