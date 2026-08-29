package model

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
// channel2advancedCustomConfig caches parsed Advanced Custom (type 58) configs so
// path-aware selection avoids re-parsing JSON per request. Refreshed on full sync.
var channel2advancedCustomConfig map[int]*dto.AdvancedCustomConfig
var channelSyncLock sync.RWMutex

func InitChannelCache() {
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		return
	}
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*dto.AdvancedCustomConfig)
	var channels []*Channel
	DB.Find(&channels)
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	var abilities []*Ability
	DB.Find(&abilities)
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]int)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]int)
	}
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue // skip disabled channels
		}
		groups := strings.Split(channel.Group, ",")
		for _, group := range groups {
			models := strings.Split(channel.Models, ",")
			for _, model := range models {
				if _, ok := newGroup2model2channels[group][model]; !ok {
					newGroup2model2channels[group][model] = make([]int, 0)
				}
				newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], channel.Id)
			}
		}
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				if oldChannel, ok := channelsIDM[i]; ok {
					// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
					if oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
						channel.ChannelInfo.MultiKeyPollingIndex = oldChannel.ChannelInfo.MultiKeyPollingIndex
					}
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	channelSyncLock.Unlock()
	// Lock ordering: InvalidatePricingCache acquires updatePricingLock, and
	// GetPricing (holding updatePricingLock) nests channelSyncLock.RLock via
	// loadPricingAdvancedCustomConfigs. channelSyncLock MUST be released before
	// invalidating the pricing cache, otherwise the reversed order deadlocks.
	InvalidatePricingCache()
	common.SysLog("channels synced from database")
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog("syncing channels from database")
		InitChannelCache()
	}
}

type ChannelSelectionOptions struct {
	Retry              int
	RequestPath        string
	PreviousPriority   *int64
	ExcludedChannelIDs map[int]struct{}
	ProtocolSensitive  bool // tool-call conversation; disallow protocol conversion
}

func GetRandomSatisfiedChannel(group string, model string, retry int, requestPath string) (*Channel, error) {
	return GetRandomSatisfiedChannelWithOptions(group, model, ChannelSelectionOptions{
		Retry:       retry,
		RequestPath: requestPath,
	})
}

func GetRandomSatisfiedChannelWithOptions(group string, model string, options ChannelSelectionOptions) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannelWithOptions(group, model, options)
	}

	channelSyncLock.RLock()
	// First, try to find channels with the exact model name.
	channelIDs := filterChannelsByRequestPathAndModel(group2model2channels[group][model], options.RequestPath, model, options.ProtocolSensitive)

	// If no channels found, try to find channels with the normalized model name.
	if len(channelIDs) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channelIDs = filterChannelsByRequestPathAndModel(group2model2channels[group][normalizedModel], options.RequestPath, model, options.ProtocolSensitive)
	}

	if len(channelIDs) == 0 {
		channelSyncLock.RUnlock()
		return nil, nil
	}

	// Copy channel pointers while holding the cache lock, then consult the
	// database-backed daily ledger after releasing it.
	channels := make([]*Channel, 0, len(channelIDs))
	for _, channelID := range channelIDs {
		channel, ok := channelsIDM[channelID]
		if !ok {
			channelSyncLock.RUnlock()
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelID)
		}
		channels = append(channels, channel)
	}
	channelSyncLock.RUnlock()

	var err error
	channels, err = FilterChannelsByDailyQuota(channels)
	if err != nil {
		return nil, fmt.Errorf("查询渠道每日额度失败: %w", err)
	}
	if len(channels) == 0 {
		return nil, nil
	}
	return selectChannelWithOptions(channels, options)
}

func selectChannelWithOptions(channels []*Channel, options ChannelSelectionOptions) (*Channel, error) {
	candidates := make([]*Channel, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		if _, excluded := options.ExcludedChannelIDs[channel.Id]; excluded {
			continue
		}
		candidates = append(candidates, channel)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	if options.PreviousPriority != nil {
		samePriority := make([]*Channel, 0, len(candidates))
		for _, channel := range candidates {
			if channel.GetPriority() == *options.PreviousPriority && channel.GetOtherSettings().SamePriorityRetryRPMLimit > 0 {
				samePriority = append(samePriority, channel)
			}
		}
		if len(samePriority) > 0 {
			return chooseWeightedChannel(samePriority)
		}

		var nextPriority *int64
		for _, channel := range candidates {
			priority := channel.GetPriority()
			if priority < *options.PreviousPriority && (nextPriority == nil || priority > *nextPriority) {
				priorityCopy := priority
				nextPriority = &priorityCopy
			}
		}
		if nextPriority == nil {
			return nil, nil
		}
		lowerPriority := make([]*Channel, 0, len(candidates))
		for _, channel := range candidates {
			if channel.GetPriority() == *nextPriority {
				lowerPriority = append(lowerPriority, channel)
			}
		}
		return chooseWeightedChannel(lowerPriority)
	}

	uniquePriorities := make(map[int64]struct{})
	for _, channel := range candidates {
		uniquePriorities[channel.GetPriority()] = struct{}{}
	}
	sortedPriorities := make([]int64, 0, len(uniquePriorities))
	for priority := range uniquePriorities {
		sortedPriorities = append(sortedPriorities, priority)
	}
	sort.Slice(sortedPriorities, func(i, j int) bool { return sortedPriorities[i] > sortedPriorities[j] })

	retry := options.Retry
	if retry >= len(sortedPriorities) {
		retry = len(sortedPriorities) - 1
	}
	targetPriority := sortedPriorities[retry]
	targetChannels := make([]*Channel, 0, len(candidates))
	for _, channel := range candidates {
		if channel.GetPriority() == targetPriority {
			targetChannels = append(targetChannels, channel)
		}
	}
	return chooseWeightedChannel(targetChannels)
}

func chooseWeightedChannel(channels []*Channel) (*Channel, error) {
	if len(channels) == 0 {
		return nil, nil
	}
	if len(channels) == 1 {
		return channels[0], nil
	}

	sumWeight := 0
	for _, channel := range channels {
		sumWeight += channel.GetWeight()
	}
	smoothingFactor := 1
	smoothingAdjustment := 0
	if sumWeight == 0 {
		sumWeight = len(channels) * 100
		smoothingAdjustment = 100
	} else if sumWeight/len(channels) < 10 {
		smoothingFactor = 100
	}

	randomWeight := rand.Intn(sumWeight * smoothingFactor)
	for _, channel := range channels {
		randomWeight -= channel.GetWeight()*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return channel, nil
		}
	}
	return nil, errors.New("channel not found")
}

