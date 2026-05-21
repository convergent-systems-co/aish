# Community persona bundles (v0.3-5.1)

This directory holds the seed persona TOMLs that get packaged into the
default community persona bundle. Each `*.toml` here is a fully-formed
`persona.Persona` (same schema as `data/personas/builtin/*.toml`).

The build tool at `shell/cmd/aish-persona/` consumes this directory:

```
$ aish-persona build \
    -src     data/personas/community \
    -out     dist/persona-bundles/community \
    -id      community-pack \
    -version 1
```

The bundle is signed with the development Ed25519 keypair compiled
into `shell/internal/persona/trust.go`. The dev signer is for
**testing only** — production bundles must be signed with a key
backed by hardware + audit, as documented alongside the
`aish-persona-dev` trust anchor.

Adding a persona to this directory is a CONTRIBUTION, not an
INSTALL. The user receives community bundles by running:

```
$ aish persona install <bundle-dir>
```

against a built and signed directory. Verification runs the same
safety-floor denylist as `persona create`; a community persona that
attempts to bypass the safety floor is rejected at install time.
