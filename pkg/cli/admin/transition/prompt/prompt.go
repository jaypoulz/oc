package prompt

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/openshift/oc/pkg/cli/admin/transition/preflight"
)

// PromptForTransition displays validation results and prompts user to confirm transition
func (p *InteractivePrompter) PromptForTransition(result *preflight.ValidationResult, opts PromptOptions) (bool, error) {
	// Display validation results
	if err := displayValidationResults(opts.Out, result); err != nil {
		return false, err
	}

	// Check for Error-severity failures (blocking - cannot proceed)
	if result.HasErrorCheckFailures() {
		fmt.Fprintf(opts.Out, "\nerror: Cannot proceed with transition - see errors listed above\n")
		return false, nil
	}

	// Check for Warning-severity failures
	hasWarnings := result.HasWarningCheckFailures()

	// If warnings exist and --allow-transition-with-warnings is NOT set, block
	if hasWarnings && !opts.AllowTransitionWithWarnings {
		fmt.Fprintf(opts.Out, "\nerror: Cluster is not ready for transition\n")
		fmt.Fprintf(opts.Out, "Use --allow-transition-with-warnings to bypass these warnings (not recommended)\n")
		return false, nil
	}

	// If warnings exist and --allow-transition-with-warnings IS set, warn but continue
	if hasWarnings && opts.AllowTransitionWithWarnings {
		fmt.Fprintf(opts.Out, "\nwarning: Proceeding despite failed preflight checks (--allow-transition-with-warnings)\n")
		fmt.Fprintf(opts.Out, "warning: This may result in cluster instability or transition failure\n\n")
	}

	// All checks passed or warnings bypassed - show one-way transition warning
	if err := displayOneWayWarning(opts.Out, result); err != nil {
		return false, err
	}

	// If --yes flag, auto-confirm
	if opts.Yes {
		fmt.Fprintf(opts.Out, "\nProceeding with transition (--yes)...\n")
		return true, nil
	}

	// Prompt user for confirmation
	return promptConfirmation(opts.Out, opts.In)
}

// displayValidationResults prints the validation results (passed/failed checks)
func displayValidationResults(w io.Writer, result *preflight.ValidationResult) error {
	fmt.Fprintf(w, "\nTopology Transition Validation: %s → %s\n", result.Current, result.Target)
	fmt.Fprintf(w, "Status: %s\n\n", result.Status)

	// Group checks by severity
	var errorChecks, warningChecks []preflight.CheckResult
	for _, check := range result.Checks {
		if check.Severity == preflight.CheckSeverityError {
			errorChecks = append(errorChecks, check)
		} else {
			warningChecks = append(warningChecks, check)
		}
	}

	// Display Error-severity checks first
	if len(errorChecks) > 0 {
		fmt.Fprintf(w, "BLOCKING CHECKS (cannot be bypassed):\n")
		for _, check := range errorChecks {
			fmt.Fprintf(w, "  %s\n", check.String())
		}
		fmt.Fprintf(w, "\n")
	}

	// Display Warning-severity checks
	if len(warningChecks) > 0 {
		fmt.Fprintf(w, "READINESS CHECKS (can be bypassed with --allow-transition-with-warnings):\n")
		for _, check := range warningChecks {
			fmt.Fprintf(w, "  %s\n", check.String())
		}
		fmt.Fprintf(w, "\n")
	}

	return nil
}

// displayOneWayWarning shows the one-way transition warning
func displayOneWayWarning(w io.Writer, result *preflight.ValidationResult) error {
	// Only show for SNO → HA (the only supported one-way transition)
	if result.Current == "SingleReplica" && result.Target == "HighlyAvailable" {
		fmt.Fprintf(w, "WARNING: This is a ONE-WAY transition\n\n")
		fmt.Fprintf(w, "Transitioning from SingleReplica to HighlyAvailable topology is IRREVERSIBLE.\n")
		fmt.Fprintf(w, "You will NOT be able to transition back to SingleReplica after this operation.\n\n")
		fmt.Fprintf(w, "This transition will:\n")
		fmt.Fprintf(w, "  - Scale control plane components from 1 to 3 replicas\n")
		fmt.Fprintf(w, "  - Scale etcd from 1 to 3 voting members\n")
		fmt.Fprintf(w, "  - Require 3 control plane nodes (must exist before transition)\n")
		fmt.Fprintf(w, "  - Take several minutes to complete\n\n")
	}

	return nil
}

// promptConfirmation prompts the user to type "yes" to confirm
func promptConfirmation(w io.Writer, r io.Reader) (bool, error) {
	fmt.Fprintf(w, "Type 'yes' to confirm transition: ")

	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("failed to read input: %w", err)
		}
		// EOF or no input
		fmt.Fprintf(w, "\nTransition cancelled.\n")
		return false, nil
	}

	response := strings.TrimSpace(scanner.Text())
	if strings.ToLower(response) != "yes" {
		fmt.Fprintf(w, "\nTransition cancelled.\n")
		return false, nil
	}

	return true, nil
}
