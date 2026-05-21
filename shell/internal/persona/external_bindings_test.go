package persona

import (
	"strings"
	"testing"
)

// TestPersona_ParseTOML_WithExternalBindings exercises the schema
// extension end-to-end: a persona TOML with an [external] block and
// all four sub-tables decodes cleanly, every binding is populated,
// and the strict-decode reject path does NOT trigger.
func TestPersona_ParseTOML_WithExternalBindings(t *testing.T) {
	t.Parallel()
	src := `
name = "work"
version = 1
system_prompt = "be helpful at work"

[tone]
verbosity = "medium"
formality = "neutral"

[external.ssh]
key_label = "work-personal"
add_lifetime_seconds = 3600

[external.cloud]
gcloud_config = "work"
aws_profile = "work-sso"
azure_subscription = "00000000-aaaa-bbbb-cccc-111122223333"

[external.kube]
context = "gke-prod-work"

[external.git]
scope = "global"
user_name = "Thomas Polliard"
user_email = "thomas@example"
signing_key = "0xABCD1234"
`
	p, err := ParseTOML([]byte(src))
	if err != nil {
		t.Fatalf("ParseTOML failed: %v", err)
	}
	if p.ExternalBindings.SSH == nil {
		t.Fatal("ExternalBindings.SSH must be populated")
	}
	if p.ExternalBindings.SSH.KeyLabel != "work-personal" {
		t.Errorf("SSH.KeyLabel = %q", p.ExternalBindings.SSH.KeyLabel)
	}
	if p.ExternalBindings.SSH.AddLifetimeSeconds != 3600 {
		t.Errorf("SSH.AddLifetimeSeconds = %d", p.ExternalBindings.SSH.AddLifetimeSeconds)
	}
	if p.ExternalBindings.Cloud == nil {
		t.Fatal("ExternalBindings.Cloud must be populated")
	}
	if p.ExternalBindings.Cloud.GcloudConfig != "work" {
		t.Errorf("Cloud.GcloudConfig = %q", p.ExternalBindings.Cloud.GcloudConfig)
	}
	if p.ExternalBindings.Cloud.AWSProfile != "work-sso" {
		t.Errorf("Cloud.AWSProfile = %q", p.ExternalBindings.Cloud.AWSProfile)
	}
	if p.ExternalBindings.Cloud.AzureSubscription != "00000000-aaaa-bbbb-cccc-111122223333" {
		t.Errorf("Cloud.AzureSubscription = %q", p.ExternalBindings.Cloud.AzureSubscription)
	}
	if p.ExternalBindings.Kube == nil {
		t.Fatal("ExternalBindings.Kube must be populated")
	}
	if p.ExternalBindings.Kube.Context != "gke-prod-work" {
		t.Errorf("Kube.Context = %q", p.ExternalBindings.Kube.Context)
	}
	if p.ExternalBindings.Git == nil {
		t.Fatal("ExternalBindings.Git must be populated")
	}
	if p.ExternalBindings.Git.Scope != "global" {
		t.Errorf("Git.Scope = %q", p.ExternalBindings.Git.Scope)
	}
	if p.ExternalBindings.Git.UserName != "Thomas Polliard" {
		t.Errorf("Git.UserName = %q", p.ExternalBindings.Git.UserName)
	}
	if p.ExternalBindings.Git.UserEmail != "thomas@example" {
		t.Errorf("Git.UserEmail = %q", p.ExternalBindings.Git.UserEmail)
	}
	if p.ExternalBindings.Git.SigningKey != "0xABCD1234" {
		t.Errorf("Git.SigningKey = %q", p.ExternalBindings.Git.SigningKey)
	}
}

// TestPersona_ParseTOML_NoExternalBlock_BackwardCompat is the
// backward-compatibility load test: a persona file with no [external]
// block decodes cleanly and ExternalBindings is the zero value (all
// pointers nil). This proves the "persona with no bindings has
// identical behaviour" claim at the schema level.
func TestPersona_ParseTOML_NoExternalBlock_BackwardCompat(t *testing.T) {
	t.Parallel()
	src := `
name = "default"
version = 1
system_prompt = "be helpful"

[tone]
verbosity = "medium"
formality = "neutral"
`
	p, err := ParseTOML([]byte(src))
	if err != nil {
		t.Fatalf("ParseTOML failed: %v", err)
	}
	if p.ExternalBindings.SSH != nil {
		t.Errorf("SSH binding must be nil when [external] absent; got %+v", p.ExternalBindings.SSH)
	}
	if p.ExternalBindings.Cloud != nil {
		t.Errorf("Cloud binding must be nil when [external] absent; got %+v", p.ExternalBindings.Cloud)
	}
	if p.ExternalBindings.Kube != nil {
		t.Errorf("Kube binding must be nil when [external] absent; got %+v", p.ExternalBindings.Kube)
	}
	if p.ExternalBindings.Git != nil {
		t.Errorf("Git binding must be nil when [external] absent; got %+v", p.ExternalBindings.Git)
	}
}

// TestPersona_ParseTOML_PartialExternal: a persona may declare only
// some of the four bindings. Undeclared bindings stay nil.
func TestPersona_ParseTOML_PartialExternal(t *testing.T) {
	t.Parallel()
	src := `
name = "git-only"
version = 1
system_prompt = "git only"

[tone]
verbosity = "medium"
formality = "neutral"

[external.git]
scope = "global"
user_name = "Test"
user_email = "test@example"
`
	p, err := ParseTOML([]byte(src))
	if err != nil {
		t.Fatalf("ParseTOML failed: %v", err)
	}
	if p.ExternalBindings.Git == nil {
		t.Fatal("Git binding must be populated")
	}
	if p.ExternalBindings.SSH != nil || p.ExternalBindings.Cloud != nil || p.ExternalBindings.Kube != nil {
		t.Errorf("undeclared bindings must remain nil; got %+v", p.ExternalBindings)
	}
}

// TestPersona_ParseTOML_StrictDecodeRejectsUnknownExternalKey: an
// unknown key inside an [external] sub-table must be rejected by the
// strict-decode pass. This is the front-line defence against silent
// schema drift.
func TestPersona_ParseTOML_StrictDecodeRejectsUnknownExternalKey(t *testing.T) {
	t.Parallel()
	src := `
name = "bogus"
version = 1
system_prompt = "x"

[tone]
verbosity = "medium"
formality = "neutral"

[external.ssh]
key_label = "ok"
extra_field_that_does_not_exist = "boom"
`
	_, err := ParseTOML([]byte(src))
	if err == nil {
		t.Fatal("ParseTOML must reject unknown key inside [external.ssh]")
	}
	if !strings.Contains(err.Error(), "extra_field") {
		t.Fatalf("error must name the offending key; got %v", err)
	}
}
