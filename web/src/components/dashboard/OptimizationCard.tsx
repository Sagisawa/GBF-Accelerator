import React from 'react'
import { Gauge, ShieldCheck } from 'lucide-react'
import { useI18n } from '../../i18n'
import { Card, CardHeader, InsetPanel } from '../common/Card'
import { Badge } from '../common/Badge'
import { SettingToggleRow } from '../common/ToggleSwitch'

export interface OptimizationCardProps {
  loadingAction: string | null
  isPrefetch: boolean
  isBrowserCache: boolean
  onTogglePrefetch: () => void
  onToggleBrowserCache: () => void
}

export const OptimizationCard: React.FC<OptimizationCardProps> = ({
  loadingAction,
  isPrefetch,
  isBrowserCache,
  onTogglePrefetch,
  onToggleBrowserCache,
}) => {
  const { t } = useI18n()

  return (
    <Card>
      <div className="space-y-3.5">
        <CardHeader
          icon={<Gauge className="w-[18px] h-[18px]" strokeWidth={1.85} />}
          title={t('cards.optim.title')}
          badge={<Badge variant="amber">{t('cards.optim.badge')}</Badge>}
        />

        <div>
          <SettingToggleRow
            label={t('cards.optim.prefetch')}
            tooltip={t('cards.optim.prefetchDesc')}
            checked={isPrefetch}
            onChange={onTogglePrefetch}
            disabled={Boolean(loadingAction)}
          />

          <SettingToggleRow
            label={t('cards.optim.browserCache')}
            tooltip={t('cards.optim.browserCacheDesc')}
            checked={isBrowserCache}
            onChange={onToggleBrowserCache}
            disabled={Boolean(loadingAction)}
          />

          <InsetPanel tone="amber" className="mt-3.5 text-xs leading-relaxed space-y-1">
            <div className="font-semibold flex items-center gap-1.5 text-amber-800">
              <ShieldCheck className="w-4 h-4 text-amber-700 shrink-0" />
              <span>{t('cards.optim.subhead')}</span>
            </div>
            <p className="text-amber-800/90 text-[11px] sm:text-xs">
              {t('cards.optim.pacingNotice')}
            </p>
          </InsetPanel>
        </div>
      </div>
    </Card>
  )
}
