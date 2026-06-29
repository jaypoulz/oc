package transition

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	kcmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/util/templates"

	"github.com/openshift/oc/pkg/cli/admin/transition/preflight"
	"github.com/openshift/oc/pkg/cli/admin/transition/prompt"
)

var (
	transitionLong = templates.LongDesc(`
		Transition the cluster control plane topology.

		This command enables transitioning between Single-Node OpenShift (SNO) and
		Highly Available (HA) topologies. Currently supports one-way transition from
		SingleReplica to HighlyAvailable topology.

		FEATURE GATE REQUIRED:
		This command requires the OC_ENABLE_CMD_TRANSITION_TOPOLOGY environment
		variable to be set to "true" to enable the command. Additionally, the
		MutableTopology feature gate must be enabled on the cluster.

		THREE MODES:

		1. Discovery mode (no flags):
		   Shows current topology and available transitions.

		2. Initiate mode (--to=<topology>):
		   Validates cluster readiness and initiates topology transition.
		   Runs preflight checks, prompts for confirmation, and patches Infrastructure spec.

		3. Status mode (status subcommand):
		   Monitors transition progress by displaying cluster operator conditions.

		PREFLIGHT CHECKS:

		The command runs comprehensive preflight validation before initiating a transition:
		- Blocking checks (Error severity): Supported transition, feature gate enabled
		- Readiness checks (Warning severity): Cluster operators, nodes, etcd stability

		Warning-severity check failures can be bypassed with --allow-transition-with-warnings.
		Error-severity check failures cannot be bypassed.
	`)

	transitionExample = templates.Examples(`
		# Show current topology and available transitions
		oc adm transition topology

		# Initiate transition to HighlyAvailable topology
		oc adm transition topology --to=HighlyAvailable

		# Validate transition without applying changes
		oc adm transition topology --to=HighlyAvailable --dry-run

		# Bypass warning-severity preflight check failures (not recommended)
		oc adm transition topology --to=HighlyAvailable --allow-transition-with-warnings

		# Auto-confirm without prompting (use in scripts)
		oc adm transition topology --to=HighlyAvailable --yes

		# Monitor transition progress
		oc adm transition topology status
	`)
)

// TransitionOptions holds all options for the topology transition command
type TransitionOptions struct {
	// Target topology to transition to (--to flag)
	To string

	// DryRun validates without applying changes (--dry-run flag)
	DryRun bool

	// AllowTransitionWithWarnings bypasses Warning-severity check failures (--allow-transition-with-warnings flag)
	AllowTransitionWithWarnings bool

	// Yes auto-confirms without prompting (--yes flag)
	Yes bool

	// Context for API calls
	ctx context.Context

	// Clients
	kubeClient     kubernetes.Interface
	configClient   configv1client.Interface
	operatorClient operatorv1client.Interface

	// Validator for preflight checks
	validator preflight.Validator

	// Prompter for user interaction
	prompter prompt.Prompter

	genericclioptions.IOStreams
}

// NewTransitionOptions creates a new TransitionOptions with default values
func NewTransitionOptions(streams genericclioptions.IOStreams) *TransitionOptions {
	return &TransitionOptions{
		IOStreams: streams,
		prompter:  prompt.NewInteractivePrompter(),
	}
}

// NewCmdTransition creates the topology transition command
func NewCmdTransition(f kcmdutil.Factory, streams genericclioptions.IOStreams) *cobra.Command {
	o := NewTransitionOptions(streams)

	cmd := &cobra.Command{
		Use:     "topology [status]",
		Short:   "Transition cluster control plane topology",
		Long:    transitionLong,
		Example: transitionExample,
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			kcmdutil.CheckErr(o.Complete(f, cmd, args))
			kcmdutil.CheckErr(o.Validate())
			kcmdutil.CheckErr(o.Run())
		},
	}

	cmd.Flags().StringVar(&o.To, "to", "", "Target topology to transition to (HighlyAvailable or SingleReplica)")
	cmd.Flags().BoolVar(&o.DryRun, "dry-run", false, "Validate transition without applying changes")
	cmd.Flags().BoolVar(&o.AllowTransitionWithWarnings, "allow-transition-with-warnings", false, "Bypass warning-severity preflight check failures (not recommended)")
	cmd.Flags().BoolVar(&o.Yes, "yes", false, "Auto-confirm without prompting")

	// Add status subcommand
	cmd.AddCommand(NewCmdStatus(f, o))

	return cmd
}

// Complete sets up all required fields from the factory
func (o *TransitionOptions) Complete(f kcmdutil.Factory, cmd *cobra.Command, args []string) error {
	// Store context for API calls
	o.ctx = cmd.Context()

	// Get REST config
	restConfig, err := f.ToRESTConfig()
	if err != nil {
		return fmt.Errorf("failed to get REST config: %w", err)
	}

	// Create clients
	o.kubeClient, err = kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	o.configClient, err = configv1client.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	o.operatorClient, err = operatorv1client.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create operator client: %w", err)
	}

	// Create validator
	o.validator = preflight.NewClientSideValidator(
		o.kubeClient,
		o.configClient,
		o.operatorClient,
	)

	return nil
}

