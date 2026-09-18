import React, { useState, useEffect } from 'react'
import { RuntimeStatus } from '../../types'
import { Input } from '../common/Input'
import { Button } from '../common/Button'
import { Switch } from '../common/Switch'
import { Globe, Save } from 'lucide-react'
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
      // Shimakaze GO uses self-signed certificates, so verify_upstream_tls must be inverted
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
    <div className="bg-surface border border-hairline rounded-xl p-5 shadow-specular space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Globe className="w-4 h-4 text-sys-blue" />
          <h3 className="text-sm font-semibold text-label-primary tracking-tight">
            上游代理与链路分流 (Upstream Routing)
          </h3>
        </div>

        <div className="text-xs text-label-secondary font-mono">
          当前链路:{' '}
          <strong className={isDirect ? 'text-sys-green font-medium' : 'text-sys-blue font-medium'}>
            {isDirect ? '直连日本官方 (Direct)' : currentUpstream}
          </strong>
        </div>
      </div>

      {/* Upstream Proxy Input */}
      {!isDirect && (
        <div className="space-y-2">
          <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-2">
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
                variant="primary"
                size="sm"
                onClick={() => handleSaveUpstream()}
                loading={busy}
                icon={<Save className="w-3.5 h-3.5" />}
              >
                保存
              </Button>
            )}
          </div>

          {/* Presets */}
          <div className="flex items-center gap-2 text-xs pt-1">
            <span className="text-label-tertiary">常用填充:</span>
            <div className="flex items-center gap-1.5 flex-wrap">
              {presets.map((p) => (
                <button
                  key={p.val}
                  type="button"
                  onClick={() => {
                    setUpstreamInput(p.val)
                    handleSaveUpstream(p.val)
                  }}
                  className="px-2 py-0.5 rounded text-[11px] font-mono bg-surface-subtle hover:bg-surface-active text-label-secondary hover:text-label-primary border border-hairline transition-colors"
                >
                  {p.name}
                </button>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* Toggles */}
      <div className="pt-2 border-t border-hairline space-y-3">
        <Switch
          checked={isDirect}
          onChange={handleToggleDirect}
          label="直连模式 (Direct Mode)"
          description="绕过上游梯子直接连接日本 Cygames 官方 CDN，享受本地静态资源毫秒缓存"
          color="green"
        />

        <Switch
          checked={!verifyTLS}
          onChange={handleToggleShimakaze}
          label="岛风 GO 兼容模式 (Shimakaze GO Compatible)"
          description="放宽上游 TLS 证书校验以适配岛风自签名证书，避免 502/SSL 握手异常"
          color="blue"
        />
      </div>
    </div>
  )
}
