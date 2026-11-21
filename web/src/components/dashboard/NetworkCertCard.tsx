import React from 'react'
import { ShieldCheck } from 'lucide-react'
import { RuntimeStatus } from '../../types'
import { useI18n } from '../../i18n'
import { Card, CardHeader, InsetPanel } from '../common/Card'
import { Badge } from '../common/Badge'
import { Button } from '../common/Button'
import { SettingToggleRow } from '../common/ToggleSwitch'

export interface NetworkCertCardProps {
  status: RuntimeStatus | null
  loadingAction: string | null
  caStatusKnown: boolean
  isCaInstalled: boolean
  caFingerprint: string
  isAutoPac: boolean
  isAutoStart: boolean
  isAutoUpdate: boolean
  onOpenCaInstall: () => void
  onOpenCaUninstall: () => void
  onToggleAutoPac: () => void
  onToggleAutoStart: () => void
  onToggleAutoUpdate: () => void
}

export const NetworkCertCard: React.FC<NetworkCertCardProps> = ({
  status,
  loadingAction,
  caStatusKnown,
  isCaInstalled,
  caFingerprint,
  isAutoPac,
  isAutoStart,
  isAutoUpdate,
  onOpenCaInstall,
  onOpenCaUninstall,
  onToggleAutoPac,
  onToggleAutoStart,
  onToggleAutoUpdate,
}) => {
  const { t } = useI18n()

  const badgeVariant = !caStatusKnown ? 'neutral' : isCaInstalled ? 'green' : 'red'
  const badgeText = !caStatusKnown
    ? t('common.loading')
    : isCaInstalled
    ? t('cards.network.certInstalled')
    : t('cards.network.certNotInstalled')

  return (
    <Card>
      <div className="space-y-3.5">
        <CardHeader
          icon={<ShieldCheck className="w-[18px] h-[18px]" strokeWidth={1.85} />}
          title={t('cards.network.certTitle')}
          badge={<Badge variant={badgeVariant} dot={caStatusKnown}>{badgeText}</Badge>}
        />

        {/* CA Status & Action Buttons */}
        <div className="space-y-2.5">
          <div className="flex items-center justify-between gap-2 flex-wrap">
            <span className="text-[13px] sm:text-sm text-label-secondary font-medium">
              {t('cards.network.certStatusLabel')}
              <strong
                className={
                  !caStatusKnown
                    ? 'text-label-tertiary ml-1'
                    : isCaInstalled
                    ? 'text-sys-green ml-1'
                    : 'text-sys-red ml-1'
                }
              >
                {badgeText}
              </strong>
            </span>
            <div className="flex items-center gap-2">
              <Button type="button" variant="desktop" size="sm" onClick={onOpenCaInstall}>
                {t('cards.network.certInstallBtn')}
              </Button>
              <Button type="button" variant="desktop" size="sm" onClick={onOpenCaUninstall}>
                {t('cards.network.certUninstallBtn')}
              </Button>
            </div>
          </div>

          <InsetPanel className="px-3 py-2 text-xs font-mono text-label-tertiary select-all break-all leading-normal">
            {t('cards.network.certFingerprintLabel')} {caFingerprint || t('common.loading')}
          </InsetPanel>
        </div>

        {/* System Integration Toggles */}
        <div className="pt-1 hairline-t">
          <SettingToggleRow
            label={t('cards.network.autoPacShort')}
            tooltip={t('cards.network.autoPacTip')}
            checked={isAutoPac}
            onChange={onToggleAutoPac}
            disabled={Boolean(loadingAction)}
          >
            {isAutoPac && status?.system_proxy_conflict && (
              <InsetPanel tone="amber" className="mt-2 p-2.5 text-xs leading-relaxed">
                <span className="font-semibold">{t('cards.network.conflictNotice')}</span>
                <span className="ml-1 break-all">{status.system_proxy_conflict}</span>
              </InsetPanel>
            )}
          </SettingToggleRow>

          <SettingToggleRow
            label={t('cards.network.autoStartShort')}
            tooltip={t('cards.network.autoStartTip')}
            checked={isAutoStart}
            onChange={onToggleAutoStart}
            disabled={Boolean(loadingAction)}
          />

          <SettingToggleRow
            label={t('cards.network.autoUpdateShort')}
            tooltip={t('cards.network.autoUpdateTip')}
            checked={isAutoUpdate}
            onChange={onToggleAutoUpdate}
            disabled={Boolean(loadingAction)}
          />
        </div>
      </div>
    </Card>
  )
}