// Validate validates the command options
func (o *TransitionOptions) Validate() error {
	// If --to flag is provided, validate the topology value
	if o.To != "" {
		validTopologies := map[string]bool{
			string(configv1.HighlyAvailableTopologyMode): true,
			string(configv1.SingleReplicaTopologyMode):   true,
		}

		if !validTopologies[o.To] {
			return fmt.Errorf("invalid topology %q, must be 'HighlyAvailable' or 'SingleReplica'", o.To)
		}
	}

	// --dry-run requires --to flag
	if o.DryRun && o.To == "" {
		return fmt.Errorf("--dry-run requires --to flag")
	}

	// --allow-transition-with-warnings requires --to flag
	if o.AllowTransitionWithWarnings && o.To == "" {
		return fmt.Errorf("--allow-transition-with-warnings requires --to flag")
	}

	// --yes requires --to flag
	if o.Yes && o.To == "" {
		return fmt.Errorf("--yes requires --to flag")
	}

	return nil
}

// Run executes the topology transition command
func (o *TransitionOptions) Run() error {
	// Get current topology from Infrastructure resource
	infra, err := o.configClient.ConfigV1().Infrastructures().Get(o.ctx, InfrastructureResourceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get Infrastructure resource: %w", err)
	}

	currentTopology := infra.Status.ControlPlaneTopology

	// Mode 1: Discovery mode (no --to flag)
	if o.To == "" {
		return o.runDiscoveryMode(currentTopology)
	}

	// Mode 2: Initiate mode (--to flag provided)
	targetTopology := configv1.TopologyMode(o.To)
	return o.runInitiateMode(o.ctx, currentTopology, targetTopology)
}

// runDiscoveryMode displays current topology and available transitions
func (o *TransitionOptions) runDiscoveryMode(current configv1.TopologyMode) error {
	fmt.Fprintf(o.Out, "Current Control Plane Topology: %s\n\n", current)

	fmt.Fprintf(o.Out, "Available Transitions:\n")

	// Currently only SNO → HA is supported
	if current == configv1.SingleReplicaTopologyMode {
		fmt.Fprintf(o.Out, "  - SingleReplica -> HighlyAvailable (one-way, irreversible)\n\n")
		fmt.Fprintf(o.Out, "To initiate transition:\n")
		fmt.Fprintf(o.Out, "  oc adm transition topology --to=HighlyAvailable\n")
	} else if current == configv1.HighlyAvailableTopologyMode {
		fmt.Fprintf(o.Out, "  (none - cluster is already HighlyAvailable)\n\n")
		fmt.Fprintf(o.Out, "Note: Transitioning from HighlyAvailable to SingleReplica is not supported\n")
	} else {
		fmt.Fprintf(o.Out, "  (unknown current topology: %s)\n", current)
	}

	return nil
}

// runInitiateMode validates cluster readiness and initiates topology transition
func (o *TransitionOptions) runInitiateMode(ctx context.Context, current, target configv1.TopologyMode) error {
	// Step 1: Run preflight validation
	fmt.Fprintf(o.Out, "Running preflight validation...\n\n")

	result, err := o.validator.Validate(ctx, current, target)
	if err != nil {
		return fmt.Errorf("preflight validation failed: %w", err)
	}

	// Step 2: Prompt user for confirmation (handles all flag logic)
	promptOpts := prompt.PromptOptions{
		Yes:                         o.Yes,
		AllowTransitionWithWarnings: o.AllowTransitionWithWarnings,
		Out:                         o.Out,
		In:                          o.In,
	}

	confirmed, err := o.prompter.PromptForTransition(result, promptOpts)
	if err != nil {
		return fmt.Errorf("prompt failed: %w", err)
	}

	if !confirmed {
		// User cancelled or validation blocked transition
		return nil
	}

	// Step 3: Apply transition (patch Infrastructure spec)
	if o.DryRun {
		fmt.Fprintf(o.Out, "\nDry run: Would patch Infrastructure.spec.controlPlaneTopology to %s\n", target)
		fmt.Fprintf(o.Out, "  (no changes applied)\n")
		return nil
	}

	return o.applyTransition(ctx, target)
}

// applyTransition patches the Infrastructure resource to initiate the transition
func (o *TransitionOptions) applyTransition(ctx context.Context, target configv1.TopologyMode) error {
	fmt.Fprintf(o.Out, "\nInitiating topology transition to %s...\n", target)

	// Get current Infrastructure resource
	infra, err := o.configClient.ConfigV1().Infrastructures().Get(ctx, InfrastructureResourceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get Infrastructure resource: %w", err)
	}

	// Patch spec.controlPlaneTopology
	infra.Spec.ControlPlaneTopology = target

	_, err = o.configClient.ConfigV1().Infrastructures().Update(ctx, infra, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Infrastructure resource: %w", err)
	}

	fmt.Fprintf(o.Out, "\nInfrastructure.spec.controlPlaneTopology patched to %s\n\n", target)
	fmt.Fprintf(o.Out, "Transition initiated. The cluster will begin scaling control plane components.\n")
	fmt.Fprintf(o.Out, "This process may take several minutes.\n\n")
	fmt.Fprintf(o.Out, "Monitor progress with:\n")
	fmt.Fprintf(o.Out, "  oc adm transition topology status\n")
	fmt.Fprintf(o.Out, "  oc get clusteroperators\n")
	fmt.Fprintf(o.Out, "  oc get etcd -n openshift-etcd\n")

	return nil
}
