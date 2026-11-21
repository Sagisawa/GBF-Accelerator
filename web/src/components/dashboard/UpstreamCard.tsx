import React from 'react'
import { Globe, QrCode } from 'lucide-react'
import { RuntimeStatus, UpstreamRuntimeStatus } from '../../types'
import { useI18n } from '../../i18n'
import { Card, CardHeader, InsetPanel, CompoundInputGroup } from '../common/Card'
import { Badge } from '../common/Badge'
import { Button } from '../common/Button'
import { SettingToggleRow, ToggleSwitch } from '../common/ToggleSwitch'
import { InfoTip } from '../common/InfoTip'

export interface UpstreamCardProps {
  status: RuntimeStatus | null
  upstreamRuntime: UpstreamRuntimeStatus | null
  language: 'zh' | 'en' | 'ja'
  loadingAction: string | null
  isDirect: boolean
  isShimakaze: boolean
  isAllowLan: boolean
  currentListenPort: number | string
  upstreamInput: string
  setUpstreamInput: (val: string) => void
  backupUpstreamInput: string
  setBackupUpstreamInput: (val: string) => void
  failoverEnabledInput: boolean
  failoverThresholdInput: string
  setFailoverThresholdInput: (val: string) => void
  failoverConsecutiveInput: string
  setFailoverConsecutiveInput: (val: string) => void
  failoverCooldownInput: string
  setFailoverCooldownInput: (val: string) => void
  failoverAutoRecoverInput: boolean
  setFailoverAutoRecoverInput: (val: boolean) => void
  failoverNotificationInput: boolean
  setFailoverNotificationInput: (val: boolean) => void
  portInput: string
  setPortInput: (val: string) => void
  onSaveUpstream: () => void
  onProbeUpstream: () => void
  onToggleFailoverEnabled: () => void
  onProbeBackupUpstream: () => void
  onSaveFailover: () => void
  onToggleDirect: () => void
  onToggleShimakaze: () => void
  onSavePort: () => void
  onResetPort: () => void
  onToggleAllowLan: () => void
  onOpenMobileGuide: () => void
}

