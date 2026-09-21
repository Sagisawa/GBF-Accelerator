import { RuntimeStatus, TelemetrySummary, CacheStats, PrefetchStatus, LogItem, UpdateInfo } from './types'

const BASE = ''

export async function fetchStatus(): Promise<RuntimeStatus> {
  const res = await fetch(`${BASE}/api/status`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function fetchConfig(): Promise<Record<string, any>> {
  const res = await fetch(`${BASE}/api/config`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const data = await res.json()
  return data.config || {}
}

export interface ApplyConfigResponse {
  ok: boolean
  message?: string
  config?: Record<string, any>
  control_url?: string
}

export async function applyConfig(patch: Record<string, any>): Promise<ApplyConfigResponse> {
  const res = await fetch(`${BASE}/api/config/apply`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(data.error || data.message || `HTTP ${res.status}`)
  }
  const data: ApplyConfigResponse = await res.json()
  if (data.control_url && data.config?.control_port) {
    const targetPort = String(data.config.control_port)
    const currentPort = window.location.port || (window.location.protocol === 'https:' ? '443' : '80')
    if (targetPort !== currentPort && (window.location.hostname === '127.0.0.1' || window.location.hostname === 'localhost')) {
      setTimeout(() => {
        window.location.href = data.control_url!
      }, 500)
    }
  }
  return data
}

export async function toggleProxy(start: boolean): Promise<RuntimeStatus> {
  const endpoint = start ? '/api/proxy/start' : '/api/proxy/stop'
  const res = await fetch(`${BASE}${endpoint}`, { method: 'POST' })
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(data.message || data.error || `HTTP ${res.status}`)
  }
  const data = await res.json()
  if (data.status) return data.status
  return fetchStatus()
}

export async function fetchCacheStats(): Promise<CacheStats> {
  const res = await fetch(`${BASE}/api/cache/stats`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function clearCache(ramOnly = false): Promise<any> {
  const res = await fetch(`${BASE}/api/cache/clear?ram_only=${ramOnly}`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function auditCache(): Promise<any> {
  const res = await fetch(`${BASE}/api/cache/audit`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function slimCache(keep = 8): Promise<any> {
  const res = await fetch(`${BASE}/api/cache/slim?keep=${keep}`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function fetchPrefetchStatus(): Promise<PrefetchStatus> {
  const res = await fetch(`${BASE}/api/prefetch/status`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function fetchTelemetry(): Promise<TelemetrySummary> {
  const res = await fetch(`${BASE}/api/telemetry`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const data = await res.json()
  return data.telemetry
}

export async function fetchLogs(): Promise<LogItem[]> {
  const res = await fetch(`${BASE}/api/logs`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const data = await res.json()
  return data.logs || []
}

export async function openCacheFolder(): Promise<void> {
  const res = await fetch(`${BASE}/api/cache/open-folder`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
}

export async function browseDirectory(): Promise<string> {
  const res = await fetch(`${BASE}/api/utils/browse-dir`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const data = await res.json()
  return data.path || ''
}

export async function testLatency(proxy?: string, fast: boolean = false): Promise<any> {
  const params = new URLSearchParams()
  if (proxy) params.set('proxy', proxy)
  if (fast) params.set('fast', '1')
  const qs = params.toString()
  const url = qs ? `${BASE}/api/latency-test?${qs}` : `${BASE}/api/latency-test`
  const res = await fetch(url, { cache: 'no-store' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function detectUpstream(): Promise<any> {
  const res = await fetch(`${BASE}/api/upstream/detect`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function detectACGPower(): Promise<any> {
  const res = await fetch(`${BASE}/api/cache/detect-acgpower`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export interface FirewallStatus {
  ok: boolean
  supported: boolean
  allowed: boolean
  port: number
  network_categories: string[]
  has_private: boolean
  has_public: boolean
  has_domain: boolean
  rule_name: string
}

export class APIError extends Error {
  code?: string

  constructor(message: string, code?: string) {
    super(message)
    this.name = 'APIError'
    this.code = code
  }
}

export async function fetchFirewallStatus(): Promise<FirewallStatus> {
  const res = await fetch(`${BASE}/api/firewall/status`, { cache: 'no-store' })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new APIError(data.message || data.error || `HTTP ${res.status}`, data.code)
  }
  return data
}

export async function applyFirewallRule(): Promise<any> {
  const res = await fetch(`${BASE}/api/firewall/apply`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new APIError(data.message || data.error || `HTTP ${res.status}`, data.code)
  }
  return data
}

export async function fetchCertStatus(): Promise<any> {
  const res = await fetch(`${BASE}/api/cert/status`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function installCert(): Promise<any> {
  const res = await fetch(`${BASE}/api/cert/install`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function uninstallCert(): Promise<any> {
  const res = await fetch(`${BASE}/api/cert/uninstall`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function cleanLegacyCert(): Promise<any> {
  const res = await fetch(`${BASE}/api/cert/clean-legacy`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function fetchStartupStatus(): Promise<any> {
  const res = await fetch(`${BASE}/api/startup/status`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function setStartup(enabled: boolean): Promise<any> {
  const res = await fetch(`${BASE}/api/startup/set`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function toggleSysProxy(enable: boolean): Promise<any> {
  const endpoint = enable ? '/api/sysproxy/enable' : '/api/sysproxy/disable'
  const res = await fetch(`${BASE}${endpoint}`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function fetchCacheTaskStatus(): Promise<any> {
  const res = await fetch(`${BASE}/api/cache/task-status`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function cancelCacheTask(): Promise<any> {
  const res = await fetch(`${BASE}/api/cache/cancel-task`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function checkForUpdate(): Promise<UpdateInfo> {
  const res = await fetch(`${BASE}/api/update/check`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function downloadUpdate(url?: string, dest?: string, sha256?: string, version?: string): Promise<any> {
  const res = await fetch(`${BASE}/api/update/download`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      url: url || '',
      dest: dest || '',
      sha256: sha256 || '',
      version: version || '',
    }),
  })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function applyDownloadedUpdate(): Promise<any> {
  const res = await fetch(`${BASE}/api/update/apply`, { method: 'POST' })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.error || data.message || `HTTP ${res.status}`)
  return data
}

export async function fetchDownloadStatus(): Promise<any> {
  const res = await fetch(`${BASE}/api/update/download-status`)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function cancelDownload(): Promise<any> {
  const res = await fetch(`${BASE}/api/update/download-cancel`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export async function quitApp(): Promise<{ ok: boolean; message?: string }> {
  const res = await fetch(`${BASE}/api/app/quit`, { method: 'POST' })
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    throw new Error(data.message || data.error || `HTTP ${res.status}`)
  }
  return res.json()
}

