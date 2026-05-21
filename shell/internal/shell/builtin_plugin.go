package shell

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	proto "github.com/convergent-systems-co/aish/libs/proto/registry"
	"github.com/convergent-systems-co/aish/shell/internal/plugins"
)

// pluginBuiltin implements `plugin ...` per v0.3-2 tasks #90–#94. The
// shell delegates install/remove/verify to the `aish-plugin` admin
// binary so the trust path lives in one place. `plugin list` reads
// the registry directly so a missing CLI binary doesn't break the
// `list` UX.
//
// Subcommands:
//
//	plugin list                  list registered plugins
//	plugin install <path>        install a plugin binary (delegates to aish-plugin)
//	plugin remove <name>         uninstall (delegates to aish-plugin)
//	plugin verify <name>         re-verify signature + binary hash
//	plugin status                print one-line summary (registered / fallback)
//
// Bare `plugin` prints a usage hint and exits 2.
func (s *Shell) pluginBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: plugin list | install <path> [--name N] | remove <name> | verify <name> | status")
		return 2
	}
	sub := strings.ToLower(args[0])
	rest := args[1:]
	switch sub {
	case "list":
		return s.pluginList(stdout, stderr)
	case "status":
		return s.pluginStatus(stdout, stderr)
	case "install":
		return s.pluginDelegate(append([]string{"install"}, rest...), stdout, stderr)
	case "remove":
		return s.pluginDelegate(append([]string{"remove"}, rest...), stdout, stderr)
	case "verify":
		return s.pluginDelegate(append([]string{"verify"}, rest...), stdout, stderr)
	default:
		fmt.Fprintf(stderr, "plugin: unknown subcommand %q (try `plugin list`)\n", sub)
		return 2
	}
}

// pluginList reads the registry directly via the proto package and
// pretty-prints the entries. Empty registry yields a one-line "no
// plugins" message + a hint at the fallback PATH binary so users see
// what aish is actually using.
func (s *Shell) pluginList(stdout, stderr io.Writer) int {
	dotAish := s.dotAishDir()
	if dotAish == "" {
		fmt.Fprintln(stderr, "plugin: $HOME not set; cannot read registry")
		return 1
	}
	entries := plugins.All(dotAish, stderr)
	if len(entries) == 0 {
		fmt.Fprintf(stdout, "No plugins registered under %s\n", plugins.Root(dotAish))
		fmt.Fprintln(stdout, "Tip: `plugin install /path/to/aish-inference-cloud`")
		// Surface the PATH fallback if there's an aish-inference-cloud on PATH;
		// this is the pre-v0.3-2 behavior users may be relying on.
		if p, err := exec.LookPath("aish-inference-cloud"); err == nil {
			fmt.Fprintf(stdout, "Fallback: %s (found on PATH)\n", p)
		}
		return 0
	}
	fmt.Fprintf(stdout, "Plugins under %s:\n", plugins.Root(dotAish))
	for _, e := range entries {
		kinds := make([]string, 0, len(e.Manifest.Kinds))
		for _, k := range e.Manifest.Kinds {
			kinds = append(kinds, string(k))
		}
		sort.Strings(kinds)
		fmt.Fprintf(stdout, "  %-20s v%-8s kinds=%s signer=%s\n",
			e.Manifest.Name, e.Manifest.Version,
			strings.Join(kinds, ","), e.Manifest.SignerID)
		fmt.Fprintf(stdout, "    binary: %s\n", e.Manifest.BinaryPath)
	}
	return 0
}

// pluginStatus prints a one-line summary mirroring `community status`.
func (s *Shell) pluginStatus(stdout, stderr io.Writer) int {
	dotAish := s.dotAishDir()
	if dotAish == "" {
		fmt.Fprintln(stderr, "plugin: $HOME not set")
		return 1
	}
	entries := plugins.All(dotAish, stderr)
	if len(entries) == 0 {
		// PATH fallback case.
		if p, err := exec.LookPath("aish-inference-cloud"); err == nil {
			fmt.Fprintf(stdout, "plugin: fallback (PATH=%s)\n", p)
		} else {
			fmt.Fprintln(stdout, "plugin: none registered")
		}
		return 0
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Manifest.Name)
	}
	fmt.Fprintf(stdout, "plugin: %d registered (%s)\n", len(names), strings.Join(names, ", "))
	return 0
}

