// Kube context adapter — T3 of the v0.3-3 atomic-persona-switch
// plan.
//
// Strategy (Thomas-approved 2026-05-21): direct YAML edit via
// gopkg.in/yaml.v3 instead of client-go's tools/clientcmd. The
// fallback path from the plan §T3 has been chosen at the outset
// because:
//
//   - client-go (and its k8s.io/api transitive) pushes the shell
//     binary past the 50MB target the plan named as the tripwire on
//     darwin. We avoid the swell entirely.
//   - The kubeconfig schema is publicly documented, stable, and
//     trivially-edited via a top-level yaml.Node tree — the cost of
//     dropping client-go is one ~100-line type-mapped struct.
//   - The Capture/Apply/Verify/Rollback surface we need only touches
//     the `current-context` field. Other fields (clusters, users,
//     contexts) are read-only for our purposes.
//
// Adapter behaviour matches the plan's Capture sha-256 / TOCTOU-
// defence story: the adapter records the SHA-256 of the merged
// config at Capture time and surfaces a non-fatal warning if the
// digest has changed by Rollback time.
//
// Limitation versus client-go: this implementation only honors
// $KUBECONFIG as a single file (no `:`-separated merge list). The
// plan's threat model is single-user single-host single-config; merge
// lists are a v0.3-fu concern.

package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// KubeAdapter implements PersonaAdapter for the kubeconfig
// current-context.
type KubeAdapter struct {
	homes HomeProvider
	// configPath, if non-empty, overrides the kubeconfig lookup. Used
	// by tests to point at a fixture kubeconfig outside HOME.
	configPath string
	// postApplyDigest is the SHA-256 of the kubeconfig immediately
	// after Apply's write. Rollback compares the file's current
	// digest against this to surface a KubeDigestWarning ONLY when
	// the file has been mutated by an external actor between Apply
	// and Rollback.
	postApplyDigest string
}

// NewKubeAdapter constructs a kube adapter wired to the host's
// $KUBECONFIG or ~/.kube/config.
func NewKubeAdapter() *KubeAdapter {
	return &KubeAdapter{homes: osHome{}}
}

// NewKubeAdapterWithDeps is the test-only constructor. configPath, if
// non-empty, is used verbatim (skipping the HOME lookup).
func NewKubeAdapterWithDeps(homes HomeProvider, configPath string) *KubeAdapter {
	return &KubeAdapter{homes: homes, configPath: configPath}
}

// Name implements PersonaAdapter.
func (k *KubeAdapter) Name() string { return "kube" }

// resolveConfigPath returns the kubeconfig path the adapter operates
// on. Precedence: explicit configPath > $KUBECONFIG > $HOME/.kube/config.
func (k *KubeAdapter) resolveConfigPath() string {
	if k.configPath != "" {
		return k.configPath
	}
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	if home := k.homes.Home(); home != "" {
		return filepath.Join(home, ".kube", "config")
	}
	return ""
}

// kubeSnapshot is the JSON-encoded Snapshot body.
type kubeSnapshot struct {
	PriorContext string `json:"prior_context"`
	// CaptureDigest is the SHA-256 of the kubeconfig at Capture
	// time. Retained for forensic value (it can detect mutations
	// that occurred between Capture and Apply).
	CaptureDigest string `json:"capture_digest"`
	ConfigPath    string `json:"config_path"`
}

// Capture reads the kubeconfig, records the current-context, and
// hashes the file content for TOCTOU detection on Rollback.
func (k *KubeAdapter) Capture(ctx context.Context) (Snapshot, error) {
	path := k.resolveConfigPath()
	if path == "" {
		return nil, fmt.Errorf("%w: no kubeconfig path resolvable", ErrNoSubsystem)
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: kubeconfig absent at %s", ErrNoSubsystem, path)
	}
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig: %w", err)
	}
	doc, err := parseKubeconfig(raw)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	digest := hashBytes(raw)
	snap := kubeSnapshot{
		PriorContext:  doc.CurrentContext,
		CaptureDigest: digest,
		ConfigPath:    path,
	}
	return json.Marshal(snap)
}

// Apply rewrites current-context to the persona's bound context.
// Returns ErrSchema (wrapped) when the requested context is not
// present in the kubeconfig's contexts list.
func (k *KubeAdapter) Apply(ctx context.Context, p persona.Persona) error {
	if p.ExternalBindings.Kube == nil {
		return fmt.Errorf("%w: persona %q has no [external.kube]", ErrNoBinding, p.Name)
	}
	target := p.ExternalBindings.Kube.Context
	if target == "" {
		return fmt.Errorf("%w: [external.kube].context is empty", ErrSchema)
	}
	path := k.resolveConfigPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read kubeconfig: %w", err)
	}
	doc, err := parseKubeconfig(raw)
	if err != nil {
		return fmt.Errorf("parse kubeconfig: %w", err)
	}
	found := false
	for _, c := range doc.Contexts {
		if c.Name == target {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("%w: context %q not defined in %s", ErrSchema, target, path)
	}
	doc.CurrentContext = target
	if err := writeKubeconfig(path, doc); err != nil {
		return err
	}
	// Record the post-Apply digest so Rollback can detect external
	// mutations that occur AFTER our write.
	if after, err := os.ReadFile(path); err == nil {
		k.postApplyDigest = hashBytes(after)
	}
	return nil
}

