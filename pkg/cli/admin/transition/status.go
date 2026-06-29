package transition

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kcmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/util/templates"
)

var (
	statusLong = templates.LongDesc(`
		Monitor topology transition progress.

		Displays the current control plane topology status and cluster operator conditions
		to help monitor the transition progress. Shows whether operators are progressing,
		available, or degraded during the transition.
	`)

	statusExample = templates.Examples(`
		# Monitor transition progress
		oc adm transition topology status
	`)
)

// StatusOptions holds options for the status subcommand
type StatusOptions struct {
	*TransitionOptions
}

// NewCmdStatus creates the status subcommand
func NewCmdStatus(f kcmdutil.Factory, parent *TransitionOptions) *cobra.Command {
	o := &StatusOptions{
		TransitionOptions: parent,
	}

	cmd := &cobra.Command{
		Use:     "status",
		Short:   "Monitor topology transition progress",
		Long:    statusLong,
		Example: statusExample,
		Run: func(cmd *cobra.Command, args []string) {
			// Ensure clients are initialized (parent Complete may not have been called yet)
			if o.configClient == nil {
				kcmdutil.CheckErr(o.Complete(f, cmd, args))
			}
			kcmdutil.CheckErr(o.Run())
		},
	}

	return cmd
}

// Run executes the status subcommand
func (o *StatusOptions) Run() error {
	ctx := context.TODO()

	// Get Infrastructure resource
	infra, err := o.configClient.ConfigV1().Infrastructures().Get(ctx, InfrastructureResourceName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get Infrastructure resource: %w", err)
	}

	// Display topology status
	fmt.Fprintf(o.Out, "Control Plane Topology Status:\n\n")

	// Show both spec (desired) and status (current) topology
	if infra.Spec.ControlPlaneTopology != "" {
		fmt.Fprintf(o.Out, "  Spec (desired):   %s\n", infra.Spec.ControlPlaneTopology)
	} else {
		fmt.Fprintf(o.Out, "  Spec (desired):   (not set)\n")
	}
	fmt.Fprintf(o.Out, "  Status (current): %s\n\n", infra.Status.ControlPlaneTopology)

	// Check if transition is in progress
	if infra.Spec.ControlPlaneTopology != "" && infra.Spec.ControlPlaneTopology != infra.Status.ControlPlaneTopology {
		fmt.Fprintf(o.Out, "Transition in progress: %s -> %s\n\n",
			infra.Status.ControlPlaneTopology,
			infra.Spec.ControlPlaneTopology)
	} else {
		fmt.Fprintf(o.Out, "No transition in progress\n\n")
	}

	// Display cluster operator status
	fmt.Fprintf(o.Out, "Cluster Operators:\n\n")

	operators, err := o.configClient.ConfigV1().ClusterOperators().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list cluster operators: %w", err)
	}

	if len(operators.Items) == 0 {
		fmt.Fprintf(o.Out, "  (no cluster operators found)\n")
		return nil
	}

	// Track operator health
	var progressing, degraded, unavailable []string

	for _, co := range operators.Items {
		var status []string
		isHealthy := true

		for _, cond := range co.Status.Conditions {
			switch cond.Type {
			case ClusterOperatorAvailable:
				if cond.Status != configv1.ConditionTrue {
					status = append(status, fmt.Sprintf("Available=%s", cond.Status))
					unavailable = append(unavailable, co.Name)
					isHealthy = false
				}
			case ClusterOperatorProgressing:
				if cond.Status == configv1.ConditionTrue {
					status = append(status, "Progressing=True")
					progressing = append(progressing, co.Name)
					isHealthy = false
				}
			case ClusterOperatorDegraded:
				if cond.Status == configv1.ConditionTrue {
					status = append(status, "Degraded=True")
					degraded = append(degraded, co.Name)
					isHealthy = false
				}
			}
		}

		// Only show unhealthy operators
		if !isHealthy {
			fmt.Fprintf(o.Out, "  %s: %s\n", co.Name, strings.Join(status, ", "))
		}
	}

	if len(progressing) == 0 && len(degraded) == 0 && len(unavailable) == 0 {
		fmt.Fprintf(o.Out, "  All cluster operators healthy\n")
	} else {
		fmt.Fprintf(o.Out, "\n")
		if len(progressing) > 0 {
			fmt.Fprintf(o.Out, "  Progressing: %d operator(s)\n", len(progressing))
		}
		if len(degraded) > 0 {
			fmt.Fprintf(o.Out, "  Degraded: %d operator(s)\n", len(degraded))
		}
		if len(unavailable) > 0 {
			fmt.Fprintf(o.Out, "  Unavailable: %d operator(s)\n", len(unavailable))
		}
	}

	// Display etcd status
	fmt.Fprintf(o.Out, "\netcd Status:\n\n")

	etcd, err := o.operatorClient.OperatorV1().Etcds().Get(ctx, EtcdOperatorResourceName, metav1.GetOptions{})
	if err != nil {
		fmt.Fprintf(o.Out, "  Failed to get etcd operator: %v\n", err)
		return nil
	}

	// Check etcd conditions
	var etcdStatus []string
	for _, cond := range etcd.Status.Conditions {
		switch cond.Type {
		case EtcdMembersAvailableCondition:
			if cond.Status == "True" {
				etcdStatus = append(etcdStatus, "Members available")
			} else {
				etcdStatus = append(etcdStatus, fmt.Sprintf("Members not available: %s", cond.Message))
			}
		case "Progressing":
			if cond.Status == "True" {
				etcdStatus = append(etcdStatus, fmt.Sprintf("Progressing: %s", cond.Message))
			}
		case "Degraded":
			if cond.Status == "True" {
				etcdStatus = append(etcdStatus, fmt.Sprintf("Degraded: %s", cond.Message))
			}
		}
	}

	if len(etcdStatus) == 0 {
		fmt.Fprintf(o.Out, "  etcd healthy\n")
	} else {
		for _, s := range etcdStatus {
			fmt.Fprintf(o.Out, "  %s\n", s)
		}
	}

	// Get etcd voting member count
	cm, err := o.kubeClient.CoreV1().ConfigMaps(EtcdNamespace).Get(ctx, EtcdEndpointsConfigMapName, metav1.GetOptions{})
	if err != nil {
		fmt.Fprintf(o.Out, "  Failed to get %s ConfigMap: %v\n", EtcdEndpointsConfigMapName, err)
	} else {
		votingMembers := len(cm.Data)
		fmt.Fprintf(o.Out, "  Voting members: %d\n", votingMembers)
	}

	return nil
}
