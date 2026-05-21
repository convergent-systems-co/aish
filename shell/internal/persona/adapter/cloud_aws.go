// aws sub-adapter — env-var-only strategy (Thomas-approved
// 2026-05-21). The persona binds AWS_PROFILE for the shell session;
// child processes (aws CLI, terraform, boto3-driven tools) inherit
// the value. We do NOT rewrite ~/.aws/credentials or ~/.aws/config —
// those are user-owned files with profile schemas we don't replicate.
//
// Rationale: cloud profile binding is conceptually session-scoped
// (the same as kube context, which also lives in env / a single
// pointer file). File-rewriting would race with the actual aws CLI's
// writes and would not survive `unset AWS_PROFILE`.
//
// The "AWS_PROFILE = persona's binding" assignment is held in an
// EnvSession value the shell builtin reads back post-Execute and
// merges into the Shell's env. See cloud.go for the EnvSession type.

package adapter
