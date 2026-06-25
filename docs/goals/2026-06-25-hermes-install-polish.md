# Goal: Hermes-Polished Install

## Supersedes

This prompt supersedes the stale blocked VM/vision goal that required UTM app screenshots before completion. Visual inspection is useful when validating the TUI, but it is not a blocker for the install-polish loop. The install goal is accepted by terminal evidence, clean-VM smoke output, and targeted file/state checks.

## Prompt

Iterate the Groundskeeper + Espalier + OMP install path until it feels as polished as Hermes: one obvious command, clean first-run output, correct dependency handling, no credential leakage, and deterministic recovery when a machine is fresh or half-configured.

Treat Groundskeeper as the distribution layer. Keep Espalier as a separate public extension dependency loaded by OMP. Keep provider authentication delegated to OMP, and do not write global OMP config unless the operator explicitly opts in.

## Acceptance Criteria

1. A clean macOS VM can run one public GitHub command and finish with:
   - `groundskeeper` installed.
   - `omp` installed or clearly discovered.
   - Espalier cloned, dependencies installed, and `dist/extensions/index.js` built.
   - `gk.db` created.
   - selected OMP model verified when `--verify-model` is requested.
2. The install command is documented and copyable with an environment-variable placeholder for credentials, never an inline key.
3. Pre-release installs are robust:
   - Release binaries remain preferred when available.
   - Source fallback bypasses stale Go module proxy state.
   - Missing source-build prerequisites fail with actionable guidance.
4. First-run setup is idempotent:
   - Re-running does not reclone or rebuild unnecessarily.
   - Existing Espalier checkouts are built if incomplete.
   - Existing OMP/Groundskeeper installs are reused.
5. Credential hygiene is explicit:
   - Provider keys are accepted only from environment variables or OMP auth.
   - Setup output never prints keys.
   - No key is written to Groundskeeper, Espalier, or OMP config by setup.
6. Failure handling is operator-grade:
   - `--run-setup` exits nonzero when setup fails.
   - Errors say what failed, why it matters, and the next command to run.
   - CDN/cache behavior has a reliable workaround for immediate `main` testing.
7. Verification evidence exists:
   - Focused installer/setup unit tests.
   - `bash -n install.sh`.
   - `go build ./cmd/groundskeeper`.
   - Clean-VM install smoke with key redaction.
   - Secret persistence scan over relevant VM install/state dirs.

## Active Blocker Backlog

Release invariants already cleaned up in the first unblock patch, and kept here
so future iterations do not regress them:

- Release artifacts must stay consistently named and built as Groundskeeper:
  `.goreleaser.yml`, `.github/workflows/release.yml`, `install.sh`, and release
  checksum tests all expect `groundskeeper_*` archives containing a
  `groundskeeper` binary.
- Self-update must keep targeting the Groundskeeper upstream/artifacts before
  release-based install is promoted as complete.

P0 blockers that must still be cleared before calling the install path
Hermes-polished:

- Pre-release public installs need either a published release binary or a
  clearly preflighted Go source-build requirement.

P1 blockers:

- Unattended setup must not print "complete" when required pieces are missing.
- Setup subprocess output must stay redacted even if tools echo provider
  environment variable names or values.
- Both uninstall paths must remove current Groundskeeper artifacts and XDG
  state paths without relying on old Agent Deck labels.

P2 polish:

- Provider credential detection should defer to OMP where possible instead of
  being Ollama-only.
- Espalier repair behavior should be explicit for half-created checkouts.
- The clean-VM smoke and secret scan should be repeatable from a checked-in
  script or documented checklist.

## Working Rules

- Prefer small patches to the installer/setup surface over broad refactors.
- Do not add new package managers or major dependencies.
- Do not commit generated binaries, local databases, `.env` files, or provider keys.
- Use `jj` as the source-of-truth VCS workflow.
- Use subagents only for bounded audits or disjoint implementation slices.
