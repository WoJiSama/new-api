package model

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDailyQuotaChannel(id int, limit int64) *Channel {
	return &Channel{
		Id:            id,
		OtherSettings: fmt.Sprintf(`{"daily_quota_limit":%d}`, limit),
	}
}

func useChannelDailyQuotaDay(t *testing.T, now time.Time) {
	t.Helper()
	original := channelDailyQuotaNow
	channelDailyQuotaNow = func() time.Time { return now }
	t.Cleanup(func() { channelDailyQuotaNow = original })
}

func TestChannelDailyQuotaReservationAndSettlement(t *testing.T) {
	truncateTables(t)
	useChannelDailyQuotaDay(t, time.Date(2026, time.August, 10, 23, 0, 0, 0, time.Local))
	channel := testDailyQuotaChannel(91001, 100)

	day, reserved, err := ReserveChannelDailyQuota(channel, 100)
	require.NoError(t, err)
	require.True(t, reserved)

	_, reserved, err = ReserveChannelDailyQuota(channel, 1)
	require.NoError(t, err)
	assert.False(t, reserved)

	require.NoError(t, RecordChannelDailyQuotaUsage(channel.Id, 60))
	require.NoError(t, ReleaseChannelDailyQuotaReservation(channel.Id, day, 100))
	row, err := GetChannelDailyQuota(channel.Id, day)
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, int64(60), row.Used)
	assert.Equal(t, int64(0), row.Reserved)

	_, reserved, err = ReserveChannelDailyQuota(channel, 40)
	require.NoError(t, err)
	assert.True(t, reserved)
}

func TestIsChannelDailyQuotaExhausted(t *testing.T) {
	truncateTables(t)
	useChannelDailyQuotaDay(t, time.Date(2026, time.August, 10, 12, 0, 0, 0, time.Local))
	channel := testDailyQuotaChannel(91008, 100)

	exhausted, err := IsChannelDailyQuotaExhausted(channel)
	require.NoError(t, err)
	assert.False(t, exhausted)
	require.NoError(t, RecordChannelDailyQuotaUsage(channel.Id, 100))

	exhausted, err = IsChannelDailyQuotaExhausted(channel)
	require.NoError(t, err)
	assert.True(t, exhausted)
}

func TestUnrestrictedChannelDailyQuotaIsRecordedAndPopulated(t *testing.T) {
	truncateTables(t)
	useChannelDailyQuotaDay(t, time.Date(2026, time.August, 12, 12, 0, 0, 0, time.Local))
	unrestricted := testDailyQuotaChannel(91006, 0)
	limited := testDailyQuotaChannel(91007, 100)

	require.NoError(t, RecordChannelDailyQuotaUsage(unrestricted.Id, 33))
	require.NoError(t, RecordChannelDailyQuotaUsage(unrestricted.Id, 7))

	row, err := GetChannelDailyQuota(unrestricted.Id, channelQuotaDay())
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, int64(40), row.Used)
	assert.Equal(t, int64(0), row.Reserved)

	require.NoError(t, PopulateChannelDailyQuota([]*Channel{unrestricted, limited}))
	assert.Equal(t, int64(0), unrestricted.DailyQuotaLimit)
	assert.Equal(t, int64(40), unrestricted.DailyQuotaUsed)
	assert.Equal(t, int64(0), unrestricted.DailyQuotaReserved)
	assert.Equal(t, channelQuotaDay(), unrestricted.DailyQuotaDay)
	assert.Equal(t, int64(0), limited.DailyQuotaUsed)
}

func TestChannelDailyQuotaResetsByCalendarDay(t *testing.T) {
	truncateTables(t)
	channel := testDailyQuotaChannel(91002, 50)
	firstDay := time.Date(2026, time.August, 10, 23, 59, 59, 0, time.Local)
	useChannelDailyQuotaDay(t, firstDay)
	day, reserved, err := ReserveChannelDailyQuota(channel, 50)
	require.NoError(t, err)
	require.True(t, reserved)
	require.NoError(t, RecordChannelDailyQuotaUsage(channel.Id, 50))
	require.NoError(t, ReleaseChannelDailyQuotaReservation(channel.Id, day, 50))

	secondDay := firstDay.Add(time.Second)
	channelDailyQuotaNow = func() time.Time { return secondDay }
	secondDayKey := channelQuotaDay()
	row, err := GetChannelDailyQuota(channel.Id, secondDayKey)
	require.NoError(t, err)
	assert.Nil(t, row)

	_, reserved, err = ReserveChannelDailyQuota(channel, 50)
	require.NoError(t, err)
	assert.True(t, reserved)
}

func TestChannelDailyQuotaConcurrentReservationsDoNotExceedLimit(t *testing.T) {
	truncateTables(t)
	useChannelDailyQuotaDay(t, time.Date(2026, time.August, 10, 12, 0, 0, 0, time.Local))
	channel := testDailyQuotaChannel(91003, 100)

	const workers = 40
	const reservation = 10
	var wg sync.WaitGroup
	results := make(chan bool, workers)
	errors := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, reserved, err := ReserveChannelDailyQuota(channel, reservation)
			if err != nil {
				errors <- err
				return
			}
			results <- reserved
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}

	successes := 0
	for reserved := range results {
		if reserved {
			successes++
		}
	}
	assert.Equal(t, 10, successes)
	row, err := GetChannelDailyQuota(channel.Id, channelQuotaDay())
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, int64(100), row.Reserved)
	assert.LessOrEqual(t, row.Used+row.Reserved, int64(100))
}
