import React from 'react'
import { HardDrive, PackageCheck, Zap, Activity, Trash2 } from 'lucide-react'
import { RuntimeStatus, CacheStats } from '../../types'
import { useI18n } from '../../i18n'
import { Card, CardHeader, InsetPanel, CompoundInputGroup } from '../common/Card'
import { Badge } from '../common/Badge'
import { Button } from '../common/Button'
import { SettingToggleRow, ToggleSwitch } from '../common/ToggleSwitch'
import { InfoTip } from '../common/InfoTip'

export interface CacheCardProps {
  isAndroid: boolean
  status: RuntimeStatus | null
  cacheStats: CacheStats | null
  loadingAction: string | null
  loadingBrowse: boolean
  loadingOpenFolder: boolean
  cacheDirInput: string
  setCacheDirInput: (val: string) => void
  ramMbInput: string
  setRamMbInput: (val: string) => void
  ramUsageMb: number
  ramMaxMb: number
  isRamCache: boolean
  isRamWarmup: boolean
  isAutoRepair: boolean
  onSaveCacheDir: () => void
  onBrowseDir: () => void
  onOpenCacheFolder: () => void
  onDetectAcgp: () => void
  onOpenMigrateStandard: () => void
  onToggleRamCache: () => void
  onApplyRamMb: () => void
  onOpenBoost: () => void
  onToggleRamWarmup: () => void
  onToggleAutoRepair: () => void
  onOpenAudit: () => void
  onOpenClearCache: () => void
}

