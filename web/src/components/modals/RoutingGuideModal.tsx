import React, { useState } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Globe, Copy, Check } from 'lucide-react'

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
  const [copied, setCopied] = useState(false)
  const pacUrl = `http://127.0.0.1:${listenPort}/proxy.pac`

  const handleCopy = () => {
    navigator.clipboard.writeText(pacUrl)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Globe className="w-4 h-4 text-blue-600" />
          <span>游戏分流与代理配置说明</span>
        </div>
      }
      subtitle="仅对碧蓝幻想游戏流量与静态资源进行加速，不影响其他网页"
      maxWidth="max-w-lg"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-1.5">
          <div className="font-bold text-slate-900 text-sm">方案 1：系统 PAC 自动分流（推荐，免插件）</div>
          <p className="leading-relaxed text-slate-600">
            勾选主界面的「自动配置 Windows 系统 PAC 代理」后，系统网络栈将自动接管 GBF 相关域名，无需在 Chrome/Edge 浏览器中安装任何代理切换扩展插件。
          </p>
          <div className="flex items-center gap-2 pt-1 flex-wrap">
            <span className="font-semibold text-slate-800">PAC 脚本地址：</span>
            <code className="bg-white border border-slate-300 px-2 py-0.5 rounded text-blue-600 font-mono text-[11px] select-all">
              {pacUrl}
            </code>
            <Button
              variant="desktop"
              size="xs"
              onClick={handleCopy}
              icon={copied ? <Check className="w-3 h-3 text-emerald-600" /> : <Copy className="w-3 h-3" />}
            >
              {copied ? '已复制' : '复制'}
            </Button>
          </div>
        </div>

        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-1.5">
          <div className="font-bold text-slate-900 text-sm">方案 2：SwitchyOmega / ZeroOmega 浏览器插件</div>
          <p className="leading-relaxed text-slate-600">
            若你习惯使用浏览器插件管理代理，推荐以下两种配置方式之一：
          </p>
          <div className="text-[11px] text-slate-600 bg-white p-2.5 rounded border border-slate-200 space-y-2 leading-relaxed font-mono">
            <div>
              <strong className="text-slate-900">方式 A（强烈推荐，PAC 情景模式）</strong>：
              <div className="pl-3 pt-0.5 text-slate-600 space-y-0.5 font-sans">
                <div>1. 打开插件设置 → 点击左侧【新建情景模式】 → 选择【PAC 情景模式】；</div>
                <div>2. PAC 网址填入：<code className="text-blue-600 font-mono select-all">{pacUrl}</code>，点击【立即更新】后点击左侧【应用选项】；</div>
                <div>3. 点击浏览器右上角插件图标，直接切换为该情景模式即可（规则全内置、自动同步，绝不漏分片）。</div>
              </div>
            </div>
            <div>
              <strong className="text-slate-900">方式 B（导入备份文件）</strong>：
              <div className="pl-3 pt-0.5 text-slate-600 space-y-0.5 font-sans">
                <div>1. 打开插件设置 → 【导入/导出】 → 导入项目根目录下的 <strong className="font-mono text-slate-900">SwitchyOmega_GBF.bak</strong>；</div>
                <div>2. <strong className="text-amber-700">关键步骤：</strong>导入完成后，<strong>必须在浏览器右上角插件图标处手动切换为【GBF_AutoSwitch】</strong>。</div>
              </div>
            </div>
          </div>
        </div>

        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-1.5">
          <div className="font-bold text-slate-900 text-sm">方案 3：SmartProxy / ZeroOmega 分流（推荐配合 Clash TUN 模式使用）</div>
          <p className="leading-relaxed text-slate-600">
            当电脑开启 Clash TUN 模式接管虚拟网卡时，TUN 会与系统 PAC 产生路由竞争。按以下 4 步在 SmartProxy 插件中配置专属分流即可完美共存：
          </p>
          <div className="text-[11px] text-slate-600 bg-white p-2.5 rounded border border-slate-200 space-y-1.5 leading-relaxed font-mono">
            <div><strong className="text-slate-900">1. 添加代理目标</strong>：在 SmartProxy 设置 → Proxies → 添加 HTTP 代理：<code className="text-blue-600">127.0.0.1:{listenPort}</code>，命名为 <code className="text-slate-800 font-bold">GbfAccelerator</code>。</div>
            <div><strong className="text-slate-900">2. 默认代理策略</strong>：在 Website Rules（网站规则）中，将 <strong className="text-slate-800">Default proxy（默认代理）</strong> 设为 <strong className="text-emerald-700">Direct（直连）</strong>（由 Clash TUN 负责接管非游戏流量）。</div>
            <div><strong className="text-slate-900">3. 添加 GBF 专属规则</strong>（代理选择 <code className="text-blue-600">GbfAccelerator</code>）：
              <div className="pl-3 pt-0.5 text-slate-500 space-y-0.5">
                <div>• granbluefantasy.jp</div>
                <div>• granbluefantasy.com</div>
                <div>• mbga.jp</div>
                <div>• mobage.jp</div>
                <div>• *granbluefantasy.akamaized.net</div>
                <div>• *gbf.akamaized.net</div>
                <div>• *granbluefantasy-steam.akamaized.net（Steam版）</div>
              </div>
            </div>
            <div><strong className="text-slate-900">4. 启用规则</strong>：将 SmartProxy 扩展图标切换为 <strong className="text-blue-700">Smart Mode（智能分流模式）</strong> 即可。</div>
          </div>
        </div>

        <div className="p-3 bg-blue-50/70 border border-blue-200 rounded-lg space-y-1">
          <div className="font-bold text-blue-900 text-xs">安全与隐私提示</div>
          <p className="text-[11px] text-blue-800 leading-relaxed">
            本项目仅对 Cygames 静态资源 CDN 进行本地镜像加速与缓存，所有账号鉴权与动态 API 均以原生网络安全穿透转发，不接触或修改任何游戏业务数据。
          </p>
        </div>

        <div className="flex justify-end pt-1">
          <Button variant="desktop" size="sm" onClick={onClose}>
            我知道了 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}
