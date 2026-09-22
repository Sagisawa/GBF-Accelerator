export interface RuntimeStatus {
  version: string;
  proxy_running: boolean;
  listen_host: string;
  listen_port: number;
  control_port: number;
  upstream_proxy: string;
  direct_mode: boolean;
  allow_lan: boolean;
  lan_ip?: string | null;
  ca_installed?: boolean;
  ca_thumbprint?: string;
  ca_fingerprint?: string;
  system_proxy_enabled?: boolean;
  system_proxy_conflict?: string | null;
  startup_enabled?: boolean;
  startup_supported?: boolean;
  is_auditing_cache?: boolean;
  is_slimming_cache?: boolean;
  cache_dir?: string;
  active_api_count: number;
  active_foreground_assets: number;
  uptime_seconds: number;
  requests: {
    total_apis: number;
    total_assets: number;
    total_hits: number;
    ram_hits: number;
    disk_hits: number;
    cache_misses: number;
    prefetch_requests: number;
    prefetch_reused: number;
    api_retries: number;
  };
  cache: {
    ram_items: number;
    ram_mb: number;
    cache_dir: string;
  };
  telemetry?: TelemetrySummary;
  last_error: string;
}


export interface TelemetrySummary {
  enabled: boolean;
  total_requests: number;
  reused_connections: number;
  new_connections: number;
  reuse_rate_percent: number;
  retry_count: number;
  percentiles: {
    p50_ms: number;
    p95_ms: number;
    p99_ms: number;
    avg_ms: number;
    min_ms: number;
    max_ms: number;
    samples: number;
  };
  protocols: Record<string, number>;
  exceptions: Record<string, number>;
}

export interface CacheStats {
  ok: boolean;
  cache_base: string;
  ram_items: number;
  ram_bytes: number;
  ram_mb: number;
  ram_max_mb: number;
  hits_total: number;
  hits_ram: number;
  hits_disk: number;
  misses: number;
  hit_ratio_percent: number;
}

export interface PrefetchStatus {
  ok: boolean;
  enabled: boolean;
  queue_size: number;
  is_yielding: boolean;
  active_api_count: number;
  prefetch_requests: number;
  prefetch_successes: number;
  prefetch_reused: number;
}

export interface LogItem {
  time: string;
  level: string;
  msg: string;
}

export interface TarouIntegrationStatus {
  id: string;
  name: string;
  enabled: boolean;
  connected: boolean;
  protocol_version: number;
  supported_protocol_version: number;
  extension_version?: string;
  last_seen_at?: string;
  capabilities: string[];
  source_url: string;
  author: string;
  message: string;
}

export interface UpdateInfo {
  has_update: boolean;
  latest_version: string;
  current_version: string;
  release_title?: string;
  release_notes?: string;
  html_url?: string;
  release_url?: string;
  download_url?: string;
  asset_download_url?: string;
  published_at?: string;
  sha256?: string;
  error?: string;
}

export interface UpdateDownloadStatus {
  ok: boolean;
  active: boolean;
  done: boolean;
  downloaded: number;
  total: number;
  percent: number;
  dest: string;
  version: string;
  sha256: string;
  managed: boolean;
  applying: boolean;
  error: string;
}

export interface ProxyCandidate {
  url: string;
  name: string;
}

