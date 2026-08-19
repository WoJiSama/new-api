package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterChannelsByDailyQuotaExcludesExhaustedChannels(t *testing.T) {
	truncateTables(t)
	useChannelDailyQuotaDay(t, time.Date(2026, time.August, 10, 12, 0, 0, 0, time.Local))
	exhausted := testDailyQuotaChannel(91004, 20)
	available := testDailyQuotaChannel(91005, 20)

	day, reserved, err := ReserveChannelDailyQuota(exhausted, 20)
	require.NoError(t, err)
	require.True(t, reserved)
	require.NoError(t, RecordChannelDailyQuotaUsage(exhausted.Id, 20))
	require.NoError(t, ReleaseChannelDailyQuotaReservation(exhausted.Id, day, 20))

	filtered, err := FilterChannelsByDailyQuota([]*Channel{exhausted, available})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, available.Id, filtered[0].Id)
}
