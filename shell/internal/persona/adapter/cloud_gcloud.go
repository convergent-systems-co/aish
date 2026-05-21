// gcloud sub-adapter helpers — file-edit strategy. The single-line
// file ~/.config/gcloud/active_config is gcloud's machine-readable
// "active configuration name" marker. Rewriting it is exactly what
// `gcloud config configurations activate <name>` does — we cut out
// the CLI invocation.
//
// File layout per gcloud docs:
//
//	~/.config/gcloud/
//	  active_config            ← single line: the active config name
//	  configurations/
//	    config_default
//	    config_work
//	    config_personal
//
// We only mutate active_config. The configuration files themselves
// (config_<name>) are NOT created by the adapter — the user is
// expected to have run `gcloud config configurations create <name>`
// before binding the persona.

package adapter
