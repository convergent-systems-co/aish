// azure sub-adapter — shell-out to `az account set --subscription`
// (Thomas-approved 2026-05-21). az is the sole cloud sub-adapter
// that shells out because:
//
//   - ~/.azure/azureProfile.json schema is undocumented and drifts
//     between az versions.
//   - The actual subscription-change involves more than a single file
//     write (tokens, cached metadata) — replicating it in Go would be
//     a fragile re-implementation.
//   - `az account set` is a single shell call with a stable contract.
//
// Rollback fidelity is limited: capturing the prior subscription
// would require `az account show` at Capture time, which is slow.
// For v0.3-3 we don't capture; Rollback no-ops on the azure side.
// Production code that needs full rollback fidelity can extend
// cloudSnapshot to embed `az account show` output.

package adapter
