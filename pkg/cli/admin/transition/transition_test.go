package transition

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	fakeconfigclient "github.com/openshift/client-go/config/clientset/versioned/fake"
	fakeoperatorclient "github.com/openshift/client-go/operator/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	fake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/openshift/oc/pkg/cli/admin/transition/preflight"
	"github.com/openshift/oc/pkg/cli/admin/transition/prompt"
)

/*
================================================================================
TRANSITION COMMAND TESTS
================================================================================

This file tests the `oc adm transition topology` command implementation.
Tests use fake clients with action recording to verify API call behavior.

--------------------------------------------------------------------------------
TEST COVERAGE
--------------------------------------------------------------------------------

COMMAND STRUCTURE (1 test)
  ✅ NewCmdTransition                        → Command metadata, flags, subcommands

VALIDATION (2 tests)
  ✅ --to flag validation                    → Valid/invalid topology values
  ✅ Flag dependencies                       → --dry-run/--yes/--allow-* require --to

DISCOVERY MODE (2 tests)
  ✅ Discovery mode from SingleReplica       → Shows available transition, one-way warning
  ✅ Discovery mode from HighlyAvailable     → Shows no transitions, not supported note

INITIATE MODE (2 tests)
  ✅ Patches Infrastructure spec             → Verifies Update action with correct topology
  ✅ Dry-run skips patch                     → Verifies no Update action, dry-run output

--------------------------------------------------------------------------------
*/

// TestNewCmdTransition verifies command structure
func TestNewCmdTransition(t *testing.T) {
	streams := genericclioptions.NewTestIOStreamsDiscard()

	// Create command with nil factory (just testing structure, not execution)
	cmd := NewCmdTransition(nil, streams)

	// Verify command metadata
	if cmd.Use != "topology [status]" {
		t.Errorf("expected Use='topology [status]', got %q", cmd.Use)
	}

	if cmd.Short == "" {
		t.Error("expected non-empty Short description")
	}

	if cmd.Long == "" {
		t.Error("expected non-empty Long description")
	}

	if cmd.Example == "" {
		t.Error("expected non-empty Example")
	}

	// Verify flags exist
	requiredFlags := []string{"to", "dry-run", "allow-transition-with-warnings", "yes"}
	for _, flagName := range requiredFlags {
		flag := cmd.Flags().Lookup(flagName)
		if flag == nil {
			t.Errorf("expected flag %q to exist", flagName)
		}
	}

	// Verify status subcommand exists
	statusCmd := findSubcommand(cmd, "status")
	if statusCmd == nil {
		t.Error("expected 'status' subcommand to exist")
	} else {
		if statusCmd.Short == "" {
			t.Error("expected status subcommand to have Short description")
		}
	}
}

