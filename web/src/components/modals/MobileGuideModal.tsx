import React from 'react'
import { RuntimeStatus } from '../../types'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { QRCodeSVG } from 'qrcode.react'
import { Smartphone, Wifi, Copy, Check, AlertCircle } from 'lucide-react'
import { applyConfig, applyFirewallRule, fetchFirewallStatus, APIError, FirewallStatus } from '../../api'
import { useI18n } from '../../i18n'

export interface MobileGuideModalProps {
  isOpen: boolean
  onClose: () => void
  status: RuntimeStatus | null
  onConfigUpdated: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error' | 'warning') => void
}

export const MobileGuideModal: React.FC<MobileGuideModalProps> = ({
  isOpen,
  onClose,
  status,
  onConfigUpdated,
  onToast,
}) => {
  const { t } = useI18n()
  const [copied, setCopied] = React.useState(false)
  const [firewallStatus, setFirewallStatus] = React.useState<FirewallStatus | null>(null)
  const [firewallLoading, setFirewallLoading] = React.useState(false)
  const [firewallApplying, setFirewallApplying] = React.useState(false)
  const [lanApplying, setLanApplying] = React.useState(false)
  const [firewallError, setFirewallError] = React.useState<string | null>(null)
  const isLanEnabled = Boolean(status?.allow_lan)
  const isProxyRunning = Boolean(status?.proxy_running)
  const host = status?.lan_ip || '192.168.x.x'
  const port = status?.listen_port || 8124
  const canUseMobileAccess = isLanEnabled && isProxyRunning
  const pacUrl = `http://${host}:${port}/proxy.pac`
  const guideUrl = `http://${host}:${port}/`
  const caUrl = `http://${host}:${port}/ca.crt`

  const refreshFirewallStatus = React.useCallback(async () => {
    setFirewallLoading(true)
    setFirewallError(null)
    try {
      setFirewallStatus(await fetchFirewallStatus())
    } catch (e: any) {
      setFirewallStatus(null)
      setFirewallError(e?.message || t('modals.mobileGuide.getFirewallFailed'))
    } finally {
      setFirewallLoading(false)
    }
  }, [t])

  React.useEffect(() => {
    if (!isOpen) return
    refreshFirewallStatus()
  }, [isOpen, refreshFirewallStatus])

  const handleApplyFirewall = async () => {
    if (firewallApplying) return
    setFirewallApplying(true)
    setFirewallError(null)
    try {
      const result = await applyFirewallRule()
      setFirewallStatus(result.status || null)
      onToast(
        result.code === 'already_allowed'
          ? t('modals.mobileGuide.firewallAlreadySatisfied')
          : t('modals.mobileGuide.firewallConfiguredVerified'),
        'success'
      )
      await refreshFirewallStatus()
    } catch (e: any) {
      const code = e instanceof APIError ? e.code : e?.code
      if (code === 'user_cancelled') {
        setFirewallError(t('modals.mobileGuide.firewallCancelNotice'))
        onToast(t('modals.mobileGuide.firewallCancelled'), 'info')
      } else {
        const errMsg = t('modals.mobileGuide.firewallConfigFailed', { error: e?.message || '' })
        setFirewallError(errMsg)
        onToast(errMsg, 'error')
      }
    } finally {
      setFirewallApplying(false)
    }
  }

  const handleEnableLan = async () => {
    if (lanApplying) return
    setLanApplying(true)
    try {
      const resp = await applyConfig({ allow_lan: true })
      if (resp.warnings && resp.warnings.length > 0) {
        onToast(t('toasts.lanWarning', { warnings: resp.warnings.join('; ') }), 'warning')
      } else {
        onToast(t('toasts.lanEnabled'), 'success')
      }
      onConfigUpdated()
      await refreshFirewallStatus()
    } catch (e: any) {
      onToast(t('toasts.lanFailed', { error: e.message }), 'error')
    } finally {
      setLanApplying(false)
    }
  }

  const handleCopyPac = () => {
    navigator.clipboard.writeText(pacUrl)
    setCopied(true)
    onToast(t('modals.mobileGuide.pacCopied'), 'success')
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Smartphone className="w-4 h-4 text-apple-blue" />
          <span>{t('modals.mobileGuide.title')}</span>
        </div>
      }
      subtitle={t('modals.mobileGuide.subtitle')}
      maxWidth="max-w-lg"
    >
      <div className="space-y-4">
        {!isLanEnabled && (
          <div className="p-3.5 bg-amber-50 border border-amber-200 rounded-lg flex items-start gap-3 text-xs text-amber-800">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5 text-amber-600" />
            <div className="space-y-2 flex-1">
              <div>
                {t('modals.mobileGuide.lanNotEnabledDesc')}
              </div>
              <Button
                variant="primary"
                size="xs"
                onClick={handleEnableLan}
                loading={lanApplying}
                disabled={lanApplying}
              >
                {lanApplying ? t('modals.mobileGuide.enabling') : t('modals.mobileGuide.enableAllowLan')}
              </Button>
            </div>
          </div>
        )}

        {/* Windows Firewall Status */}
        <div className="p-3.5 bg-slate-50 border border-slate-200 rounded-lg space-y-2.5 text-xs">
          <div className="flex items-center justify-between gap-2">
            <span className="font-semibold text-slate-900">{t('modals.mobileGuide.fwTitle')}</span>
            {firewallLoading ? (
              <span className="text-slate-400">{t('common.checking')}</span>
            ) : firewallStatus?.allowed ? (
              <span className="text-emerald-700 font-semibold">{t('modals.mobileGuide.fwAllowed')}</span>
            ) : firewallStatus?.supported === false ? (
              <span className="text-slate-400">{t('modals.mobileGuide.fwNotNeeded')}</span>
            ) : firewallStatus ? (
              <span className="text-amber-700 font-semibold">{t('modals.mobileGuide.fwNotAllowed')}</span>
            ) : (
              <span className="text-rose-600 font-semibold">{t('common.failed')}</span>
            )}
          </div>

          {firewallStatus?.has_public && !firewallStatus.has_private && (
            <div className="p-2.5 bg-amber-50 border border-amber-200 rounded-md text-amber-800 leading-relaxed">
              {t('modals.mobileGuide.fwPublicWarning')}
            </div>
          )}

          {firewallStatus?.supported !== false && firewallStatus?.allowed && (
            <div className="text-emerald-700 leading-relaxed">
              {t('modals.mobileGuide.fwAllowedNotice', { port: firewallStatus.port })}
            </div>
          )}

          {firewallStatus?.supported !== false && firewallStatus && !firewallStatus.allowed && (
            <div className="flex items-center justify-between gap-2 flex-wrap">
              <span className="text-slate-500 leading-relaxed">
                {t('modals.mobileGuide.fwUnconfiguredNotice', { port: firewallStatus.port })}
              </span>
              <Button
                variant="primary"
                size="xs"
                onClick={handleApplyFirewall}
                loading={firewallApplying}
              >
                {firewallApplying ? t('common.applying') : t('modals.mobileGuide.applyFwBtn')}
              </Button>
            </div>
          )}

          {firewallError && (
            <div className="flex items-center justify-between gap-2 text-rose-700 leading-relaxed">
              <span>{firewallError}</span>
              <Button variant="desktop" size="xs" onClick={refreshFirewallStatus} disabled={firewallLoading || firewallApplying}>
                {t('common.refresh')}
              </Button>
            </div>
          )}

          {((firewallStatus?.network_categories || []).length > 0) && (
            <div className="text-[11px] text-slate-500 font-mono">
              {t('modals.mobileGuide.fwNetworkCategory', { category: (firewallStatus?.network_categories || []).join(', ') })}
            </div>
          )}
        </div>

        {/* QR Code and Instructions */}
        <div className="flex flex-col sm:flex-row items-center gap-4 p-4 bg-slate-50 border border-slate-200 rounded-lg">
          <div className={`bg-white p-2.5 rounded-lg border border-slate-200 shadow-sm shrink-0 ${!canUseMobileAccess ? 'opacity-45' : ''}`}>
            {canUseMobileAccess ? (
              <QRCodeSVG value={guideUrl} size={120} level="M" />
            ) : (
              <div className="w-[120px] h-[120px] flex items-center justify-center text-center text-[11px] leading-relaxed text-slate-400 whitespace-pre-line">
                {!isLanEnabled ? t('modals.mobileGuide.enableLanFirst') : t('modals.mobileGuide.startProxyFirst')}
              </div>
            )}
          </div>

          <div className="space-y-1.5 text-xs text-slate-600">
            <div className="font-bold text-slate-900 flex items-center gap-1.5">
              <Wifi className="w-3.5 h-3.5 text-emerald-600" /> {t('modals.mobileGuide.scanTitle')}
            </div>
            <p className="leading-relaxed">
              {t('modals.mobileGuide.step1')}<br />
              {t('modals.mobileGuide.step2')}<br />
              {t('modals.mobileGuide.step3')}<br />
              {t('modals.mobileGuide.step4')}
            </p>
          </div>
        </div>

        {/* Manual Configuration Block */}
        <div className="p-3.5 bg-slate-50 border border-slate-200 rounded-lg space-y-2 text-xs">
          <div className="flex items-center justify-between text-slate-700">
            <span className="font-semibold text-slate-900">{t('modals.mobileGuide.pacLabel')}</span>
            <Button
              variant="desktop"
              size="xs"
              onClick={handleCopyPac}
              icon={copied ? <Check className="w-3 h-3 text-emerald-600" /> : <Copy className="w-3 h-3" />}
            >
              {copied ? t('common.copied') : t('common.copy')}
            </Button>
          </div>
          <div className="font-mono text-[11px] text-blue-600 bg-white p-2 rounded border border-slate-300 select-all break-all">
            {pacUrl}
          </div>
          <div className="text-[11px] text-slate-500 leading-relaxed">
            {canUseMobileAccess
              ? t('modals.mobileGuide.guideAddress', { url: guideUrl })
              : !isLanEnabled
                ? t('modals.mobileGuide.maintenanceDesc')
                : t('modals.mobileGuide.blockedDesc')}
          </div>

          <div className="text-[11px] text-slate-500 pt-1 flex items-center justify-between">
            <span>{t('modals.mobileGuide.manualHost', { host })}</span>
            <span>{t('modals.mobileGuide.manualPort', { port })}</span>
          </div>
        </div>

        {/* Detailed Step-by-Step Instructions & FAQ */}
        <div className="border border-slate-200 rounded-lg overflow-hidden text-xs">
          <details className="group" open>
            <summary className="p-3 bg-slate-50 hover:bg-slate-100/80 cursor-pointer font-semibold text-slate-800 flex items-center justify-between transition-colors select-none">
              <span>{t('modals.mobileGuide.stepByStepTitle')}</span>
              <span className="text-slate-400 group-open:rotate-180 transition-transform">▼</span>
            </summary>
            <div className="p-3.5 space-y-3.5 bg-white border-t border-slate-200 text-slate-700 leading-relaxed max-h-[320px] overflow-y-auto">
              <div className="space-y-1.5">
                <div className="font-bold text-sky-700">{t('modals.mobileGuide.step1TitleDetailed')}</div>
                <ol className="list-decimal list-inside space-y-1 text-slate-600 pl-1">
                  <li>{t('modals.mobileGuide.step1EnsureWifi')}</li>
                  <li>{t('modals.mobileGuide.step1Safari', { caUrl, guideUrl })}</li>
                  <li>{t('modals.mobileGuide.step1DownloadTip')}</li>
                  <li>{t('modals.mobileGuide.step1OpenSettings')}</li>
                  <li>
                    <strong>{t('modals.mobileGuide.step1TrustTitle')}</strong><br />
                    {t('modals.mobileGuide.step1TrustDesc')}
                  </li>
                </ol>
              </div>

              <div className="space-y-1.5 pt-2.5 border-t border-slate-100">
                <div className="font-bold text-sky-700">{t('modals.mobileGuide.step2TitleDetailed')}</div>
                <ol className="list-decimal list-inside space-y-1 text-slate-600 pl-1">
                  <li>{t('modals.mobileGuide.step2Settings')}</li>
                  <li>{t('modals.mobileGuide.step2Details')}</li>
                  <li>{t('modals.mobileGuide.step2SwipeBottom')}</li>
                </ol>

                <div className="mt-2 space-y-2 pl-2">
                  <div className="p-2.5 bg-slate-50 border border-slate-200 rounded-md">
                    <div className="font-semibold text-slate-800">{t('modals.mobileGuide.step2Mode1Title')}</div>
                    <div className="text-slate-600 mt-1">{t('modals.mobileGuide.step2SelectAuto')}</div>
                    <div className="text-slate-600">{t('modals.mobileGuide.step2FillUrl')}<code className="font-mono text-sky-600 select-all">{pacUrl}</code></div>
                    <div className="text-slate-600">{t('modals.mobileGuide.step2Save')}</div>
                  </div>

                  <div className="p-2.5 bg-slate-50 border border-slate-200 rounded-md">
                    <div className="font-semibold text-slate-800">{t('modals.mobileGuide.step2Mode2Title')}</div>
                    <div className="text-slate-600 mt-1">{t('modals.mobileGuide.step2SelectManual')}</div>
                    <div className="text-slate-600">{t('modals.mobileGuide.step2FillServer')}<code className="font-mono text-sky-600 select-all">{host}</code></div>
                    <div className="text-slate-600">{t('modals.mobileGuide.step2FillPort')}<code className="font-mono text-sky-600 select-all">{port}</code></div>
                    <div className="text-slate-600">{t('modals.mobileGuide.step2Save')}</div>
                  </div>
                </div>
              </div>

              <div className="space-y-2 pt-2.5 border-t border-slate-100">
                <div className="font-bold text-slate-800">{t('modals.mobileGuide.faqTitle')}</div>
                <div className="space-y-2 text-slate-600 text-[11.5px]">
                  <div>
                    <strong className="text-slate-700">{t('modals.mobileGuide.faqAndroidTitle')}</strong><br />
                    <span className="text-slate-600 leading-relaxed block mt-0.5">
                      {t('modals.mobileGuide.faqAndroidDesc')}
                    </span>
                  </div>
                  <div>
                    <strong className="text-slate-700">{t('modals.mobileGuide.faqUrlTitle')}</strong><br />
                    {t('modals.mobileGuide.faqUrlDesc1')}<br />
                    <a href="https://game.granbluefantasy.jp" target="_blank" rel="noreferrer" className="text-sky-600 underline">https://game.granbluefantasy.jp</a><br />
                    <span className="text-slate-500 leading-relaxed block mt-0.5">{t('modals.mobileGuide.faqUrlDesc2')}</span>
                  </div>
                  <div>
                    <strong className="text-slate-700">{t('modals.mobileGuide.faqTimeoutTitle')}</strong><br />
                    {t('modals.mobileGuide.faqTimeoutDesc', { port })}
                  </div>
                  <div>
                    <strong className="text-slate-700">{t('modals.mobileGuide.faqCertTitle')}</strong><br />
                    {t('modals.mobileGuide.faqCertDesc')}
                  </div>
                  <div>
                    <strong className="text-slate-700">{t('modals.mobileGuide.faqIpTitle')}</strong><br />
                    {t('modals.mobileGuide.faqIpDesc')}
                  </div>
                </div>
              </div>
            </div>
          </details>
        </div>

        <div className="flex justify-end pt-1">
          <Button variant="desktop" size="sm" onClick={onClose}>
            {t('common.closeEsc')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

