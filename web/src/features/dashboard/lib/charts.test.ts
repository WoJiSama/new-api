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

import type { QuotaDataItem } from '../types'
import { processChartData } from './charts'

test('uses raw token usage for token distribution charts', () => {
  const data: QuotaDataItem[] = [
    {
      model_name: 'alpha',
      created_at: 1_720_000_000,
      quota: 500_000,
      token_used: 1_200,
      count: 1,
    },
    {
      model_name: 'beta',
      created_at: 1_720_000_000,
      quota: 1_000_000,
      token_used: 800,
      count: 1,
    },
  ]

  const chart = processChartData(data, 'day', undefined, undefined, 'tokens')
  const values = chart.spec_line.data[0].values as Array<{
    Model: string
    rawValue: number
    Usage: number
  }>
  const nonZeroValues = values
    .filter((value) => value.rawValue > 0)
    .map((value) => [value.Model, value.rawValue, value.Usage])

  assert.deepEqual(nonZeroValues, [
    ['alpha', 1_200, 1_200],
    ['beta', 800, 800],
  ])
  assert.equal(chart.totalTokensDisplay.replaceAll(/[^0-9]/g, ''), '2000')
})
