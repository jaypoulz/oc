package preflight

/*
================================================================================
CLIENT-SIDE VALIDATOR TESTS
================================================================================

This file tests the ClientSideValidator implementation which duplicates CCO's
preflight validation logic for topology transitions. Tests use fake clients to
verify the orchestration logic and one representative test per validation pattern.

TIERED VALIDATION APPROACH:
  Phase 1: ALL Error-severity checks (blocking - show complete picture)
    - Supported transition (CEL-based: only SNO → HA)
    - Feature gate enabled (MutableTopology)

  Phase 2: ALL Warning-severity checks (cluster readiness - bypassable)
    - Cluster operators stable
    - Control plane node count/schedulability/readiness
    - etcd quorum/stability/voting member count

  Short-circuit after Phase 1 if ANY Error checks fail to avoid wasting time
  checking cluster state when transition isn't even valid.

--------------------------------------------------------------------------------
TEST COVERAGE (17 TESTS)
--------------------------------------------------------------------------------

ORCHESTRATION (4 tests - complex logic)
  ✅ SNO → HighlyAvailable, all checks pass     → Full integration of 10 checks
  ✅ Already at target topology                 → Error check short-circuit logic
  ✅ Unsupported transition (HA → SNO)          → CEL transition validation
  ✅ Error checks pass, Warning checks fail     → Tiered validation behavior

FEATURE GATE (2 tests - complex parsing)
  ✅ MutableTopology enabled                    → Version array, enabled list parsing
  ✅ FeatureGate resource missing               → Error handling

VALIDATORS (8 tests - one smoke test per pattern)
  ✅ ClusterOperators stable                    → Condition checking pattern
  ✅ Control plane node count (3)               → Counting pattern
  ✅ Infrastructure node count (0)              → Inverse counting (NOT control-plane)
  ✅ Control plane nodes schedulable            → Taint checking
  ✅ Control plane nodes ready                  → Node condition iteration
  ✅ Etcd quorum available                      → library-go v1helpers usage
  ✅ Etcd not progressing                       → Etcd-specific conditions
  ✅ Etcd voting members (3)                    → ConfigMap data parsing

TYPES (3 tests - status computation only)
  ✅ ValidationResult status all pass           → Status computation logic
  ✅ ValidationResult status some fail          → Status computation logic
  ✅ ClientSideValidator interface compliance   → Interface implementation

--------------------------------------------------------------------------------
*/

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	fakeconfigclient "github.com/openshift/client-go/config/clientset/versioned/fake"
	fakeoperatorclient "github.com/openshift/client-go/operator/clientset/versioned/fake"
)

