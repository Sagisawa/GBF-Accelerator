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
            若你习惯使用浏览器插件管理代理，可使用 ZeroOmega / SwitchyOmega 导入项目根目录下的 <strong className="font-mono text-slate-900">SwitchyOmega_GBF.bak</strong> 配置文件。
          </p>
          <div className="text-[11px] text-slate-500 bg-white p-2 rounded border border-slate-200 space-y-1 font-mono">
            <div>• 代理协议：HTTP 127.0.0.1:{listenPort}</div>
            <div>• 匹配域名：*.granbluefantasy.jp、*.mbga.jp、prd-game-a-granbluefantasy.akamaized.net</div>
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