// filterChannelsByRequestPathAndModel restricts candidates by request path and
// model. Only Advanced Custom (type 58) channels are path-checked: they are kept
// only when one of their configured routes matches requestPath and model. All
// other channel types always pass. When requestPath is empty, filtering is skipped.
// Caller must hold channelSyncLock (read lock). The cached slice is never mutated.
func filterChannelsByRequestPathAndModel(channels []int, requestPath string, model string, protocolSensitive ...bool) []int {
	if requestPath == "" || len(channels) == 0 {
		return channels
	}
	filtered := make([]int, 0, len(channels))
	preferNativeResponses := strings.HasPrefix(requestPath, "/v1/responses")
	sensitive := len(protocolSensitive) > 0 && protocolSensitive[0]
	nativeResponses := make([]int, 0, len(channels))
	for _, channelId := range channels {
		channel, ok := channelsIDM[channelId]
		if !ok {
			// keep it so the downstream consistency error is raised as before
			filtered = append(filtered, channelId)
			continue
		}
		if channel.Type != constant.ChannelTypeAdvancedCustom {
			if channel.Type == constant.ChannelTypeCodex && strings.HasPrefix(requestPath, "/v1/chat/completions") && !channel.GetSetting().ChatCompletionsToResponses {
				continue
			}
			if preferNativeResponses && !channel.GetSetting().ResponsesToChatCompletions {
				nativeResponses = append(nativeResponses, channelId)
			} else if !sensitive || !channel.GetSetting().ResponsesToChatCompletions {
				filtered = append(filtered, channelId)
			}
			continue
		}
		if config := channel2advancedCustomConfig[channelId]; config != nil && config.SupportsPathForModel(requestPath, model) {
			if sensitive {
				route, ok := config.MatchPathForModel(requestPath, model)
				if ok && route.Converter == "openai_responses_to_openai_chat_completions" {
					continue
				}
			}
			filtered = append(filtered, channelId)
		}
	}
	if preferNativeResponses && len(nativeResponses) > 0 {
		return nativeResponses
	}
	return filtered
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return c, nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return &c.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channel, ok := channelsIDM[id]; ok {
		channel.Status = status
	}
	if status != common.ChannelStatusEnabled {
		// delete the channel from group2model2channels
		for group, model2channels := range group2model2channels {
			for model, channels := range model2channels {
				for i, channelId := range channels {
					if channelId == id {
						// remove the channel from the slice
						group2model2channels[group][model] = append(channels[:i], channels[i+1:]...)
						break
					}
				}
			}
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	if channel == nil {
		channelSyncLock.Unlock()
		return
	}

	if channelsIDM == nil {
		channelsIDM = make(map[int]*Channel)
	}
	if oldChannel, ok := channelsIDM[channel.Id]; ok {
		logger.LogDebug(nil, "CacheUpdateChannel before: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, oldChannel.ChannelInfo.MultiKeyPollingIndex)
	}
	channelsIDM[channel.Id] = channel
	if channel2advancedCustomConfig == nil {
		channel2advancedCustomConfig = make(map[int]*dto.AdvancedCustomConfig)
	}
	delete(channel2advancedCustomConfig, channel.Id)
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			channel2advancedCustomConfig[channel.Id] = config
		}
	}
	logger.LogDebug(nil, "CacheUpdateChannel after: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)
	// Lock ordering: do NOT hold channelSyncLock while calling
	// InvalidatePricingCache. GetPricing acquires updatePricingLock first and then
	// channelSyncLock.RLock (via loadPricingAdvancedCustomConfigs); acquiring
	// updatePricingLock while holding channelSyncLock would be an AB-BA deadlock.
	channelSyncLock.Unlock()
	InvalidatePricingCache()
}
