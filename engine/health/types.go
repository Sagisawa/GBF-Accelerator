package health

import "time"

// StatusLevel represents the health status of a component or the overall system.
type StatusLevel string

const (
	StatusOk      StatusLevel = "ok"
	StatusWarning StatusLevel = "warning"
	StatusError   StatusLevel = "error"
)

// HealthItem represents the health check result of an individual subsystem.
type HealthItem struct {
	Name                 string                 `json:"name"`
	Status               StatusLevel            `json:"status"` // "ok", "warning", "error"
	Code                 string                 `json:"code"`
	Message              string                 `json:"message"`
	Details              map[string]interface{} `json:"details,omitempty"`
	Repairable           bool                   `json:"repairable"`
	RequiresConfirmation bool                   `json:"requires_confirmation"`
	RequiresElevation    bool                   `json:"requires_elevation"`
	Action               string                 `json:"action,omitempty"`
}

// RepairRequest represents the request body for POST /api/health/repair.
type RepairRequest struct {
	Code string `json:"code,omitempty"`
	All  bool   `json:"all,omitempty"`
}

// RepairStepResult represents the outcome of an individual repair operation.
type RepairStepResult struct {
	Code    string `json:"code"`
	Action  string `json:"action"`
	Success bool   `json:"success"`
	Skipped bool   `json:"skipped,omitempty"`
	Status  string `json:"status,omitempty"` // "fixed", "failed", "skipped"
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// RepairResponse represents the response body for POST /api/health/repair.
type RepairResponse struct {
	Success bool               `json:"success"`
	Message string             `json:"message"`
	Results []RepairStepResult `json:"results"`
	Health  Response           `json:"health"`
}

// Summary contains summary statistics for the health check run.
type Summary struct {
	TotalChecks int `json:"total_checks"`
	Passed      int `json:"passed"`
	Warnings    int `json:"warnings"`
	Errors      int `json:"errors"`
}

// CoreHealth represents the core subsystem health checks that determine system readiness.
type CoreHealth struct {
	Core         HealthItem `json:"core"`
	ControlPlane HealthItem `json:"control_plane"`
	DataPlane    HealthItem `json:"data_plane"`
	PAC          HealthItem `json:"pac"`
	RootCA       HealthItem `json:"root_ca"`
	Cache        HealthItem `json:"cache"`
	Config       HealthItem `json:"config"`
	LANFirewall  HealthItem `json:"lan_firewall"`
}

// RecentErrorLog represents an error log extracted from the telemetry buffer.
type RecentErrorLog struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

// RuntimeStatus contains purely informational runtime observations (does not affect Core Health status).
type RuntimeStatus struct {
	UpstreamRoute  map[string]interface{} `json:"upstream_route"`
	APIRetries     int64                  `json:"api_retries"`
	RecentErrors   []RecentErrorLog       `json:"recent_errors"`
	TrafficMetrics map[string]interface{} `json:"traffic_metrics"`
}

// Response is the full response payload for GET /api/health.
type Response struct {
	Status        StatusLevel   `json:"status"` // "ok", "warning", "error"
	Timestamp     time.Time     `json:"timestamp"`
	Summary       Summary       `json:"summary"`
	CoreHealth    CoreHealth    `json:"core_health"`
	RuntimeStatus RuntimeStatus `json:"runtime_status"`
}