export const CacheCard: React.FC<CacheCardProps> = ({
  isAndroid,
  status,
  cacheStats,
  loadingAction,
  loadingBrowse,
  loadingOpenFolder,
  cacheDirInput,
  setCacheDirInput,
  ramMbInput,
  setRamMbInput,
  ramUsageMb,
  ramMaxMb,
  isRamCache,
  isRamWarmup,
  isAutoRepair,
  onSaveCacheDir,
  onBrowseDir,
  onOpenCacheFolder,
  onDetectAcgp,
  onOpenMigrateStandard,
  onToggleRamCache,
  onApplyRamMb,
  onOpenBoost,
  onToggleRamWarmup,
  onToggleAutoRepair,
  onOpenAudit,
  onOpenClearCache,
}) => {
  const { t } = useI18n()
  const isActionLoading = (id: string) => loadingAction === id
  const ramPct = Math.min(
    100,
    Math.round(((cacheStats?.total_ram_mb ?? ramUsageMb) / Math.max(1, ramMaxMb)) * 100)
  )

  return (
    <Card>
      <div className="space-y-3.5">
        <CardHeader
          icon={<HardDrive className="w-[18px] h-[18px]" strokeWidth={1.85} />}
          title={t('cards.cache.title')}
          badge={<Badge variant="green">{t('cards.cache.badge')}</Badge>}
        />

        {/* Local Cache Dir */}
        <div className="space-y-2">
          <label className="text-[13px] sm:text-sm text-label-secondary font-medium flex items-center gap-1.5">
            <span>{isAndroid ? t('cards.cache.dirLabelAndroid') : t('cards.cache.dirLabelShort')}</span>
            {!isAndroid && <InfoTip text={t('cards.cache.dirLabelTip')} />}
          </label>
          <div className="space-y-2">
            <CompoundInputGroup>
              <input
                type="text"
                disabled={Boolean(loadingAction) || loadingBrowse}
                value={cacheDirInput}
                onChange={(e) => setCacheDirInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    onSaveCacheDir()
                  }
                }}
                className="flex-1 px-2.5 py-1 text-xs sm:text-[13px] font-mono"
              />
              <Button
                type="button"
                variant="primary"
                size="sm"
                onClick={onSaveCacheDir}
                disabled={Boolean(loadingAction) || loadingBrowse}
                aria-busy={isActionLoading('save-cache-dir')}
              >
                {isActionLoading('save-cache-dir') ? t('common.saving') : t('common.save')}
              </Button>
              {!isAndroid && (
                <Button
                  type="button"
                  variant="desktop"
                  size="sm"
                  onClick={onBrowseDir}
                  disabled={loadingBrowse}
                  aria-busy={loadingBrowse}
                >
                  {loadingBrowse ? t('common.loading') : t('common.browse')}
                </Button>
              )}
            </CompoundInputGroup>

            {!isAndroid && (
              <div className="flex items-center gap-2 flex-wrap">
                <Button
                  type="button"
                  variant="desktop"
                  size="sm"
                  onClick={onOpenCacheFolder}
                  disabled={loadingOpenFolder}
                >
                  {loadingOpenFolder ? t('common.loading') : t('common.openFolder')}
                </Button>
                <Button
                  type="button"
                  variant="desktop"
                  size="sm"
                  onClick={onDetectAcgp}
                  disabled={Boolean(loadingAction)}
                  aria-busy={isActionLoading('detect-acgp')}
                >
                  {isActionLoading('detect-acgp') ? t('common.loading') : t('cards.cache.detectAcgpower')}
                </Button>
                {status?.platform === 'darwin' && !status?.cache?.is_standard_dir && (
                  <button
                    type="button"
                    onClick={onOpenMigrateStandard}
                    disabled={Boolean(loadingAction)}
                    aria-busy={isActionLoading('migrate-standard')}
                    className="px-2.5 py-1 rounded-lg text-xs font-medium text-sys-amber bg-sys-amberBg hover:opacity-90 border border-amber-500/30 active:scale-[0.98] transition-all shrink-0 cursor-pointer flex items-center gap-1.5"
                    title={t('cards.cache.migrateStandardTooltip')}
                  >
                    <PackageCheck className="w-3.5 h-3.5 shrink-0" />
                    <span>
                      {isActionLoading('migrate-standard') ? t('cards.cache.migrating') : t('cards.cache.migrateStandard')}
                    </span>
                  </button>
                )}
              </div>
            )}
          </div>
        </div>

        {/* RAM Cache Setting */}
        <div className="pt-2 hairline-t">
          <SettingToggleRow
            label={t('cards.cache.ramCacheShort')}
            meta={
              <span className="text-[11px] font-mono tnum text-label-tertiary bg-surface-active px-1.5 py-0.5 rounded-full border border-hairline">
                {ramMaxMb}MB{ramMaxMb >= 1024 ? ` (${Math.round((ramMaxMb / 1024) * 10) / 10}GB)` : ''}
              </span>
            }
            tooltip={t('cards.cache.ramCacheTip', {
              maxMb: ramMaxMb,
              gbText: ramMaxMb >= 1024 ? ` (${Math.round((ramMaxMb / 1024) * 10) / 10}GB)` : '',
            })}
            checked={isRamCache}
            onChange={onToggleRamCache}
            disabled={Boolean(loadingAction)}
          >
            {isRamCache && (
              <InsetPanel className="mt-2 space-y-2.5">
                <div className="flex items-center gap-2 flex-wrap text-xs text-label-secondary">
                  <span>{t('cards.cache.limitMbLabel')}</span>
                  <CompoundInputGroup className="w-auto">
                    <input
                      type="text"
                      disabled={Boolean(loadingAction)}
                      value={ramMbInput}
                      onChange={(e) => setRamMbInput(e.target.value)}
                      className="w-16 px-2 py-0.5 text-xs font-mono text-right tnum"
                    />
                    <Button
                      type="button"
                      variant="desktop"
                      size="xs"
                      onClick={onApplyRamMb}
                      disabled={Boolean(loadingAction)}
                      aria-busy={isActionLoading('ram-mb')}
                    >
                      {isActionLoading('ram-mb') ? t('cards.cache.applying') : t('cards.cache.apply')}
                    </Button>
                  </CompoundInputGroup>

                  {(cacheStats?.resident_bytes ?? 0) > 0 ? (
                    <div className="flex items-center gap-2 text-xs font-mono tnum text-label-secondary flex-wrap">
                      <span>{t('cards.cache.regularLabel', { mb: ramUsageMb })}</span>
                      <span className="text-sys-amber font-medium">
                        {t('cards.cache.residentLabel', { mb: cacheStats?.resident_mb ?? 0 })}
                      </span>
                      <span className="font-semibold text-label-primary">
                        {t('cards.cache.usedLabel', {
                          used: cacheStats?.total_ram_mb ?? (ramUsageMb + (cacheStats?.resident_mb ?? 0)),
                          max: ramMaxMb,
                        })}
                      </span>
                    </div>
                  ) : (
                    <span className="text-label-tertiary font-mono text-xs tnum">
                      {ramUsageMb} / {ramMaxMb} MB ({Math.min(100, Math.round((ramUsageMb / Math.max(1, ramMaxMb)) * 100))}%)
                    </span>
                  )}

                  <div className="w-24 h-2 bg-slate-500/15 rounded-full overflow-hidden shrink-0">
                    <div
                      className="h-full bg-emerald-500 rounded-full transition-all duration-300"
                      style={{ width: `${ramPct}%` }}
                    />
                  </div>

                  <button
                    type="button"
                    onClick={onOpenBoost}
                    className={`px-2.5 py-1 rounded-lg text-xs font-medium border active:scale-[0.98] transition-all cursor-pointer flex items-center gap-1.5 shrink-0 ${
                      (cacheStats?.resident_bytes ?? 0) > 0
                        ? 'text-sys-green bg-sys-greenBg border-emerald-500/30 hover:opacity-90'
                        : cacheStats?.boost_state === 'loading'
                        ? 'text-sys-blue bg-sys-blueBg border-sky-500/30 animate-pulse'
                        : 'text-sys-amber bg-sys-amberBg border-amber-500/30 hover:opacity-90'
                    }`}
                    title={t('cards.cache.boostTooltip')}
                  >
                    <Zap className="w-3.5 h-3.5 shrink-0" />
                    <span>
                      {cacheStats?.boost_state === 'loading'
                        ? t('cards.cache.boostLoading')
                        : (cacheStats?.resident_bytes ?? 0) > 0
                        ? t('cards.cache.boostActiveWithMb', { mb: cacheStats?.resident_mb ?? 0 })
                        : t('cards.cache.boostBtn')}
                    </span>
                  </button>

                  {(cacheStats?.system_total_mb ?? 0) > 0 && (
                    <span className="text-label-tertiary text-xs font-mono tnum ml-auto sm:ml-0">
                      {t('cards.cache.hostRam', { gb: Math.round((cacheStats?.system_total_mb ?? 0) / 1024) })}
                    </span>
                  )}
                </div>

                <div className="pt-2 hairline-t flex items-center justify-between gap-4">
                  <div
                    className="text-xs sm:text-[13px] text-label-secondary cursor-pointer select-none leading-snug flex items-center gap-1.5 flex-wrap"
                    onClick={!loadingAction ? onToggleRamWarmup : undefined}
                  >
                    <span>{t('cards.cache.ramWarmupShort')}</span>
                    <InfoTip text={t('cards.cache.ramWarmupTip')} />
                  </div>
                  <ToggleSwitch
                    checked={isRamWarmup}
                    onChange={onToggleRamWarmup}
                    disabled={Boolean(loadingAction)}
                    ariaLabel={t('cards.cache.ramWarmupShort')}
                  />
                </div>
              </InsetPanel>
            )}
          </SettingToggleRow>
        </div>

        {/* Cache Maintenance & Integrity */}
        <div className="pt-2 hairline-t space-y-2.5">
          <SettingToggleRow
            label={t('cards.cache.cleanZombiesShort')}
            tooltip={t('cards.cache.cleanZombiesTip')}
            checked={isAutoRepair}
            onChange={onToggleAutoRepair}
            disabled={Boolean(loadingAction)}
          />

          <div className="flex items-center gap-2 pt-0.5 flex-wrap">
            <Button
              type="button"
              variant="desktop"
              size="sm"
              icon={<Activity className="w-3.5 h-3.5" />}
              onClick={onOpenAudit}
            >
              {t('cards.cache.auditCache')}
            </Button>
            <Button
              type="button"
              variant="desktop"
              size="sm"
              icon={<Trash2 className="w-3.5 h-3.5" />}
              onClick={onOpenClearCache}
            >
              {t('cards.cache.clearCache')}
            </Button>
          </div>
        </div>
      </div>
    </Card>
  )
}