// TestValidate_ToFlag tests --to flag validation
func TestValidate_ToFlag(t *testing.T) {
	testCases := []struct {
		name      string
		to        string
		expectErr bool
	}{
		{
			name:      "valid HighlyAvailable",
			to:        "HighlyAvailable",
			expectErr: false,
		},
		{
			name:      "valid SingleReplica",
			to:        "SingleReplica",
			expectErr: false,
		},
		{
			name:      "invalid topology",
			to:        "InvalidTopology",
			expectErr: true,
		},
		{
			name:      "empty (discovery mode)",
			to:        "",
			expectErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			streams := genericclioptions.NewTestIOStreamsDiscard()
			o := NewTransitionOptions(streams)
			o.To = tc.to

			err := o.Validate()

			if tc.expectErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.expectErr && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

// TestValidate_FlagDependencies tests flag dependencies
func TestValidate_FlagDependencies(t *testing.T) {
	testCases := []struct {
		name                        string
		to                          string
		dryRun                      bool
		allowTransitionWithWarnings bool
		yes                         bool
		expectErr                   bool
		errContains                 string
	}{
		{
			name:        "--dry-run requires --to",
			dryRun:      true,
			expectErr:   true,
			errContains: "--dry-run requires --to",
		},
		{
			name:                        "--allow-transition-with-warnings requires --to",
			allowTransitionWithWarnings: true,
			expectErr:                   true,
			errContains:                 "--allow-transition-with-warnings requires --to",
		},
		{
			name:        "--yes requires --to",
			yes:         true,
			expectErr:   true,
			errContains: "--yes requires --to",
		},
		{
			name:      "all flags with --to is valid",
			to:        "HighlyAvailable",
			dryRun:    true,
			yes:       true,
			expectErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			streams := genericclioptions.NewTestIOStreamsDiscard()
			o := NewTransitionOptions(streams)
			o.To = tc.to
			o.DryRun = tc.dryRun
			o.AllowTransitionWithWarnings = tc.allowTransitionWithWarnings
			o.Yes = tc.yes

			err := o.Validate()

			if tc.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("expected error containing %q, got %v", tc.errContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

// TestRunDiscoveryMode_SingleReplica tests discovery mode output for SNO
func TestRunDiscoveryMode_SingleReplica(t *testing.T) {
	streams, _, out, _ := genericclioptions.NewTestIOStreams()
	o := NewTransitionOptions(streams)

	err := o.runDiscoveryMode("SingleReplica")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()

	// Should show current topology
	if !strings.Contains(output, "Current Control Plane Topology: SingleReplica") {
		t.Errorf("expected current topology in output, got:\n%s", output)
	}

	// Should show available transition
	if !strings.Contains(output, "SingleReplica -> HighlyAvailable") {
		t.Errorf("expected available transition in output, got:\n%s", output)
	}

	// Should show one-way warning
	if !strings.Contains(output, "one-way, irreversible") {
		t.Errorf("expected one-way warning in output, got:\n%s", output)
	}

	// Should show command to initiate
	if !strings.Contains(output, "oc adm transition topology --to=HighlyAvailable") {
		t.Errorf("expected initiate command in output, got:\n%s", output)
	}
}

// TestRunDiscoveryMode_HighlyAvailable tests discovery mode output for HA
func TestRunDiscoveryMode_HighlyAvailable(t *testing.T) {
	streams, _, out, _ := genericclioptions.NewTestIOStreams()
	o := NewTransitionOptions(streams)

	err := o.runDiscoveryMode("HighlyAvailable")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()

	// Should show current topology
	if !strings.Contains(output, "Current Control Plane Topology: HighlyAvailable") {
		t.Errorf("expected current topology in output, got:\n%s", output)
	}

	// Should show no available transitions
	if !strings.Contains(output, "(none") {
		t.Errorf("expected no available transitions in output, got:\n%s", output)
	}

	// Should mention HA → SNO not supported
	if !strings.Contains(output, "not supported") {
		t.Errorf("expected unsupported transition note in output, got:\n%s", output)
	}
}

// findSubcommand finds a subcommand by name
func findSubcommand(parent *cobra.Command, name string) *cobra.Command {
	for _, cmd := range parent.Commands() {
		if cmd.Name() == name {
			return cmd
		}
	}
	return nil
}

// Helper functions for test fixtures

func newFakeNode(name string, master, worker, ready, schedulable bool) *corev1.Node {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{},
		},
		Spec: corev1.NodeSpec{
			Unschedulable: !schedulable,
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{},
		},
	}

	if master {
		node.Labels["node-role.kubernetes.io/master"] = ""
	}
	if worker {
		node.Labels["node-role.kubernetes.io/worker"] = ""
	}
	if ready {
		node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{
			Type:   corev1.NodeReady,
			Status: corev1.ConditionTrue,
		})
	}

	return node
}

func newFakeClusterOperator(name string, available, progressing, degraded bool) *configv1.ClusterOperator {
	co := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: configv1.ClusterOperatorStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:   configv1.OperatorAvailable,
					Status: boolToConfigConditionStatus(available),
				},
				{
					Type:   configv1.OperatorProgressing,
					Status: boolToConfigConditionStatus(progressing),
				},
				{
					Type:   configv1.OperatorDegraded,
					Status: boolToConfigConditionStatus(degraded),
				},
			},
		},
	}

	return co
}

func boolToConfigConditionStatus(b bool) configv1.ConditionStatus {
	if b {
		return configv1.ConditionTrue
	}
	return configv1.ConditionFalse
}

func newFakeEtcdOperator(available, progressing bool) *operatorv1.Etcd {
	etcd := &operatorv1.Etcd{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
	}

	// EtcdStatus embeds StaticPodOperatorStatus which embeds OperatorStatus
	// Set Conditions directly on Status
	if available {
		etcd.Status.Conditions = append(etcd.Status.Conditions, operatorv1.OperatorCondition{
			Type:   "EtcdMembersAvailable",
			Status: operatorv1.ConditionTrue,
		})
	}
	if progressing {
		etcd.Status.Conditions = append(etcd.Status.Conditions, operatorv1.OperatorCondition{
			Type:   operatorv1.OperatorStatusTypeProgressing,
			Status: operatorv1.ConditionTrue,
		})
	}

	return etcd
}

func newFakeEtcdConfigMap(memberCount int) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-endpoints",
			Namespace: "openshift-etcd",
		},
		Data: map[string]string{},
	}

	for i := 0; i < memberCount; i++ {
		cm.Data[string(rune('a'+i))] = "etcd-member"
	}

	return cm
}

