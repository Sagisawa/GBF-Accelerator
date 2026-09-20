package firewall

import "testing"

func TestRuleName(t *testing.T) {
	tests := []struct {
		port int
		want string
	}{
		{1, "GBF-Accelerator-Port-1"},
		{65535, "GBF-Accelerator-Port-65535"},
		{0, ""},
		{65536, ""},
	}
	for _, tt := range tests {
		if got := RuleName(tt.port); got != tt.want {
			t.Fatalf("RuleName(%d) = %q, want %q", tt.port, got, tt.want)
		}
	}
}

func validRule() RuleInfo {
	return RuleInfo{
		Enabled:       true,
		Direction:     "Inbound",
		Action:        "Allow",
		Protocol:      "TCP",
		LocalPort:     "8124",
		Profile:       []string{"Private"},
		RemoteAddress: []string{"LocalSubnet"},
	}
}

func TestValidateRuleAttributes(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*RuleInfo)
		want bool
	}{
		{"valid", func(r *RuleInfo) {}, true},
		{"port mismatch", func(r *RuleInfo) { r.LocalPort = "8125" }, false},
		{"action mismatch", func(r *RuleInfo) { r.Action = "Block" }, false},
		{"direction mismatch", func(r *RuleInfo) { r.Direction = "Outbound" }, false},
		{"disabled", func(r *RuleInfo) { r.Enabled = false }, false},
		{"public only", func(r *RuleInfo) { r.Profile = []string{"Public"} }, false},
		{"private plus public", func(r *RuleInfo) { r.Profile = []string{"Private", "Public"} }, false},
		{"any remote address", func(r *RuleInfo) { r.RemoteAddress = []string{"Any"} }, false},
		{"local subnet", func(r *RuleInfo) { r.RemoteAddress = []string{"LocalSubnet"} }, true},
		{"local subnet plus any", func(r *RuleInfo) { r.RemoteAddress = []string{"LocalSubnet", "Any"} }, false},
		{"local subnet plus explicit subnet", func(r *RuleInfo) { r.RemoteAddress = []string{"LocalSubnet", "192.168.1.0/24"} }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := validRule()
			tt.mut(&rule)
			if got := ValidateRuleAttributes(rule, 8124); got != tt.want {
				t.Fatalf("ValidateRuleAttributes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateRuleAttributesRejectsInvalidExpectedPort(t *testing.T) {
	if ValidateRuleAttributes(validRule(), 0) {
		t.Fatal("expected invalid expected port to be rejected")
	}
	if ValidateRuleAttributes(validRule(), 65536) {
		t.Fatal("expected invalid expected port to be rejected")
	}
}

func TestNonWindowsUnsupportedContract(t *testing.T) {
	if IsSupported() {
		return
	}
	ok, err := CheckRule(8124)
	if ok || ErrorCode(err) != CodeUnsupported {
		t.Fatalf("CheckRule unsupported result = (%v, %v), want false + %q", ok, err, CodeUnsupported)
	}
	_, err = ApplyRule(8124)
	if ErrorCode(err) != CodeUnsupported {
		t.Fatalf("ApplyRule unsupported error = %v, want %q", err, CodeUnsupported)
	}
}
