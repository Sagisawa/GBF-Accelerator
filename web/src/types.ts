export interface RuntimeStatus {
  version: string;
  platform?: string;
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

export interface UpstreamRuntimeStatus {
  enabled: boolean;
  backup_configured: boolean;
  active: 'primary' | 'backup';
  failure_count: number;
  last_switch_at?: string;
  reason?: string;
  auto_recover: boolean;
  threshold_ms: number;
  consecutive_failures: number;
  cooldown_seconds: number;
}

export interface AndroidComponentItem {
  id: string;
  file_name: string;
  path?: string;
  installed: boolean;
  verified: boolean;
  size: number;
  expected_sha256: string;
  actual_sha256?: string;
  error?: string;
}

export interface AndroidComponentDownloadProgress {
  active: boolean;
  current_file?: string;
  file_index?: number;
  total_files?: number;
  downloaded_bytes?: number;
  total_bytes?: number;
  percent: number;
  speed_bytes_sec?: number;
  stage: string;
  error?: string;
  done?: boolean;
}

export interface AndroidEnvStatus {
  ok: boolean;
  ready: boolean;
  full_env_ready?: boolean;
  tools_disk_bytes?: number;
  backup_dir?: string;
  components_installed: boolean;
  components_verified: boolean;
  components_corrupted: boolean;
  components_error?: string;
  tools_dir: string;
  components?: AndroidComponentItem[];
  download?: AndroidComponentDownloadProgress;
  java: {
    found: boolean;
    path: string;
    version: string;
    error: string;
  };
  lspatch: {
    found: boolean;
    path: string;
    version: string;
    sha256: string;
    expected_sha256: string;
    verified: boolean;
    error: string;
  };
  module: {
    found: boolean;
    path: string;
    verified: boolean;
    error: string;
  };
  adb?: {
    found: boolean;
    path: string;
    version: string;
    error: string;
  };
}

export interface AndroidPackageInspection {
  ok: boolean;
  file_path: string;
  base_input_name: string;
  package_name: string;
  version_name: string;
  is_split: boolean;
  total_apks: number;
  is_official_skyleap: boolean;
  is_system_webview?: boolean;
  engine_desc?: string;
  unsupported_reason?: string;
  suggested_clone_package?: string;
  error?: string;
}

export interface AndroidPatchResult {
  is_split?: boolean;
  single_apk?: string;
  split_dir?: string;
  apks_archive?: string;
  output_dir?: string;
  total_apks?: number;
  total_bytes?: number;
  package_files?: string[];
  backup_path?: string;
  backup_dir?: string;
  IsSplit?: boolean;
  SingleApk?: string;
  SplitDir?: string;
  ApksArchive?: string;
  OutputDir?: string;
  TotalApks?: number;
  TotalBytes?: number;
  PackageFiles?: string[];
  BackupPath?: string;
  BackupDir?: string;
}

export interface AndroidPatchProgress {
  ok: boolean;
  running: boolean;
  stage: number;
  stage_text: string;
  progress: number;
  logs: string[];
  error: string;
  done: boolean;
  backup_dir?: string;
  result?: AndroidPatchResult | null;
}

export interface AdbDevice {
  serial: string;
  state: string;
  model: string;
  product: string;
}

export interface AdbDevicesResponse {
  ok: boolean;
  adb_found: boolean;
  adb_path?: string;
  devices: AdbDevice[];
  error?: string;
}

export interface AdbProbeAppResponse {
  ok: boolean;
  installed: boolean;
  package_name: string;
  version_name?: string;
  is_split?: boolean;
  total_apks?: number;
  remote_paths?: string[];
  error?: string;
}

export interface AdbInstallResponse {
  ok: boolean;
  message?: string;
  signature_mismatch?: boolean;
  details?: string;
  error?: string;
}

export interface DeviceBrowserItem {
  package_name: string;
  label: string;
  is_installed: boolean;
  is_skyleap: boolean;
  is_system_webview?: boolean;
  engine_desc?: string;
}

export interface AdbListBrowsersResponse {
  ok: boolean;
  browsers: DeviceBrowserItem[];
  error?: string;
}

