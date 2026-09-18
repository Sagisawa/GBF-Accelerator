import React, { useState, useEffect } from 'react'
import { RuntimeStatus } from '../../types'
import { Input } from '../common/Input'
import { Button } from '../common/Button'
import { Switch } from '../common/Switch'
import { Globe, Save, Zap, ShieldCheck } from 'lucide-react'
import { applyConfig } from '../../api'

export interface UpstreamDeckProps {
  status: RuntimeStatus | null
  config: Record<string, any>
  onConfigUpdated: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
}

export const UpstreamDeck: React.FC<UpstreamDeckProps> = ({
  status,
  config,
  onConfigUpdated,
  onToast,
}) => {
  const currentUpstream = config.upstream_proxy || status?.upstream_proxy || 'auto'
  const isDirect = Boolean(config.direct_mode ?? status?.direct_mode)
  const verifyTLS = config.verify_upstream_tls ?? true

  const [upstreamInput, setUpstreamInput] = useState<string>(currentUpstream)
  const [busy, setBusy] = useState<boolean>(false)

  useEffect(() => {
    if (currentUpstream && !upstreamInput) {
      setUpstreamInput(currentUpstream)
    }
  }, [currentUpstream])

  const isDirty = upstreamInput.trim() !== currentUpstream.trim() && upstreamInput.trim() !== ''

  const handleSaveUpstream = async (newVal?: string) => {
    const val = newVal ?? upstreamInput.trim()
    if (!val) return
    setBusy(true)
    try {
      await applyConfig({ upstream_proxy: val })
      onToast(`上游代理已更新: ${val}`, 'success')
      onConfigUpdated()
    } catch (e: any) {
      onToast(`保存失败: ${e.message}`, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleToggleDirect = async (checked: boolean) => {
    try {
      await applyConfig({ direct_mode: checked })
      onToast(checked ? '直连模式已启用 (直通官方CDN)' : '直连模式已关闭 (走上游代理)', 'info')
      onConfigUpdated()
    } catch (e: any) {
      onToast(`切换直连模式失败: ${e.message}`, 'error')
    }
  }

  const handleToggleShimakaze = async (checked: boolean) => {
    try {
      await applyConfig({ verify_upstream_tls: !checked })
      onToast(checked ? '岛风GO兼容模式已开启 (放宽TLS强校验)' : '岛风GO兼容模式已关闭 (开启标准TLS校验)', 'info')
      onConfigUpdated()
    } catch (e: any) {
      onToast(`切换兼容模式失败: ${e.message}`, 'error')
    }
  }

  const presets = [
    { name: '自动探测 (auto)', val: 'auto' },
    { name: 'Clash (7897)', val: 'http://127.0.0.1:7897' },
    { name: 'Clash (7890)', val: 'http://127.0.0.1:7890' },
    { name: 'v2rayN (10808)', val: 'http://127.0.0.1:10808' },
    { name: '岛风 GO (8099)', val: 'http://127.0.0.1:8099' },
  ]

  return (
    <section className="space-y-2 select-none">
      <div className="flex items-center justify-between px-1">
        <h3 className="text-[11px] font-semibold text-label-secondary uppercase tracking-wider">
          网络与上游代理
        </h3>
        <span className="text-[11px] font-mono text-label-secondary">
          链路:{' '}
          <strong className={isDirect ? 'text-apple-green font-medium' : 'text-apple-blue font-medium'}>
            {isDirect ? '日本官方直连 (Direct)' : (currentUpstream === 'auto' ? '自动分流 (auto)' : currentUpstream)}
          </strong>
        </span>
      </div>

      {/* Apple Inset Grouped Container */}
      <div className="bg-[#1c1c1e] border border-white/[0.08] rounded-2xl divide-y divide-white/[0.06] overflow-hidden">
        {/* Row 1: Direct Mode Switch */}
        <div className="px-4 sm:px-5 py-3.5 flex items-center justify-between gap-4">
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-xl bg-apple-green/15 text-apple-green flex items-center justify-center shrink-0">
              <Zap className="w-4 h-4" />
            </div>
            <div>
              <div className="text-sm font-medium text-white tracking-tight">
                直连模式 (Direct Mode)
              </div>
              <div className="text-xs text-label-secondary mt-0.5">
                绕过上游梯子直接连接日本 Cygames 官方 CDN，享受本地静态资源微秒缓存
              </div>
            </div>
          </div>

          <Switch
            checked={isDirect}
            onChange={handleToggleDirect}
            color="green"
          />
        </div>

        {/* Row 2: Upstream Proxy Input (when not in direct mode) */}
        {!isDirect && (
          <div className="p-4 sm:p-5 space-y-3">
            <div className="flex items-center gap-2.5">
              <div className="w-7 h-7 rounded-xl bg-apple-blue/15 text-apple-blue flex items-center justify-center shrink-0">
                <Globe className="w-4 h-4" />
              </div>
              <div>
                <div className="text-sm font-medium text-white tracking-tight">
                  上游代理服务器
                </div>
                <div className="text-xs text-label-secondary mt-0.5">
                  填写 Clash / v2rayN / 岛风GO 的本地代理监听地址 (支持 HTTP 与 SOCKS5)
                </div>
              </div>
            </div>

            <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-2 pt-1">
              <div className="flex-1">
                <Input
                  value={upstreamInput}
                  onChange={(e) => setUpstreamInput(e.target.value)}
                  placeholder="例如: http://127.0.0.1:7897 或 auto"
                  mono
                />
              </div>

              {isDirty && (
                <Button
                  variant="apple"
                  size="sm"
                  onClick={() => handleSaveUpstream()}
                  loading={busy}
                  icon={<Save className="w-3.5 h-3.5" />}
                >
                  保存
                </Button>
              )}
            </div>
          </div>
        )}

        {/* Row 3: Upstream Presets (when not in direct mode) */}
        {!isDirect && (
          <div className="px-4 sm:px-5 py-3.5 flex flex-col sm:flex-row sm:items-center justify-between gap-2.5">
            <div className="text-xs text-label-secondary">
              常用代理客户端快速填充
            </div>

            <div className="inline-flex p-1 rounded-xl bg-black/40 border border-white/[0.06] self-start sm:self-auto gap-1 flex-wrap">
              {presets.map((p) => {
                const isSelected = upstreamInput === p.val
                return (
                  <button
                    key={p.val}
                    type="button"
                    onClick={() => {
                      setUpstreamInput(p.val)
                      handleSaveUpstream(p.val)
                    }}
                    className={`px-3 py-1 rounded-lg text-xs font-medium transition-all ${
                      isSelected
                        ? 'bg-white/[0.14] text-white shadow-sm font-semibold'
                        : 'text-label-secondary hover:text-white'
                    }`}
                  >
                    {p.name}
                  </button>
                )
              })}
            </div>
          </div>
        )}

        {/* Row 4: Shimakaze GO Compatible Mode */}
        <div className="px-4 sm:px-5 py-3.5 flex items-center justify-between gap-4">
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-xl bg-apple-amber/15 text-apple-amber flex items-center justify-center shrink-0">
              <ShieldCheck className="w-4 h-4" />
            </div>
            <div>
              <div className="text-sm font-medium text-white tracking-tight">
                岛风 GO 兼容模式
              </div>
              <div className="text-xs text-label-secondary mt-0.5">
                放宽上游 TLS 证书校验以适配岛风自签名证书，杜绝 502 / SSL 握手异常
              </div>
            </div>
          </div>

          <Switch
            checked={!verifyTLS}
            onChange={handleToggleShimakaze}
            color="blue"
          />
        </div>
      </div>
    </section>
  )
}
