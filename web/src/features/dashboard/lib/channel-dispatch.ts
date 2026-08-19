/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type {
  ChannelDispatchData,
  FlowMetric,
  FlowQuotaDataItem,
} from '@/features/dashboard/types'

function numberValue(value: unknown): number {
  const numeric = Number(value)
  return Number.isFinite(numeric) ? numeric : 0
}

function metricValue(
  metric: FlowMetric,
  row: Pick<FlowQuotaDataItem, 'quota' | 'token_used' | 'count'>
): number {
  if (metric === 'tokens') return numberValue(row.token_used)
  if (metric === 'requests') return numberValue(row.count)
  return numberValue(row.quota)
}

function channelMetricValue(
  metric: FlowMetric,
  channel: { quota: number; tokens: number; requests: number }
): number {
  if (metric === 'tokens') return channel.tokens
  if (metric === 'requests') return channel.requests
  return channel.quota
}

export function buildChannelDispatchData(
  rows: FlowQuotaDataItem[],
  metric: FlowMetric
): ChannelDispatchData {
  const byChannel = new Map<
    string,
    {
      id: number
      name: string
      requests: number
      tokens: number
      quota: number
      models: Set<string>
    }
  >()

  for (const row of rows) {
    const id = Math.trunc(numberValue(row.channel_id))
    const configuredName = row.channel_name?.trim()
    const key = id > 0 ? `id:${id}` : `name:${configuredName || 'unknown'}`
    const name = configuredName || (id > 0 ? `#${id}` : 'Unknown channel')
    const existing = byChannel.get(key) ?? {
      id,
      name,
      requests: 0,
      tokens: 0,
      quota: 0,
      models: new Set<string>(),
    }

    existing.requests += numberValue(row.count)
    existing.tokens += numberValue(row.token_used)
    existing.quota += numberValue(row.quota)
    if (row.model_name?.trim()) existing.models.add(row.model_name.trim())
    byChannel.set(key, existing)
  }

  const total = rows.reduce((sum, row) => sum + metricValue(metric, row), 0)
  const items = [...byChannel.values()]
    .map((channel) => {
      const value = channelMetricValue(metric, channel)
      return {
        ...channel,
        value,
        share: total > 0 ? value / total : 0,
        models: [...channel.models].sort(),
      }
    })
    .sort(
      (left, right) =>
        right.value - left.value || left.name.localeCompare(right.name)
    )

  return { items, total }
}
