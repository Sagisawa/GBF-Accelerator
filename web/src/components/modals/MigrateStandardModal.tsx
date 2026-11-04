import React, { useState, useEffect } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { FolderSync, CheckCircle2, ArrowRight, AlertTriangle } from 'lucide-react'
import { useI18n } from '../../i18n'
import { CacheMigrationMode } from '../../api'

export interface MigrateStandardModalProps {
  isOpen: boolean
  onClose: () => void
  currentDir: string
  targetDir: string
  onMigrate: (mode?: CacheMigrationMode) => Promise<any>
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
  const [isConflict, setIsConflict] = useState(false)
  const [showOverwriteConfirm, setShowOverwriteConfirm] = useState(false)
  const [conflictError, setConflictError] = useState<string | null>(null)
  const [dontRemind, setDontRemind] = useState<boolean>(
    () => typeof localStorage !== 'undefined' && localStorage.getItem('gbf_dismiss_darwin_cache_migrate') === 'true'
  )

  useEffect(() => {
    if (isOpen) {
      setIsConflict(false)
      setShowOverwriteConfirm(false)
      setConflictError(null)
      setLoading(false)
    }
  }, [isOpen])

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

  const handleAction = async (mode: CacheMigrationMode) => {
    setLoading(true)
    setConflictError(null)
    try {
      await onMigrate(mode)
      onClose()
    } catch (e: any) {
      if (e.code === 'CACHE_MIGRATION_TARGET_NOT_EMPTY') {
        setIsConflict(true)
        setShowOverwriteConfirm(false)
      } else {
        setConflictError(e.message || String(e))
      }
    } finally {
      setLoading(false)
    }
  }

  // --- View 3: Overwrite Secondary Confirmation ---
  if (isConflict && showOverwriteConfirm) {
    return (
      <Modal
        isOpen={isOpen}
        onClose={loading ? () => {} : onClose}
        title={
          <div className="flex items-center gap-2 text-rose-700">
            <AlertTriangle className="w-5 h-5 text-rose-600 shrink-0" />
            <span>{t('modals.migrateStandard.overwriteConfirmTitle')}</span>
          </div>
        }
        subtitle={t('modals.migrateStandard.conflictSubtitle')}
        maxWidth="max-w-lg"
      >
        <div className="space-y-4 text-xs sm:text-sm text-slate-700">
          <div className="p-3 bg-rose-50/90 border border-rose-200 rounded-xl space-y-2">
            <p className="text-xs text-rose-900 leading-relaxed font-medium">
              {t('modals.migrateStandard.overwriteConfirmDesc')}
            </p>
            <p className="text-[11px] text-rose-800 leading-relaxed">
              {t('modals.migrateStandard.overwriteConfirmWarning')}
            </p>
          </div>

          <div className="space-y-2 p-3 bg-slate-50 border border-slate-200/80 rounded-xl text-[11px]">
            <div>
              <span className="font-semibold text-slate-600">{t('modals.migrateStandard.currentDir')} </span>
              <span className="font-mono text-slate-800 break-all">{currentDir}</span>
            </div>
            <div>
              <span className="font-semibold text-rose-700">{t('modals.migrateStandard.targetDir')} </span>
              <span className="font-mono text-rose-900 font-medium break-all">{targetDir}</span>
            </div>
          </div>

          {conflictError && (
            <div className="p-2.5 bg-rose-100/90 border border-rose-300 rounded-lg text-rose-800 text-xs break-all">
              {conflictError}
            </div>
          )}

          <div className="pt-2 flex items-center justify-end gap-2.5 border-t border-slate-100">
            <Button
              variant="desktop"
              size="sm"
              onClick={() => {
                setShowOverwriteConfirm(false)
                setConflictError(null)
              }}
              disabled={loading}
            >
              {t('modals.migrateStandard.back')}
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={() => handleAction('overwrite')}
              loading={loading}
              className="bg-rose-600 hover:bg-rose-700 active:bg-rose-800 border-rose-600 text-white font-medium"
            >
              {t('modals.migrateStandard.overwriteConfirmBtn')}
            </Button>
          </div>
        </div>
      </Modal>
    )
  }

