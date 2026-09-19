import React, { useState, useEffect } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Network, Check, Radio } from 'lucide-react'
import { ProxyCandidate } from '../../types'
import { cn } from '../../utils/cn'

export interface UpstreamSelectModalProps {
  isOpen: boolean
  onClose: () => void
  candidates: ProxyCandidate[]
  currentProxy?: string
  onSelect: (candidate: ProxyCandidate) => Promise<void>
}

export const UpstreamSelectModal: React.FC<UpstreamSelectModalProps> = ({
  isOpen,
  onClose,
  candidates,
  currentProxy,
  onSelect,
}) => {
  const [selectedUrl, setSelectedUrl] = useState<string>('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (isOpen && candidates.length > 0) {
      // Prioritize current proxy if it matches any candidate, otherwise default to first candidate
      const match = candidates.find((c) => c.url.toLowerCase() === currentProxy?.toLowerCase())
      setSelectedUrl(match ? match.url : candidates[0].url)
    }
  }, [isOpen, candidates, currentProxy])

  const handleConfirm = async () => {
    const target = candidates.find((c) => c.url === selectedUrl) || candidates[0]
    if (!target) return
    setSubmitting(true)
    try {
      await onSelect(target)
      onClose()
    } finally {
      setSubmitting(false)
    }
  }

  // Keyboard Enter shortcut to confirm
  useEffect(() => {
    if (!isOpen) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Enter' && !submitting) {
        e.preventDefault()
        handleConfirm()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, submitting, selectedUrl, candidates])

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Network className="w-4 h-4 text-blue-600" />
          <span>选择上游网络代理</span>
        </div>
      }
      subtitle={`检测到本机有 ${candidates.length} 个活跃代理端口，请选择要使用的服务`}
      maxWidth="max-w-md"
    >
      <div className="space-y-3">
        <div className="space-y-2 max-h-[320px] overflow-y-auto pr-0.5">
          {candidates.map((cand) => {
            const isSelected = cand.url === selectedUrl
            const isCurrent = cand.url.toLowerCase() === currentProxy?.toLowerCase()

            return (
              <div
                key={cand.url}
                onClick={() => setSelectedUrl(cand.url)}
                onDoubleClick={handleConfirm}
                className={cn(
                  'flex items-center justify-between p-3 rounded-lg border transition-all cursor-pointer select-none text-left',
                  isSelected
                    ? 'border-blue-500 bg-blue-50/70 shadow-2xs ring-1 ring-blue-500/30'
                    : 'border-slate-200 bg-white hover:border-slate-300 hover:bg-slate-50'
                )}
              >
                <div className="flex items-center gap-3 min-w-0">
                  <div
                    className={cn(
                      'w-4 h-4 rounded-full border flex items-center justify-center shrink-0 transition-colors',
                      isSelected
                        ? 'border-blue-600 bg-blue-600 text-white'
                        : 'border-slate-300 bg-white'
                    )}
                  >
                    {isSelected && <div className="w-1.5 h-1.5 rounded-full bg-white" />}
                  </div>
                  <div className="min-w-0">
                    <div className="text-xs font-semibold text-slate-800 truncate flex items-center gap-1.5">
                      <span>{cand.name}</span>
                      {isCurrent && (
                        <span className="text-[10px] font-normal px-1.5 py-0.2 bg-emerald-100 text-emerald-700 rounded border border-emerald-200">
                          当前使用
                        </span>
                      )}
                    </div>
                    <div className="text-[11px] font-mono text-slate-500 truncate mt-0.5">
                      {cand.url}
                    </div>
                  </div>
                </div>

                {isSelected && (
                  <Check className="w-4 h-4 text-blue-600 shrink-0 ml-2" />
                )}
              </div>
            )
          })}
        </div>

        <div className="text-[11px] text-slate-500 bg-slate-50 p-2.5 rounded border border-slate-200 flex items-start gap-1.5">
          <Radio className="w-3.5 h-3.5 text-blue-600 shrink-0 mt-0.5" />
          <span>选定后将即刻断开旧上游连接池，切换到所选代理地址并保存配置。</span>
        </div>

        <div className="pt-2 flex justify-end gap-2 border-t border-slate-100">
          <Button
            variant="desktop"
            size="sm"
            onClick={onClose}
            disabled={submitting}
          >
            取消
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={handleConfirm}
            loading={submitting}
          >
            确认选用
          </Button>
        </div>
      </div>
    </Modal>
  )
}
