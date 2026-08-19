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
import { useQuery } from '@tanstack/react-query'
import { Activity, CircleAlert, Hash, Route, WalletCards } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getFlowQuotaDates } from '@/features/dashboard/api'
import {
  buildChannelDispatchData,
  buildQueryParams,
  getDefaultDays,
} from '@/features/dashboard/lib'
import { requireSuccessfulFlowRows } from '@/features/dashboard/lib/flow-selection'
import type { DashboardFilters, FlowMetric } from '@/features/dashboard/types'
import { formatNumber, formatQuota } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
import { cn } from '@/lib/utils'

interface ChannelDispatchDashboardProps {
  filters?: DashboardFilters
}

const METRIC_OPTIONS = [
  { value: 'requests', labelKey: 'Requests', icon: Activity },
  { value: 'tokens', labelKey: 'Tokens', icon: Hash },
  { value: 'quota', labelKey: 'Quota', icon: WalletCards },
] as const

function formatMetricValue(metric: FlowMetric, value: number): string {
  return metric === 'quota' ? formatQuota(value) : formatNumber(value)
}

export function ChannelDispatchDashboard(props: ChannelDispatchDashboardProps) {
  const { t } = useTranslation()
  const [metric, setMetric] = useState<FlowMetric>('requests')
  const timeRange = useMemo(
    () =>
      computeTimeRange(
        getDefaultDays(props.filters?.time_granularity),
        props.filters?.start_timestamp,
        props.filters?.end_timestamp
      ),
    [
      props.filters?.end_timestamp,
      props.filters?.start_timestamp,
      props.filters?.time_granularity,
    ]
  )
  const queryParams = useMemo(
    () => buildQueryParams(timeRange, props.filters),
    [props.filters, timeRange]
  )
  const { data, error, isError, isLoading } = useQuery({
    queryKey: ['dashboard', 'channel-dispatch', queryParams],
    queryFn: () => getFlowQuotaDates(queryParams, true),
    select: (response) =>
      requireSuccessfulFlowRows(response, t('Please try again later.')),
    staleTime: 60_000,
  })
  const dispatch = useMemo(
    () => buildChannelDispatchData(data ?? [], metric),
    [data, metric]
  )
  const metricLabel = t(
    METRIC_OPTIONS.find((option) => option.value === metric)?.labelKey ??
      'Requests'
  )
  const errorMessage =
    error instanceof Error ? error.message : t('Please try again later.')

  if (isLoading) {
    return <Skeleton className='h-[32rem] w-full rounded-lg' />
  }

  if (isError) {
    return (
      <Alert variant='destructive'>
        <CircleAlert />
        <AlertTitle>{t('Failed to load')}</AlertTitle>
        <AlertDescription>{errorMessage}</AlertDescription>
      </Alert>
    )
  }

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex flex-col gap-3 border-b px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5'>
        <div className='flex items-center gap-2'>
          <IconBadge tone='success' size='sm'>
            <Route />
          </IconBadge>
          <div>
            <div className='text-sm font-semibold'>{t('Channel Dispatch')}</div>
            <div className='text-muted-foreground text-xs'>
              {t('Successful dispatches only')}
            </div>
          </div>
        </div>
        <Tabs
          value={metric}
          onValueChange={(value) => setMetric(value as FlowMetric)}
        >
          <TabsList aria-label={t('Channel Dispatch Metric')}>
            {METRIC_OPTIONS.map((option) => {
              const Icon = option.icon
              return (
                <TabsTrigger
                  key={option.value}
                  value={option.value}
                  className='gap-1.5 px-2.5 text-xs'
                >
                  <Icon data-icon='inline-start' aria-hidden='true' />
                  {t(option.labelKey)}
                </TabsTrigger>
              )
            })}
          </TabsList>
        </Tabs>
      </div>

      {dispatch.items.length === 0 ? (
        <div className='flex min-h-72 flex-col items-center justify-center gap-2 p-6 text-center'>
          <IconBadge tone='neutral' size='lg'>
            <Route />
          </IconBadge>
          <div className='text-sm font-medium'>{t('No data available')}</div>
        </div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Channel')}</TableHead>
              <TableHead className='text-right'>{metricLabel}</TableHead>
              <TableHead className='w-[22%]'>{t('Share')}</TableHead>
              <TableHead className='text-right'>{t('Requests')}</TableHead>
              <TableHead className='text-right'>{t('Tokens')}</TableHead>
              <TableHead className='text-right'>{t('Quota')}</TableHead>
              <TableHead>{t('Models')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {dispatch.items.map((channel) => (
              <TableRow key={`${channel.id}:${channel.name}`}>
                <TableCell className='font-medium'>
                  <div className='flex items-center gap-2'>
                    <span className='text-muted-foreground'>#{channel.id}</span>
                    <span className='max-w-48 truncate'>{channel.name}</span>
                  </div>
                </TableCell>
                <TableCell className='text-right font-medium'>
                  {formatMetricValue(metric, channel.value)}
                </TableCell>
                <TableCell>
                  <div className='flex min-w-36 items-center gap-2'>
                    <div className='bg-muted h-1.5 min-w-0 flex-1 overflow-hidden rounded-full'>
                      <div
                        className='bg-primary h-full rounded-full'
                        style={{
                          width: `${Math.max(channel.share * 100, 1)}%`,
                        }}
                      />
                    </div>
                    <span className='text-muted-foreground w-12 text-right text-xs'>
                      {formatNumber(channel.share * 100)}%
                    </span>
                  </div>
                </TableCell>
                <TableCell className='text-right'>
                  {formatNumber(channel.requests)}
                </TableCell>
                <TableCell className='text-right'>
                  {formatNumber(channel.tokens)}
                </TableCell>
                <TableCell className='text-right'>
                  {formatQuota(channel.quota)}
                </TableCell>
                <TableCell>
                  <span
                    className={cn(
                      'block max-w-72 truncate text-muted-foreground'
                    )}
                    title={channel.models.join(', ')}
                  >
                    {channel.models.join(', ')}
                  </span>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
