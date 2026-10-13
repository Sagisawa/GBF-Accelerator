import React, { useState, useEffect } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Activity, RefreshCw } from 'lucide-react'
import { testLatency } from '../../api'

export interface LatencyTestModalProps {
  isOpen: boolean
  onClose: () => void
  isDirect: boolean
  upstreamProxy?: string
}

export interface WarmLatencySummary {
  samples: number[]
  min: number
  mid: number
  max: number
}

export function summarizeWarmLatency(
  res:
    | {
        warm_samples?: number[]
        warm_min_ms?: number
        warm_mid_ms?: number
        warm_max_ms?: number
      }
    | null
    | undefined
): WarmLatencySummary {
  const rawSamples = Array.isArray(res?.warm_samples)
    ? res!.warm_samples.filter((s): s is number => typeof s === 'number' && Number.isFinite(s))
    : []
  const sorted = [...rawSamples].sort((a, b) => a - b).map((s) => Math.round(s))
  if (sorted.length === 0) {
    return { samples: [], min: 0, mid: 0, max: 0 }
  }
  const min =
    typeof res?.warm_min_ms === 'number' && Number.isFinite(res.warm_min_ms)
      ? Math.round(res.warm_min_ms)
      : sorted[0]
  const mid =
    typeof res?.warm_mid_ms === 'number' && Number.isFinite(res.warm_mid_ms)
      ? Math.round(res.warm_mid_ms)
      : sorted[Math.floor(sorted.length / 2)]
  const max =
    typeof res?.warm_max_ms === 'number' && Number.isFinite(res.warm_max_ms)
      ? Math.round(res.warm_max_ms)
      : sorted[sorted.length - 1]
  return { samples: sorted, min, mid, max }
}

export const LatencyTestModal: React.FC<LatencyTestModalProps> = ({
  isOpen,
  onClose,
  isDirect,
  upstreamProxy,
}) => {
  const [testing, setTesting] = useState(false)
  const [coldMs, setColdMs] = useState<number | null>(null)
  const [warmSummary, setWarmSummary] = useState<WarmLatencySummary>({
    samples: [],
    min: 0,
    mid: 0,
    max: 0,
  })
  const [errorMsg, setErrorMsg] = useState<string>('')
  const [currentRoute, setCurrentRoute] = useState<string>('')
  const [currentTarget, setCurrentTarget] = useState<string>('https://game.granbluefantasy.jp/')

  const fallbackRouteDesc = isDirect
    ? '直连模式（不经过上游代理）'
    : `经上游代理 ${upstreamProxy || '（未配置）'}`

  const runTest = async () => {
    setTesting(true)
    setErrorMsg('')
    setColdMs(null)
    setWarmSummary({ samples: [], min: 0, mid: 0, max: 0 })

    try {
      const res = await testLatency()
      if (res.target) setCurrentTarget(res.target)
      if (res.route_desc) setCurrentRoute(res.route_desc)
      if (!res.ok) {
        setErrorMsg(res.error || '测试失败')
        return
      }
      setColdMs(res.cold_ms !== undefined ? Math.round(res.cold_ms) : null)
      setWarmSummary(summarizeWarmLatency(res))
    } catch (e: any) {
      setErrorMsg(e.message || '网络请求超时')
    } finally {
      setTesting(false)
    }
  }

  useEffect(() => {
    if (isOpen) {
      setCurrentRoute('')
      runTest()
    }
  }, [isOpen])

  const warmList = warmSummary.samples
  const wMin = warmSummary.min
  const wMid = warmSummary.mid
  const wMax = warmSummary.max

  const getEvalColor = () => {
    if (wMid <= 120) return '#28a745'
    if (wMid <= 220) return '#007bff'
    if (wMid <= 350) return '#e67e22'
    return '#dc3545'
  }

  const getEvalText = () => {
    if (wMid <= 120) return '● 延迟极佳（<120ms），游戏接口响应丝滑'
    if (wMid <= 220) return '● 延迟优良（120~220ms），满足多人战与战斗流畅需求'
    if (wMid <= 350) return '● 延迟尚可（220~350ms），可正常游玩'
    return '● 延迟较高（>350ms），建议检查代理节点或网络线路'
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Activity className="w-4 h-4 text-emerald-600" />
          <span>上游网络延迟测试结果</span>
        </div>
      }
      subtitle="真实测量游戏主站链路 RTT 延迟与建链开销"
      maxWidth="max-w-md"
    >
      <div className="space-y-3.5">
        {/* Card Box */}
        <div className="p-3.5 bg-slate-50 border border-slate-200 rounded-lg space-y-2.5 text-xs">
          <div className="space-y-1 text-slate-600">
            <div className="flex justify-between">
              <span className="font-bold text-slate-800">测试目标：</span>
              <span className="font-mono text-slate-800">{currentTarget}</span>
            </div>
            <div className="flex justify-between">
              <span className="font-bold text-slate-800">路由通道：</span>
              <span className="text-slate-700">{currentRoute || fallbackRouteDesc}</span>
            </div>
          </div>

          <hr className="border-slate-200" />

          {testing ? (
            <div className="py-6 flex flex-col items-center justify-center gap-2 text-slate-500">
              <RefreshCw className="w-5 h-5 animate-spin text-blue-600" />
              <span>正在连通游戏服务器测速中...</span>
            </div>
          ) : errorMsg ? (
            <div className="py-4 text-center text-red-600 space-y-1">
              <div className="font-bold">测试失败</div>
              <div className="text-[11px]">{errorMsg}</div>
            </div>
          ) : (
            <div className="space-y-2">
              <div className="flex justify-between">
                <span className="font-bold text-slate-800">冷连接耗时：</span>
                <span className="font-mono text-slate-800">
                  {coldMs !== null ? `${coldMs} ms (含代理握手与 TLS 建链)` : '未测'}
                </span>
              </div>

              {warmList.length > 0 && (
                <div className="space-y-1.5 pt-1">
                  <div className="flex justify-between">
                    <span className="font-bold text-slate-800">热连接 RTT：</span>
                    <span
                      className="font-bold font-mono"
                      style={{ color: getEvalColor() }}
                    >
                      最快 {wMin} ms ｜ 中位 {wMid} ms ｜ 最慢 {wMax} ms
                    </span>
                  </div>
                  <div
                    className="text-[11px] font-medium pt-1"
                    style={{ color: getEvalColor() }}
                  >
                    {getEvalText()}
                  </div>
                </div>
              )}
            </div>
          )}
        </div>

        <div className="flex items-center justify-between pt-1">
          <Button
            variant="desktop"
            size="sm"
            onClick={runTest}
            disabled={testing}
            icon={<RefreshCw className={`w-3.5 h-3.5 ${testing ? 'animate-spin' : ''}`} />}
          >
            重新测试
          </Button>

          <Button variant="desktop" size="sm" onClick={onClose}>
            确定 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}