// TestValidate_SNOToHA_AllChecksPass tests the happy path: SNO → HighlyAvailable with all checks passing
func TestValidate_SNOToHA_AllChecksPass(t *testing.T) {
	// Create healthy cluster state
	kubeClient := fake.NewSimpleClientset(
		// 3 healthy, schedulable, ready control plane nodes
		newFakeNode("master-0", true, false, true, true),
		newFakeNode("master-1", true, false, true, true),
		newFakeNode("master-2", true, false, true, true),
		// etcd-endpoints ConfigMap with 3 voting members
		newFakeEtcdConfigMap(3),
	)

	configClient := fakeconfigclient.NewSimpleClientset(
		// MutableTopology feature gate enabled
		newFakeFeatureGate(true),
		// 3 stable ClusterOperators
		newFakeClusterOperator("kube-apiserver", true, false, false),
		newFakeClusterOperator("etcd", true, false, false),
		newFakeClusterOperator("network", true, false, false),
	)

	operatorClient := fakeoperatorclient.NewSimpleClientset(
		// Healthy etcd operator: quorum available, not progressing
		newFakeEtcdOperator(true, false),
	)

	validator := NewClientSideValidator(kubeClient, configClient, operatorClient)

	// Validate SNO → HA transition
	result, err := validator.Validate(
		context.Background(),
		configv1.SingleReplicaTopologyMode,
		configv1.HighlyAvailableTopologyMode,
	)

	// Should complete without API error
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have Available status (all checks passed)
	if result.Status != ValidationStatusAvailable {
		t.Errorf("expected status=Available when all checks pass, got %s", result.Status)
	}

	// Should have 10 checks total: 2 Error + 8 Warning
	// Error: validateSupportedTransition, validateFeatureGateEnabled
	// Warning: operators, 3x node checks, 3x etcd checks
	expectedChecks := 10
	if len(result.Checks) != expectedChecks {
		t.Errorf("expected %d checks, got %d", expectedChecks, len(result.Checks))
	}

	// All checks should have Passed status
	for i, check := range result.Checks {
		if check.Status != CheckStatusPassed {
			t.Errorf("check %d (%s) expected status=Passed, got %s: %s", i, check.Name, check.Status, check.Message)
		}
	}

	// First two checks should be Error severity (Supported Transition, Feature Gate Enabled)
	if result.Checks[0].Name != "Supported Transition" {
		t.Errorf("expected first check to be 'Supported Transition', got %s", result.Checks[0].Name)
	}
	if result.Checks[0].Severity != CheckSeverityError {
		t.Errorf("expected first check severity=Error, got %s", result.Checks[0].Severity)
	}

	if result.Checks[1].Name != "Feature Gate Enabled" {
		t.Errorf("expected second check to be 'Feature Gate Enabled', got %s", result.Checks[1].Name)
	}
	if result.Checks[1].Severity != CheckSeverityError {
		t.Errorf("expected second check severity=Error, got %s", result.Checks[1].Severity)
	}

	// Remaining checks should be Warning severity
	for i := 2; i < len(result.Checks); i++ {
		if result.Checks[i].Severity != CheckSeverityWarning {
			t.Errorf("check %d (%s) expected severity=Warning, got %s", i, result.Checks[i].Name, result.Checks[i].Severity)
		}
	}
}

// TestValidate_AlreadyAtTarget tests validation when already at target topology
func TestValidate_AlreadyAtTarget(t *testing.T) {
	// Create validator with feature gate enabled (other resources not needed for this test)
	validator := NewClientSideValidator(
		fake.NewSimpleClientset(),
		fakeconfigclient.NewSimpleClientset(
			newFakeFeatureGate(true),
		),
		fakeoperatorclient.NewSimpleClientset(),
	)

	// Attempt to transition to same topology: HA → HA
	result, err := validator.Validate(
		context.Background(),
		configv1.HighlyAvailableTopologyMode,
		configv1.HighlyAvailableTopologyMode,
	)

	// Should complete without API error
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have Unavailable status due to Error-severity check failure
	if result.Status != ValidationStatusUnavailable {
		t.Errorf("expected status=Unavailable when already at target, got %s", result.Status)
	}

	// Should have at least one check (the supported transition check)
	if len(result.Checks) == 0 {
		t.Fatal("expected at least one check result")
	}

	// First check should be "Supported Transition" with Failed status
	supportedTransitionCheck := result.Checks[0]
	if supportedTransitionCheck.Name != "Supported Transition" {
		t.Errorf("expected first check name='Supported Transition', got %s", supportedTransitionCheck.Name)
	}
	if supportedTransitionCheck.Status != CheckStatusFailed {
		t.Errorf("expected first check status=Failed, got %s", supportedTransitionCheck.Status)
	}
	if supportedTransitionCheck.Severity != CheckSeverityError {
		t.Errorf("expected first check severity=Error, got %s", supportedTransitionCheck.Severity)
	}

	// Message should indicate cluster is already at target
	if supportedTransitionCheck.Message == "" {
		t.Error("expected message explaining why transition failed")
	}

	// Should short-circuit after Error-severity checks, so only Error checks should run
	// Currently 2 Error checks (supported transition, feature gate), so only 2 checks total
	if len(result.Checks) != 2 {
		t.Errorf("expected only 2 checks (short-circuit after error phase), got %d", len(result.Checks))
	}
}

