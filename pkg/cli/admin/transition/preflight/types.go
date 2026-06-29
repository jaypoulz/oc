package preflight

import (
	"context"
	"fmt"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

// Validator validates whether a topology transition is possible.
// This interface allows swapping between client-side validation (current)
// and status-published validation (future) without changing command code.
type Validator interface {
	// Validate checks if transition to target topology is possible.
	// Returns validation results for each check and overall availability.
	Validate(ctx context.Context, current, target configv1.TopologyMode) (*ValidationResult, error)
}

// CheckSeverity indicates the severity of a validation check failure.
type CheckSeverity string

const (
	// CheckSeverityError indicates a check failure that blocks the transition.
	// Cannot be bypassed with --allow-transition-with-warnings.
	// Example: Unsupported transition (HA → SNO).
	CheckSeverityError CheckSeverity = "Error"

	// CheckSeverityWarning indicates a check failure that can be bypassed.
	// Can proceed with --allow-transition-with-warnings.
	// Example: Insufficient node count, etcd quorum issues.
	CheckSeverityWarning CheckSeverity = "Warning"
)

// CheckStatus represents the outcome of a validation check.
type CheckStatus string

const (
	// CheckStatusPassed indicates the check succeeded
	CheckStatusPassed CheckStatus = "Passed"
	// CheckStatusFailed indicates the check failed (known issue blocking transition)
	CheckStatusFailed CheckStatus = "Failed"
	// CheckStatusUnknown indicates the check could not complete (API error, etc.)
	CheckStatusUnknown CheckStatus = "Unknown"
)

// ValidationStatus represents the overall outcome of validation.
type ValidationStatus string

const (
	// ValidationStatusAvailable indicates the transition can proceed (all checks passed)
	ValidationStatusAvailable ValidationStatus = "Available"
	// ValidationStatusUnavailable indicates the transition cannot proceed (checks failed)
	ValidationStatusUnavailable ValidationStatus = "Unavailable"
	// ValidationStatusUnknown indicates validation could not complete (API errors, etc.)
	ValidationStatusUnknown ValidationStatus = "Unknown"
)

// ValidationResult contains the outcome of preflight validation checks
// for a topology transition.
type ValidationResult struct {
	// Current topology of the cluster
	Current configv1.TopologyMode

	// Target topology being validated
	Target configv1.TopologyMode

	// Status indicates the overall validation outcome
	Status ValidationStatus

	// Checks contains individual validation check results
	Checks []CheckResult
}

// NewValidationResult creates a new validation result with status initialized to Unknown
func NewValidationResult(current, target configv1.TopologyMode) *ValidationResult {
	return &ValidationResult{
		Current: current,
		Target:  target,
		Status:  ValidationStatusUnknown, // Unknown until checks run
		Checks:  []CheckResult{},
	}
}

// CheckResult represents the outcome of a single validation check.
type CheckResult struct {
	// Name is the human-readable name of the check
	Name string

	// Severity indicates whether a check failure blocks the transition
	Severity CheckSeverity

	// Status indicates whether the check passed, failed, or could not complete
	Status CheckStatus

	// Message provides details about the check result.
	// Empty for passed checks, contains error details for failed/unknown checks.
	Message string
}

// AddCheck appends a check result to the validation result and updates overall status
func (vr *ValidationResult) AddCheck(check CheckResult) {
	vr.Checks = append(vr.Checks, check)

	// Update overall status based on check results
	// Priority: Unknown (API error) > Unavailable (check failed) > Available (all passed)
	switch check.Status {
	case CheckStatusPassed:
		// Set to Available if no failures/unknowns yet (status is still initial Unknown)
		if vr.Status == ValidationStatusUnknown {
			vr.Status = ValidationStatusAvailable
		}
		// else: keep current status (Available/Unavailable/Unknown all stay the same)

	case CheckStatusFailed:
		// Set to Unavailable unless status is already Unknown from a previous API error
		if vr.Status == ValidationStatusUnknown || vr.Status == ValidationStatusAvailable {
			vr.Status = ValidationStatusUnavailable
		}
		// else: keep Unknown status from previous API error

	case CheckStatusUnknown:
		// Unknown (API error) takes precedence over everything
		vr.Status = ValidationStatusUnknown
	}
}

// HasErrorCheckFailures returns true if any Error-severity checks failed or are unknown.
// Error-severity checks cannot be bypassed with --allow-transition-with-warnings.
func (vr *ValidationResult) HasErrorCheckFailures() bool {
	for _, check := range vr.Checks {
		if check.Severity == CheckSeverityError && check.Status != CheckStatusPassed {
			return true
		}
	}
	return false
}

// HasWarningCheckFailures returns true if any Warning-severity checks failed or are unknown.
// Warning-severity checks can be bypassed with --allow-transition-with-warnings.
func (vr *ValidationResult) HasWarningCheckFailures() bool {
	for _, check := range vr.Checks {
		if check.Severity == CheckSeverityWarning && check.Status != CheckStatusPassed {
			return true
		}
	}
	return false
}

// Error returns an aggregate error if the validation status is not Available.
// Returns nil if status is Available.
func (vr *ValidationResult) Error() error {
	if vr.Status == ValidationStatusAvailable {
		return nil
	}

	var errorFailed, errorUnknown []string
	var warningFailed, warningUnknown []string

	for _, check := range vr.Checks {
		var dest *[]string
		switch check.Status {
		case CheckStatusFailed:
			if check.Severity == CheckSeverityError {
				dest = &errorFailed
			} else {
				dest = &warningFailed
			}
		case CheckStatusUnknown:
			if check.Severity == CheckSeverityError {
				dest = &errorUnknown
			} else {
				dest = &warningUnknown
			}
		default:
			continue
		}
		*dest = append(*dest, fmt.Sprintf("%s: %s", check.Name, check.Message))
	}

	var parts []string

	// Error-severity checks listed first (cannot be bypassed)
	if len(errorFailed) > 0 {
		parts = append(parts, fmt.Sprintf("%d error(s):\n  - %s", len(errorFailed), strings.Join(errorFailed, "\n  - ")))
	}
	if len(errorUnknown) > 0 {
		parts = append(parts, fmt.Sprintf("%d error(s) - could not complete:\n  - %s", len(errorUnknown), strings.Join(errorUnknown, "\n  - ")))
	}

	// Warning-severity checks listed second (can be bypassed)
	if len(warningFailed) > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s):\n  - %s", len(warningFailed), strings.Join(warningFailed, "\n  - ")))
	}
	if len(warningUnknown) > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s) - could not complete:\n  - %s", len(warningUnknown), strings.Join(warningUnknown, "\n  - ")))
	}

	if len(parts) == 0 {
		return nil
	}

	return fmt.Errorf("%s", strings.Join(parts, "\n\n"))
}

// String returns a formatted string representation of the check result
func (cr CheckResult) String() string {
	switch cr.Status {
	case CheckStatusPassed:
		return fmt.Sprintf("  %s: passed", cr.Name)
	case CheckStatusFailed:
		return fmt.Sprintf("  %s: %s", cr.Name, cr.Message)
	case CheckStatusUnknown:
		return fmt.Sprintf("  %s: unknown (%s)", cr.Name, cr.Message)
	default:
		return fmt.Sprintf("  %s: %s", cr.Name, cr.Message)
	}
}