// TestRunInitiateMode_PatchesInfrastructure verifies that the command patches Infrastructure.spec.controlPlaneTopology
func TestRunInitiateMode_PatchesInfrastructure(t *testing.T) {
	streams, _, out, _ := genericclioptions.NewTestIOStreams()

	// Create fake infrastructure resource (current: SingleReplica)
	infra := &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Status: configv1.InfrastructureStatus{
			ControlPlaneTopology: configv1.SingleReplicaTopologyMode,
		},
		Spec: configv1.InfrastructureSpec{
			ControlPlaneTopology: configv1.SingleReplicaTopologyMode,
		},
	}

	configClient := fakeconfigclient.NewSimpleClientset(
		infra,
		&configv1.FeatureGate{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			Status: configv1.FeatureGateStatus{
				FeatureGates: []configv1.FeatureGateDetails{
					{
						Version: "4.18",
						Enabled: []configv1.FeatureGateAttributes{
							{Name: "MutableTopology"},
						},
					},
				},
			},
		},
		newFakeClusterOperator("kube-apiserver", true, false, false),
	)

	kubeClient := fake.NewSimpleClientset(
		newFakeNode("master-0", true, false, true, true),
		newFakeNode("master-1", true, false, true, true),
		newFakeNode("master-2", true, false, true, true),
		newFakeEtcdConfigMap(3),
	)

	operatorClient := fakeoperatorclient.NewSimpleClientset(
		newFakeEtcdOperator(true, false),
	)

	o := &TransitionOptions{
		To:  "HighlyAvailable",
		Yes: true, // Auto-confirm to avoid prompt
		IOStreams: streams,
		ctx: context.Background(),
		kubeClient: kubeClient,
		configClient: configClient,
		operatorClient: operatorClient,
		validator: preflight.NewClientSideValidator(kubeClient, configClient, operatorClient),
		prompter: prompt.NewInteractivePrompter(),
	}

	// Run the command
	err := o.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify Infrastructure was updated
	actions := configClient.Actions()
	var updateAction ktesting.UpdateAction
	for _, action := range actions {
		if action.GetVerb() == "update" && action.GetResource().Resource == "infrastructures" {
			updateAction = action.(ktesting.UpdateAction)
			break
		}
	}

	if updateAction == nil {
		t.Fatal("expected Infrastructure update action, got none")
	}

	updatedInfra := updateAction.GetObject().(*configv1.Infrastructure)
	if updatedInfra.Spec.ControlPlaneTopology != configv1.HighlyAvailableTopologyMode {
		t.Errorf("expected spec.controlPlaneTopology=%s, got %s",
			configv1.HighlyAvailableTopologyMode, updatedInfra.Spec.ControlPlaneTopology)
	}

	// Verify output confirms transition
	if !strings.Contains(out.String(), "Initiating topology transition") {
		t.Error("expected output to confirm transition initiation")
	}
}

// TestRunInitiateMode_DryRunSkipsPatch verifies that --dry-run skips the Infrastructure patch
func TestRunInitiateMode_DryRunSkipsPatch(t *testing.T) {
	streams, _, out, _ := genericclioptions.NewTestIOStreams()

	// Create fake infrastructure resource
	infra := &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Status: configv1.InfrastructureStatus{
			ControlPlaneTopology: configv1.SingleReplicaTopologyMode,
		},
		Spec: configv1.InfrastructureSpec{
			ControlPlaneTopology: configv1.SingleReplicaTopologyMode,
		},
	}

	configClient := fakeconfigclient.NewSimpleClientset(
		infra,
		&configv1.FeatureGate{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			Status: configv1.FeatureGateStatus{
				FeatureGates: []configv1.FeatureGateDetails{
					{
						Version: "4.18",
						Enabled: []configv1.FeatureGateAttributes{
							{Name: "MutableTopology"},
						},
					},
				},
			},
		},
		newFakeClusterOperator("kube-apiserver", true, false, false),
	)

	kubeClient := fake.NewSimpleClientset(
		newFakeNode("master-0", true, false, true, true),
		newFakeNode("master-1", true, false, true, true),
		newFakeNode("master-2", true, false, true, true),
		newFakeEtcdConfigMap(3),
	)

	operatorClient := fakeoperatorclient.NewSimpleClientset(
		newFakeEtcdOperator(true, false),
	)

	o := &TransitionOptions{
		To:      "HighlyAvailable",
		DryRun:  true, // Dry run mode
		Yes:     true,
		IOStreams: streams,
		ctx: context.Background(),
		kubeClient: kubeClient,
		configClient: configClient,
		operatorClient: operatorClient,
		validator: preflight.NewClientSideValidator(kubeClient, configClient, operatorClient),
		prompter: prompt.NewInteractivePrompter(),
	}

	// Run the command
	err := o.Run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify NO Infrastructure update occurred
	actions := configClient.Actions()
	for _, action := range actions {
		if action.GetVerb() == "update" {
			t.Error("expected no update action in dry-run mode")
		}
	}

	// Verify dry-run message in output
	if !strings.Contains(out.String(), "Dry run") {
		t.Error("expected output to indicate dry-run mode")
	}
	if !strings.Contains(out.String(), "no changes applied") {
		t.Error("expected output to confirm no changes applied")
	}
}
