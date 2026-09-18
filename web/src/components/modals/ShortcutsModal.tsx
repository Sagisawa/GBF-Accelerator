import React from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Keyboard, ShieldCheck } from 'lucide-react'

export interface ShortcutsModalProps {
  isOpen: boolean
  onClose: () => void
}

export const ShortcutsModal: React.FC<ShortcutsModalProps> = ({ isOpen, onClose }) => {
  const shortcuts = [
    { key: 'Space', desc: '启动 / 停止代理服务' },
    { key: 'L', desc: '展开 / 收起实时日志终端' },
    { key: 'D', desc: '切换直连模式 (Direct Mode)' },
    { key: 'C', desc: '呼出清空缓存 (RAM / 磁盘) 窗口' },
    { key: '?', desc: '打开此快捷键速查面板' },
    { key: 'Esc', desc: '关闭当前展开的弹窗或抽屉' },
  ]

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Keyboard className="w-4 h-4 text-blue-600" />
          <span>键盘快捷键速查</span>
        </div>
      }
      subtitle="全局单键快速中控操作 (打字时自动屏蔽防误触)"
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
          <span>在任何文本输入框打字时，单键快捷键会自动屏蔽，彻底杜绝误触。</span>
        </div>

        <div className="flex justify-end pt-1">
          <Button variant="desktop" size="sm" onClick={onClose}>
            关闭 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}

