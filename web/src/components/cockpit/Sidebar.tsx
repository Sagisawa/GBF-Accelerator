import React from 'react'
import { RuntimeStatus } from '../../types'
import {
  Compass,
  HardDrive,
  Globe,
  Wifi,
  Sliders,
  Terminal,
  Smartphone,
  Trash2,
  Keyboard,
  ShieldCheck,
  Zap,
} from 'lucide-react'

export interface SidebarProps {
  activeSection: string
  onSelectSection: (sectionId: string) => void
  status: RuntimeStatus | null
  config: Record<string, any>
  onToggleLogs: () => void
  onOpenMobileGuide: () => void
  onOpenClearModal: () => void
  onOpenShortcuts: () => void
  logCount: number
}

export const Sidebar: React.FC<SidebarProps> = ({
  activeSection,
  onSelectSection,
  status,
  config,
  onToggleLogs,
  onOpenMobileGuide,
  onOpenClearModal,
  onOpenShortcuts,
  logCount,
}) => {
  const isRunning = Boolean(status?.proxy_running)
  const port = status?.listen_port || 8124
  const isDirect = Boolean(config.direct_mode ?? status?.direct_mode)

  const navItems = [
    { id: 'overview', label: '实时概览', icon: Compass },
    { id: 'cache', label: '存储与缓存', icon: HardDrive },
    { id: 'upstream', label: '上游与网络', icon: Globe },
    { id: 'network', label: '端口与共享', icon: Wifi },
    { id: 'advanced', label: '进阶调优', icon: Sliders },
  ]

  return (
    <aside className="w-60 h-screen bg-[#121214]/95 backdrop-blur-2xl border-r border-white/[0.08] flex flex-col justify-between p-4 shrink-0 select-none hidden lg:flex fixed left-0 top-0 z-30">
      {/* Top Brand & Status */}
      <div className="space-y-5">
        {/* Apple Music Style App Branding */}
        <div className="flex items-center gap-3 px-2 pt-1">
          <div className="w-9 h-9 rounded-xl bg-gradient-to-br from-apple-red via-[#e02448] to-[#990022] shadow-apple flex items-center justify-center text-white shrink-0">
            <Zap className="w-5 h-5 fill-current" />
          </div>
          <div className="flex flex-col min-w-0">
            <div className="flex items-center gap-1.5">
              <span className="font-bold text-white tracking-tight text-sm truncate">
                GBF 加速
              </span>
              <span className="text-[10px] font-mono px-1.5 py-0.2 rounded-full bg-white/[0.08] text-label-secondary border border-white/[0.06]">
                v{status?.version || '2.0'}
              </span>
            </div>
            <span className="text-[11px] text-label-secondary truncate">
              本地静态资源代理
            </span>
          </div>
        </div>

        {/* Status Capsule Pill */}
        <div className="px-3 py-2 rounded-xl bg-white/[0.04] border border-white/[0.06] flex items-center justify-between text-xs">
          <div className="flex items-center gap-2 truncate">
            <span
              className={`w-2 h-2 rounded-full ${
                isRunning ? 'bg-apple-green animate-pulse' : 'bg-label-tertiary'
              }`}
            />
            <span className="font-mono text-label-primary text-[11px] truncate">
              {isRunning ? `127.0.0.1:${port}` : '服务已就绪'}
            </span>
          </div>
          {isRunning && (
            <span className="text-[10px] font-medium text-apple-green px-1.5 py-0.5 rounded-full bg-apple-green/15 shrink-0">
              {isDirect ? '直连' : '代理'}
            </span>
          )}
        </div>

        {/* Primary Navigation Sections */}
        <div className="space-y-1">
          <div className="px-2.5 pb-1 text-[11px] font-semibold text-label-tertiary uppercase tracking-wider">
            控制中心
          </div>
          {navItems.map((item) => {
            const Icon = item.icon
            const isActive = activeSection === item.id
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => onSelectSection(item.id)}
                className={`w-full flex items-center gap-2.5 px-3 py-2 rounded-xl text-xs font-medium transition-all duration-150 text-left ${
                  isActive
                    ? 'bg-apple-red text-white font-semibold shadow-apple'
                    : 'text-label-secondary hover:text-white hover:bg-white/[0.06]'
                }`}
              >
                <Icon className={`w-4 h-4 shrink-0 ${isActive ? 'text-white' : 'text-label-tertiary'}`} />
                <span>{item.label}</span>
              </button>
            )
          })}
        </div>

        {/* Quick Operations (Apple Music Library Style) */}
        <div className="space-y-1 pt-2 border-t border-white/[0.06]">
          <div className="px-2.5 pb-1 text-[11px] font-semibold text-label-tertiary uppercase tracking-wider">
            常用工具
          </div>

          <button
            type="button"
            onClick={onToggleLogs}
            className="w-full flex items-center justify-between px-3 py-2 rounded-xl text-xs text-label-secondary hover:text-white hover:bg-white/[0.06] transition-colors"
          >
            <div className="flex items-center gap-2.5">
              <Terminal className="w-4 h-4 text-apple-blue shrink-0" />
              <span>运行终端</span>
            </div>
            {logCount > 0 && (
              <span className="px-1.5 py-0.5 min-w-[18px] text-[10px] font-mono font-bold rounded-full bg-white/[0.12] text-label-primary text-center">
                {logCount > 99 ? '99+' : logCount}
              </span>
            )}
          </button>

          <button
            type="button"
            onClick={onOpenMobileGuide}
            className="w-full flex items-center gap-2.5 px-3 py-2 rounded-xl text-xs text-label-secondary hover:text-white hover:bg-white/[0.06] transition-colors"
          >
            <Smartphone className="w-4 h-4 text-apple-green shrink-0" />
            <span>移动端扫码</span>
          </button>

          <button
            type="button"
            onClick={onOpenClearModal}
            className="w-full flex items-center gap-2.5 px-3 py-2 rounded-xl text-xs text-label-secondary hover:text-apple-red hover:bg-white/[0.06] transition-colors"
          >
            <Trash2 className="w-4 h-4 text-apple-red shrink-0" />
            <span>清空缓存</span>
          </button>

          <button
            type="button"
            onClick={onOpenShortcuts}
            className="w-full flex items-center gap-2.5 px-3 py-2 rounded-xl text-xs text-label-secondary hover:text-white hover:bg-white/[0.06] transition-colors"
          >
            <Keyboard className="w-4 h-4 text-label-tertiary shrink-0" />
            <span>快捷键指南</span>
          </button>
        </div>
      </div>

      {/* Sidebar Footer: CA & System Meta */}
      <div className="pt-4 border-t border-white/[0.06] px-2 space-y-2">
        <div className="flex items-center gap-2 text-[11px] text-apple-green">
          <ShieldCheck className="w-3.5 h-3.5 shrink-0" />
          <span className="truncate">CA 根证书内建就绪</span>
        </div>
        <div className="text-[10px] text-label-tertiary leading-tight">
          GBF-Accelerator · 高性能代理
        </div>
      </div>
    </aside>
  )
}
