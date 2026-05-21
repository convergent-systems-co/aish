// Command aish-plugin is the admin CLI for the v0.3-2 plugin
// registry. It scans, installs, removes, and verifies plugin
// manifests under a registry root (default ~/.aish/plugins/).
//
// Subcommands:
//
//	aish-plugin list                       — list installed plugins
//	aish-plugin install --binary <path>    — install a plugin manifest
//	    [--name N] [--version V] [--kinds inference[,...]]
//	    [--signer aish-dev] [--force]
//	aish-plugin remove <name>              — uninstall a plugin
//	aish-plugin verify <name>              — re-verify a plugin's signature + binary hash
//
// Trust mirrors libs/proto/registry: only signers in the compiled-in
// trust-anchor list can produce installable manifests. The default
// signer is "aish-dev" (the development key); production callers
// supply --signer + their own key out-of-band (a vault-backed signer
// is a follow-up).
//
// The CLI exits 0 on success, 1 on operational failure (missing
// binary, lock contention, etc.), and 2 on usage error.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	proto "github.com/convergent-systems-co/aish/libs/proto/registry"
	"github.com/convergent-systems-co/aish/plugins/cloud/internal/registry"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run is the testable entrypoint. argv[0] is the program name; the
// remainder are subcommand + flags. Returns the process exit code.
func run(argv []string, stdout, stderr io.Writer) int {
	if len(argv) < 2 {
		usage(stderr)
		return 2
	}
	sub := argv[1]
	rest := argv[2:]
	switch sub {
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	case "-v", "--version", "version":
		fmt.Fprintf(stdout, "aish-plugin %s (built %s)\n", version, buildTime)
		return 0
	case "list":
		return cmdList(rest, stdout, stderr)
	case "install":
		return cmdInstall(rest, stdout, stderr)
	case "remove":
		return cmdRemove(rest, stdout, stderr)
	case "verify":
		return cmdVerify(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aish-plugin: unknown subcommand %q\n", sub)
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "aish-plugin — admin CLI for the aish plugin registry")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage: aish-plugin <subcommand> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  list                       list installed plugins")
	fmt.Fprintln(w, "  install --binary <path>    sign + install a plugin manifest")
	fmt.Fprintln(w, "                             [--name N] [--version V] [--kinds inference[,..]]")
	fmt.Fprintln(w, "                             [--signer aish-dev] [--force] [--root DIR]")
	fmt.Fprintln(w, "  remove <name>              uninstall a plugin manifest")
	fmt.Fprintln(w, "  verify <name>              re-verify signature + binary hash")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --root DIR                 override the registry root (default ~/.aish/plugins/)")
	fmt.Fprintln(w, "  -v, --version              print version + build time")
	fmt.Fprintln(w, "  -h, --help                 print this help")
}

// defaultRoot returns the platform-conventional registry root.
// Honours $AISH_PLUGINS_DIR for tests; falls back to ~/.aish/plugins.
func defaultRoot() (string, error) {
	if v := os.Getenv("AISH_PLUGINS_DIR"); v != "" {
		return v, nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		return "", errors.New("aish-plugin: neither HOME nor USERPROFILE is set")
	}
	return filepath.Join(home, ".aish", proto.DirName), nil
}

func cmdList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rootFlag := fs.String("root", "", "override the registry root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root, err := resolveRoot(*rootFlag)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	entries, err := proto.Load(root, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "aish-plugin: list: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Fprintf(stdout, "No plugins installed under %s\n", root)
		fmt.Fprintln(stdout, "Tip: `aish-plugin install --binary <path> --name <name>`")
		return 0
	}
	fmt.Fprintf(stdout, "Plugins under %s:\n", root)
	for _, e := range entries {
		kinds := make([]string, 0, len(e.Manifest.Kinds))
		for _, k := range e.Manifest.Kinds {
			kinds = append(kinds, string(k))
		}
		sort.Strings(kinds)
		fmt.Fprintf(stdout, "  %-20s v%-8s kinds=%s signer=%s\n",
			e.Manifest.Name,
			e.Manifest.Version,
			strings.Join(kinds, ","),
			e.Manifest.SignerID,
		)
		fmt.Fprintf(stdout, "    binary: %s\n", e.Manifest.BinaryPath)
	}
	return 0
}

func cmdInstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rootFlag := fs.String("root", "", "override the registry root")
	binary := fs.String("binary", "", "absolute path to the plugin binary (required)")
	name := fs.String("name", "", "plugin name (defaults to basename of --binary)")
	versionFlag := fs.String("version", "0.0.0", "plugin author version")
	kindsCSV := fs.String("kinds", string(proto.KindInference), "comma-separated kinds list")
	signer := fs.String("signer", "aish-dev", "signer id (must be in compiled-in trust anchors)")
	force := fs.Bool("force", false, "overwrite an existing manifest")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *binary == "" {
		fmt.Fprintln(stderr, "aish-plugin: install: --binary is required")
		return 2
	}
	absBin, err := filepath.Abs(*binary)
	if err != nil {
		fmt.Fprintf(stderr, "aish-plugin: install: abs(%s): %v\n", *binary, err)
		return 1
	}
	if *name == "" {
		base := filepath.Base(absBin)
		// Strip a leading "aish-" prefix so "aish-inference-cloud" -> "inference-cloud".
		// Strip a trailing ".exe" so "ollama.exe" -> "ollama".
		base = strings.TrimSuffix(base, ".exe")
		base = strings.TrimPrefix(base, "aish-")
		*name = base
	}
	kinds := []proto.Kind{}
	for _, k := range strings.Split(*kindsCSV, ",") {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		kinds = append(kinds, proto.Kind(k))
	}
	if len(kinds) == 0 {
		fmt.Fprintln(stderr, "aish-plugin: install: --kinds resolved to an empty list")
		return 2
	}
	// At v0.3-2 the only available signer is the dev key. A non-dev
	// SignerID is rejected by post-sign verify anyway; surface the
	// error early with a clearer message.
	if *signer != "aish-dev" {
		fmt.Fprintln(stderr, "aish-plugin: install: only signer=aish-dev is supported at v0.3-2")
		return 2
	}
	root, err := resolveRoot(*rootFlag)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	m, err := registry.Install(root, registry.InstallSource{
		Name:       *name,
		Version:    *versionFlag,
		BinaryPath: absBin,
		Kinds:      kinds,
		SignerID:   *signer,
		PrivateKey: proto.DevPrivateKey(),
	}, registry.InstallOpts{Force: *force, Logger: stderr})
	if err != nil {
		fmt.Fprintf(stderr, "aish-plugin: install: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "installed %s v%s (signer=%s)\n", m.Name, m.Version, m.SignerID)
	return 0
}

func cmdRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rootFlag := fs.String("root", "", "override the registry root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	positional := fs.Args()
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "aish-plugin: remove: <name> is required")
		return 2
	}
	root, err := resolveRoot(*rootFlag)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	if err := registry.Remove(root, positional[0], stderr); err != nil {
		fmt.Fprintf(stderr, "aish-plugin: remove: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "removed %s\n", positional[0])
	return 0
}

func cmdVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rootFlag := fs.String("root", "", "override the registry root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	positional := fs.Args()
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "aish-plugin: verify: <name> is required")
		return 2
	}
	root, err := resolveRoot(*rootFlag)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	m, err := registry.VerifyInstalled(root, positional[0])
	if err != nil {
		fmt.Fprintf(stderr, "aish-plugin: verify: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "ok: %s v%s signer=%s sha256=%s\n",
		m.Name, m.Version, m.SignerID, m.SHA256)
	return 0
}

func resolveRoot(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	return defaultRoot()
}
