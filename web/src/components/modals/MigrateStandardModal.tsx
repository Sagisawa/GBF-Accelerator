import React, { useState } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { FolderSync, CheckCircle2, ArrowRight } from 'lucide-react'
import { useI18n } from '../../i18n'

export interface MigrateStandardModalProps {
  isOpen: boolean
  onClose: () => void
  currentDir: string
  targetDir: string
  onMigrate: () => Promise<void>
}

export const MigrateStandardModal: React.FC<MigrateStandardModalProps> = ({
  isOpen,
  onClose,
  currentDir,
  targetDir,
  onMigrate,
}) => {
  const { t } = useI18n()
  const [loading, setLoading] = useState(false)
  const [dontRemind, setDontRemind] = useState<boolean>(
    () => typeof localStorage !== 'undefined' && localStorage.getItem('gbf_dismiss_darwin_cache_migrate') === 'true'
  )

  const handleToggleDontRemind = (checked: boolean) => {
    setDontRemind(checked)
    if (typeof localStorage !== 'undefined') {
      if (checked) {
        localStorage.setItem('gbf_dismiss_darwin_cache_migrate', 'true')
      } else {
        localStorage.removeItem('gbf_dismiss_darwin_cache_migrate')
      }
    }
  }

  const handleConfirm = async () => {
    setLoading(true)
    try {
      await onMigrate()
      onClose()
    } finally {
      setLoading(false)
    }
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={loading ? () => {} : onClose}
      title={
        <div className="flex items-center gap-2">
          <FolderSync className="w-5 h-5 text-amber-600" />
          <span>{t('modals.migrateStandard.title')}</span>
        </div>
      }
      subtitle={t('modals.migrateStandard.subtitle')}
      maxWidth="max-w-lg"
    >
      <div className="space-y-4 text-xs sm:text-sm text-slate-700">
        <div className="p-3 bg-amber-50/80 border border-amber-200/90 rounded-xl space-y-2.5">
          <p className="text-xs text-amber-900 leading-relaxed">
            {t('modals.migrateStandard.prompt')}
          </p>

          <div className="space-y-2 pt-1">
            <div>
              <div className="text-[11px] font-semibold text-amber-800 mb-1">
                {t('modals.migrateStandard.currentDir')}
              </div>
              <div className="p-2 bg-white/90 border border-amber-200 rounded-lg font-mono text-[11px] text-slate-800 break-all select-all">
                {currentDir}
              </div>
            </div>

            <div className="flex justify-center text-amber-600 py-0.5">
              <ArrowRight className="w-4 h-4 rotate-90 sm:rotate-0" />
            </div>

            <div>
              <div className="text-[11px] font-semibold text-emerald-800 mb-1">
                {t('modals.migrateStandard.targetDir')}
              </div>
              <div className="p-2 bg-emerald-50/90 border border-emerald-200 rounded-lg font-mono text-[11px] text-emerald-900 break-all select-all font-medium">
                {targetDir}
              </div>
            </div>
          </div>
        </div>

        <div className="space-y-1.5 pl-1 text-[11px] sm:text-xs text-slate-600 leading-relaxed">
          <div className="flex items-start gap-1.5">
            <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600 shrink-0 mt-0.5" />
            <span>{t('modals.migrateStandard.featurePoint1')}</span>
          </div>
          <div className="flex items-start gap-1.5">
            <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600 shrink-0 mt-0.5" />
            <span>{t('modals.migrateStandard.featurePoint2')}</span>
          </div>
          <div className="flex items-start gap-1.5">
            <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600 shrink-0 mt-0.5" />
            <span>{t('modals.migrateStandard.featurePoint3')}</span>
          </div>
        </div>

        <div className="text-[11px] text-slate-500 leading-relaxed border-t border-slate-100 pt-2.5">
          {t('modals.migrateStandard.note')}
        </div>

        <div className="pt-2 flex items-center justify-between gap-2.5 border-t border-slate-100 flex-wrap">
          <label className="inline-flex items-center gap-1.5 text-[11px] text-slate-500 hover:text-slate-700 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={dontRemind}
              onChange={(e) => handleToggleDontRemind(e.target.checked)}
              className="w-3.5 h-3.5 rounded text-amber-600 border-slate-300 focus:ring-amber-500/20 cursor-pointer accent-amber-600"
            />
            <span>{t('modals.migrateStandard.dontRemind')}</span>
          </label>
          <div className="flex items-center gap-2">
            <Button
              variant="desktop"
              size="sm"
              onClick={onClose}
              disabled={loading}
            >
              {t('modals.migrateStandard.cancel')}
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={handleConfirm}
              loading={loading}
              className="bg-amber-600 hover:bg-amber-700 active:bg-amber-800 border-amber-600 text-white font-medium"
            >
              {t('modals.migrateStandard.confirm')}
            </Button>
          </div>
        </div>
      </div>
    </Modal>
  )
}
