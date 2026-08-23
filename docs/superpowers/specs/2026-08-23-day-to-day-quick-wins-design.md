# Day-to-day quick wins design

## Goal

Improve routine local and CI use of `tf-version-bump` with a machine-friendly check mode, complete
Terraform update reporting, copyable automation guidance, maintained scenario examples, and a
simpler pull-request configuration-validation workflow.

## Delivery sequence

The work is split across two pull requests because the copyable GitHub Actions example deliberately
executes an independently downloaded release artefact rather than a binary built from its checkout.

1. Add the CLI, report, documentation, and scenario changes; merge them to `main`; publish
   `v1.0.0-rc.10`.
2. Verify the published Linux x86-64 archive and use its digest to migrate the Actions example from
   `v1.0.0-rc.9` to `v1.0.0-rc.10`.

The first pull request must remain compatible with the currently pinned rc.9 example harness. The
second pull request changes the strict report reader and release pin together, so the example never
expects one report schema while executing a release that emits another.

## CLI check mode

Add `-check` to every update mode. It performs the same selection, parsing, filtering, diagnostics,
and human-readable preview as `-dry-run`, but its process status is designed for automation:

- `0` when no eligible version value would change;
- `2` when one or more eligible version values would change;
- `1` for invalid arguments, invalid input, or processing failures.

`-check` never modifies Terraform files. It is mutually exclusive with `-dry-run` because the two
flags have different exit contracts, and with `-report-file` because creating a report would violate
the no-write promise. It remains compatible with `-config`, the three direct update modes,
`-force-add`, filters, `-verbose`, and `-output`.

The check status is decided only after all selected files have been processed. A processing error
always wins over status 2. A normal return represents status 0; the existing injected exit hook is
used only to request status 2 or report a fatal status 1.

The existing `-dry-run` output and status contract stay unchanged. Check mode reuses its preview
wording and adds a distinct mode banner so humans can tell which exit contract is active.

## Update report schema version 2

The JSON update report gains an exact `terraform_blocks_updated` integer and advances from schema
version 1 to schema version 2:

```json
{
  "schema_version": 2,
  "terraform_blocks_updated": 1,
  "module_blocks_updated": 4,
  "provider_blocks_updated": 2
}
```

The Terraform count represents unique top-level `terraform` blocks whose `required_version` value
changed. Missing `required_version` attributes count when added. Blocks already semantically equal
to the requested constant string do not count. Hard-linked paths to the same physical file and
repeated processing of the same block count once, matching the existing module and provider report
identity rules.

Dry-run reports continue to contain zero counts because they describe applied changes, not proposed
changes. Check mode rejects reports entirely.

The internal Terraform updater returns changed top-level block indexes alongside its existing
updated/error result. `processTerraformVersion` records those identities only in write mode and
only when a report was requested.

## Documentation and maintained examples

Expand `examples/README.md` into a compact automation cookbook that shows:

1. standalone configuration validation;
2. previewing updates;
3. using `-check` in CI while handling status 2 explicitly;
4. applying updates with a report;
5. inspecting schema version 2 counts with `jq`.

Every maintained `tf-version-bump` YAML configuration begins with the published
`yaml-language-server` schema declaration so editors can provide completion and diagnostics.

Correct the usage reference's expression wording. Source and filter matching remain literal, and
Terraform variables and functions are not evaluated. For idempotency only, a version expression
that HCL can evaluate as a wholly known constant string is compared with the requested value before
rewriting.

Add two focused, copy-safe scenario directories:

- `examples/scenarios/force-add` contains a registry module without `version` and demonstrates the
  default warning followed by `-force-add`.
- `examples/scenarios/idempotency` contains Terraform, provider, and module targets and demonstrates
  that applying the same config a second time produces no filesystem change.

The checked-in fixtures are never modified by documentation commands. A maintained scenario runner
copies each fixture into a temporary directory, builds the repository binary once, executes the real
commands, and verifies output and file effects. It has help text, precise failures, and automatic
temporary-directory cleanup. `make docs-check` runs the scenario runner so copyable examples cannot
silently rot.

## GitHub Actions example migration

After rc.10 is published, update every Actions example release pin and verified archive digest to
that release. Its strict update-report reader accepts exactly schema version 2 with the four report
keys, validates `terraform_blocks_updated` as a non-negative integer, and continues aggregating the
module/provider counts used by its existing manifest. Propagating the Terraform count into the POC
manifest or pull-request body is deliberately out of scope; the CLI report itself owns that new
contract.

Replace the configuration-validation workflow's generated Terraform fixture and config-mode dry
runs with direct calls of:

```bash
tf-version-bump -validate-config <config>
```

The workflow retains read-only permissions, an immutable action pin, archive checksum verification,
binary version verification, and validation of both control configurations. The harness exercises
the published rc.10 binary and asserts that invalid configuration is rejected without selecting or
modifying Terraform files.

## Release and acceptance

All commits created or rewritten by Codex are SSH-signed with the dedicated GenAI key. Pull-request
commits are verified as signed before rebase merge.

Before tagging rc.10, the first pull request must pass the complete Go test suite with race detection
and coverage, lint, build, module verification, documentation checks, and the current rc.9 Actions
example harness. Publish an annotated `v1.0.0-rc.10` tag only from the verified merged `main` commit.
Wait for the release workflow, then verify the expected artefacts, checksum, provenance, and Linux
x86-64 binary version before recording the archive digest.

The second pull request must pass the same repository verification plus the Actions harness using
the published rc.10 artefact. No second release is required because that pull request changes only
the copyable example and its documentation/tests.

## Non-goals

- Change `-dry-run` exit behaviour.
- Emit proposed counts in dry-run reports.
- Preserve report schema version 1 or accept both report schemas in one consumer.
- Add stale pull-request cleanup, hostile Terraform isolation, GitHub App authentication, commit
  signing in Actions, or publication-race recovery.
- Add provider-only or Terraform-only YAML files that duplicate the existing combined config.
- Propagate Terraform counts into the Actions POC manifest or generated pull-request body.
