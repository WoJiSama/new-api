package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChannelDailyQuota is a per-channel ledger for one server-local calendar day.
// Limits belong to the channel settings JSON, keeping the upstream channels
// table unchanged. A separate row per day avoids reset races at midnight.
type ChannelDailyQuota struct {
	ChannelId int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false"`
	Day       string `json:"day" gorm:"primaryKey;type:varchar(10);autoIncrement:false"`
	Used      int64  `json:"used" gorm:"not null;default:0"`
	Reserved  int64  `json:"reserved" gorm:"not null;default:0"`
}

var channelDailyQuotaNow = time.Now

func channelQuotaDay() string {
	return channelDailyQuotaNow().Format("2006-01-02")
}

func GetChannelDailyQuotaLimit(channel *Channel) int64 {
	if channel == nil {
		return 0
	}
	return channel.GetOtherSettings().DailyQuotaLimit
}

// PopulateChannelDailyQuota attaches today's usage to API-facing channel rows.
// Limits are optional: unrestricted channels still expose their daily usage.
// The function deliberately does not create ledger rows for channels without
// traffic.
func PopulateChannelDailyQuota(channels []*Channel) error {
	if len(channels) == 0 {
		return nil
	}

	channelIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		channel.DailyQuotaLimit = GetChannelDailyQuotaLimit(channel)
		channel.DailyQuotaUsed = 0
		channel.DailyQuotaReserved = 0
		channel.DailyQuotaDay = channelQuotaDay()
		channelIDs = append(channelIDs, channel.Id)
	}
	if len(channelIDs) == 0 {
		return nil
	}

	byChannel, err := getChannelDailyQuotaRows(channelIDs, channelQuotaDay())
	if err != nil {
		return err
	}
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		if row, ok := byChannel[channel.Id]; ok {
			channel.DailyQuotaUsed = row.Used
			channel.DailyQuotaReserved = row.Reserved
		}
	}
	return nil
}

// FilterChannelsByDailyQuota removes channels whose configured daily budget is
// already consumed or fully reserved. The query is intentionally outside the
// channel-cache lock; selection can be hot while the ledger is durable state.
func FilterChannelsByDailyQuota(channels []*Channel) ([]*Channel, error) {
	if len(channels) == 0 {
		return channels, nil
	}
	channelIDs := make([]int, 0, len(channels))
	for _, channel := range channels {
		if channel != nil && GetChannelDailyQuotaLimit(channel) > 0 {
			channelIDs = append(channelIDs, channel.Id)
		}
	}
	byChannel, err := getChannelDailyQuotaRows(channelIDs, channelQuotaDay())
	if err != nil {
		return nil, err
	}
	available := make([]*Channel, 0, len(channels))
	for _, channel := range channels {
		limit := GetChannelDailyQuotaLimit(channel)
		row := byChannel[channel.Id]
		if limit <= 0 || row.Used+row.Reserved < limit {
			available = append(available, channel)
		}
	}
	return available, nil
}

func getChannelDailyQuotaRows(channelIDs []int, day string) (map[int]ChannelDailyQuota, error) {
	rowsByChannel := make(map[int]ChannelDailyQuota)
	if len(channelIDs) == 0 {
		return rowsByChannel, nil
	}
	var rows []ChannelDailyQuota
	if err := DB.Where("day = ? AND channel_id IN ?", day, channelIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		rowsByChannel[row.ChannelId] = row
	}
	return rowsByChannel, nil
}

// ReserveChannelDailyQuota atomically reserves the maximum quota of one
// upstream attempt. A false result is an ordinary capacity miss, not an error.
func ReserveChannelDailyQuota(channel *Channel, quota int64) (string, bool, error) {
	limit := GetChannelDailyQuotaLimit(channel)
	if limit <= 0 || quota <= 0 {
		return "", false, nil
	}
	if quota > limit {
		return "", false, nil
	}

	day := channelQuotaDay()
	row := ChannelDailyQuota{ChannelId: channel.Id, Day: day}
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return "", false, err
	}

	result := DB.Model(&ChannelDailyQuota{}).
		Where("channel_id = ? AND day = ? AND used + reserved + ? <= ?", channel.Id, day, quota, limit).
		Update("reserved", gorm.Expr("reserved + ?", quota))
	if result.Error != nil {
		return "", false, result.Error
	}
	return day, result.RowsAffected == 1, nil
}

// ReleaseChannelDailyQuotaReservation returns a failed attempt's reservation.
// It is idempotent for callers that clear their reservation after release.
func ReleaseChannelDailyQuotaReservation(channelID int, day string, quota int64) error {
	if channelID <= 0 || day == "" || quota <= 0 {
		return nil
	}
	result := DB.Model(&ChannelDailyQuota{}).
		Where("channel_id = ? AND day = ?", channelID, day).
		Update("reserved", gorm.Expr("CASE WHEN reserved >= ? THEN reserved - ? ELSE 0 END", quota, quota))
	return result.Error
}

// RecordChannelDailyQuotaUsage atomically upserts actual channel spend for
// every channel. Reservations still exist only for configured limits, but
// unrestricted channels must also retain a daily ledger row for reporting.
func RecordChannelDailyQuotaUsage(channelID int, quota int64) error {
	if channelID <= 0 || quota == 0 {
		return nil
	}
	row := ChannelDailyQuota{
		ChannelId: channelID,
		Day:       channelQuotaDay(),
		Used:      quota,
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "channel_id"},
			{Name: "day"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"used": gorm.Expr("used + ?", quota),
		}),
	}).Create(&row).Error
}

func GetChannelDailyQuota(channelID int, day string) (*ChannelDailyQuota, error) {
	if day == "" {
		day = channelQuotaDay()
	}
	row := &ChannelDailyQuota{}
	err := DB.Where("channel_id = ? AND day = ?", channelID, day).First(row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}