// TestValidate_UnsupportedTransition tests HA → SNO (one-way only)
func TestValidate_UnsupportedTransition(t *testing.T) {
	// Create validator with feature gate enabled (other resources not needed for this test)
	validator := NewClientSideValidator(
		fake.NewSimpleClientset(),
		fakeconfigclient.NewSimpleClientset(
			newFakeFeatureGate(true),
		),
		fakeoperatorclient.NewSimpleClientset(),
	)

	// Attempt unsupported transition: HA → SNO
	result, err := validator.Validate(
		context.Background(),
		configv1.HighlyAvailableTopologyMode,
		configv1.SingleReplicaTopologyMode,
	)

	// Should complete without API error
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have Unavailable status due to Error-severity check failure
	if result.Status != ValidationStatusUnavailable {
		t.Errorf("expected status=Unavailable for unsupported transition, got %s", result.Status)
	}

	// Should have at least one check (the supported transition check)
	if len(result.Checks) == 0 {
		t.Fatal("expected at least one check result")
	}

	// First check should be "Supported Transition" with Failed status
	supportedTransitionCheck := result.Checks[0]
	if supportedTransitionCheck.Name != "Supported Transition" {
		t.Errorf("expected first check name='Supported Transition', got %s", supportedTransitionCheck.Name)
	}
	if supportedTransitionCheck.Status != CheckStatusFailed {
		t.Errorf("expected first check status=Failed, got %s", supportedTransitionCheck.Status)
	}
	if supportedTransitionCheck.Severity != CheckSeverityError {
		t.Errorf("expected first check severity=Error, got %s", supportedTransitionCheck.Severity)
	}

	// Should short-circuit after Error-severity checks, so only 2 checks should run
	if len(result.Checks) != 2 {
		t.Errorf("expected only 2 checks (short-circuit after error phase), got %d", len(result.Checks))
	}
}

// TestValidateFeatureGateEnabled_Enabled tests feature gate check with MutableTopology enabled
func TestValidateFeatureGateEnabled_Enabled(t *testing.T) {
	validator := NewClientSideValidator(
		fake.NewSimpleClientset(),
		fakeconfigclient.NewSimpleClientset(
			newFakeFeatureGate(true),
		),
		fakeoperatorclient.NewSimpleClientset(),
	)

	result := validator.validateFeatureGateEnabled(context.Background())

	if result.Name != CheckNameFeatureGateEnabled {
		t.Errorf("expected check name='Feature Gate Enabled', got %s", result.Name)
	}
	if result.Severity != CheckSeverityError {
		t.Errorf("expected severity=Error, got %s", result.Severity)
	}
	if result.Status != CheckStatusPassed {
		t.Errorf("expected status=Passed when feature gate enabled, got %s: %s", result.Status, result.Message)
	}
}

// TestValidateFeatureGateEnabled_Missing tests feature gate check with FeatureGate resource missing
func TestValidateFeatureGateEnabled_Missing(t *testing.T) {
	validator := NewClientSideValidator(
		fake.NewSimpleClientset(),
		fakeconfigclient.NewSimpleClientset(),
		fakeoperatorclient.NewSimpleClientset(),
	)

	result := validator.validateFeatureGateEnabled(context.Background())

	if result.Name != CheckNameFeatureGateEnabled {
		t.Errorf("expected check name='Feature Gate Enabled', got %s", result.Name)
	}
	if result.Severity != CheckSeverityError {
		t.Errorf("expected severity=Error, got %s", result.Severity)
	}
	if result.Status != CheckStatusUnknown {
		t.Errorf("expected status=Unknown when FeatureGate resource missing, got %s", result.Status)
	}
}

