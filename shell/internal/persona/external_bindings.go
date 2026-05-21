package persona

// ExternalBindings extends the persona TOML schema with declarations
// of which external OS-level identities a persona binds. Every field
// is OPTIONAL — a persona with no bindings continues to behave
// exactly as it did pre-#104 (the orchestrator's empty-adapter path
// is a no-op).
//
// Schema layout in TOML:
//
//	[external.ssh]
//	key_label = "work-personal"
//	add_lifetime_seconds = 3600
//
//	[external.cloud]
//	gcloud_config       = "work"
//	aws_profile         = "work-sso"
//	azure_subscription  = "work-sub-id-uuid"
//
//	[external.kube]
//	context = "gke-prod-work"
//
//	[external.git]
//	scope        = "global"
//	user_name    = "Thomas Polliard"
//	user_email   = "thomas@example"
//	signing_key  = "0xABCD1234"
type ExternalBindings struct {
	SSH   *SSHBinding   `toml:"ssh,omitempty"`
	Cloud *CloudBinding `toml:"cloud,omitempty"`
	Kube  *KubeBinding  `toml:"kube,omitempty"`
	Git   *GitBinding   `toml:"git,omitempty"`
}

// SSHBinding declares the SSH agent identity this persona binds.
//
// When present, `aish persona use <name>` will:
//   - Capture the agent's currently-loaded identities (public-key
//     bytes only — no private material).
//   - Read the private key from the secrets vault under KeyLabel,
//     load it into the agent with the requested lifetime, and zero
//     the in-memory buffer immediately after the Add call.
//   - Verify the new key's fingerprint appears in the agent's list.
//   - On Rollback, remove every key whose fingerprint is NOT in the
//     captured set (preserves any key the user added by hand).
type SSHBinding struct {
	// KeyLabel is the vault label whose value is the PEM-encoded
	// private key bytes for this persona. Required.
	KeyLabel string `toml:"key_label"`

	// AddLifetimeSeconds is the `ssh-add -t` lifetime applied to the
	// loaded key. Zero means "agent default" (typically forever).
	AddLifetimeSeconds int `toml:"add_lifetime_seconds"`
}

// CloudBinding declares cloud-CLI profile bindings for this persona.
//
// All three sub-fields are independently optional — a persona may
// bind only gcloud, only aws, only azure, or any combination. Each
// sub-CLI's mutation strategy is per the plan §Open questions
// (Thomas-approved leans):
//
//   - gcloud: file-edit `~/.config/gcloud/active_config`.
//   - aws:    env-var snapshot of AWS_PROFILE for this shell session
//             (NOT a file rewrite — chosen to match kube's
//             session-wide semantics).
//   - azure:  shell-out to `az account set --subscription` (the
//             undocumented azureProfile.json schema drifts between az
//             versions; az is the only Cloud sub-adapter that
//             shells out).
type CloudBinding struct {
	// GcloudConfig is the name of a gcloud configuration to activate
	// (e.g., "work"). Mutation: rewrite the single-line file
	// ~/.config/gcloud/active_config. Empty means "leave gcloud
	// alone."
	GcloudConfig string `toml:"gcloud_config"`

	// AWSProfile is the value to set into the AWS_PROFILE env var
	// for child processes of this shell session. The persona
	// remembers the prior AWS_PROFILE in its Snapshot and restores
	// it on Rollback. Empty means "leave aws alone."
	AWSProfile string `toml:"aws_profile"`

	// AzureSubscription is the subscription id/name passed to
	// `az account set --subscription <value>`. Empty means "leave
	// azure alone."
	AzureSubscription string `toml:"azure_subscription"`
}

// KubeBinding declares the kubectl context this persona activates.
//
// Mutation strategy: read/write only via
// k8s.io/client-go/tools/clientcmd against $KUBECONFIG (or the
// fallback ~/.kube/config). The adapter records a sha256 of the
// merged-config bytes at Capture time and surfaces a warning in the
// Outcome if the merged-config sha256 has changed by Rollback time
// (i.e., something outside aish mutated the kubeconfig mid-flight).
type KubeBinding struct {
	// Context is the kubectl context name to activate. Must exist
	// in the merged kubeconfig at Apply time, or Apply returns a
	// typed error (ErrSchema) and the orchestrator rolls back.
	Context string `toml:"context"`
}

// GitBinding declares the git config user.{name,email,signingkey}
// values this persona binds.
//
// Mutation strategy: shell-out to `git config --global …` (Thomas-
// approved exception to the native-Go rule — git owns the config
// file format including conditional includes; reimplementing that in
// Go is a tax we don't need to pay for three keys).
//
// Scope is locked to "global" for v0.3-3. Repo-local scope is
// deferred to v0.3-fu (Thomas-approved in plan §Open questions #2).
type GitBinding struct {
	// Scope MUST be "global" in v0.3-3. Any other value is rejected
	// at schema-validation time with ErrSchema.
	Scope string `toml:"scope"`

	// UserName is the value to set into git config user.name.
	// Required (empty when [external.git] is declared is a schema
	// error per plan §Adversarial self-pass).
	UserName string `toml:"user_name"`

	// UserEmail is the value to set into git config user.email.
	// Required (same as UserName).
	UserEmail string `toml:"user_email"`

	// SigningKey is the value to set into git config
	// user.signingkey. May be empty — empty means "unset the
	// signing key" rather than "leave it alone." A persona that
	// wants to leave the signing key untouched should omit the
	// entire [external.git] block, not declare it with an empty
	// signing_key.
	SigningKey string `toml:"signing_key"`
}
