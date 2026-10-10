// Package mcpstdio preserves an MCP stdio exchange while attaching the
// launch's identity to initialization and tool results.
package mcpstdio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const MetadataKey = "netcap/binding"

type Stamp struct {
	Version      int    `json:"version"`
	Digest       string `json:"digest"`
	AssetID      string `json:"asset_id"`
	AssetVersion string `json:"asset_version"`
	Server       string `json:"server"`
}

// Binding is safe to persist. It contains environment names and a digest,
// never command arguments or credential values. Context binds the caller's
// source versions and execution identity without exporting those details.
type Binding struct {
	Stamp
	Context     string   `json:"context"`
	Cwd         string   `json:"cwd"`
	Environment []string `json:"environment"`
}

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (s Stamp) Valid() bool {
	return s.Version == 1 && digestPattern.MatchString(s.Digest) && namePattern.MatchString(s.AssetID) && namePattern.MatchString(s.Server) &&
		s.AssetVersion != "" && len(s.AssetVersion) <= 200
}

func (b Binding) Valid() bool {
	if !b.Stamp.Valid() || !digestPattern.MatchString(b.Context) || !filepath.IsAbs(b.Cwd) || filepath.Clean(b.Cwd) != b.Cwd ||
		len(b.Environment) > 128 || !sort.StringsAreSorted(b.Environment) || len(slices.Compact(slices.Clone(b.Environment))) != len(b.Environment) {
		return false
	}
	for _, name := range b.Environment {
		if !envPattern.MatchString(name) {
			return false
		}
	}
	return true
}

// Bind fingerprints the exact expanded launch values. Run checks the same
// values at the child-process boundary, before executing the configured MCP.
func Bind(identity Stamp, context, cwd, command string, args []string, environment map[string]string) (Binding, error) {
	identity.Version, identity.Digest = 1, strings.Repeat("0", 64)
	if !identity.Valid() || !digestPattern.MatchString(context) || !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd || strings.TrimSpace(command) == "" || len(command) > 32768 || strings.ContainsRune(command, '\x00') || len(args) > 256 || len(environment) > 128 {
		return Binding{}, fmt.Errorf("MCP launch binding is invalid")
	}
	env := map[string]string{}
	names := make([]string, 0, len(environment))
	for name, value := range environment {
		if !envPattern.MatchString(name) || len(value) > 32768 || strings.ContainsRune(value, '\x00') {
			return Binding{}, fmt.Errorf("MCP launch environment is invalid")
		}
		names = append(names, name)
		env[name] = value
	}
	sort.Strings(names)
	for _, value := range args {
		if len(value) > 32768 || strings.ContainsRune(value, '\x00') {
			return Binding{}, fmt.Errorf("MCP launch arguments are invalid")
		}
	}
	identity.Digest = ""
	body, err := json.Marshal(struct {
		Identity    Stamp             `json:"identity"`
		Context     string            `json:"context"`
		Cwd         string            `json:"cwd"`
		Command     string            `json:"command"`
		Args        []string          `json:"args"`
		Environment map[string]string `json:"environment"`
	}{identity, context, cwd, command, append([]string{}, args...), env})
	if err != nil {
		return Binding{}, fmt.Errorf("MCP launch binding cannot be encoded")
	}
	sum := sha256.Sum256(append([]byte("netcap-mcp-stdio-v1\x00"), body...))
	identity.Digest = hex.EncodeToString(sum[:])
	return Binding{Stamp: identity, Context: context, Cwd: cwd, Environment: names}, nil
}

func (b Binding) verify(command string, args []string, lookup func(string) (string, bool)) error {
	if !b.Valid() {
		return fmt.Errorf("MCP launch binding is invalid")
	}
	values := map[string]string{}
	for _, name := range b.Environment {
		value, present := lookup(name)
		if !present {
			return fmt.Errorf("MCP launch environment no longer matches")
		}
		values[name] = value
	}
	expected, err := Bind(b.Stamp, b.Context, b.Cwd, command, args, values)
	if err != nil || expected.Stamp != b.Stamp {
		return fmt.Errorf("MCP launch values no longer match their binding")
	}
	return nil
}
