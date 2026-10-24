import React, { useEffect, useState } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { ShieldCheck, CheckCircle2, Sparkles } from 'lucide-react'
import { useI18n } from '../../i18n'

export interface ShimakazeSuggestModalProps {
  isOpen: boolean
  onClose: () => void
  onEnable: () => Promise<void>
  targetName?: string
}

export const ShimakazeSuggestModal: React.FC<ShimakazeSuggestModalProps> = ({
  isOpen,
  onClose,
  onEnable,
  targetName,
}) => {
  const { t } = useI18n()
  const [loading, setLoading] = useState(false)

  const isAcgp = Boolean(targetName?.includes('8123') || targetName?.toLowerCase().includes('acgpower'))
  const clientName = isAcgp ? 'ACGPower' : t('modals.shimakazeSuggest.clientShimakaze')
  const defaultTarget = isAcgp ? 'ACGPower (8123)' : t('modals.shimakazeSuggest.defaultTargetShimakaze')

  const handleConfirm = async () => {
    setLoading(true)
    try {
      await onEnable()
      onClose()
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (!isOpen) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Enter' && !loading) {
        e.preventDefault()
        handleConfirm()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, loading])

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-amber-600" />
          <span>{t('modals.shimakazeSuggest.titleModal', { name: clientName })}</span>
        </div>
      }
      subtitle={t('modals.shimakazeSuggest.subtitleModal', { target: targetName || defaultTarget })}
      maxWidth="max-w-md"
    >
      <div className="space-y-3.5">
        <div className="p-3 bg-amber-50/70 border border-amber-200/80 rounded-lg space-y-2">
          <div className="text-xs font-semibold text-amber-900 flex items-center gap-1.5">
            <Sparkles className="w-3.5 h-3.5 text-amber-600 shrink-0" />
            <span>{t('modals.shimakazeSuggest.prompt', { name: clientName })}</span>
          </div>
          <div className="text-[11px] text-amber-800/90 leading-relaxed space-y-1">
            <div className="flex items-start gap-1.5">
              <CheckCircle2 className="w-3 h-3 text-amber-600 shrink-0 mt-0.5" />
              <span>{t('modals.shimakazeSuggest.b1')}</span>
            </div>
            <div className="flex items-start gap-1.5">
              <CheckCircle2 className="w-3 h-3 text-amber-600 shrink-0 mt-0.5" />
              <span>{t('modals.shimakazeSuggest.b2')}</span>
            </div>
            <div className="flex items-start gap-1.5">
              <CheckCircle2 className="w-3 h-3 text-amber-600 shrink-0 mt-0.5" />
              <span>{t('modals.shimakazeSuggest.b3', { name: clientName })}</span>
            </div>
            {!isAcgp && (
              <div className="flex items-start gap-1.5">
                <CheckCircle2 className="w-3 h-3 text-amber-600 shrink-0 mt-0.5" />
                <span>{t('modals.shimakazeSuggest.b4')}</span>
              </div>
            )}
          </div>
        </div>

        <div className="text-[11px] text-slate-500 leading-relaxed">
          {t('modals.shimakazeSuggest.note')}
        </div>

        <div className="pt-2 flex justify-end gap-2 border-t border-slate-100">
          <Button
            variant="desktop"
            size="sm"
            onClick={onClose}
            disabled={loading}
          >
            {t('modals.shimakazeSuggest.ignoreBtn')}
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={handleConfirm}
            loading={loading}
          >
            {t('modals.shimakazeSuggest.enableBtn')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