  // --- View 2: Conflict Choices (switch_only or overwrite) ---
  if (isConflict) {
    return (
      <Modal
        isOpen={isOpen}
        onClose={loading ? () => {} : onClose}
        title={
          <div className="flex items-center gap-2 text-amber-700">
            <FolderSync className="w-5 h-5 text-amber-600 shrink-0" />
            <span>{t('modals.migrateStandard.conflictTitle')}</span>
          </div>
        }
        subtitle={t('modals.migrateStandard.conflictSubtitle')}
        maxWidth="max-w-xl"
      >
        <div className="space-y-4 text-xs sm:text-sm text-slate-700">
          <div className="p-3 bg-amber-50/90 border border-amber-200 rounded-xl space-y-1.5">
            <p className="text-xs text-amber-900 leading-relaxed font-medium">
              {t('modals.migrateStandard.conflictPrompt')}
            </p>
            <div className="font-mono text-[11px] text-amber-800 bg-white/80 p-1.5 rounded border border-amber-200/80 break-all select-all">
              {targetDir}
            </div>
          </div>

          {conflictError && (
            <div className="p-2.5 bg-rose-100/90 border border-rose-300 rounded-lg text-rose-800 text-xs break-all">
              {conflictError}
            </div>
          )}

          <div className="space-y-3">
            {/* Option A: Switch Only */}
            <div className="p-3.5 bg-emerald-50/60 border border-emerald-200/90 rounded-xl space-y-2 transition-all hover:border-emerald-300">
              <div className="flex items-start justify-between gap-2">
                <div className="space-y-1">
                  <div className="flex items-center gap-2">
                    <span className="font-semibold text-emerald-900 text-xs sm:text-sm">
                      {t('modals.migrateStandard.switchOnlyTitle')}
                    </span>
                  </div>
                  <p className="text-[11px] text-emerald-800/90 leading-relaxed">
                    {t('modals.migrateStandard.switchOnlyDesc')}
                  </p>
                  <p className="text-[10px] text-slate-500 leading-normal">
                    {t('modals.migrateStandard.switchOnlyNote')}
                  </p>
                </div>
                <Button
                  variant="primary"
                  size="sm"
                  onClick={() => handleAction('switch_only')}
                  loading={loading}
                  className="bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 border-emerald-600 text-white shrink-0 mt-0.5"
                >
                  {t('modals.migrateStandard.switchOnlyBtn')}
                </Button>
              </div>
            </div>

            {/* Option B: Overwrite & Migrate */}
            <div className="p-3.5 bg-amber-50/50 border border-amber-200/80 rounded-xl space-y-2 transition-all hover:border-amber-300">
              <div className="flex items-start justify-between gap-2">
                <div className="space-y-1">
                  <span className="font-semibold text-amber-900 text-xs sm:text-sm">
                    {t('modals.migrateStandard.overwriteTitle')}
                  </span>
                  <p className="text-[11px] text-amber-800/90 leading-relaxed">
                    {t('modals.migrateStandard.overwriteDesc')}
                  </p>
                  <p className="text-[10px] text-amber-700/80 leading-normal">
                    {t('modals.migrateStandard.overwriteWarning')}
                  </p>
                </div>
                <Button
                  variant="desktop"
                  size="sm"
                  onClick={() => setShowOverwriteConfirm(true)}
                  disabled={loading}
                  className="text-amber-800 border-amber-300 hover:bg-amber-100/60 shrink-0 mt-0.5 font-medium"
                >
                  {t('modals.migrateStandard.overwriteBtn')}
                </Button>
              </div>
            </div>
          </div>

          <div className="pt-2 flex items-center justify-between gap-2.5 border-t border-slate-100">
            <Button
              variant="desktop"
              size="sm"
              onClick={() => {
                setIsConflict(false)
                setConflictError(null)
              }}
              disabled={loading}
            >
              {t('modals.migrateStandard.back')}
            </Button>
            <Button
              variant="desktop"
              size="sm"
              onClick={onClose}
              disabled={loading}
            >
              {t('modals.migrateStandard.cancel')}
            </Button>
          </div>
        </div>
      </Modal>
    )
  }

  // --- View 1: Default Migration Prompt ---
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

        {conflictError && (
          <div className="p-2.5 bg-rose-100/90 border border-rose-300 rounded-lg text-rose-800 text-xs break-all">
            {conflictError}
          </div>
        )}

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
              onClick={() => handleAction('migrate')}
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
