package firewall

import (
	"fmt"
	"regexp"
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
	if ok := AsError(err, &fe); ok {
		return fe.Code
	}
	return CodeOperationFailed
}

func AsError(err error, target **Error) bool {
	if err == nil {
		return false
	}
	for cur := err; cur != nil; {
		if fe, ok := cur.(*Error); ok {
			*target = fe
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := cur.(unwrapper)
		if !ok {
			return false
		}
		cur = u.Unwrap()
	}
	return false
}

var _ = regexp.MustCompile

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

	hasPrivate := false
	for _, profile := range rule.Profile {
		if strings.EqualFold(strings.TrimSpace(profile), "Private") {
			hasPrivate = true
			break
		}
	}
	if !hasPrivate {
		return false
	}

	hasLocalSubnet := false
	for _, address := range rule.RemoteAddress {
		address = strings.TrimSpace(address)
		if strings.EqualFold(address, "Any") {
			return false
		}
		if strings.EqualFold(address, "LocalSubnet") {
			hasLocalSubnet = true
		}
	}
	return hasLocalSubnet
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
