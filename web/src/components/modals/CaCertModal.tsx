import React, { useState } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { ShieldCheck, Download, Copy, Check, Trash2, CheckCircle2, AlertCircle } from 'lucide-react'
import { installCert, uninstallCert, cleanLegacyCert } from '../../api'
import { useI18n } from '../../i18n'

export interface CaCertModalProps {
  isOpen: boolean
  onClose: () => void
  isInstalled: boolean
  fingerprint?: string
  actionType: 'install' | 'uninstall'
  onRefresh?: () => void
  onToast?: (msg: string, type?: 'info' | 'success' | 'error') => void
}

export const CaCertModal: React.FC<CaCertModalProps> = ({
  isOpen,
  onClose,
  isInstalled,
  fingerprint = '8D:7A:35:CA:F9:19:9B:C5:EE:3B:B4:0D:F9:41:15:F4:F3:57:12:65:94:3D:8B:14:D4:0A:43:19:20:B7:5E:75',
  actionType,
  onRefresh,
  onToast,
}) => {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const [loading, setLoading] = useState(false)
  const [message, setMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null)
  const caDownloadUrl = `/ca.crt`

  const handleCopyFp = () => {
    navigator.clipboard.writeText(fingerprint)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  const handleAutoInstall = async () => {
    setLoading(true)
    setMessage(null)
    try {
      const res = await installCert()
      if (res.ok) {
        setMessage({
          type: 'success',
          text: t('modals.caCert.installedSuccess') + ' ' + t('modals.caCert.restartBrowserTip')
        })
        if (onToast) onToast(t('modals.caCert.installedToast'), 'success')
        if (onRefresh) onRefresh()
      } else {
        setMessage({ type: 'error', text: res.error || res.message || t('modals.caCert.installFailed') })
        if (onToast) onToast(res.error || t('modals.caCert.installFailed'), 'error')
      }
    } catch (e: any) {
      setMessage({ type: 'error', text: t('modals.caCert.installFailedError', { error: e.message }) })
      if (onToast) onToast(t('modals.caCert.installFailedError', { error: e.message }), 'error')
    } finally {
      setLoading(false)
    }
  }

  const handleAutoUninstall = async () => {
    setLoading(true)
    setMessage(null)
    try {
      const res = await uninstallCert()
      if (res.ok) {
        setMessage({ type: 'success', text: res.message || t('modals.caCert.uninstalledSuccess') })
        if (onToast) onToast(t('modals.caCert.uninstalledToast'), 'info')
        if (onRefresh) onRefresh()
      } else {
        setMessage({ type: 'error', text: res.error || res.message || t('modals.caCert.uninstallFailed') })
        if (onToast) onToast(res.error || t('modals.caCert.uninstallFailed'), 'error')
      }
    } catch (e: any) {
      setMessage({ type: 'error', text: t('modals.caCert.uninstallFailedError', { error: e.message }) })
      if (onToast) onToast(t('modals.caCert.uninstallFailedError', { error: e.message }), 'error')
    } finally {
      setLoading(false)
    }
  }

  const handleCleanLegacy = async () => {
    setLoading(true)
    setMessage(null)
    try {
      const res = await cleanLegacyCert()
      if (res.ok) {
        setMessage({ type: 'success', text: res.message || t('modals.caCert.cleanedLegacy', { count: res.cleaned ?? 0 }) })
        if (onToast) onToast(res.message || t('modals.caCert.cleanedLegacy', { count: res.cleaned ?? 0 }), 'success')
        if (onRefresh) onRefresh()
      } else {
        setMessage({ type: 'error', text: res.error || res.message || t('common.failed') })
      }
    } catch (e: any) {
      setMessage({ type: 'error', text: e.message || t('common.failed') })
    } finally {
      setLoading(false)
    }
  }

  if (actionType === 'uninstall') {
    return (
      <Modal
        isOpen={isOpen}
        onClose={onClose}
        title={
          <div className="flex items-center gap-2">
            <ShieldCheck className="w-4 h-4 text-red-600" />
            <span>{t('modals.caCert.uninstallTitle')}</span>
          </div>
        }
        subtitle={t('modals.caCert.uninstallSubtitle')}
        maxWidth="max-w-md"
      >
        <div className="space-y-3.5 text-xs text-slate-700">
          {message && (
            <div
              className={`p-3 rounded-lg border flex items-start gap-2 ${
                message.type === 'success'
                  ? 'bg-emerald-50 border-emerald-200 text-emerald-800'
                  : 'bg-red-50 border-red-200 text-red-800'
              }`}
            >
              {message.type === 'success' ? (
                <CheckCircle2 className="w-4 h-4 shrink-0 text-emerald-600 mt-0.5" />
              ) : (
                <AlertCircle className="w-4 h-4 shrink-0 text-red-600 mt-0.5" />
              )}
              <span className="text-xs leading-relaxed">{message.text}</span>
            </div>
          )}

          <div className="p-3 bg-red-50/70 border border-red-200 rounded-lg space-y-2 text-slate-700 leading-relaxed">
            <div className="font-bold text-red-800">{t('modals.caCert.uninstallLead')}</div>
            <p className="text-xs text-slate-600">
              {t('modals.caCert.uninstallDesc')}
            </p>
            <div className="pt-1">
              <Button
                variant="desktop"
                size="sm"
                loading={loading}
                onClick={handleAutoUninstall}
                icon={<Trash2 className="w-3.5 h-3.5 text-red-600" />}
              >
                {t('modals.caCert.uninstallBtn')}
              </Button>
            </div>
          </div>

          <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-1.5 text-slate-700 leading-relaxed">
            <div className="font-bold text-slate-800">{t('modals.caCert.guideTitle')}：</div>
            <ol className="list-decimal list-inside space-y-1 text-slate-600 text-xs">
              <li>{t('modals.caCert.guideWin')}</li>
              <li>{t('modals.caCert.guideMac')}</li>
              <li>{t('modals.caCert.guideAndroid')}</li>
            </ol>
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

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-emerald-600" />
          <span>{t('modals.caCert.title')}</span>
        </div>
      }
      subtitle={t('modals.caCert.subtitle')}
      maxWidth="max-w-lg"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        {message && (
          <div
            className={`p-3 rounded-lg border flex items-start gap-2 ${
              message.type === 'success'
                ? 'bg-emerald-50 border-emerald-200 text-emerald-800'
                : 'bg-red-50 border-red-200 text-red-800'
            }`}
          >
            {message.type === 'success' ? (
              <CheckCircle2 className="w-4 h-4 shrink-0 text-emerald-600 mt-0.5" />
            ) : (
              <AlertCircle className="w-4 h-4 shrink-0 text-red-600 mt-0.5" />
            )}
            <span className="text-xs leading-relaxed">{message.text}</span>
          </div>
        )}

        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-2">
          <div className="flex items-center justify-between">
            <span className="font-bold text-slate-800">{t('modals.caCert.statusLead')}</span>
            <span className={`font-bold ${isInstalled ? 'text-emerald-600' : 'text-red-600'}`}>
              {isInstalled ? t('modals.caCert.statusTrusted') : t('modals.caCert.statusUntrusted')}
            </span>
          </div>

          <div className="space-y-1">
            <div className="flex items-center justify-between">
              <span className="text-slate-500 font-mono text-xs">{t('modals.caCert.fpLead')}</span>
              <Button
                variant="desktop"
                size="xs"
                onClick={handleCopyFp}
                icon={copied ? <Check className="w-3 h-3 text-emerald-600" /> : <Copy className="w-3 h-3" />}
              >
                {copied ? t('modals.caCert.copiedFp') : t('modals.caCert.copyFp')}
              </Button>
            </div>
            <div className="bg-white p-2 rounded border border-slate-200 font-mono text-[11px] text-slate-700 break-all select-all">
              {fingerprint}
            </div>
          </div>
        </div>

        {/* Action Panel: One click install & Clean legacy */}
        <div className="p-3 bg-emerald-50/60 border border-emerald-200 rounded-lg space-y-2 leading-relaxed">
          <div className="font-bold text-emerald-950">{t('modals.caCert.manageLead')}</div>
          <div className="flex items-center gap-2 flex-wrap">
            <Button
              variant="primary"
              size="sm"
              loading={loading}
              onClick={handleAutoInstall}
              icon={<ShieldCheck className="w-3.5 h-3.5" />}
            >
              {t('modals.caCert.installBtn')}
            </Button>
            <Button
              variant="desktop"
              size="sm"
              loading={loading}
              onClick={handleCleanLegacy}
            >
              {t('modals.caCert.cleanLegacyBtn')}
            </Button>
          </div>
          <p className="text-xs text-slate-600">
            {t('modals.caCert.installTip')}
          </p>
        </div>

        {/* Restart browser notice */}
        <div className="p-3 bg-amber-50 border border-amber-200 rounded-lg text-xs text-amber-900 leading-relaxed flex items-start gap-2.5">
          <AlertCircle className="w-4 h-4 shrink-0 text-amber-600 mt-0.5" />
          <div>
            <strong className="font-semibold text-amber-950">{t('modals.caCert.restartNoticeLead')}</strong>
            {t('modals.caCert.restartNoticeDesc')}
          </div>
        </div>

        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-2 leading-relaxed">
          <div className="font-bold text-slate-900">{t('modals.caCert.manualLead')}</div>
          <div className="space-y-1.5 text-slate-600 text-xs">
            <div>
              <strong>{t('modals.caCert.manualMethod1')}</strong>
              <code className="font-mono bg-white px-1.5 py-0.5 border border-slate-200 rounded text-blue-600">install_ca.bat</code> (macOS: <code className="font-mono bg-white px-1.5 py-0.5 border border-slate-200 rounded text-blue-600">./install_ca.sh</code>)
            </div>
            <div>
              <strong>{t('modals.caCert.manualMethod2')}</strong>
              {t('modals.caCert.guideWin')}
            </div>
          </div>
        </div>

        <div className="flex items-center justify-between pt-1">
          <a
            href={caDownloadUrl}
            download="gbf_ca.crt"
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-blue-600 hover:bg-blue-700 text-white font-medium text-xs transition-colors"
          >
            <Download className="w-3.5 h-3.5" />
            <span>{t('modals.caCert.downloadCertBtn')}</span>
          </a>

          <Button variant="desktop" size="sm" onClick={onClose}>
            {t('common.closeEsc')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