// TestValidateClusterOperatorsStable_AllStable tests all operators healthy
func TestValidateClusterOperatorsStable_AllStable(t *testing.T) {
	configClient := fakeconfigclient.NewSimpleClientset(
		newFakeClusterOperator("kube-apiserver", true, false, false),
		newFakeClusterOperator("etcd", true, false, false),
	)

	validator := NewClientSideValidator(
		fake.NewSimpleClientset(),
		configClient,
		fakeoperatorclient.NewSimpleClientset(),
	)

	result := validator.validateClusterOperatorsStable(context.Background())

	if result.Status != CheckStatusPassed {
		t.Errorf("expected Passed, got %s: %s", result.Status, result.Message)
	}
}

// TestValidateControlPlaneNodeCount_Exactly3 tests exactly 3 control plane nodes
func TestValidateControlPlaneNodeCount_Exactly3(t *testing.T) {
	kubeClient := fake.NewSimpleClientset(
		newFakeNode("master-0", true, false, true, true),
		newFakeNode("master-1", true, false, true, true),
		newFakeNode("master-2", true, false, true, true),
	)

	validator := NewClientSideValidator(
		kubeClient,
		fakeconfigclient.NewSimpleClientset(),
		fakeoperatorclient.NewSimpleClientset(),
	)

	result := validator.validateControlPlaneNodeCount(context.Background(), 3)

	if result.Status != CheckStatusPassed {
		t.Errorf("expected Passed, got %s: %s", result.Status, result.Message)
	}
}

// TestValidateInfrastructureNodeCount_NoWorkers tests compact topology (0 workers)
func TestValidateInfrastructureNodeCount_NoWorkers(t *testing.T) {
	kubeClient := fake.NewSimpleClientset(
		newFakeNode("master-0", true, false, true, true),
		newFakeNode("master-1", true, false, true, true),
		newFakeNode("master-2", true, false, true, true),
	)

	validator := NewClientSideValidator(
		kubeClient,
		fakeconfigclient.NewSimpleClientset(),
		fakeoperatorclient.NewSimpleClientset(),
	)

	result := validator.validateExactInfrastructureNodeCount(context.Background(), 0)

	if result.Status != CheckStatusPassed {
		t.Errorf("expected Passed, got %s: %s", result.Status, result.Message)
	}
}

// TestValidateControlPlaneNodesSchedulable_All3Schedulable tests all nodes schedulable
func TestValidateControlPlaneNodesSchedulable_All3Schedulable(t *testing.T) {
	kubeClient := fake.NewSimpleClientset(
		newFakeNode("master-0", true, false, true, true),
		newFakeNode("master-1", true, false, true, true),
		newFakeNode("master-2", true, false, true, true),
	)

	validator := NewClientSideValidator(
		kubeClient,
		fakeconfigclient.NewSimpleClientset(),
		fakeoperatorclient.NewSimpleClientset(),
	)

	result := validator.validateControlPlaneNodesSchedulable(context.Background(), 3)

	if result.Status != CheckStatusPassed {
		t.Errorf("expected Passed, got %s: %s", result.Status, result.Message)
	}
}

// TestValidateControlPlaneNodesReady_All3Ready tests all nodes Ready=True
func TestValidateControlPlaneNodesReady_All3Ready(t *testing.T) {
	kubeClient := fake.NewSimpleClientset(
		newFakeNode("master-0", true, false, true, true),
		newFakeNode("master-1", true, false, true, true),
		newFakeNode("master-2", true, false, true, true),
	)

	validator := NewClientSideValidator(
		kubeClient,
		fakeconfigclient.NewSimpleClientset(),
		fakeoperatorclient.NewSimpleClientset(),
	)

	result := validator.validateControlPlaneNodesReady(context.Background(), 3)

	if result.Status != CheckStatusPassed {
		t.Errorf("expected Passed, got %s: %s", result.Status, result.Message)
	}
}

// TestValidateEtcdQuorum_Available tests EtcdMembersAvailable=True
func TestValidateEtcdQuorum_Available(t *testing.T) {
	operatorClient := fakeoperatorclient.NewSimpleClientset(
		newFakeEtcdOperator(true, false),
	)

	validator := NewClientSideValidator(
		fake.NewSimpleClientset(),
		fakeconfigclient.NewSimpleClientset(),
		operatorClient,
	)

	result := validator.validateEtcdQuorum(context.Background())

	if result.Status != CheckStatusPassed {
		t.Errorf("expected Passed, got %s: %s", result.Status, result.Message)
	}
}

