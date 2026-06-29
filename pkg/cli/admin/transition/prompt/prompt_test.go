package prompt

/*
================================================================================
PROMPT TESTS
================================================================================

This file tests the user interaction logic for topology transitions.
Tests use in-memory readers/writers to verify prompt behavior without
actual terminal I/O.

--------------------------------------------------------------------------------
TEST COVERAGE AT-A-GLANCE
--------------------------------------------------------------------------------

BLOCKING SCENARIOS (cannot proceed)
  ✅ Error-severity failures present           → Display errors, return false
  ✅ Warning failures, no --allow flag         → Display warnings, return false

BYPASS SCENARIOS (proceed with warnings)
  ✅ Warning failures, --allow flag set        → Display warning message, continue to prompt
  ✓ Warning failures, --allow + --yes         → Display warning message, auto-confirm (covered by AllPass_YesFlag)

AUTO-CONFIRM SCENARIOS
  ✅ All checks pass, --yes flag               → Display results, auto-confirm (no prompt)

USER CONFIRMATION SCENARIOS
  ✅ All checks pass, user types "yes"         → Confirm transition
  ✅ Valid input variations (yes/Yes/YES/yEs)  → Confirm (case-insensitive)
  ✅ All checks pass, user types "no"          → Cancel transition
  ✅ Invalid input (y/yep/sure/empty)          → Cancel transition

DISPLAY FORMATTING
  ✅ Validation results grouped by severity    → Error checks first, then Warning
  ✅ One-way warning displayed for SNO → HA    → Warning box with details
  ✅ No one-way warning for unsupported transitions → Clean output

--------------------------------------------------------------------------------
*/

import (
	"bytes"
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/oc/pkg/cli/admin/transition/preflight"
)

