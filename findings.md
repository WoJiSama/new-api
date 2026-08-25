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
