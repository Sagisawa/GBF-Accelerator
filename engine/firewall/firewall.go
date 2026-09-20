package firewall

import (
	"errors"
	"fmt"
	"strings"
)

const (
	CodeAlreadyAllowed          = "already_allowed"
	CodeUserCancelled           = "user_cancelled"
	CodeVerificationFailed      = "verification_failed"
	CodeOperationFailed         = "firewall_operation_failed"
	CodeUnsupported              = "unsupported"
	CodeInvalidPort             = "invalid_port"
)

type Status struct {
	OK                bool     `json:"ok"`
	Supported         bool     `json:"supported"`
	Allowed           bool     `json:"allowed"`
	Port              int      `json:"port"`
	NetworkCategories []string `json:"network_categories"`
	HasPrivate        bool     `json:"has_private"`
	HasPublic         bool     `json:"has_public"`
	HasDomain         bool     `json:"has_domain"`
	RuleName          string   `json:"rule_name"`
}

type RuleInfo struct {
	Enabled       bool
	Direction     string
	Action        string
	Protocol      string
	LocalPort     string
	Profile       []string
	RemoteAddress []string
}

type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Err)
}

func (e *Error) Unwrap() error {
	return e.Err
}

func NewError(code string, err error) error {
	return &Error{Code: code, Err: err}
}

func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var fe *Error
	if errors.As(err, &fe) {
		return fe.Code
	}
	return CodeOperationFailed
}

func validPort(port int) bool {
	return port >= 1 && port <= 65535
}

func RuleName(port int) string {
	if !validPort(port) {
		return ""
	}
	return fmt.Sprintf("GBF-Accelerator-Port-%d", port)
}

func ValidateRuleAttributes(rule RuleInfo, expectedPort int) bool {
	if !validPort(expectedPort) {
		return false
	}
	if !rule.Enabled ||
		!strings.EqualFold(strings.TrimSpace(rule.Direction), "Inbound") ||
		!strings.EqualFold(strings.TrimSpace(rule.Action), "Allow") ||
		!strings.EqualFold(strings.TrimSpace(rule.Protocol), "TCP") ||
		strings.TrimSpace(rule.LocalPort) != fmt.Sprintf("%d", expectedPort) {
		return false
	}

	if len(rule.Profile) != 1 || !strings.EqualFold(strings.TrimSpace(rule.Profile[0]), "Private") {
		return false
	}
	if len(rule.RemoteAddress) != 1 || !strings.EqualFold(strings.TrimSpace(rule.RemoteAddress[0]), "LocalSubnet") {
		return false
	}
	return true
}

func IsSupported() bool {
	return platformSupported()
}

func GetStatus(port int) (Status, error) {
	return platformGetStatus(port)
}

func CheckRule(port int) (bool, error) {
	return platformCheckRule(port)
}

func ApplyRule(port int) (string, error) {
	return platformApplyRule(port)
}
