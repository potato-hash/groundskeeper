# Original Prompt Compliance Matrix

Requirement → implementation → tests/evidence → status.

| Phase | Requirement | Implementation | Tests/Evidence | Status |
|---|---|---|---|---|
| 0 | Agent Deck audit | docs/upstream-agent-deck-audit.md | exists | done |
| 0 | roboomp audit | docs/upstream-roboomp-audit.md | exists | done |
| 1 | Rename to Groundskeeper | module path, binary, XDG, README, AGENTS.md, THIRD_PARTY_NOTICES | go build passes, statedb tests pass | done |
| 2 | Ownership map | docs/ownership.md | exists | done |
| 2 | Roadmap | docs/roadmap.md | exists, honest status | done |
| 3 | Durable substrate (10 tables) | internal/gkdb schema.go | TestThreadPersists, TestJobPersists, TestApprovalRequestThenResolve, TestAuditRedacts | done |
| 3 | CLI status/thread/approvals | gk-status, gk-thread, gk-approvals | CLI smoke test | done |
| 4 | Runtime adapter interface | internal/runtime/adapter.go | TestFakeStartThreadEmitsReady, TestFakeSendTurnSequence | done |
| 4 | Fake adapter | internal/runtime/fake.go | TestFakePromptAckIsNotCompletion | done |
| 5 | OMP RPC adapter | internal/runtime/omp.go | TestOmpStartThreadEmitsReady, TestOmpSendTurnStreamsAgentEnd, TestOmpShutdownClosesStream, TestScrubEnvStripsAPIKeys | done |
| 5 | Live OMP smoke | omp_live_test.go (build tag omp_live) | TestOmpLive_TurnCompletes passes with real omp | done |
| 6 | Worker pool | internal/worker/pool.go | TestPoolRunsJobToCompletion, TestPoolPerThreadSerialization, TestPoolRequeuesStuckOnRestart | done |
| 6 | Per-thread serialization | ClaimNextJob anti-join | TestClaimNextJobSerializesSameThread | done |
| 6 | Stuck-running reset | ResetStuckRunning | TestPoolRequeuesStuckOnRestart | done |
| 6 | Dead-letter | DeadLetter + FailJob | TestFailJobDeadLettersAfterMaxAttempts | done |
| 7 | Loop turns + max_turns | loop_runs table + runLoop | TestLoopMaxTurnsEnqueuesExactly, TestLoopCounterPersistsAcrossReopen, TestTwoLoopRunsSeparateCounters, TestRetryDoesNotCountAsNewTurn | done |
| 7 | Loop stop conditions | ShouldStop (agent_says_done, tests_pass, diff_empty, approval_required, same_failure_repeated, max_turns, max_wall, max_tools, max_cost, secret_or_policy) | TestLoopSpecShouldStop | done |
| 7 | Thread prompt/resume/fork | gk-thread prompt/resume/fork CLI | CLI smoke | done |
| 7 | Loop set/start/stop | loop set/start/stop/show CLI | CLI smoke | done |
| 8 | Host tools | internal/host/tools.go (request_approval, record_audit, task_update, job_status, notify_user, draft_message) | TestRequestApprovalCreatesRow, TestRecordAuditRedacts, TestJobStatus | done |
| 8 | pa:// URI scheme | internal/host/uri.go | TestURISchemeReadTasks, TestURISchemeWriteRefusedByDefault, TestURISchemeUnknownScheme | done |
| 8 | Host tool result bridge | omp.go dispatchFrame host_tool_call → host_tool_result | TestOmpSendTurnStreamsAgentEnd (stub sends result back) | done |
| 9 | Agent Deck UI integration | internal/ui/gk_home.go (HomeItem/HomeSource/AgentDeckSessionSource/GroundskeeperThreadSource) + home.go wiring | TestGkSourceDisabledWhenNoDB, TestGkSourceLoadsThreads, TestGkItemsNilSafe, TestGkFooterLineDisabled, TestAgentDeckSessionItem | done |
| 10 | Espalier status | espalier status CLI, --espalier-path daemon flag | CLI smoke | done |
| 11 | Sidecar boundary | internal/sidecar (local_notes stub, HMAC server) | TestServerRejectsBadSignature, TestServerAcceptsGoodSignature, TestEmailHandlerNoCredential | done |
| 12 | Watchers | internal/watcher/gk_webhook.go | TestWebhookBadSignature, TestWebhookEnqueuesTurn, TestWebhookRejectsUnknownThread | done |
| 13 | Auth pass-through | auth status CLI | CLI smoke | done |
| — | Managed process launcher | internal/process/launcher.go | TestProcessOmpRpcRefusedWithoutFields, TestProcessOmpRpcAcceptedWithAllFields | done |
| — | State machine | Job statuses (queued/running/waiting_runtime/waiting_approval/done/retry/failed/dead_letter) | TestPromptAckDoesNotCompleteJob, TestAgentEndCompletesJob, TestProcessExitRetriesOrDeadLetters | done |
| — | App identity | internal/appidentity | TestAppNames, TestIsLegacy, TestShouldUseLegacy | done |
| — | Config | config.toml (7 sections) | exists | done |
| — | Test strategy | docs/test-strategy.md | exists | done |
| — | Upstream compat | docs/upstream-compatibility.md | exists | done |

## Security checklist

| Check | Status | Evidence |
|---|---|---|
| OMP worker env scrubbed | done | TestScrubEnvStripsAPIKeys |
| logs redact secrets | done | TestAuditRedactsSensitiveValues, TestRecordAuditRedacts |
| audit logs redact host tool args/results | done | host.Bridge audits via RecordAudit (redacted) |
| host tools fail closed | done | TestBridgeUnknownTool returns error |
| approval required for external side effects | done | host_tool_call → RequestApproval for high/medium risk |
| no provider auth storage in Groundskeeper | done | auth status inspects OMP, never stores |
| sidecar token never enters orchestrator env | done | HMAC model; daemon holds only signing key |
| stuck running jobs recover on restart | done | TestPoolRequeuesStuckOnRestart |
| loop budgets enforced | done | TestLoopMaxTurnsEnqueuesExactly |
| same-thread concurrency prevented | done | TestClaimNextJobSerializesSameThread |
| prompts accepted != jobs complete | done | TestPromptAckDoesNotCompleteJob, TestOmpSendTurnStreamsAgentEnd |
| host URI writes default deny | done | TestURISchemeWriteRefusedByDefault |
| watcher cannot launch unmanaged worker | done | TestWebhookRejectsUnknownThread, process launcher allowlist |
