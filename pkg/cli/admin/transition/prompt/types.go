package prompt

import (
	"io"

	"github.com/openshift/oc/pkg/cli/admin/transition/preflight"
)

// Prompter handles user interaction for topology transitions.
// Abstraction allows testing without actual terminal I/O.
type Prompter interface {
	// PromptForTransition displays validation results and prompts user to confirm transition.
	// Returns true if user confirms, false if user cancels.
	// Behavior depends on flags and validation result:
	//   - If --yes flag: auto-confirm (no prompt)
	//   - If Error-severity failures: display errors, return false (cannot proceed)
	//   - If Warning-severity failures AND --allow-transition-with-warnings: display warnings, auto-confirm
	//   - If Warning-severity failures AND NOT --allow-transition-with-warnings: display warnings, return false
	//   - If all checks passed: display one-way warning, prompt user (unless --yes)
	PromptForTransition(result *preflight.ValidationResult, opts PromptOptions) (bool, error)
}

// PromptOptions configures prompt behavior based on command-line flags
type PromptOptions struct {
	// Yes skips all prompts and auto-confirms (--yes flag)
	Yes bool

	// AllowTransitionWithWarnings bypasses Warning-severity check failures (--allow-transition-with-warnings flag)
	AllowTransitionWithWarnings bool

	// Out is the output writer for displaying validation results and prompts
	Out io.Writer

	// In is the input reader for reading user confirmation (typically os.Stdin)
	In io.Reader
}

// InteractivePrompter implements Prompter using terminal I/O
type InteractivePrompter struct{}

// NewInteractivePrompter creates a new interactive prompter
func NewInteractivePrompter() *InteractivePrompter {
	return &InteractivePrompter{}
}
