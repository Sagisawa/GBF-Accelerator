import React from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Globe } from 'lucide-react'
import { useI18n } from '../../i18n'

export interface RoutingGuideModalProps {
  isOpen: boolean
  onClose: () => void
  listenPort?: number
}

export const RoutingGuideModal: React.FC<RoutingGuideModalProps> = ({
  isOpen,
  onClose,
  listenPort = 8124,
}) => {
  const { t } = useI18n()
  const pacUrl = `http://127.0.0.1:${listenPort}/proxy.pac`

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Globe className="w-4 h-4 text-blue-600" />
          <span>{t('modals.routingGuide.title')}</span>
        </div>
      }
      subtitle={t('modals.routingGuide.subtitle')}
      maxWidth="max-w-lg"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-1.5">
          <div className="font-bold text-slate-900 text-sm">{t('modals.routingGuide.scheme1Title')}</div>
          <p className="leading-relaxed text-slate-600">
            {t('modals.routingGuide.scheme1Desc')}
          </p>
        </div>

        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-1.5">
          <div className="font-bold text-slate-900 text-sm">{t('modals.routingGuide.scheme2Title')}</div>
          <p className="leading-relaxed text-slate-600 text-xs">
            {t('modals.routingGuide.scheme2Desc')}
          </p>
          <div className="text-xs text-slate-700 bg-white p-3 rounded-lg border border-slate-200 space-y-2.5 leading-relaxed">
            <div>
              <div className="font-semibold text-slate-900">{t('modals.routingGuide.methodATitle')}</div>
              <div className="pl-2 pt-1 text-slate-600 space-y-1">
                <div>{t('modals.routingGuide.methodAStep1')}</div>
                <div>{t('modals.routingGuide.methodAStep2')}<code className="text-blue-600 font-mono select-all bg-slate-50 px-1.5 py-0.5 rounded border border-slate-200">{pacUrl}</code>{t('modals.routingGuide.methodAStep2Tail')}</div>
                <div>{t('modals.routingGuide.methodAStep3')}</div>
              </div>
            </div>
            <div className="pt-2 border-t border-slate-100">
              <div className="font-semibold text-slate-900">{t('modals.routingGuide.methodBTitle')}</div>
              <div className="pl-2 pt-1 text-slate-600 space-y-1">
                <div>{t('modals.routingGuide.methodBStep1')}<strong className="text-slate-900 font-semibold">SwitchyOmega_GBF.bak</strong></div>
                <div>{t('modals.routingGuide.methodBStep2')}</div>
              </div>
            </div>
          </div>
        </div>

        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-1.5">
          <div className="font-bold text-slate-900 text-sm">{t('modals.routingGuide.scheme3Title')}</div>
          <p className="leading-relaxed text-slate-600 text-xs">
            {t('modals.routingGuide.scheme3Desc')}
          </p>
          <div className="text-xs text-slate-700 bg-white p-3 rounded-lg border border-slate-200 space-y-2 leading-relaxed">
            <div><strong className="text-slate-900 font-semibold">{t('modals.routingGuide.step1Target')}</strong>{t('modals.routingGuide.step1TargetDesc')}<code className="text-blue-600 font-mono bg-slate-50 px-1.5 py-0.5 rounded border border-slate-200">127.0.0.1:{listenPort}</code>{t('modals.routingGuide.step1TargetName')}<code className="text-slate-800 font-mono font-semibold">GbfAccelerator</code>。</div>
            <div><strong className="text-slate-900 font-semibold">{t('modals.routingGuide.step2Policy')}</strong>{t('modals.routingGuide.step2PolicyDesc')}</div>
            <div><strong className="text-slate-900 font-semibold">{t('modals.routingGuide.step3Rules')}</strong>{t('modals.routingGuide.step3RulesDesc')}
              <div className="grid grid-cols-2 gap-x-2 gap-y-0.5 pl-2 pt-1 text-slate-700 font-mono text-[11.5px]">
                <div>• granbluefantasy.jp</div>
                <div>• granbluefantasy.com</div>
                <div>• mbga.jp</div>
                <div>• mobage.jp</div>
                <div>• *granbluefantasy.akamaized.net</div>
                <div>• *gbf.akamaized.net</div>
                <div className="col-span-2">• *granbluefantasy-steam.akamaized.net</div>
              </div>
            </div>
            <div><strong className="text-slate-900 font-semibold">{t('modals.routingGuide.step4Switch')}</strong>{t('modals.routingGuide.step4SwitchDesc')}</div>
          </div>
        </div>

        <div className="p-3 bg-blue-50/70 border border-blue-200 rounded-lg space-y-1">
          <div className="font-bold text-blue-900 text-xs">{t('modals.routingGuide.privacyNoticeTitle')}</div>
          <p className="text-xs text-blue-800 leading-relaxed">
            {t('modals.routingGuide.privacyNoticeDesc')}
          </p>
        </div>

        <div className="flex justify-end pt-1">
          <Button variant="desktop" size="sm" onClick={onClose}>
            {t('modals.routingGuide.iUnderstandEsc')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
