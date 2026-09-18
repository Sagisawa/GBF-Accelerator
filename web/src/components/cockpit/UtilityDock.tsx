import React from 'react'
import { Button } from '../common/Button'
import { Trash2, Terminal, Keyboard, Github, Gamepad2 } from 'lucide-react'

export interface UtilityDockProps {
  onOpenClearModal: () => void
  onToggleLogs: () => void
  onOpenShortcuts: () => void
  logCount: number
}

export const UtilityDock: React.FC<UtilityDockProps> = ({
  onOpenClearModal,
  onToggleLogs,
  onOpenShortcuts,
  logCount,
}) => {
  return (
    <div className="pt-2 pb-8 flex flex-wrap items-center justify-between gap-3 text-xs text-label-secondary border-t border-hairline">
      {/* Left Action Buttons */}
      <div className="flex items-center gap-2 flex-wrap">
        <Button
          variant="secondary"
          size="sm"
          onClick={onOpenClearModal}
          icon={<Trash2 className="w-3.5 h-3.5 text-sys-red" />}
          shortcut="C"
        >
          清空缓存
        </Button>

        <Button
          variant="secondary"
          size="sm"
          onClick={onToggleLogs}
          icon={<Terminal className="w-3.5 h-3.5 text-sys-blue" />}
          shortcut="L"
        >
          实时日志终端 {logCount > 0 && <span className="text-[10px] font-mono text-label-tertiary">({logCount})</span>}
        </Button>

        <Button
          variant="ghost"
          size="sm"
          onClick={onOpenShortcuts}
          icon={<Keyboard className="w-3.5 h-3.5" />}
          shortcut="?"
        >
          快捷键
        </Button>
      </div>

      {/* Right Links */}
      <div className="flex items-center gap-3">
        <a
          href="https://game.granbluefantasy.jp"
          target="_blank"
          rel="noreferrer"
          className="hover:text-label-primary flex items-center gap-1.5 transition-colors"
        >
          <Gamepad2 className="w-3.5 h-3.5 text-sys-blue" />
          <span>打开游戏页</span>
        </a>

        <a
          href="https://github.com/Sagisawa/GBF-Accelerator"
          target="_blank"
          rel="noreferrer"
          className="hover:text-label-primary flex items-center gap-1.5 transition-colors"
        >
          <Github className="w-3.5 h-3.5" />
          <span>GitHub</span>
        </a>
      </div>
    </div>
  )
}