export const UpstreamCard: React.FC<UpstreamCardProps> = ({
  status,
  upstreamRuntime,
  language,
  loadingAction,
  isDirect,
  isShimakaze,
  isAllowLan,
  currentListenPort,
  upstreamInput,
  setUpstreamInput,
  backupUpstreamInput,
  setBackupUpstreamInput,
  failoverEnabledInput,
  failoverThresholdInput,
  setFailoverThresholdInput,
  failoverConsecutiveInput,
  setFailoverConsecutiveInput,
  failoverCooldownInput,
  setFailoverCooldownInput,
  failoverAutoRecoverInput,
  setFailoverAutoRecoverInput,
  failoverNotificationInput,
  setFailoverNotificationInput,
  portInput,
  setPortInput,
  onSaveUpstream,
  onProbeUpstream,
  onToggleFailoverEnabled,
  onProbeBackupUpstream,
  onSaveFailover,
  onToggleDirect,
  onToggleShimakaze,
  onSavePort,
  onResetPort,
  onToggleAllowLan,
  onOpenMobileGuide,
}) => {
  const { t } = useI18n()
  const isActionLoading = (id: string) => loadingAction === id

  const failoverBadgeVariant =
    isDirect || !failoverEnabledInput || !backupUpstreamInput.trim()
      ? 'neutral'
      : upstreamRuntime?.active === 'backup'
      ? 'amber'
      : 'green'

  const failoverBadgeText = isDirect
    ? t('cards.upstream.failoverDisabledDirect')
    : !failoverEnabledInput || !backupUpstreamInput.trim()
    ? t('cards.upstream.failoverNotEnabled')
    : upstreamRuntime?.active === 'backup'
    ? t('cards.upstream.activeBackup')
    : t('cards.upstream.activePrimary')

  return (
    <Card>
      <div className="space-y-3.5">
        <CardHeader
          icon={<Globe className="w-[18px] h-[18px]" strokeWidth={1.85} />}
          title={t('cards.upstream.title')}
          badge={<Badge variant="blue">{t('cards.upstream.badge')}</Badge>}
        />

        {/* Upstream Proxy */}
        <div className="space-y-1.5">
          <label className="text-[13px] sm:text-sm text-label-secondary font-medium block">
            {t('cards.upstream.upstreamLabel')}
          </label>
          <CompoundInputGroup>
            <input
              type="text"
              disabled={isDirect || Boolean(loadingAction)}
              value={upstreamInput}
              onChange={(e) => setUpstreamInput(e.target.value)}
              className="flex-1 px-2.5 py-1 text-xs sm:text-[13px] font-mono"
            />
            <Button
              type="button"
              variant="desktop"
              size="sm"
              disabled={isDirect || Boolean(loadingAction)}
              onClick={onSaveUpstream}
            >
              {isActionLoading('save-upstream') ? t('common.saving') : t('common.save')}
            </Button>
            <Button
              type="button"
              variant="desktop"
              size="sm"
              disabled={isDirect || Boolean(loadingAction)}
              aria-busy={isActionLoading('probe-upstream')}
              onClick={onProbeUpstream}
            >
              {isActionLoading('probe-upstream') ? t('common.loading') : t('cards.upstream.autoDetect')}
            </Button>
          </CompoundInputGroup>
        </div>

        {/* Upstream Failover */}
        <div className="space-y-2.5 pt-2 hairline-t">
          <div className="flex items-center justify-between gap-2 flex-wrap">
            <div className="text-[13px] sm:text-sm text-label-primary font-medium flex items-center gap-1.5">
              <span>{t('cards.upstream.failoverTitle')}</span>
              <InfoTip text={t('cards.upstream.failoverSubtitle')} />
            </div>
            <Badge variant={failoverBadgeVariant}>{failoverBadgeText}</Badge>
          </div>

          <div className="flex items-center justify-between gap-3 py-1">
            <div
              className={`text-[13px] sm:text-sm font-medium ${
                isDirect ? 'text-label-tertiary cursor-not-allowed' : 'text-label-primary cursor-pointer select-none'
              } leading-snug`}
              onClick={!isDirect && !loadingAction ? onToggleFailoverEnabled : undefined}
            >
              <span>{t('cards.upstream.enableFailover')}</span>
              {isDirect && (
                <span className="text-xs text-label-tertiary ml-1.5 font-normal">
                  {t('cards.upstream.failoverDirectNote')}
                </span>
              )}
            </div>
            <ToggleSwitch
              checked={!isDirect && failoverEnabledInput}
              onChange={onToggleFailoverEnabled}
              disabled={isDirect || Boolean(loadingAction)}
              ariaLabel={t('cards.upstream.enableFailover')}
            />
          </div>

          {!isDirect && failoverEnabledInput && (
            <div className="space-y-2.5 pt-0.5">
              {/* Backup upstream input + probe button */}
              <div className="space-y-1.5">
                <div className="flex items-center gap-2">
                  <label className="text-xs sm:text-[13px] text-label-secondary shrink-0">
                    {t('cards.upstream.backupAddress')}
                  </label>
                  <CompoundInputGroup className="flex-1 min-w-0">
                    <input
                      type="text"
                      value={backupUpstreamInput}
                      onChange={(e) => setBackupUpstreamInput(e.target.value)}
                      disabled={Boolean(loadingAction)}
                      placeholder={t('cards.upstream.backupPlaceholder')}
                      className="flex-1 px-2.5 py-1 text-xs sm:text-[13px] font-mono"
                    />
                    <Button
                      type="button"
                      variant="desktop"
                      size="sm"
                      disabled={Boolean(loadingAction)}
                      aria-busy={isActionLoading('probe-backup-upstream')}
                      onClick={onProbeBackupUpstream}
                    >
                      {isActionLoading('probe-backup-upstream')
                        ? t('cards.upstream.backupProbing')
                        : t('cards.upstream.backupAutoDetect')}
                    </Button>
                  </CompoundInputGroup>
                </div>
                <div className="text-[11px] sm:text-xs text-label-tertiary leading-relaxed">
                  {t('cards.upstream.backupTip')}
                </div>
              </div>

              {/* 3 Parameter Inputs */}
              <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
                <InsetPanel className="p-2.5 flex flex-col gap-1">
                  <span className="text-[11px] font-medium text-label-tertiary whitespace-nowrap" title={t('cards.upstream.thresholdTip')}>
                    {t('cards.upstream.thresholdLabel')}
                  </span>
                  <div className="flex items-center gap-1.5 mt-0.5">
                    <input
                      type="number"
                      min={0}
                      max={60000}
                      step={100}
                      value={failoverThresholdInput}
                      onChange={(e) => setFailoverThresholdInput(e.target.value)}
                      disabled={Boolean(loadingAction)}
                      className="ui-input w-full min-w-0 rounded-lg px-2 py-1 text-right font-mono tnum text-xs"
                    />
                    <span className="text-xs text-label-tertiary shrink-0 font-mono">ms</span>
                  </div>
                </InsetPanel>

                <InsetPanel className="p-2.5 flex flex-col gap-1">
                  <span className="text-[11px] font-medium text-label-tertiary whitespace-nowrap" title={t('cards.upstream.failuresTip')}>
                    {t('cards.upstream.failuresLabel')}
                  </span>
                  <div className="flex items-center gap-1.5 mt-0.5">
                    <input
                      type="number"
                      min={1}
                      max={10}
                      step={1}
                      value={failoverConsecutiveInput}
                      onChange={(e) => setFailoverConsecutiveInput(e.target.value)}
                      disabled={Boolean(loadingAction)}
                      className="ui-input w-full min-w-0 rounded-lg px-2 py-1 text-right font-mono tnum text-xs"
                    />
                    <span className="text-xs text-label-tertiary shrink-0">{t('cards.upstream.times')}</span>
                  </div>
                </InsetPanel>

                <InsetPanel className="p-2.5 flex flex-col gap-1">
                  <span className="text-[11px] font-medium text-label-tertiary whitespace-nowrap">
                    {t('cards.upstream.cooldownLabel')}
                  </span>
                  <div className="flex items-center gap-1.5 mt-0.5">
                    <input
                      type="number"
                      min={1}
                      max={3600}
                      step={10}
                      value={failoverCooldownInput}
                      onChange={(e) => setFailoverCooldownInput(e.target.value)}
                      disabled={Boolean(loadingAction)}
                      className="ui-input w-full min-w-0 rounded-lg px-2 py-1 text-right font-mono tnum text-xs"
                    />
                    <span className="text-xs text-label-tertiary shrink-0">{t('cards.upstream.seconds')}</span>
                  </div>
                </InsetPanel>
              </div>

              <div className="text-[11px] text-label-tertiary leading-relaxed px-0.5">
                {t('cards.upstream.failoverMechanismNotice')}
              </div>

              <div className="flex items-center justify-between gap-2 flex-wrap pt-0.5">
                <div className="flex flex-col gap-2 min-w-[220px]">
                  <div className="flex items-center justify-between gap-3">
                    <span
                      className="text-xs sm:text-[13px] text-label-secondary cursor-pointer select-none"
                      onClick={!loadingAction ? () => setFailoverAutoRecoverInput(!failoverAutoRecoverInput) : undefined}
                    >
                      {t('cards.upstream.autoRecoverCheckbox')}
                    </span>
                    <ToggleSwitch
                      checked={failoverAutoRecoverInput}
                      onChange={() => setFailoverAutoRecoverInput(!failoverAutoRecoverInput)}
                      disabled={Boolean(loadingAction)}
                      ariaLabel={t('cards.upstream.autoRecoverCheckbox')}
                    />
                  </div>

                  {status?.platform === 'windows' && (
                    <div className="flex items-center justify-between gap-3">
                      <span
                        className="text-xs sm:text-[13px] text-label-secondary cursor-pointer select-none"
                        onClick={!loadingAction ? () => setFailoverNotificationInput(!failoverNotificationInput) : undefined}
                      >
                        {t('cards.upstream.notificationCheckbox')}
                      </span>
                      <ToggleSwitch
                        checked={failoverNotificationInput}
                        onChange={() => setFailoverNotificationInput(!failoverNotificationInput)}
                        disabled={Boolean(loadingAction)}
                        ariaLabel={t('cards.upstream.notificationCheckbox')}
                      />
                    </div>
                  )}
                </div>

                <Button
                  type="button"
                  variant="primary"
                  size="sm"
                  onClick={onSaveFailover}
                  disabled={Boolean(loadingAction)}
                  aria-busy={isActionLoading('save-failover')}
                >
                  {isActionLoading('save-failover') ? t('common.saving') : t('cards.upstream.saveFailoverBtn')}
                </Button>
              </div>

              {backupUpstreamInput.trim() && !isDirect && (
                <InsetPanel className="p-2.5 text-xs text-label-secondary">
                  <div className="flex items-center justify-between gap-2 flex-wrap">
                    <span>
                      {t('cards.upstream.currentRoute')}
                      <strong className={upstreamRuntime?.active === 'backup' ? 'text-sys-amber' : 'text-sys-green'}>
                        {upstreamRuntime?.active === 'backup'
                          ? t('cards.upstream.backupRoute')
                          : t('cards.upstream.primaryRoute')}
                      </strong>
                    </span>
                    <span className="font-mono tnum text-label-tertiary">
                      {t('cards.upstream.consecutiveAbnormal')} {upstreamRuntime?.failure_count ?? 0} /{' '}
                      {upstreamRuntime?.consecutive_failures ?? (Number(failoverConsecutiveInput) || 3)}
                    </span>
                  </div>
                  <div className="mt-1.5 flex items-center justify-between gap-2 flex-wrap">
                    <span className="text-label-tertiary">
                      {upstreamRuntime?.reason
                        ? ({
                            timeout: t('cards.upstream.reasonTimeout'),
                            connection_error: t('cards.upstream.reasonConnError'),
                            latency_threshold: t('cards.upstream.reasonLatency'),
                            primary_recovered: t('cards.upstream.reasonRecovered'),
                            primary_recovery_failed: t('cards.upstream.reasonRecoveryFailed'),
                          } as Record<string, string>)[upstreamRuntime.reason] || upstreamRuntime.reason
                        : t('cards.upstream.waitingTelemetry')}
                    </span>
                    <span className="font-mono tnum text-label-tertiary">
                      {upstreamRuntime?.last_switch_at
                        ? t('cards.upstream.lastSwitch', {
                            time: new Date(upstreamRuntime.last_switch_at).toLocaleTimeString(
                              language === 'zh' ? 'zh-CN' : language === 'ja' ? 'ja-JP' : 'en-US',
                              { hour12: false }
                            ),
                          })
                        : t('cards.upstream.neverSwitched')}
                    </span>
                  </div>
                </InsetPanel>
              )}

              {!backupUpstreamInput.trim() && (
                <InsetPanel tone="amber" className="text-xs px-3 py-2">
                  {t('cards.upstream.missingBackupWarning')}
                </InsetPanel>
              )}
            </div>
          )}
        </div>

        {/* Mode Toggles */}
        <div className="pt-2 hairline-t">
          <SettingToggleRow
            label={t('cards.upstream.directModeShort')}
            tooltip={t('cards.upstream.directModeTip')}
            checked={isDirect}
            onChange={onToggleDirect}
            disabled={Boolean(loadingAction)}
          />

          <SettingToggleRow
            label={t('cards.upstream.shimakazeShort')}
            tooltip={t('cards.upstream.shimakazeTip')}
            checked={isShimakaze}
            onChange={onToggleShimakaze}
            disabled={Boolean(loadingAction)}
          >
            {isShimakaze && (
              <InsetPanel tone="sky" className="mt-2 text-xs leading-relaxed space-y-1">
                <div>{t('cards.upstream.shimakazeNotice')}</div>
                <div className="opacity-90">{t('cards.upstream.shimakazeRemoteCacheNotice')}</div>
              </InsetPanel>
            )}
          </SettingToggleRow>
        </div>

        {/* Listen Port & Allow LAN */}
        <div className="space-y-2.5 pt-2 hairline-t">
          <div className="flex items-center gap-2 flex-wrap">
            <label className="text-[13px] sm:text-sm text-label-secondary font-medium">
              {t('cards.network.portLabel')}
            </label>
            <CompoundInputGroup className="w-auto">
              <input
                type="text"
                disabled={Boolean(loadingAction)}
                value={portInput}
                onChange={(e) => setPortInput(e.target.value)}
                className="w-16 px-2 py-0.5 text-xs sm:text-[13px] font-mono tnum text-center"
              />
              <Button
                type="button"
                variant="desktop"
                size="xs"
                onClick={onSavePort}
                disabled={Boolean(loadingAction)}
                aria-busy={isActionLoading('save-port')}
              >
                {isActionLoading('save-port') ? t('common.saving') : t('cards.network.savePort')}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="xs"
                onClick={onResetPort}
                disabled={Boolean(loadingAction)}
                aria-busy={isActionLoading('reset-port')}
              >
                {isActionLoading('reset-port') ? t('common.loading') : t('cards.network.resetDefault')}
              </Button>
            </CompoundInputGroup>
          </div>

          <div className="pt-0.5">
            <SettingToggleRow
              label={
                <div className="flex items-center gap-2 flex-wrap">
                  <span>{t('cards.network.allowLan')}</span>
                  <InfoTip text={t('cards.network.allowLanDesc')} />
                  <Button
                    type="button"
                    variant="desktop"
                    size="xs"
                    icon={<QrCode className="w-3.5 h-3.5" />}
                    onClick={(e) => {
                      e.stopPropagation()
                      onOpenMobileGuide()
                    }}
                  >
                    {t('cards.network.mobileGuide')}
                  </Button>
                </div>
              }
              description={
                isAllowLan
                  ? t('cards.network.lanIpDesc', { ip: status?.lan_ip || '192.168.x.x', port: currentListenPort })
                  : undefined
              }
              checked={isAllowLan}
              onChange={onToggleAllowLan}
              disabled={Boolean(loadingAction)}
            />
          </div>
        </div>
      </div>
    </Card>
  )
}