// Verify confirms the kubeconfig's current-context matches the
// persona's bound context after Apply.
func (k *KubeAdapter) Verify(ctx context.Context, p persona.Persona) error {
	if p.ExternalBindings.Kube == nil {
		return nil
	}
	target := p.ExternalBindings.Kube.Context
	path := k.resolveConfigPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("verify read: %w", err)
	}
	doc, err := parseKubeconfig(raw)
	if err != nil {
		return fmt.Errorf("verify parse: %w", err)
	}
	if doc.CurrentContext != target {
		return fmt.Errorf("verify: current-context = %q want %q", doc.CurrentContext, target)
	}
	return nil
}

// Rollback writes the prior current-context back. If the kubeconfig
// digest has changed since Capture, the prior context is still
// restored — the adapter records the mismatch via its OutcomeWarner
// (callers surface it as a Warning at the orchestrator boundary).
func (k *KubeAdapter) Rollback(ctx context.Context, snap Snapshot) error {
	var s kubeSnapshot
	if err := json.Unmarshal(snap, &s); err != nil {
		return fmt.Errorf("decode kube snapshot: %w", err)
	}
	path := s.ConfigPath
	if path == "" {
		path = k.resolveConfigPath()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("rollback read: %w", err)
	}
	currentDigest := hashBytes(raw)
	doc, err := parseKubeconfig(raw)
	if err != nil {
		return fmt.Errorf("rollback parse: %w", err)
	}
	doc.CurrentContext = s.PriorContext
	if err := writeKubeconfig(path, doc); err != nil {
		return fmt.Errorf("rollback write: %w", err)
	}
	// Warn ONLY when external mutation occurred between Apply and
	// Rollback — i.e., the file's pre-Rollback digest differs from
	// the digest we recorded post-Apply. (The capture-vs-current
	// comparison cannot fire useful warnings because Apply itself
	// changes the file.)
	if k.postApplyDigest != "" && currentDigest != k.postApplyDigest {
		return KubeDigestWarning{
			Path:           path,
			CapturedDigest: k.postApplyDigest,
			CurrentDigest:  currentDigest,
		}
	}
	return nil
}

// KubeDigestWarning is the typed non-fatal warning emitted by
// Rollback when the kubeconfig has been mutated between Capture and
// Rollback. The orchestrator's Outcome layer converts it into a
// Warnings entry instead of letting it propagate as a transaction
// error.
type KubeDigestWarning struct {
	Path           string
	CapturedDigest string
	CurrentDigest  string
}

func (w KubeDigestWarning) Error() string {
	return fmt.Sprintf("kube: kubeconfig %s digest changed (was %s, now %s) — prior context restored anyway",
		w.Path, w.CapturedDigest[:8], w.CurrentDigest[:8])
}

// IsKubeDigestWarning is a convenience predicate used by the
// orchestrator-side warning surface in T5.
func IsKubeDigestWarning(err error) bool {
	var w KubeDigestWarning
	return errors.As(err, &w)
}

// kubeconfigDoc is the minimal type-mapped slice of the kubeconfig
// schema. Fields we don't need (clusters, users, preferences,
// apiVersion, kind) are preserved verbatim via the rawNode round-trip
// path so untouched data isn't lost on a write-back.
type kubeconfigDoc struct {
	APIVersion     string         `yaml:"apiVersion"`
	Kind           string         `yaml:"kind"`
	CurrentContext string         `yaml:"current-context"`
	Clusters       []namedNode    `yaml:"clusters,omitempty"`
	Contexts       []namedContext `yaml:"contexts,omitempty"`
	Users          []namedNode    `yaml:"users,omitempty"`
	Preferences    yaml.Node      `yaml:"preferences,omitempty"`
}

type namedContext struct {
	Name    string    `yaml:"name"`
	Context yaml.Node `yaml:"context"`
}

type namedNode struct {
	Name  string    `yaml:"name"`
	Value yaml.Node `yaml:"-"` // unused; round-tripped via raw yaml.Node
}

// To preserve unknown fields cleanly, we round-trip through a
// generic yaml.Node and only touch the `current-context` scalar.

// parseKubeconfig unmarshals raw kubeconfig bytes into a doc with
// CurrentContext + Contexts populated. The rest of the document is
// preserved by writeKubeconfig via a separate node-based path.
func parseKubeconfig(raw []byte) (*kubeconfigDoc, error) {
	doc := &kubeconfigDoc{}
	if err := yaml.Unmarshal(raw, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// writeKubeconfig persists doc back to path. We use a node-level
// in-place edit (find the `current-context` mapping value and
// overwrite it) so non-modeled fields survive the round-trip.
func writeKubeconfig(path string, doc *kubeconfigDoc) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read for write: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("yaml unmarshal: %w", err)
	}
	if err := setMappingScalar(&root, "current-context", doc.CurrentContext); err != nil {
		return fmt.Errorf("set current-context: %w", err)
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("yaml marshal: %w", err)
	}
	tmp := path + ".aish-tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// setMappingScalar finds (or creates) a top-level mapping key on the
// document and sets its value to a scalar.
func setMappingScalar(root *yaml.Node, key, value string) error {
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("expected document node")
	}
	m := root.Content[0]
	if m.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node at document root")
	}
	for i := 0; i < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			m.Content[i+1].Kind = yaml.ScalarNode
			m.Content[i+1].Tag = "!!str"
			m.Content[i+1].Value = value
			// Clear any anchor/alias artifacts from prior parse.
			m.Content[i+1].Content = nil
			return nil
		}
	}
	// Key not present — append it.
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
	return nil
}

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Compile-time assertion.
var _ PersonaAdapter = (*KubeAdapter)(nil)
