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
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { FlowQuotaDataItem } from '../types'
import { buildChannelDispatchData } from './channel-dispatch'

const rows: FlowQuotaDataItem[] = [
  {
    channel_id: 12,
    channel_name: 'primary',
    model_name: 'gpt-5',
    count: 4,
    token_used: 1_200,
    quota: 800_000,
  },
  {
    channel_id: 12,
    channel_name: 'primary',
    model_name: 'gpt-5-mini',
    count: 2,
    token_used: 300,
    quota: 200_000,
  },
  {
    channel_id: 19,
    channel_name: 'backup',
    model_name: 'gpt-5',
    count: 8,
    token_used: 500,
    quota: 100_000,
  },
]

test('aggregates successful channel dispatches by selected metric', () => {
  const result = buildChannelDispatchData(rows, 'tokens')

  assert.equal(result.total, 2_000)
  assert.deepEqual(result.items, [
    {
      id: 12,
      name: 'primary',
      value: 1_500,
      share: 0.75,
      requests: 6,
      tokens: 1_500,
      quota: 1_000_000,
      models: ['gpt-5', 'gpt-5-mini'],
    },
    {
      id: 19,
      name: 'backup',
      value: 500,
      share: 0.25,
      requests: 8,
      tokens: 500,
      quota: 100_000,
      models: ['gpt-5'],
    },
  ])
})
