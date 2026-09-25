//go:build windows

package firewall

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"
)

const (
	powershellExe = "powershell.exe"
	uacCancelCode = 1223
)

type windowsRuleJSON struct {
	Exists        bool     `json:"exists"`
	Enabled       bool     `json:"enabled"`
	Direction     string   `json:"direction"`
	Action        string   `json:"action"`
	Profile       []string `json:"profile"`
	Protocol      string   `json:"protocol"`
	LocalPort     string   `json:"local_port"`
	RemoteAddress []string `json:"remote_address"`
}

type networkCategoryJSON struct {
	Categories []string `json:"categories"`
}

func platformSupported() bool { return true }

func platformGetStatus(port int) (Status, error) {
	if !validPort(port) {
		return Status{Supported: true, Port: port, RuleName: RuleName(port)}, NewError(CodeInvalidPort, fmt.Errorf("invalid port %d", port))
	}
	status := Status{Supported: true, Port: port, RuleName: RuleName(port)}

	rule, err := readRule(port)
	if err != nil {
		if cats, catErr := readNetworkCategories(); catErr == nil {
			applyCategories(&status, cats)
		}
		return status, err
	}

	status.OK = true
	status.Allowed = rule.Exists && ValidateRuleAttributes(ruleToInfo(rule), port)
	cats, err := readNetworkCategories()
	if err != nil {
		status.OK = false
		return status, err
	}
	applyCategories(&status, cats)
	return status, nil
}

func platformCheckRule(port int) (bool, error) {
	if !validPort(port) {
		return false, NewError(CodeInvalidPort, fmt.Errorf("invalid port %d", port))
	}
	rule, err := readRule(port)
	if err != nil {
		return false, err
	}
	return rule.Exists && ValidateRuleAttributes(ruleToInfo(rule), port), nil
}

func platformApplyRule(port int) (string, error) {
	if !validPort(port) {
		return "", NewError(CodeInvalidPort, fmt.Errorf("invalid port %d", port))
	}

	allowed, err := platformCheckRule(port)
	if err != nil {
		return "", err
	}
	if allowed {
		return CodeAlreadyAllowed, nil
	}

	encoded := encodePowerShell(elevatedApplyScript(port))
	outer := buildElevationScript(encoded)
	cmd := exec.Command(
		powershellExe,
		"-NoProfile",
		"-NonInteractive",
		"-WindowStyle", "Hidden",
		"-ExecutionPolicy", "Bypass",
		"-Command", outer,
	)
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		if exitCode, ok := processExitCode(runErr); ok && exitCode == uacCancelCode {
			return "", NewError(CodeUserCancelled, fmt.Errorf("UAC prompt was cancelled"))
		}
		return "", NewError(CodeOperationFailed, fmt.Errorf("elevated firewall operation failed: %w: %s", runErr, strings.TrimSpace(string(output))))
	}

	allowed, err = platformCheckRule(port)
	if err != nil {
		return "", NewError(CodeVerificationFailed, err)
	}
	if !allowed {
		return "", NewError(CodeVerificationFailed, fmt.Errorf("firewall rule did not pass post-apply verification"))
	}
	return "applied", nil
}

