package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ReserveChannelDailyQuotaForAttempt reserves this attempt's maximum expected
// channel spend. A false result means the channel has insufficient daily room.
func ReserveChannelDailyQuotaForAttempt(info *relaycommon.RelayInfo, channel *model.Channel) (bool, error) {
	if info == nil || channel == nil {
		return false, fmt.Errorf("missing relay info or channel")
	}
	quota := int64(info.PriceData.QuotaToPreConsume)
	if preConsumed := int64(info.FinalPreConsumedQuota); preConsumed > quota {
		quota = preConsumed
	}
	day, reserved, err := model.ReserveChannelDailyQuota(channel, quota)
	if err != nil {
		return false, err
	}
	if !reserved {
		return model.GetChannelDailyQuotaLimit(channel) <= 0 || quota <= 0, nil
	}
	info.DailyQuotaChannelId = channel.Id
	info.DailyQuotaDay = day
	info.DailyQuotaReserved = quota
	return true, nil
}

// ReleaseChannelDailyQuotaForAttempt is safe to invoke in every failure path.
func ReleaseChannelDailyQuotaForAttempt(info *relaycommon.RelayInfo) {
	if info == nil || info.DailyQuotaReserved <= 0 {
		return
	}
	if err := model.ReleaseChannelDailyQuotaReservation(info.DailyQuotaChannelId, info.DailyQuotaDay, info.DailyQuotaReserved); err != nil {
		common.SysError(fmt.Sprintf("failed to release channel daily quota reservation: channel_id=%d, error=%v", info.DailyQuotaChannelId, err))
	}
	info.DailyQuotaChannelId = 0
	info.DailyQuotaDay = ""
	info.DailyQuotaReserved = 0
}

// SettleChannelDailyQuotaForAttempt releases a successful attempt's reservation
// after UpdateChannelUsedQuota has recorded the actual spend in the ledger.
func SettleChannelDailyQuotaForAttempt(info *relaycommon.RelayInfo) {
	ReleaseChannelDailyQuotaForAttempt(info)
}
