import React from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Keyboard, ShieldCheck } from 'lucide-react'
import { useI18n } from '../../i18n'

export interface ShortcutsModalProps {
  isOpen: boolean
  onClose: () => void
}

export const ShortcutsModal: React.FC<ShortcutsModalProps> = ({ isOpen, onClose }) => {
  const { t } = useI18n()
  const shortcuts = [
    { key: 'Space', desc: t('modals.shortcuts.toggleProxy') },
    { key: 'L', desc: t('modals.shortcuts.toggleLogs') },
    { key: 'D', desc: t('modals.shortcuts.toggleDirect') },
    { key: 'C', desc: t('modals.shortcuts.clearCache') },
    { key: '?', desc: t('modals.shortcuts.shortcuts') },
    { key: 'Esc', desc: t('modals.shortcuts.closeAll') },
  ]

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Keyboard className="w-4 h-4 text-blue-600" />
          <span>{t('modals.shortcuts.title')}</span>
        </div>
      }
      subtitle={t('modals.shortcuts.subtitle')}
      maxWidth="max-w-md"
    >
      <div className="space-y-4">
        <div className="divide-y divide-slate-200 bg-slate-50 border border-slate-200 rounded-lg overflow-hidden">
          {shortcuts.map((s) => (
            <div
              key={s.key}
              className="px-4 py-2 flex items-center justify-between text-xs hover:bg-slate-100 transition-colors"
            >
              <span className="text-slate-800 font-medium">{s.desc}</span>
              <kbd className="px-2 py-0.5 rounded text-[11px] font-mono bg-white text-slate-800 border border-slate-300 shadow-xs">
                {s.key}
              </kbd>
            </div>
          ))}
        </div>

        <div className="p-3 bg-blue-50/60 border border-blue-100 rounded-lg flex items-center gap-2 text-[11px] text-blue-800">
          <ShieldCheck className="w-4 h-4 text-emerald-600 shrink-0" />
          <span>{t('modals.shortcuts.typingGuard')}</span>
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