func readRule(port int) (windowsRuleJSON, error) {
	name := RuleName(port)
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$rule = Get-NetFirewallRule -Name '%s' -ErrorAction SilentlyContinue
if ($null -eq $rule) {
  [pscustomobject]@{ exists=$false; enabled=$false; direction=''; action=''; profile=@(); protocol=''; local_port=''; remote_address=@() } | ConvertTo-Json -Compress
  exit 0
}
if ($rule -is [array]) { $rule = $rule | Select-Object -First 1 }
$portFilter = $rule | Get-NetFirewallPortFilter
$addressFilter = $rule | Get-NetFirewallAddressFilter
$profiles = @([string]$rule.Profile -split ',')
$remote = @($addressFilter.RemoteAddress | ForEach-Object { [string]$_ })
[pscustomobject]@{
  exists=$true
  enabled=([string]$rule.Enabled -eq 'True')
  direction=[string]$rule.Direction
  action=[string]$rule.Action
  profile=$profiles
  protocol=[string]$portFilter.Protocol
  local_port=[string]$portFilter.LocalPort
  remote_address=$remote
} | ConvertTo-Json -Compress -Depth 5
`, name)

	out, err := runPowerShell(script)
	if err != nil {
		return windowsRuleJSON{}, err
	}
	var data windowsRuleJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &data); err != nil {
		return windowsRuleJSON{}, NewError(CodeOperationFailed, fmt.Errorf("invalid firewall status JSON: %w", err))
	}
	return data, nil
}

func readNetworkCategories() ([]string, error) {
	script := `
$ErrorActionPreference = 'Stop'
$items = @(Get-NetConnectionProfile | ForEach-Object { [string]$_.NetworkCategory })
[pscustomobject]@{ categories=$items } | ConvertTo-Json -Compress -Depth 3
`
	out, err := runPowerShell(script)
	if err != nil {
		return nil, err
	}
	var data networkCategoryJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &data); err != nil {
		return nil, NewError(CodeOperationFailed, fmt.Errorf("invalid network category JSON: %w", err))
	}
	return data.Categories, nil
}

func runPowerShell(script string) (string, error) {
	encoded := encodePowerShell(script)
	cmd := exec.Command(
		powershellExe,
		"-NoProfile",
		"-NonInteractive",
		"-WindowStyle", "Hidden",
		"-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encoded,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}

	// Keep stdout (the machine-readable JSON protocol) isolated from stderr.
	// Windows PowerShell serializes native error records to stderr as CLIXML
	// beginning with "#< CLIXML". Mixing that stream into stdout makes otherwise
	// valid JSON fail with "invalid character '#'", even when the query produced
	// useful JSON before a non-fatal diagnostic.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		return "", NewError(CodeOperationFailed, fmt.Errorf("PowerShell query failed: %w: %s", err, message))
	}
	return stdout.String(), nil
}

func buildElevationScript(encodedApply string) string {
	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
try {
  $child = Start-Process -FilePath '%s' -Verb RunAs -Wait -PassThru -WindowStyle Hidden -ArgumentList @(
    '-NoProfile',
    '-NonInteractive',
    '-ExecutionPolicy',
    'Bypass',
    '-EncodedCommand',
    '%s'
  )
  if ($null -eq $child) { exit 1 }
  exit ([int]$child.ExitCode)
} catch {
  $native = $null
  try { $native = [int]$_.Exception.NativeErrorCode } catch {}
  if ($native -eq %d) { exit %d }
  $message = [string]$_.Exception.Message
  if ($message -match '(?i)cancel|cancell|operation was canceled|操作已取消') { exit %d }
  Write-Error $message
  exit 1
}
`, powershellExe, encodedApply, uacCancelCode, uacCancelCode, uacCancelCode)
}

func elevatedApplyScript(port int) string {
	name := RuleName(port)
	return fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$name = '%s'
$prefix = 'GBF-Accelerator-Port-*'

Get-NetFirewallRule -Name $prefix -ErrorAction SilentlyContinue |
  Where-Object { $_.Name -ne $name } |
  Remove-NetFirewallRule -ErrorAction Stop

Get-NetFirewallRule -Name $name -ErrorAction SilentlyContinue |
  Remove-NetFirewallRule -ErrorAction Stop

New-NetFirewallRule -Name $name -DisplayName $name -Direction Inbound -Protocol TCP -LocalPort %d -Action Allow -Profile Private -RemoteAddress LocalSubnet -Enabled True -ErrorAction Stop | Out-Null

$rule = Get-NetFirewallRule -Name $name -ErrorAction Stop
$portFilter = $rule | Get-NetFirewallPortFilter
$addressFilter = $rule | Get-NetFirewallAddressFilter
$remote = @($addressFilter.RemoteAddress | ForEach-Object { [string]$_ })
if (-not (
  [string]$rule.Enabled -eq 'True' -and
  [string]$rule.Direction -eq 'Inbound' -and
  [string]$rule.Action -eq 'Allow' -and
  [string]$rule.Profile -match '(?i)(^|,)Private(,|$)' -and
  [string]$portFilter.Protocol -eq 'TCP' -and
  [string]$portFilter.LocalPort -eq '%d' -and
  ($remote -contains 'LocalSubnet') -and
  -not ($remote -contains 'Any')
)) {
  throw 'Created firewall rule failed attribute verification'
}
`, name, port, port)
}

func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		raw = append(raw, byte(unit), byte(unit>>8))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func ruleToInfo(r windowsRuleJSON) RuleInfo {
	return RuleInfo{
		Enabled:       r.Enabled,
		Direction:     r.Direction,
		Action:        r.Action,
		Protocol:      r.Protocol,
		LocalPort:     r.LocalPort,
		Profile:       r.Profile,
		RemoteAddress: r.RemoteAddress,
	}
}

func applyCategories(status *Status, categories []string) {
	seen := make(map[string]bool, len(categories))
	for _, category := range categories {
		category = strings.TrimSpace(category)
		if category == "" || seen[category] {
			continue
		}
		seen[category] = true
		status.NetworkCategories = append(status.NetworkCategories, category)
		if strings.EqualFold(category, "Private") {
			status.HasPrivate = true
		}
		if strings.EqualFold(category, "Public") {
			status.HasPublic = true
		}
		if strings.EqualFold(category, "DomainAuthenticated") {
			status.HasDomain = true
		}
	}
}

func processExitCode(err error) (int, bool) {
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ProcessState == nil {
		return 0, false
	}
	return exitErr.ProcessState.ExitCode(), true
}