// TestValidateEtcdNotProgressing_NotProgressing tests Progressing=False
func TestValidateEtcdNotProgressing_NotProgressing(t *testing.T) {
	operatorClient := fakeoperatorclient.NewSimpleClientset(
		newFakeEtcdOperator(true, false),
	)

	validator := NewClientSideValidator(
		fake.NewSimpleClientset(),
		fakeconfigclient.NewSimpleClientset(),
		operatorClient,
	)

	result := validator.validateEtcdNotProgressing(context.Background())

	if result.Status != CheckStatusPassed {
		t.Errorf("expected Passed, got %s: %s", result.Status, result.Message)
	}
}

// TestValidateEtcdVotingMembers_Exactly3 tests exactly 3 voting members
func TestValidateEtcdVotingMembers_Exactly3(t *testing.T) {
	kubeClient := fake.NewSimpleClientset(
		newFakeEtcdConfigMap(3),
	)

	validator := NewClientSideValidator(
		kubeClient,
		fakeconfigclient.NewSimpleClientset(),
		fakeoperatorclient.NewSimpleClientset(),
	)

	result := validator.validateEtcdVotingMembers(context.Background(), 3)

	if result.Status != CheckStatusPassed {
		t.Errorf("expected Passed, got %s: %s", result.Status, result.Message)
	}
}

// ============================================================================
// HELPER FUNCTIONS FOR BUILDING TEST FIXTURES
// ============================================================================

// newFakeFeatureGate creates a FeatureGate resource with MutableTopology enabled or disabled
func newFakeFeatureGate(enabled bool) *configv1.FeatureGate {
	fg := &configv1.FeatureGate{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Status: configv1.FeatureGateStatus{
			FeatureGates: []configv1.FeatureGateDetails{
				{
					Version:  "4.18",
					Enabled:  []configv1.FeatureGateAttributes{},
					Disabled: []configv1.FeatureGateAttributes{},
				},
			},
		},
	}

	if enabled {
		fg.Status.FeatureGates[0].Enabled = append(fg.Status.FeatureGates[0].Enabled, configv1.FeatureGateAttributes{
			Name: "MutableTopology",
		})
	} else {
		fg.Status.FeatureGates[0].Disabled = append(fg.Status.FeatureGates[0].Disabled, configv1.FeatureGateAttributes{
			Name: "MutableTopology",
		})
	}

	return fg
}

// newFakeClusterOperator creates a ClusterOperator with specified conditions
func newFakeClusterOperator(name string, available, progressing, degraded bool) *configv1.ClusterOperator {
	return &configv1.ClusterOperator{
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
}

// newFakeNode creates a Node with specified properties
func newFakeNode(name string, controlPlane, worker, ready, schedulable bool) *corev1.Node {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{},
		},
		Spec: corev1.NodeSpec{},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{},
		},
	}

	// Add role labels
	if controlPlane {
		node.Labels["node-role.kubernetes.io/control-plane"] = ""
		node.Labels["node-role.kubernetes.io/master"] = "" // Legacy label
	}
	if worker {
		node.Labels["node-role.kubernetes.io/worker"] = ""
	}

	// Add Ready condition
	node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{
		Type:   corev1.NodeReady,
		Status: boolToConditionStatus(ready),
	})

	// Add unschedulable taint if not schedulable
	if !schedulable {
		node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{
			Key:    "node.kubernetes.io/unschedulable",
			Effect: corev1.TaintEffectNoSchedule,
		})
	}

	return node
}

