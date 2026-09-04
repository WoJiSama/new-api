# Findings

## Restored State

- Branch `custom/upgradeable-daily-channel-quota` is based on official `v1.0.0-rc.24` commit `5c3abffe` and contains the affinity retry regression-test commit `f595d151`.
- Existing uncommitted daily-quota skeleton is incomplete: it resets mutable rows without atomicity, reads the database while the channel cache read lock is held, and has no configuration or reservation handling.
- Daily quota must be independent of auto-ban status because automatic channel tests can re-enable `status=3` before midnight.

## Intended Design

- Store channel daily limit in the existing `settings` JSON to avoid changing the upstream channels table.
- Store usage in a dedicated, date-keyed ledger table.
- Reserve request quota atomically before dispatch and reconcile it against actual channel settlement so concurrent dispatch cannot exceed the configured cap.
- Expose current-day values on the channel API/list without exposing ledger implementation details.

## Lifecycle Details

- Standard relay pricing and user pre-consume happen before a channel is selected. After selection, tiered billing may raise the pre-consume amount for a new group.
- The daily ledger must therefore reserve after channel selection and tiered billing preparation, using the final pre-consumed estimate. A failed attempt releases its reservation before retrying another channel.
- Existing quota settlement updates `channels.used_quota` before `SettleBilling`. The daily ledger can safely add actual usage there and release the reservation during settlement; `used + reserved` never exceeds the configured limit while actual usage stays within the pre-consumed estimate.
- Cache-disabled selection also needs daily filtering; filtering only the memory-cache path would violate the feature contract.
- The rc.24 channel status cell already renders `other_info.status_reason` and `status_time` for auto-disabled channels, so that previous custom UI behavior is preserved by the new upstream implementation.

## Per-Error Recovery Intervals

- The global default is `AutoTestChannelMinutes` in monitor settings. The scheduler invokes one channel-test task at that cadence; it does not currently retain a separate per-channel recovery deadline.
- Request errors reach `controller/relay.go:processChannelError`, which calls `service.DisableChannel`; automatic test failures use the same function. The matching logic can therefore live in the service layer and apply consistently to both sources.
- Auto-disable status is stored in `other_info` for normal channels and in the multi-key disabled-reason/time maps for individual keys. Recovery metadata must follow both representations so multi-key channels obey the selected error rule.
- The queued manual "test all" task carries `Mode=scheduled_all` and `Notify=true`, whereas the scheduled task has an empty payload. An explicit `scheduled` flag at the test-loop boundary safely distinguishes the two without changing the public task payload.
- A per-channel interval shorter than the global cadence cannot take effect while the scheduler only wakes every global interval. The system-task handler must therefore use the smallest valid configured recovery interval (capped by the global interval) as its wake cadence; per-channel `retry_after` filtering ensures only due auto-disabled channels are probed on the extra passes.

## Unrestricted Daily Usage Statistics

- `PopulateChannelDailyQuota` currently queries only channels whose `daily_quota_limit` is positive, and `RecordChannelDailyQuotaUsage` updates only an already-existing ledger row. This causes channels without a limit to have neither durable daily usage nor a list value.
- The ledger must upsert actual daily usage for every channel at settlement. Reservation remains limited to configured budgets, preserving the existing concurrency and selection behavior for capped channels.
- The list should show just today's used quota for an unlimited channel, while retaining `used / limit`, reserved, remaining, and exhaustion state when a positive limit exists.

## Fixed-Channel Quota Exhaustion

- The production CPU profile captures `controller.Relay -> service.ReserveChannelDailyQuotaForAttempt -> model.Channel.GetOtherSettings -> json.Unmarshal` for essentially all samples.
- When `RelayInfo.ChannelMeta` is nil, `getChannel` returns the same fixed channel and ignores `RetryParam.ExcludeChannel`. If daily quota reservation fails, the relay loop currently excludes then continues, reselecting that same channel forever.
- The correction must set a relay error and exit the selection loop for a fixed channel. Dynamic channel selection must keep excluding the exhausted channel and moving to eligible alternatives.

