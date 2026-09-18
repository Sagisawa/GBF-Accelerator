import React, { useState } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Sparkles, CheckCircle2 } from 'lucide-react'
import { slimCache } from '../../api'

export interface SlimModalProps {
  isOpen: boolean
  onClose: () => void
  onRefresh: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
}

export const SlimModal: React.FC<SlimModalProps> = ({
  isOpen,
  onClose,
  onRefresh,
  onToast,
}) => {
  const [keepCount, setKeepCount] = useState<number>(8)
  const [running, setRunning] = useState(false)
  const [triggered, setTriggered] = useState(false)

  const handleStartSlim = async () => {
    setRunning(true)
    try {
      await slimCache(keepCount)
      setTriggered(true)
      onToast('缓存瘦身任务已在后台执行', 'info')
      onRefresh()
    } catch (e: any) {
      onToast(`瘦身启动失败: ${e.message}`, 'error')
    } finally {
      setRunning(false)
    }
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Sparkles className="w-4 h-4 text-blue-600" />
          <span>缓存安全瘦身与历史版本清理</span>
        </div>
      }
      subtitle="仅清理废弃历史版本代码碎片，立绘、语音与全部公共素材 100% 完好保留"
      maxWidth="max-w-md"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-2 leading-relaxed">
          <div className="font-bold text-slate-900">安全瘦身说明：</div>
          <p className="text-slate-600">
            碧蓝幻想每次游戏更新都会生成新的版本哈希目录。长久运行后，磁盘中会残留大量已被游戏废弃的历史版本代码文件。
          </p>
          <div className="text-[11px] text-slate-600 space-y-1">
            <div>• <strong>保留最新版本数：</strong>默认保留最近 {keepCount} 个健康更新版本</div>
            <div>• <strong>完全不受影响：</strong>所有角色立绘、武器召唤、背景及音频等高价值大素材全部完好无损</div>
          </div>
        </div>

        <div className="flex items-center justify-between p-3 bg-slate-50 border border-slate-200 rounded-lg">
          <span className="font-semibold text-slate-800">保留最新版本数量：</span>
          <div className="flex items-center gap-2">
            {[4, 8, 12].map((num) => (
              <button
                key={num}
                type="button"
                onClick={() => setKeepCount(num)}
                className={`px-2.5 py-1 rounded text-xs font-mono border transition-colors ${
                  keepCount === num
                    ? 'bg-blue-600 text-white border-blue-600 font-bold'
                    : 'bg-white text-slate-700 border-slate-300 hover:bg-slate-100'
                }`}
              >
                {num} 个
              </button>
            ))}
          </div>
        </div>

        {triggered && (
          <div className="p-3 bg-emerald-50 border border-emerald-200 rounded-lg text-emerald-800 space-y-1">
            <div className="font-bold flex items-center gap-1.5 text-emerald-900">
              <CheckCircle2 className="w-4 h-4 text-emerald-600" />
              <span>瘦身任务已开始后台执行</span>
            </div>
            <p className="text-[11px] text-emerald-700">
              清理过程不影响当前游戏正常加速，清理结果将同步打印至实时终端日志。
            </p>
          </div>
        )}

        <div className="flex items-center justify-between pt-1">
          <Button variant="desktop" size="sm" onClick={onClose}>
            关闭 (Esc)
          </Button>

          <Button
            variant="primary"
            size="sm"
            loading={running}
            onClick={handleStartSlim}
          >
            开始安全瘦身
          </Button>
        </div>
      </div>
    </Modal>
  )
}