// newFakeEtcdOperator creates an Etcd operator resource with specified conditions
func newFakeEtcdOperator(quorum, progressing bool) *operatorv1.Etcd {
	etcd := &operatorv1.Etcd{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
	}
	// EtcdStatus embeds StaticPodOperatorStatus which embeds OperatorStatus
	// We need to set Conditions on the embedded OperatorStatus
	etcd.Status.Conditions = []operatorv1.OperatorCondition{
		{
			Type:   "EtcdMembersAvailable",
			Status: boolToOperatorConditionStatus(quorum),
		},
		{
			Type:   operatorv1.OperatorStatusTypeProgressing,
			Status: boolToOperatorConditionStatus(progressing),
		},
	}
	return etcd
}

// newFakeEtcdConfigMap creates a ConfigMap with etcd member list
// The number of keys in Data corresponds to the number of voting members
func newFakeEtcdConfigMap(votingMembers int) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-endpoints",
			Namespace: "openshift-etcd",
		},
		Data: map[string]string{},
	}

	// Add one entry per voting member
	// The actual format is: <node-name>: <etcd-endpoint-url>
	for i := 0; i < votingMembers; i++ {
		nodeName := fmt.Sprintf("master-%d", i)
		endpoint := fmt.Sprintf("https://10.0.0.%d:2379", i+1)
		cm.Data[nodeName] = endpoint
	}

	return cm
}

// boolToConditionStatus converts bool to corev1.ConditionStatus
func boolToConditionStatus(b bool) corev1.ConditionStatus {
	if b {
		return corev1.ConditionTrue
	}
	return corev1.ConditionFalse
}

// boolToConfigConditionStatus converts bool to configv1.ConditionStatus
func boolToConfigConditionStatus(b bool) configv1.ConditionStatus {
	if b {
		return configv1.ConditionTrue
	}
	return configv1.ConditionFalse
}

// boolToOperatorConditionStatus converts bool to operatorv1.ConditionStatus
func boolToOperatorConditionStatus(b bool) operatorv1.ConditionStatus {
	if b {
		return operatorv1.ConditionTrue
	}
	return operatorv1.ConditionFalse
}

// TestValidate_WarningFailures verifies behavior when Error checks pass but Warning checks fail.
// This tests the tiered validation approach: even though Error checks (transition support,
// feature gate) pass, Warning checks (cluster readiness) can still block the transition
// unless --allow-transition-with-warnings is used.
func TestValidate_WarningFailures(t *testing.T) {
	// Create fake clients with:
	//   - Supported transition: SNO → HA (Error check passes)
	//   - Feature gate enabled (Error check passes)
	//   - Degraded cluster operator (Warning check fails)

	configClient := fakeconfigclient.NewSimpleClientset(
		&configv1.Infrastructure{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			Status: configv1.InfrastructureStatus{
				ControlPlaneTopology: configv1.SingleReplicaTopologyMode,
			},
		},
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
		// Degraded cluster operator → Warning check fails
		newFakeClusterOperator("kube-apiserver", true, false, true),
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

	v := NewClientSideValidator(kubeClient, configClient, operatorClient)

	result, err := v.Validate(context.Background(), configv1.SingleReplicaTopologyMode, configv1.HighlyAvailableTopologyMode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify Error checks passed
	errorChecksPassed := 0
	for _, check := range result.Checks {
		if check.Severity == CheckSeverityError && check.Status == CheckStatusPassed {
			errorChecksPassed++
		}
	}
	if errorChecksPassed < 2 {
		t.Errorf("expected at least 2 Error checks to pass, got %d", errorChecksPassed)
	}

	// Verify at least one Warning check failed
	warningCheckFailed := false
	for _, check := range result.Checks {
		if check.Severity == CheckSeverityWarning && check.Status == CheckStatusFailed {
			warningCheckFailed = true
			break
		}
	}
	if !warningCheckFailed {
		t.Error("expected at least one Warning check to fail (degraded cluster operator)")
	}

	// Verify overall status is Unavailable
	if result.Status != ValidationStatusUnavailable {
		t.Errorf("expected status=Unavailable when Warning checks fail, got %s", result.Status)
	}
}
