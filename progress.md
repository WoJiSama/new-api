# Progress

## 2026-08-10

- Restored the custom rc.24 branch state and replaced stale staging notes with the active daily-quota implementation plan.
- No server command or production container operation has been performed in this session.
- Traced standard relay lifecycle, cache and database selectors, and the existing billing settlement order. The implementation will use post-selection atomic reservations, not post-response-only accounting.
- Added the date-keyed daily ledger, atomic reservation/release operations, both selector filters, standard relay settlement wiring, channel API fields, form input, and list cell.
- `go test ./model ./service ./controller` could not start because the local module cache lacks dependencies and `proxy.golang.org` timed out repeatedly. No compilation result is available yet.
- Added a selector regression test: an exhausted high-priority channel is removed before priority selection, so the first attempt chooses the lower available priority.
- Downloaded missing Go modules through the fallback proxy and passed `go test ./model ./service ./controller`.
- Passed `go test -race ./model -run 'ChannelDailyQuota' -count=1`.
- Installed frontend dependencies with `bun install --frozen-lockfile`; passed frontend typecheck, targeted lint for changed channel files, and `bun run build`.
- Full frontend lint remains red on pre-existing rc.24 files outside this change; the changed channel files pass targeted lint.
- Built `/tmp/new-api-daily-quota-linux-amd64` successfully with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath`.
- Began the requested per-channel recovery-interval feature. Confirmed it concerns scheduled recovery tests after auto-disable, not in-request failover. No production process or container was started or changed.
- Confirmed an implementation detail required for exact per-channel timing: the scheduler itself must wake at the minimum of the global interval and configured channel rules, while channel-level deadlines prevent unrelated auto-disabled channels from being tested early.
- First focused Go test attempt: `relaykit/dto` is not a standalone package in this module, and the service call needed a local settings variable because the matcher has a pointer receiver. The call has been corrected; coverage will run through the importing packages.
- The corrected root-module test then found two local controller compile errors (an unused scheduler variable and a missing `time` test import). Both are corrected; the nested `relaykit` module will be tested from its own module root.
- Added the channel-editor recovery-rule UI and channel status tooltip fields. The form serializes rules into the existing `settings` JSON and keeps no server-specific migration state.
- Root-module backend tests pass. The nested relaykit module attempted to fetch `golang.org/x/text` for its independent test run and did not complete in the available network window, so the pure matcher regression test was moved into the root `model` test package that imports the same local relaykit code and is covered by the passing root test run.
- Added a model integration regression test proving an auto-disable stores the matched recovery deadline and automatic enable clears it.
- Added the failed-recovery renewal path: an auto-disabled channel that still errors on a due scheduled test receives a fresh deadline from the same matching rule; a non-matching subsequent error clears the custom deadline and returns to the global cadence.
- Frontend typecheck initially reported two implicit callback parameter types while parsing persisted JSON; explicit rule types have been added. A combined verification command also attempted Go paths from `web/`, which correctly failed as missing directories and made no filesystem changes; backend and frontend checks will now run from their respective roots.
- Hardened the recovery loop for local test failures that do not provide a `NewAPIError`: the custom deadline is cleared so the channel returns to global cadence instead of repeatedly scheduling an already-due recovery probe.
- Final verification passed: `go test ./model ./controller ./service`; `go test -race ./model -run 'RecoveryRetry|UpdateChannelStatusPersists' -count=1`; `web/` `bun run typecheck`, targeted `oxlint`, and `bun run build`; plus `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/new-api-channel-recovery-retry-linux-amd64 .`.
- No service, container, server process, or production deployment was started or changed.
- 2026-08-11 deployment phase: confirmed 3.8 GiB available memory, 21 GiB disk availability, existing `new-api` container configuration, and a matching uploaded Linux binary SHA-256. The first long compound SSH command failed authentication before any remote action; no container state changed. Retry deployment as short independent commands.
- Deployed with no parallel app instance: created rollback image `wojisama/new-api:backup-pre-channel-recovery-20260811` (sha256:e15c09ad722b...), copied the old binary to `/root/new-api-pre-channel-recovery-20260811`, atomically replaced `/new-api` in the existing container, and committed `wojisama/new-api:channel-recovery-20260811` (sha256:50a853b4a525...).
- Restarted the same `new-api` container once. It is running; localhost ports 3000 and 80 both returned HTTP 200. The deployed `/new-api` SHA-256 is `b939f5dc9df642bb2fc439cb52942144726c35de18055a87d8e3269f421d635a`; steady-state memory was 59.1 MiB. Startup logs showed successful channel tests and normal relay traffic.
- 2026-08-12: traced the missing unrestricted daily statistic to two coupled conditions: ledger writes only update pre-existing reservation rows and list hydration queries only positive-limit channel IDs. The planned correction is unrestricted settlement upserts plus all-channel hydration; no server process will be started during implementation.
- Implemented all-channel daily usage accounting: settlement now atomically upserts a day ledger row even for unlimited channels, list hydration fetches all visible channels, and the frontend renders unrestricted daily usage with an `Unlimited` tooltip state. Added a regression test covering recording and hydrating an unrestricted channel.
- `go test ./model ./service` passed. The broad `./controller` package remains blocked by the unrelated existing `TestChannelFieldsAreClassified` assertion for `current_rpm` in `controller/channel_authz_test.go`; it fails before and outside the unrestricted daily-usage path. Continue with focused controller coverage, model race tests, and frontend checks.
- The existing controller classification test exposed a legitimate read-only authorization omission for the already-present server-managed `current_rpm` field. It is now classified and cleared alongside the other response-only channel metrics, allowing the full controller suite to run without weakening edit authorization.
- Final unrestricted-usage verification passed: `go test ./model ./service ./controller`, `go test -race ./model -run 'ChannelDailyQuota|UnrestrictedChannelDailyQuota' -count=1`, and from `web/`, `bun run typecheck`, targeted `bunx oxlint`, and `bun run build`.
- Passed `go test ./model ./controller ./service` after the recovery-rule changes. `bun run typecheck` is not defined by this rc.24 frontend package; inspect available scripts and use its actual TypeScript check next. The command proceeded to dependency resolution for the targeted lint invocation, so the lockfile will be checked before continuing.

## 2026-08-25

- Investigated production `new-api` CPU saturation. A 15-second perf profile attributed 99.79% of samples to daily-quota reservation on the relay path.
- Identified an infinite fixed-channel retry: `getChannel` returns a pinned channel when `ChannelMeta` is nil, so `ExcludeChannel` cannot affect the subsequent iteration after a failed daily-quota reservation.
- Started Phase 10 to add a regression test, return a proper quota-exhausted error for the fixed-channel path, verify the backend, and deploy through the existing container only.
- Added `fixedChannelDailyQuotaExhaustionError`: a fixed/pinned channel now returns a 503 `get_channel_failed` error with retry disabled when its daily reservation cannot be obtained. Dynamic selection still receives nil and can exclude the exhausted channel for a fallback attempt.
- Added controller regressions for both paths. `go test ./controller -run 'FixedChannelDailyQuota|DynamicChannelDailyQuota' -count=1` passed.
- Full affected backend verification passed: `go test ./model ./service ./controller`. `gofmt -d` and `git diff --check` produced no issues. Linux production binary built successfully at `/tmp/new-api-fixed-channel-quota-20260825-amd64`, SHA-256 `b2dabb096857bb25988048eb0a6af1b67214fe09238c1caa23585f067f1da4ad`.
- Deployment preflight found the local worktree contains unrelated in-progress changes, so its binary is not eligible for deployment. The first attempt to create an isolated temporary source copy used a blocked recursive deletion command; no files or server state changed. Continue using a fresh `mktemp -d` directory.
- Isolated the source snapshot at `/tmp/new-api-fixed-channel-source.BCGSfA` and applied only the fixed-channel branch. Its first build command stopped before compiling because the transferred snapshot deliberately excludes `.git`, so `git diff --check` was not applicable; use `gofmt -d` and compile directly instead.
- The isolated source build then stopped at Go embed validation because `web/dist` is not retained in the remote source snapshot. Fetch the currently deployed frontend assets into the isolated source; this does not alter frontend behavior or include unrelated local work.
- Rebuilt the isolated source frontend successfully, then built the Linux hotfix binary `/tmp/new-api-fixed-channel-current-source-amd64` with SHA-256 `666da22fc66576c4ebbb2fb1c7d8eec1e6c351dc2cf3941ee9ea59ebdda7bb60`. This binary contains the remote source snapshot plus only the fixed-channel quota branch.
- Deployed the isolated hotfix through the existing container only. Created Docker rollback tag `wojisama/new-api:backup-before-fixed-channel-quota-20260825` and copied the prior executable to `/root/new-api-pre-fixed-channel-quota-20260825`; committed the installed version as `wojisama/new-api:fixed-channel-quota-20260825`.
- Restarted `new-api` once. It is running with the expected `/new-api` SHA-256 `666da22fc66576c4ebbb2fb1c7d8eec1e6c351dc2cf3941ee9ea59ebdda7bb60`; `/api/status` returned HTTP 200 and three five-second samples each reported 0.00% CPU with 19.8 MiB memory.
- The user authorized deploying all current worktree changes from `custom/upgradeable-daily-channel-quota`, including work from another session. The scope is 26 modified and 4 new files spanning relay safety, channel scheduler/RPM metrics, rate-limit settings, and frontend configuration/UI.
- Full-branch verification passed: `go test ./model ./service ./controller ./middleware`; from `web/`, `bun run typecheck` and `bun run build`; and backend formatting plus `git diff --check` were clean.
- Targeted frontend lint initially found five `parseInt` style warnings in the new rate-limit form. Replaced them with `Number.parseInt` without changing form semantics. Targeted lint, typecheck, production build, and `git diff --check` now pass cleanly.
- Built the complete authorized worktree as `/tmp/new-api-full-branch-20260825-amd64`; SHA-256 `590175b106ab01475b2f32beab9131614016094814b56d0eac3ed9e6441a8d01`.
- Deployed the complete authorized worktree through the existing container. The prior version is retained as `wojisama/new-api:backup-before-full-branch-20260825` and `/root/new-api-pre-full-branch-20260825`; the new image is `wojisama/new-api:full-branch-20260825`.
- Final verification passed: container is running with binary SHA-256 `590175b106ab01475b2f32beab9131614016094814b56d0eac3ed9e6441a8d01`, `GET /api/status` returned HTTP 200, and three 5-second samples were 0.00%, 0.00%, and 0.16% CPU at approximately 19 MiB memory. Startup completed normally and live UI/API requests returned HTTP 200.

## 2026-08-25 Manual Test Auto-Disable

- Confirmed the single-channel manual test endpoint currently only returns `{success:false}` on failure; it does not call the existing disable flow. The test-all task path already disables eligible failures.
- The requested behavior will use `service.DisableChannel` directly for enabled channels with `AutoBan` enabled, bypassing the global automatic-disable switch because this is an explicit manual action.

## 2026-08-30 Failure-Limiter Failover Preservation

- User reported `exceeded retry limit, last status: 429` although resubmitting later succeeds. Confirmed the relay failure limiter returns a local 429 before `Distribute`, preventing a new selection/failover attempt after repeated 5xx responses.
- Started Phase 13 to retain protection from runaway retries without blocking recovery through another channel.
- Added recovery-probe behavior and deterministic middleware regressions for successful recovery, failed recovery re-blocking, and local pre-dispatch 503 handling. Full `go test ./...` passed; production Linux binary SHA-256 was `9e2c323f7ddf3575fed899ec5688a898d0d1937b4dab9110eecb27e766148f5a`.
- Deployed commit `1f534e441` through the existing `new-api` container only. The prior image is `wojisama/new-api:backup-before-relay-failure-failover-20260830`, and the prior executable is `/root/new-api-pre-relay-failure-failover-20260830`. After restart, `/api/status` returned 200, the installed binary matched the expected SHA-256, and steady-state CPU was 0.01% with 26.27 MiB memory.

## 2026-08-31 Daily Quota Currency UX

- Investigated the `$0 / $0.0001` daily quota display. It originates from raw limits of 30 and 40 units on channels 10 and 9; 500,000 internal units equal one displayed USD.
- Updated channel daily-budget entry to use the configured display currency and added nonzero minimum formatting in the list/tooltip. `web/` typecheck and production build pass. Existing channel limits were deliberately left untouched pending the user's desired dollar budgets.
- Committed the UI fix as `fdd27f25f` and deployed it in the existing `new-api` container. The installed binary SHA-256 is `48c4a27a917dbd70aec7598e17095c0129c16cfdc41758c7325c35ab4c82f63f`; `/api/status` returned 200 after restart, CPU was 0.13%, and a live relay request succeeded. Rollback image: `wojisama/new-api:backup-before-daily-quota-currency-20260831`.

## 2026-08-31 Sticky-Session 429 Failover

- Confirmed request `202608311217524365375598268d9d6VbtdRqQr` received a real upstream `429 DAILY_LIMIT_EXCEEDED` on sticky channel 9. The default affinity rule's `skip_retry_on_failure=true` stopped the request before the normal 429 retry path because 429 was not classified as a transient upstream failure.
- Added upstream 429 to `isTransientUpstreamFailure`, so a dispatched 429 clears the current affinity and permits the existing retry loop to select another eligible channel. Added both classification coverage and a real affinity-cache regression proving the cached channel is removed.
- Verification passed: `go test ./controller ./service ./middleware -count=1`, `go test ./...`, and `git diff --check`. The Linux amd64 binary SHA-256 is `107b41312c39696587554f4f77f1fe34a0b90c0e566867a77541deafef43b24c`.
- Committed the product change as `51e207053 fix: fail over sticky sessions after upstream 429`; only `controller/relay.go` and `controller/relay_affinity_retry_test.go` were included.
- Deployed through the existing `new-api` container only. The previous binary is `/root/new-api-pre-sticky-429-failover-20260831`, and rollback image `wojisama/new-api:backup-before-sticky-429-failover-20260831` is `sha256:0d8e5aff1fe1...`. The installed image is `wojisama/new-api:sticky-429-failover-20260831` (`sha256:be8f073d71bc...`).
- Post-restart verification passed: installed binary SHA-256 matched, `/api/status` returned 200, CPU was 0.12%, memory was 39.3 MiB, startup completed normally, and live `/v1/responses` requests returned 200.

## 2026-08-31 Client-Cancellation Failure State Cleanup

- Investigated the recurring client-visible `exceeded retry limit, last status: 429`. Production logs established that the relay failure limiter repeatedly failed to clear successful token state because the client request context was already canceled.
- Added independent bounded Redis contexts for recording/clearing relay failure state and a cancellation regression. `go test ./...` passed; Linux binary SHA-256 `d0a49a183eca68eb195b2f1a0d9bdba4d0d97ff666c678f9ec334357a5a844c4` was deployed in the existing container.
- Post-restart: `/api/status` was 200, installed binary checksum matched, and the container was serving live relay traffic. Rollback image: `wojisama/new-api:backup-before-relay-failure-context-20260831`.
