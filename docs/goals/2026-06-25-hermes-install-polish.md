# Goal: Hermes-Polished Install

## Supersedes

This prompt supersedes the stale blocked VM/vision goal that required UTM app screenshots before completion. Visual inspection is useful when validating the TUI, but it is not a blocker for the install-polish loop. The install goal is accepted by terminal evidence, clean-VM smoke output, and targeted file/state checks.

2026-06-26 update: the VM comparison was explicitly reopened for the resumed
visual/UX parity goal. The install-polish acceptance criteria above remain
terminal-evidence based, but the current parity work also records UTM VM
screenshots and VM-local install/setup transcripts.

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

## 2026-06-26 VM Comparison Status

Target VM:

- UTM running VM: `macOS`
- UUID: `EBE40DB0-188A-4DB0-AEA8-EBDE87C444CC`
- Bundle: `/Volumes/iCloudStaging/macOS.utm`
- Guest: macOS `26.3.1`, arm64
- Network: Apple shared networking, MAC `da:77:1c:c1:8b:47`, IP `192.168.64.3`
- Guest-agent `utmctl exec`/`ip-address`: unavailable for this Apple backend
- SSH: reachable as `admin@192.168.64.3`

VM evidence captured under `/Volumes/iCloudStaging/groundskeeper-vm-ux-comparison/`:

- `05-vm-ssh-baseline.txt`: before comparison, VM had curl/bash/git, stale
  `Groundskeeper v0.1.0-gk`, and no Hermes binary.
- `06-hermes-install.txt`: Hermes public installer transcript.
- `07-hermes-version-setup-help.txt`: Hermes `v0.17.0` version and setup help.
- `08-hermes-setup-noninteractive.txt`: Hermes non-interactive setup behavior
  with no secrets.
- `10-groundskeeper-public-install-skip-setup.txt`: Groundskeeper public
  installer updated VM binary to `v0.1.2`.
- `11-groundskeeper-v012-version-setup-help-noninteractive.txt`: released
  Groundskeeper `v0.1.2` version, setup help, and non-interactive setup.
- `12-groundskeeper-local-patched-setup-help.txt`: current local patched binary
  copied to `/tmp/groundskeeper-next` and run in the VM.
- `13-groundskeeper-local-patched-help-clean-combined.txt`: patched
  help/version paths captured with combined stdout/stderr; no tmux warning
  precedes help or version output.
- `14-vm-hermes-setup-help-window.png`: UTM screenshot of Hermes setup help in
  the VM.
- `15-vm-groundskeeper-patched-setup-help-window.png`: UTM screenshot of the
  patched Groundskeeper setup help in the VM.
- `16-vm-groundskeeper-release-setup-help-window.png`: UTM screenshot of the
  released Groundskeeper setup help in the VM.

Comparison results:

- Hermes install UX is more complete and branded. It has a framed installer
  header, progress markers, managed Python/Node setup, browser-tool setup,
  config-template creation, skill sync, and a final command list.
- Groundskeeper public install UX is concise and functional. It detects
  platform/package manager, downloads a release archive, verifies checksum,
  installs to `~/.local/bin`, reports dependencies, and gives first-run
  commands.
- Released Groundskeeper `setup --help` is not at Hermes polish level. It is
  the default Go flag dump (`Usage of setup:`) with no purpose statement,
  idempotency note, examples, or grouped prose.
- Current local patched Groundskeeper `setup --help` closes that specific gap:
  it uses `Usage: groundskeeper setup [options]`, explains the full stack,
  calls out safe re-runs, groups options, and includes copyable examples.
- Released Groundskeeper non-interactive setup is stronger than its help text.
  In this VM it found OMP, Espalier, `gk.db`, credentials, model config, tmux,
  git, bun, and jj, then printed `Setup complete!` with quick-start commands.
- Hermes non-interactive setup does not configure without a TTY or secrets; it
  clearly tells the operator to use config commands, environment variables, or
  interactive `hermes setup`.

Local code changes made from the comparison:

- `cmd/groundskeeper/gk_cmd.go`: custom `setup --help` usage.
- `cmd/groundskeeper/gk_cmd_test.go`: structural guard for the polished setup
  help copy.
- `cmd/groundskeeper/main.go`: skip startup tmux warning for help/version
  invocations.
- `cmd/groundskeeper/main_test.go`: guard root help/version, setup help, and
  profiled setup help warning suppression.

Verification status:

- Focused local tests passed:
  `go test ./cmd/groundskeeper -run 'TestHelpAndVersionInvocationsSkipStartupTmuxWarning|TestSetupHelpMatchesHermesPolishSurface|TestSetupDoesNotRunOmpVersion|TestInstallScriptOffersFirstRunSetup' -count=1`
- Broader relevant tests passed with Go temp/build/module caches on
  `/Volumes/iCloudStaging` because the host data volume was nearly full:
  `go test ./cmd/groundskeeper ./internal/tmux/... -count=1`
- Manual local binary checks passed for `setup --help`, root `--help`, and
  `version`; each produced zero stderr bytes.
- VM patched binary checks passed for `setup --help`, root `--help`, and
  `version` with combined stdout/stderr and no startup tmux warning.
- `git diff --check` passed.
- Secret-pattern scan over captured VM comparison files produced no matches.
- Direct build/manual check passed with output on `/Volumes/iCloudStaging`:
  `go build -o /Volumes/iCloudStaging/groundskeeper-help-check-new ./cmd/groundskeeper`.

Current gap:

- The patched setup help is verified in the VM as `/tmp/groundskeeper-next`,
  but it is not part of the released `v0.1.2` binary or public installer yet.
