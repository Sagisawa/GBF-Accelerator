import React from 'react'
import { RuntimeStatus } from '../../types'
import { Button } from '../common/Button'
import { Badge } from '../common/Badge'
import { Play, Square, Zap } from 'lucide-react'

export interface HeaderBarProps {
  status: RuntimeStatus | null
  loading: boolean
  onToggleProxy: () => void
}

export const HeaderBar: React.FC<HeaderBarProps> = ({
  status,
  loading,
  onToggleProxy,
}) => {
  const isRunning = Boolean(status?.proxy_running)
  const port = status?.listen_port || 8124
  const version = status?.version || '2.0.0'
  const uptimeMinutes = status ? Math.floor(status.uptime_seconds / 60) : 0

  return (
    <header className="bg-surface/80 border-b border-hairline sticky top-0 z-30 backdrop-blur-md">
      <div className="max-w-4xl mx-auto px-4 sm:px-6 h-16 flex items-center justify-between">
        {/* Left: Brand & Engine Identity */}
        <div className="flex items-center gap-3 select-none">
          <div className="w-8 h-8 rounded-lg bg-gradient-to-b from-white/15 to-white/5 border border-hairline-strong flex items-center justify-center shadow-specular">
            <Zap className="w-4 h-4 text-sys-blue" />
          </div>

          <div>
            <div className="flex items-center gap-2">
              <span className="font-semibold text-label-primary tracking-tight text-sm">
                GBF-Accelerator
              </span>
              <span className="text-[10px] uppercase font-mono px-1.5 py-0.5 rounded bg-surface-active text-label-secondary border border-hairline">
                v{version}
              </span>
              <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-sys-blueBg text-sys-blue border border-sys-blue/20">
                Go Core
              </span>
            </div>
            <div className="text-[11px] text-label-tertiary">
              高性能本地静态资源缓存与透明代理
            </div>
          </div>
        </div>

        {/* Right: State indicator & Primary Action */}
        <div className="flex items-center gap-4">
          {/* Status Capsule */}
          <div className="hidden sm:flex items-center gap-2 text-xs">
            <Badge
              variant={isRunning ? 'green' : 'neutral'}
              dot
              pulse={isRunning}
              mono
            >
              {isRunning ? `运行中 :${port}` : '已停止'}
            </Badge>
            {isRunning && uptimeMinutes > 0 && (
              <span className="text-[11px] text-label-tertiary font-mono">
                {uptimeMinutes}m
              </span>
            )}
          </div>

          {/* Large Start/Stop Button */}
          <Button
            variant={isRunning ? 'danger' : 'success'}
            size="md"
            loading={loading}
            onClick={onToggleProxy}
            icon={isRunning ? <Square className="w-3.5 h-3.5 fill-current" /> : <Play className="w-3.5 h-3.5 fill-current" />}
            shortcut="Space"
          >
            {isRunning ? '停止代理' : '启动加速'}
          </Button>
        </div>
      </div>
    </header>
  )
}
