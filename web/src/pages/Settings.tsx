import React, { useState } from 'react'
import { RuntimeStatus } from '../types'
import { applyConfig } from '../api'
import { QRCodeSVG } from 'qrcode.react'
import { 
  Smartphone, 
  CheckCircle2, 
  Sliders, 
  Globe
} from 'lucide-react'

interface SettingsProps {
  status: RuntimeStatus | null
  config: Record<string, any>
  onConfigUpdated: () => void
}

export const Settings: React.FC<SettingsProps> = ({ status, config, onConfigUpdated }) => {
  const [upstreamProxy, setUpstreamProxy] = useState<string>(config.upstream_proxy || 'auto')
  const [directMode, setDirectMode] = useState<boolean>(Boolean(config.direct_mode))
  const [allowLan, setAllowLan] = useState<boolean>(Boolean(config.allow_lan))
  const [enablePrefetch, setEnablePrefetch] = useState<boolean>(config.enable_prefetch ?? true)
  const [ramCacheMb, setRamCacheMb] = useState<number>(config.ram_cache_max_mb || 256)
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)

  const handleSave = async () => {
    setBusy(true)
    try {
      await applyConfig({
        upstream_proxy: upstreamProxy,
        direct_mode: directMode,
        allow_lan: allowLan,
        enable_prefetch: enablePrefetch,
        ram_cache_max_mb: Number(ramCacheMb),
      })
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
      onConfigUpdated()
    } catch (e: any) {
      alert(`保存配置失败: ${e.message}`)
    } finally {
      setBusy(false)
    }
  }

  const lanUrl = status?.lan_ip ? `http://${status.lan_ip}:${status.listen_port}/` : null

  return (
    <div className="space-y-6 max-w-4xl">
      {saved && (
        <div className="p-4 bg-emerald-950/60 border border-emerald-800 text-emerald-300 rounded-xl text-sm flex items-center gap-2">
          <CheckCircle2 className="w-4 h-4" />
          <span>配置已成功保存并立即生效！</span>
        </div>
      )}

      {/* 1. Upstream Proxy Settings */}
      <div className="bg-[#131c2e] border border-slate-800 rounded-2xl p-6 space-y-4">
        <h3 className="text-base font-semibold text-white tracking-wide flex items-center gap-2">
          <Globe className="w-4 h-4 text-sky-400" /> 上游网络代理设置
        </h3>

        <div className="space-y-4 pt-2">
          {/* Direct Mode */}
          <div className="flex items-center justify-between p-3 bg-slate-900/60 rounded-xl border border-slate-800">
            <div>
              <div className="text-sm font-medium text-white">直连模式 (Direct Mode)</div>
              <div className="text-xs text-slate-400">绕过上游梯子直接连接日本官方，同时完整享受本地静态资源缓存</div>
            </div>
            <input
              type="checkbox"
              checked={directMode}
              onChange={(e) => setDirectMode(e.target.checked)}
              className="w-5 h-5 rounded bg-slate-800 border-slate-700 text-sky-500 focus:ring-0 cursor-pointer"
            />
          </div>

          {/* Upstream Proxy URL */}
          {!directMode && (
            <div className="space-y-2">
              <label className="text-xs font-medium text-slate-300">上游代理地址 (HTTP / SOCKS5)</label>
              <input
                type="text"
                value={upstreamProxy}
                onChange={(e) => setUpstreamProxy(e.target.value)}
                placeholder="例如: http://127.0.0.1:7897 或 auto"
                className="w-full bg-slate-900 border border-slate-800 rounded-lg px-3 py-2 text-sm text-white font-mono placeholder-slate-500 focus:outline-none focus:border-sky-500"
              />
              <div className="flex flex-wrap gap-2 pt-1 text-xs">
                <span className="text-slate-500 py-1">常用快捷填充:</span>
                {[
                  { name: '自动探测 (auto)', val: 'auto' },
                  { name: 'Clash Verge (7897)', val: 'http://127.0.0.1:7897' },
                  { name: 'Clash 经典 (7890)', val: 'http://127.0.0.1:7890' },
                  { name: 'v2rayN (10808)', val: 'http://127.0.0.1:10808' },
                  { name: '岛风 GO (8099)', val: 'http://127.0.0.1:8099' },
                ].map((item) => (
                  <button
                    key={item.val}
                    type="button"
                    onClick={() => setUpstreamProxy(item.val)}
                    className="px-2.5 py-1 bg-slate-800 hover:bg-slate-700 text-slate-300 rounded-md transition"
                  >
                    {item.name}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* 2. Performance & Cache Settings */}
      <div className="bg-[#131c2e] border border-slate-800 rounded-2xl p-6 space-y-4">
        <h3 className="text-base font-semibold text-white tracking-wide flex items-center gap-2">
          <Sliders className="w-4 h-4 text-emerald-400" /> 性能与缓存参数
        </h3>

        <div className="space-y-4 pt-2">
          {/* Prefetch Toggle */}
          <div className="flex items-center justify-between p-3 bg-slate-900/60 rounded-xl border border-slate-800">
            <div>
              <div className="text-sm font-medium text-white">场景素材智能平滑预加载 (Prefetch)</div>
              <div className="text-xs text-slate-400">在后台异步解析场景引用的素材并带平滑避让预拉取，减少换图黑屏</div>
            </div>
            <input
              type="checkbox"
              checked={enablePrefetch}
              onChange={(e) => setEnablePrefetch(e.target.checked)}
              className="w-5 h-5 rounded bg-slate-800 border-slate-700 text-sky-500 focus:ring-0 cursor-pointer"
            />
          </div>

          {/* RAM Cache Limit */}
          <div className="space-y-2 p-3 bg-slate-900/60 rounded-xl border border-slate-800">
            <div className="flex justify-between items-center">
              <div>
                <div className="text-sm font-medium text-white">RAM 内存热缓存上限</div>
                <div className="text-xs text-slate-400">热点素材保留在物理内存中以实现微秒级零 I/O 极速响应</div>
              </div>
              <span className="text-sm font-bold font-mono text-emerald-400">{ramCacheMb} MB</span>
            </div>
            <input
              type="range"
              min={64}
              max={1024}
              step={64}
              value={ramCacheMb}
              onChange={(e) => setRamCacheMb(Number(e.target.value))}
              className="w-full h-2 bg-slate-800 rounded-lg appearance-none cursor-pointer accent-emerald-500"
            />
          </div>
        </div>
      </div>

      {/* 3. LAN Sharing & Mobile QR Code */}
      <div className="bg-[#131c2e] border border-slate-800 rounded-2xl p-6 space-y-4">
        <h3 className="text-base font-semibold text-white tracking-wide flex items-center gap-2">
          <Smartphone className="w-4 h-4 text-indigo-400" /> 局域网分享与手机/iPad 配置
        </h3>

        <div className="space-y-4 pt-2">
          <div className="flex items-center justify-between p-3 bg-slate-900/60 rounded-xl border border-slate-800">
            <div>
              <div className="text-sm font-medium text-white">允许局域网连接 (Allow LAN)</div>
              <div className="text-xs text-slate-400">将代理服务（端口 8124）开放给同一 Wi-Fi 下的移动设备共享加速</div>
            </div>
            <input
              type="checkbox"
              checked={allowLan}
              onChange={(e) => setAllowLan(e.target.checked)}
              className="w-5 h-5 rounded bg-slate-800 border-slate-700 text-sky-500 focus:ring-0 cursor-pointer"
            />
          </div>

          {allowLan && lanUrl && (
            <div className="p-4 bg-slate-900/80 rounded-xl border border-slate-800 flex flex-col md:flex-row items-center gap-6">
              <div className="bg-white p-3 rounded-xl shrink-0 shadow-lg">
                <QRCodeSVG value={lanUrl} size={130} level="M" />
              </div>
              <div className="space-y-2 text-xs text-slate-300">
                <div className="font-semibold text-sm text-white flex items-center gap-1.5">
                  <span>手机扫码一键配置</span>
                </div>
                <p>
                  1. 确保手机/iPad 已连接到与电脑相同的 Wi-Fi 网络；<br />
                  2. 使用手机相机扫描左侧二维码打开引导页，一键下载并信任加速证书；<br />
                  3. 在手机 Wi-Fi 代理设置中选择【手动】或【自动】，填入对应地址：
                </p>
                <div className="p-2 bg-slate-950 rounded font-mono text-sky-400 select-all">
                  代理服务器: {status?.lan_ip} | 端口: {status?.listen_port}
                </div>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Save Button */}
      <div className="flex justify-end pt-2">
        <button
          onClick={handleSave}
          disabled={busy}
          className="px-6 py-2.5 bg-sky-600 hover:bg-sky-500 text-white font-medium text-sm rounded-xl transition shadow-lg shadow-sky-900/30"
        >
          {busy ? '正在保存...' : '保存并应用更改'}
        </button>
      </div>
    </div>
  )
}
