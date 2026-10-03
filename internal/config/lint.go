// Package config provides enterprise-grade capabilities, configuration, and structural components for the config subsystem.
package config

import (
	"fmt"
	"net/url"
	"strings"
)

// Severity identifies the impact level of a configuration validation finding.
type Severity string

const (
	// SeverityError defines a specific variation or structural setting for SeverityError.
	SeverityError Severity = "error"
	// SeverityWarning defines a specific variation or structural setting for SeverityWarning.
	SeverityWarning Severity = "warning"
)

// LintIssue represents a discrete configuration defect, describing the violated
// rule, its severity, and a human-readable remediation message.
type LintIssue struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// LintOpts configure the strictness of the policy linting engine.
type LintOpts struct {
	Strict bool // when true, treat warnings as errors
}

// Lint inspects a parsed Config and reports semantic, calibration, security, and coverage issues.
//
// Validation covers:
// 1. Syntactic/Base constraints (c.validate)
// 2. Cache calibration constraints (e.g. enabling cache without tuning)
// 3. Pricing coverage constraints for budget tracking
// 4. Admin exposure risks (e.g. unauthenticated non-loopback exposure)
// 5. Insecure transport (HTTP over WAN)
func Lint(c *Config, opts ...LintOpts) []LintIssue {
	if c == nil {
		return nil
	}
	strict := len(opts) > 0 && opts[0].Strict

	var issues []LintIssue
	add := func(rule string, sev Severity, format string, a ...any) {
		if strict && sev == SeverityWarning {
			sev = SeverityError
		}
		issues = append(issues, LintIssue{
			Rule:     rule,
			Severity: sev,
			Message:  fmt.Sprintf(format, a...),
		})
	}

	// 1. Validation check
	if err := c.validate(); err != nil {
		add("config/valid", SeverityError, "validation error: %v", err)
	}

	// 2. Cache calibration checks (C7)
	minThreshold := c.Proof.MinThreshold
	if minThreshold <= 0 {
		minThreshold = 0.86
	}

	for _, r := range c.Routes {
		cc := r.Cache
		if cc.Mode == "on" && !cc.AcknowledgeUncalibrated {
			add("cache/calibration", SeverityError,
				"route %q has cache.mode: 'on' without calibration. Calibrate route quality or set cache.acknowledge_uncalibrated: true", r.Name)
		}
		if cc.Semantic && cc.Threshold > 0 && cc.Threshold < minThreshold {
			add("cache/threshold", SeverityWarning,
				"route %q semantic threshold %.2f is below the calibrated floor of %.2f", r.Name, cc.Threshold, minThreshold)
		}
	}

	// 3. Pricing coverage check (C5)
	if c.Budget.IsRequirePricing() {
		for _, r := range c.Routes {
			for _, t := range r.Targets {
				targetKey := t.Provider + "/" + t.Model
				if _, ok := c.Pricing[targetKey]; !ok {
					add("budget/pricing_coverage", SeverityError,
						"route %q target %q lacks pricing in config under require_pricing", r.Name, targetKey)
				}
			}
			if r.SmartRoute.Mode != "off" && r.SmartRoute.Mode != "" {
				if r.SmartRoute.CheapTarget != "" {
					if _, ok := c.Pricing[r.SmartRoute.CheapTarget]; !ok {
						add("budget/pricing_coverage", SeverityError,
							"route %q smart_route cheap_target %q lacks pricing in config", r.Name, r.SmartRoute.CheapTarget)
					}
				}
				if r.SmartRoute.StrongTarget != "" {
					if _, ok := c.Pricing[r.SmartRoute.StrongTarget]; !ok {
						add("budget/pricing_coverage", SeverityError,
							"route %q smart_route strong_target %q lacks pricing in config", r.Name, r.SmartRoute.StrongTarget)
					}
				}
			}
		}
	}

	// 4. Admin exposure check (C13)
	adminAddr := c.Server.AdminAddr
	if adminAddr != "" {
		if !IsLoopbackAddr(adminAddr) && !c.AdminAuth.Enabled {
			add("admin/exposure", SeverityError,
				"server.admin_addr %q is non-loopback but admin_auth.enabled is false. Unauthenticated admin APIs must not be exposed.", adminAddr)
		}
	}

	// 5. Provider base_url transport security check (R13)
	for _, p := range c.Providers {
		u, err := url.Parse(p.BaseURL)
		if err == nil && u.Scheme == "http" {
			h := u.Hostname()
			isLoop := h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".local") || strings.Contains(h, "docker") || strings.HasPrefix(p.Name, "mock") || strings.Contains(h, "ollama") || !strings.Contains(h, ".")
			if !isLoop && !p.AllowInsecureBaseURL {
				add("provider/tls", SeverityWarning,
					"provider %q base_url %q uses unencrypted http to an external host", p.Name, p.BaseURL)
			}
		}
	}

	return issues
}
