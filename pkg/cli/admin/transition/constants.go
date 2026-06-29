package transition

import (
	configv1 "github.com/openshift/api/config/v1"
)

// Kubernetes and OpenShift resource names
const (
	// InfrastructureResourceName is the name of the cluster-scoped Infrastructure resource
	InfrastructureResourceName = "cluster"

	// EtcdOperatorResourceName is the name of the cluster-scoped Etcd operator resource
	EtcdOperatorResourceName = "cluster"

	// EtcdNamespace is the namespace where etcd resources are located
	EtcdNamespace = "openshift-etcd"

	// EtcdEndpointsConfigMapName is the name of the ConfigMap containing etcd endpoints
	EtcdEndpointsConfigMapName = "etcd-endpoints"
)

// ClusterOperator condition types
const (
	// ClusterOperatorAvailable indicates the operand is available
	ClusterOperatorAvailable configv1.ClusterStatusConditionType = configv1.OperatorAvailable

	// ClusterOperatorProgressing indicates the operand is being updated
	ClusterOperatorProgressing configv1.ClusterStatusConditionType = configv1.OperatorProgressing

	// ClusterOperatorDegraded indicates the operand is degraded
	ClusterOperatorDegraded configv1.ClusterStatusConditionType = configv1.OperatorDegraded
)

// Etcd operator condition types
const (
	// EtcdMembersAvailableCondition indicates etcd has quorum
	EtcdMembersAvailableCondition = "EtcdMembersAvailable"
)