// TestPromptForTransition_ErrorFailures tests Error-severity failures block transition
func TestPromptForTransition_ErrorFailures(t *testing.T) {
	result := preflight.NewValidationResult(
		configv1.HighlyAvailableTopologyMode,
		configv1.SingleReplicaTopologyMode,
	)
	result.AddCheck(preflight.CheckResult{
		Name:     "Supported Transition",
		Severity: preflight.CheckSeverityError,
		Status:   preflight.CheckStatusFailed,
		Message:  "HighlyAvailable to SingleReplica transition is not supported",
	})

	var out bytes.Buffer
	prompter := NewInteractivePrompter()

	confirmed, err := prompter.PromptForTransition(result, PromptOptions{
		Out: &out,
		In:  strings.NewReader(""),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if confirmed {
		t.Error("expected confirmed=false for Error-severity failures")
	}

	output := out.String()
	if !strings.Contains(output, "error: Cannot proceed with transition") {
		t.Errorf("expected error message in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Supported Transition") {
		t.Errorf("expected check name in output, got:\n%s", output)
	}
}

// TestPromptForTransition_WarningFailures_NoBypass tests Warning failures without --allow flag
func TestPromptForTransition_WarningFailures_NoBypass(t *testing.T) {
	result := preflight.NewValidationResult(
		configv1.SingleReplicaTopologyMode,
		configv1.HighlyAvailableTopologyMode,
	)
	// Supported transition passes
	result.AddCheck(preflight.CheckResult{
		Name:     "Supported Transition",
		Severity: preflight.CheckSeverityError,
		Status:   preflight.CheckStatusPassed,
	})
	// But node count fails
	result.AddCheck(preflight.CheckResult{
		Name:     "Control Plane Node Count",
		Severity: preflight.CheckSeverityWarning,
		Status:   preflight.CheckStatusFailed,
		Message:  "need 3, have 1",
	})

	var out bytes.Buffer
	prompter := NewInteractivePrompter()

	confirmed, err := prompter.PromptForTransition(result, PromptOptions{
		Out:                         &out,
		In:                          strings.NewReader(""),
		AllowTransitionWithWarnings: false,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if confirmed {
		t.Error("expected confirmed=false for Warning failures without --allow flag")
	}

	output := out.String()
	if !strings.Contains(output, "error: Cluster is not ready") {
		t.Errorf("expected warning message in output, got:\n%s", output)
	}
	if !strings.Contains(output, "--allow-transition-with-warnings") {
		t.Errorf("expected suggestion for --allow flag in output, got:\n%s", output)
	}
}

// TestPromptForTransition_WarningFailures_WithBypass tests Warning failures with --allow flag
func TestPromptForTransition_WarningFailures_WithBypass(t *testing.T) {
	result := preflight.NewValidationResult(
		configv1.SingleReplicaTopologyMode,
		configv1.HighlyAvailableTopologyMode,
	)
	result.AddCheck(preflight.CheckResult{
		Name:     "Supported Transition",
		Severity: preflight.CheckSeverityError,
		Status:   preflight.CheckStatusPassed,
	})
	result.AddCheck(preflight.CheckResult{
		Name:     "Control Plane Node Count",
		Severity: preflight.CheckSeverityWarning,
		Status:   preflight.CheckStatusFailed,
		Message:  "need 3, have 1",
	})

	var out bytes.Buffer
	prompter := NewInteractivePrompter()

	// User types "yes" to confirm despite warnings
	confirmed, err := prompter.PromptForTransition(result, PromptOptions{
		Out:                         &out,
		In:                          strings.NewReader("yes\n"),
		AllowTransitionWithWarnings: true,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !confirmed {
		t.Error("expected confirmed=true when user types 'yes' with --allow flag")
	}

	output := out.String()
	if !strings.Contains(output, "warning: Proceeding despite failed preflight checks") {
		t.Errorf("expected bypass warning in output, got:\n%s", output)
	}
	if !strings.Contains(output, "ONE-WAY transition") {
		t.Errorf("expected one-way warning in output, got:\n%s", output)
	}
}

// TestPromptForTransition_AllPass_YesFlag tests auto-confirm with --yes
func TestPromptForTransition_AllPass_YesFlag(t *testing.T) {
	result := preflight.NewValidationResult(
		configv1.SingleReplicaTopologyMode,
		configv1.HighlyAvailableTopologyMode,
	)
	result.AddCheck(preflight.CheckResult{
		Name:     "Supported Transition",
		Severity: preflight.CheckSeverityError,
		Status:   preflight.CheckStatusPassed,
	})
	result.AddCheck(preflight.CheckResult{
		Name:     "Control Plane Node Count",
		Severity: preflight.CheckSeverityWarning,
		Status:   preflight.CheckStatusPassed,
	})

	var out bytes.Buffer
	prompter := NewInteractivePrompter()

	confirmed, err := prompter.PromptForTransition(result, PromptOptions{
		Out: &out,
		In:  strings.NewReader(""), // No input needed with --yes
		Yes: true,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !confirmed {
		t.Error("expected confirmed=true with --yes flag")
	}

	output := out.String()
	if !strings.Contains(output, "Proceeding with transition (--yes)") {
		t.Errorf("expected --yes confirmation message, got:\n%s", output)
	}
	if strings.Contains(output, "Type 'yes' to confirm") {
		t.Errorf("should not prompt when --yes flag is set, got:\n%s", output)
	}
}

// TestPromptForTransition_AllPass_UserConfirms tests user typing "yes"
func TestPromptForTransition_AllPass_UserConfirms(t *testing.T) {
	result := preflight.NewValidationResult(
		configv1.SingleReplicaTopologyMode,
		configv1.HighlyAvailableTopologyMode,
	)
	result.AddCheck(preflight.CheckResult{
		Name:     "Supported Transition",
		Severity: preflight.CheckSeverityError,
		Status:   preflight.CheckStatusPassed,
	})

	var out bytes.Buffer
	prompter := NewInteractivePrompter()

	confirmed, err := prompter.PromptForTransition(result, PromptOptions{
		Out: &out,
		In:  strings.NewReader("yes\n"),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !confirmed {
		t.Error("expected confirmed=true when user types 'yes'")
	}

	output := out.String()
	if !strings.Contains(output, "Type 'yes' to confirm") {
		t.Errorf("expected confirmation prompt, got:\n%s", output)
	}
}

// TestPromptForTransition_AllPass_UserCancels tests user typing "no"
func TestPromptForTransition_AllPass_UserCancels(t *testing.T) {
	result := preflight.NewValidationResult(
		configv1.SingleReplicaTopologyMode,
		configv1.HighlyAvailableTopologyMode,
	)
	result.AddCheck(preflight.CheckResult{
		Name:     "Supported Transition",
		Severity: preflight.CheckSeverityError,
		Status:   preflight.CheckStatusPassed,
	})

	var out bytes.Buffer
	prompter := NewInteractivePrompter()

	confirmed, err := prompter.PromptForTransition(result, PromptOptions{
		Out: &out,
		In:  strings.NewReader("no\n"),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if confirmed {
		t.Error("expected confirmed=false when user types 'no'")
	}

	output := out.String()
	if !strings.Contains(output, "Transition cancelled") {
		t.Errorf("expected cancellation message, got:\n%s", output)
	}
}

// TestPromptForTransition_InvalidInput tests user typing invalid input
func TestPromptForTransition_InvalidInput(t *testing.T) {
	result := preflight.NewValidationResult(
		configv1.SingleReplicaTopologyMode,
		configv1.HighlyAvailableTopologyMode,
	)
	result.AddCheck(preflight.CheckResult{
		Name:     "Supported Transition",
		Severity: preflight.CheckSeverityError,
		Status:   preflight.CheckStatusPassed,
	})

	invalidCases := []string{
		"y\n",
		"yep\n",
		"sure\n",
		"no\n",
		"\n",
	}

	for _, input := range invalidCases {
		t.Run(input, func(t *testing.T) {
			var out bytes.Buffer
			prompter := NewInteractivePrompter()

			confirmed, err := prompter.PromptForTransition(result, PromptOptions{
				Out: &out,
				In:  strings.NewReader(input),
			})

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if confirmed {
				t.Errorf("expected confirmed=false for input %q", input)
			}
		})
	}
}

// TestPromptForTransition_ValidInputVariations tests case-insensitive "yes"
func TestPromptForTransition_ValidInputVariations(t *testing.T) {
	result := preflight.NewValidationResult(
		configv1.SingleReplicaTopologyMode,
		configv1.HighlyAvailableTopologyMode,
	)
	result.AddCheck(preflight.CheckResult{
		Name:     "Supported Transition",
		Severity: preflight.CheckSeverityError,
		Status:   preflight.CheckStatusPassed,
	})

	validCases := []string{
		"yes\n",
		"Yes\n",
		"YES\n",
		"yEs\n",
		"  yes  \n", // whitespace trimmed
	}

	for _, input := range validCases {
		t.Run(input, func(t *testing.T) {
			var out bytes.Buffer
			prompter := NewInteractivePrompter()

			confirmed, err := prompter.PromptForTransition(result, PromptOptions{
				Out: &out,
				In:  strings.NewReader(input),
			})

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !confirmed {
				t.Errorf("expected confirmed=true for input %q", input)
			}
		})
	}
}

// TestDisplayValidationResults_GroupsBySeverity tests check grouping in output
func TestDisplayValidationResults_GroupsBySeverity(t *testing.T) {
	result := preflight.NewValidationResult(
		configv1.SingleReplicaTopologyMode,
		configv1.HighlyAvailableTopologyMode,
	)
	result.AddCheck(preflight.CheckResult{
		Name:     "Supported Transition",
		Severity: preflight.CheckSeverityError,
		Status:   preflight.CheckStatusPassed,
	})
	result.AddCheck(preflight.CheckResult{
		Name:     "Control Plane Node Count",
		Severity: preflight.CheckSeverityWarning,
		Status:   preflight.CheckStatusPassed,
	})
	result.AddCheck(preflight.CheckResult{
		Name:     "etcd Quorum",
		Severity: preflight.CheckSeverityWarning,
		Status:   preflight.CheckStatusFailed,
		Message:  "no quorum",
	})

	var out bytes.Buffer
	err := displayValidationResults(&out, result)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()

	// Should show both sections
	if !strings.Contains(output, "BLOCKING CHECKS") {
		t.Errorf("expected 'BLOCKING CHECKS' section, got:\n%s", output)
	}
	if !strings.Contains(output, "READINESS CHECKS") {
		t.Errorf("expected 'READINESS CHECKS' section, got:\n%s", output)
	}

	// Error check should appear before Warning checks in output
	errorIdx := strings.Index(output, "Supported Transition")
	warningIdx := strings.Index(output, "Control Plane Node Count")
	if errorIdx == -1 || warningIdx == -1 || errorIdx > warningIdx {
		t.Errorf("expected Error checks before Warning checks, got:\n%s", output)
	}
}

// TestDisplayOneWayWarning_SNOToHA tests one-way warning display
func TestDisplayOneWayWarning_SNOToHA(t *testing.T) {
	result := preflight.NewValidationResult(
		configv1.SingleReplicaTopologyMode,
		configv1.HighlyAvailableTopologyMode,
	)

	var out bytes.Buffer
	err := displayOneWayWarning(&out, result)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()

	if !strings.Contains(output, "ONE-WAY transition") {
		t.Errorf("expected one-way warning, got:\n%s", output)
	}
	if !strings.Contains(output, "IRREVERSIBLE") {
		t.Errorf("expected irreversibility warning, got:\n%s", output)
	}
	if !strings.Contains(output, "Scale control plane components") {
		t.Errorf("expected transition details, got:\n%s", output)
	}
}

// TestDisplayOneWayWarning_NoWarningForUnsupported tests no warning for unsupported transitions
func TestDisplayOneWayWarning_NoWarningForUnsupported(t *testing.T) {
	result := preflight.NewValidationResult(
		configv1.HighlyAvailableTopologyMode,
		configv1.SingleReplicaTopologyMode,
	)

	var out bytes.Buffer
	err := displayOneWayWarning(&out, result)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()

	if strings.Contains(output, "ONE-WAY") {
		t.Errorf("should not show one-way warning for unsupported transition, got:\n%s", output)
	}
}