## Manual Channel-Test Auto-Disable

- `GET /test/:id` is the single-channel manual test endpoint. It currently calls `testChannel` and returns a failure JSON response without invoking any channel status update.
- The queued “test all channels” path already calls `processChannelError` for eligible failures, so the requested behavior only needs to cover `TestChannel`.
- Manual failure handling should target an enabled channel with `AutoBan` enabled, use the existing `service.DisableChannel` path (which persists recovery metadata and notifications), and not alter manually-disabled channels or channels that explicitly opt out of auto-ban.
- The explicit manual action should not depend on the global automatic-disable toggle; the user requested direct disabling after a manual connection-test failure.

## Failure-Limiter Failover Preservation

- `RelayFailureRateLimit` runs before `Distribute` on relay routes. Its current key is only token/user identity, and after the configured count of 5xx responses it aborts every new request with a local 429 before channel selection can run.
- This explains the observed `exceeded retry limit, last status: 429`: failing/depleted upstream channels have incremented the token-wide failure counter, so a subsequent request cannot fail over until the fixed window expires. A manual retry after the cooldown works because the counter expires.
- The correction must preserve the anti-retry protection when no recovery path exists, while allowing one request through to fresh selection after a limiter hit. The limiter also must not treat local selection failures as evidence that a healthy upstream is unavailable.
- Implemented in commit `1f534e441`: a limiter hit uses a Redis `SETNX` probe lease for one request per failure window, clears the current affinity, and marks the request so `Distribute` skips affinity. A successful dispatched request clears both the failure counter and the probe lease. A failed probe leaves the limiter closed. Errors with no `use_channel` entry are ignored by the limiter.

## Daily Quota Currency UX

- Production channels 9 (`ala-2`) and 10 (`ala-3`) have raw `daily_quota_limit` settings of 40 and 30 respectively. With the configured `quotaPerUnit=500000`, these are only $0.00008 and $0.00006. Thus the table's `$0 / $0.0001` is an accurate but unusable rendering, not an accounting error.
- The channel editor previously accepted raw quota units while showing currency in the list, creating the misleading configuration experience. It now presents and accepts the configured display currency, converts to raw units only when saving, and labels the field accordingly. Existing values are converted when editing; no production settings are changed automatically.

## Client-Cancellation Failure State Cleanup

- Production logs showed a repeated pattern from 15:06 onward: `relay failure limiter clear failed: context canceled`. Successful relay responses can finish after the client has disconnected, at which point the middleware was trying to delete the token failure window with the canceled request context.
- The deletion then failed, leaving stale Redis 5xx counters capable of producing later local `429` responses and client-visible `exceeded retry limit`. Independent of that bug, channels 3 and 5 were also correctly auto-disabled after upstream `DAILY_LIMIT_EXCEEDED`, and channel 10 after `WEEKLY_LIMIT_EXCEEDED`.
- Commit `5d1174370` uses a two-second `context.Background()` timeout for failure-window writes and deletes. The new regression cancels the request context before a successful recovery response and proves that the subsequent request is not rate-limited.

## Sticky-Session 429 Failover

- Request `202608311217524365375598268d9d6VbtdRqQr` reached channel 9 and received a real upstream `429 DAILY_LIMIT_EXCEEDED`. The channel was auto-disabled, but the request returned 429 after 1.23 seconds with only channel 9 in its path.
- The default Codex affinity rule has `skip_retry_on_failure=true`. `shouldRetry` only overrides that sticky failure policy when `isTransientUpstreamFailure` recognizes the error; the helper currently recognizes 502/503/504 and timeouts, but not 429. This short-circuits the normal retry configuration even though 429 is an enabled automatic-retry status.
- A dispatched upstream 429 should invalidate the current sticky entry and allow the existing retry loop to select another eligible channel. The pre-dispatch local failure limiter is separate middleware and does not pass through this controller branch.