// pluginDelegate execs the `aish-plugin` binary with the supplied
// args. Output is streamed straight through. The binary is looked up
// on PATH first, then alongside the running aish binary
// (<aish>/../aish-plugin), so a `make build`'d tree finds it without
// the user installing globally.
func (s *Shell) pluginDelegate(args []string, stdout, stderr io.Writer) int {
	bin := s.locatePluginCLI()
	if bin == "" {
		fmt.Fprintln(stderr, "plugin: aish-plugin CLI not found on PATH or alongside aish")
		fmt.Fprintln(stderr, "        Build it via `make -C plugins/cloud build` or install it on PATH.")
		return 1
	}
	cmd := exec.Command(bin, args...) // #nosec G204 — bin is resolved via PATH / sibling-of-aish lookup, not user input.
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		// exec.ExitError already carried by the child; surface its
		// exit code if available so the user sees a useful "$?".
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(stderr, "plugin: %v\n", err)
		return 1
	}
	return 0
}

// locatePluginCLI returns the path to the aish-plugin binary, looked
// up first on PATH and then alongside the running aish executable.
// Returns "" when neither location yields a hit.
func (s *Shell) locatePluginCLI() string {
	if p, err := exec.LookPath("aish-plugin"); err == nil {
		return p
	}
	// Sibling-of-aish lookup: useful in dev (`shell/dist/aish` ↔
	// `plugins/cloud/dist/aish-plugin`). We resolve the running
	// binary's symlinks so a launcher like `/usr/local/bin/aish ->
	// /opt/aish/dist/aish` still finds the sibling.
	self := currentBinaryPath()
	if self == "" {
		return ""
	}
	dir := filepath.Dir(self)
	candidates := []string{
		filepath.Join(dir, "aish-plugin"),
		// dev tree: shell/dist/aish ↔ plugins/cloud/dist/aish-plugin
		filepath.Join(dir, "..", "..", "plugins", "cloud", "dist", "aish-plugin"),
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	return ""
}

// selectRegistryInferencePlugin is the bridge between the registry
// loader and the cache.PluginClient. Called by tryStartPlugin during
// shell boot; returns the absolute binary path of a registered
// inference plugin, or "" when the registry is empty, absent, or the
// selected binary fails its hash check.
//
// The shell uses this BEFORE falling back to DefaultPluginBinary on
// PATH. Empty result is the normal pre-v0.3-2 path.
//
// Binary-hash verification at spawn: a manifest can have a valid
// signature but reference a tampered (or stale) binary on disk. We
// re-hash the binary and compare against manifest.SHA256 here so a
// post-install swap is caught before we spawn an untrusted child.
// The cost is one SHA-256 read per startup — acceptable for the
// security guarantee.
func selectRegistryInferencePlugin(dotAish string, warn io.Writer) string {
	if dotAish == "" {
		return ""
	}
	sel, ok := plugins.Select(proto.KindInference, dotAish, warn)
	if !ok {
		return ""
	}
	// Re-hash the binary to confirm it hasn't been swapped since
	// install time. The shell-side check is independent of the CLI's
	// `plugin verify` subcommand — both surface the same integrity
	// guarantee but the shell-side one runs unconditionally at boot.
	gotHash, err := proto.HashBinary(sel.BinaryPath)
	if err != nil {
		if warn != nil {
			_, _ = io.WriteString(warn,
				"aish: plugins: cannot read registered binary "+sel.BinaryPath+": "+err.Error()+
					" — falling back to PATH lookup\n")
		}
		return ""
	}
	// Reload the manifest to compare hashes. Cheap — JSON parse only.
	entries := plugins.All(dotAish, nil)
	var manifestHash string
	for _, e := range entries {
		if e.Manifest.Name == sel.Name {
			manifestHash = e.Manifest.SHA256
			break
		}
	}
	if manifestHash != "" && gotHash != manifestHash {
		if warn != nil {
			_, _ = io.WriteString(warn,
				"aish: plugins: binary "+sel.BinaryPath+" sha256 does not match manifest "+
					"(expected "+manifestHash+" got "+gotHash+
					") — falling back to PATH lookup; run `plugin verify "+sel.Name+
					"` for details\n")
		}
		return ""
	}
	return sel.BinaryPath
}
