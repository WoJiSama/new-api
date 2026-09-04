# Task Plan: Channel Daily Quota and Per-Error Recovery Intervals

## Goal

Preserve the completed daily quota implementation and add upgrade-friendly per-channel recovery rules. A rule matches an automatic-disable error by HTTP status and optional message text, then delays the next scheduled recovery test for its configured number of minutes. No matching rule retains the global monitor cadence; manual tests bypass the delay.

## Phases

### Phase 1: Restore and Inspect
- [x] Confirm branch base and existing uncommitted skeleton
- [x] Trace channel selection and quota settlement lifecycle
- **Status:** complete

### Phase 2: Backend Daily Ledger
- [x] Add durable daily usage/reservation model and atomic operations
- [x] Connect settings, selectors, and settlement/release paths
- **Status:** complete

### Phase 3: Channel UI
- [x] Add the editor field and API-visible daily usage state
- [x] Add list columns and automatic-disable reason display
- **Status:** complete

### Phase 4: Verification
- [x] Add focused concurrent backend tests
- [x] Run Go tests, frontend checks, and a Linux build
- **Status:** complete

### Phase 5: Recovery-Interval Backend
- [x] Add channel settings schema and validation for status/message/interval rules
- [x] Persist the matched recovery deadline when a channel or key is auto-disabled
- [x] Skip only scheduled tests until that deadline; keep manual tests immediate
- **Status:** complete

### Phase 6: Recovery-Interval UI
- [x] Add per-channel rule editing with status code, message keyword, and minutes
- [x] Show the next scheduled recovery time in the channel status detail
- **Status:** complete

### Phase 7: Recovery-Interval Verification
- [x] Add matching, persistence, scheduled-skip, and manual-bypass tests
- [x] Run focused Go tests, race test, frontend checks, and production build
- **Status:** complete

### Phase 8: Single-Instance Deployment
- [x] Verify server capacity, container configuration, and uploaded Linux binary checksum
- [x] Create rollback image/binary backup and replace the executable in-place
- [x] Restart the existing container once and verify runtime health
- **Status:** complete

### Phase 9: Daily Usage Statistics Without a Limit
- [x] Record daily channel usage even when no daily limit is configured
- [x] Populate the channel list for every channel and render an unrestricted usage state
- [x] Add regression coverage and run backend/frontend verification
- **Status:** complete

### Phase 10: Fixed-Channel Daily Quota Exhaustion
- [x] Reproduce the fixed-channel reservation failure path and add a regression test
- [x] Return a quota-exhausted relay error instead of reselecting the same fixed channel
- [x] Run focused backend tests and deploy the corrected binary to the existing container
- **Status:** complete

### Phase 11: Full Branch Deployment
- [x] Confirm the authorized branch and enumerate the complete worktree scope
- [x] Verify backend and frontend changes together
- [x] Build and deploy the authorized branch through the existing production container
- [x] Verify health and resource use after deployment
- **Status:** complete

### Phase 12: Manual Channel-Test Auto-Disable
- [ ] Add regression coverage for single-channel manual-test failures
- [ ] Disable an enabled channel immediately when its manual connection test fails
- [ ] Run backend/frontend verification and deploy the complete branch
- **Status:** in_progress

### Phase 13: Failure-Limiter Failover Preservation
- [x] Trace the production 429 path and define the retry/failover boundary
- [x] Let an otherwise rate-limited relay request make one fresh channel-selection attempt
- [x] Avoid recording local channel-selection failures as upstream failures
- [x] Add regression coverage, verify, deploy, and check production health
- **Status:** complete

### Phase 14: Daily Quota Currency UX
- [x] Identify the raw quota-unit values behind the confusing display
- [x] Make channel daily-budget input use the configured display currency
- [x] Preserve nonzero tiny amounts in the daily-quota column
- [x] Verify the frontend build
- **Status:** complete

### Phase 15: Client-Cancellation Failure State Cleanup
- [x] Correlate intermittent local 429s with production logs and Redis behavior
- [x] Decouple relay-failure state writes from canceled client request contexts
- [x] Add regression coverage and run full backend verification
- [x] Deploy through the existing container and verify health
- **Status:** complete

### Phase 16: Sticky-Session 429 Failover
- [x] Treat dispatched upstream 429 as a transient sticky-channel failure
- [x] Add regression coverage for affinity release on 429
- [x] Run full backend verification and production Linux build
- [x] Deploy through the existing container and verify health
- **Status:** complete

## Constraints

- Keep the custom commit series limited to product changes so it can be rebased onto future official releases.
- Do not start another production app instance, build on the server, stop, remove, or replace the current production container during this implementation phase.
- Do not use auto-ban status for daily exhaustion.
- A custom rule controls only scheduled recovery probing after auto-disable; it does not delay the same-request retry/failover path.
- Deploy only through the existing `new-api` container; never run a second application instance during the cutover.
- A zero daily limit means unlimited scheduling, not disabled daily usage statistics.
