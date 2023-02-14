/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"net"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingV1 "k8s.io/api/networking/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	airflowLabelKey        = "component"
	workerLabelValue       = "worker"
	allowListAnnotationKey = "allowlist"
)

//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=update
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Pod object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			// we'll ignore not-found errors, since they can't be fixed by an immediate
			// requeue (we'll need to wait for a new notification), and we can get them
			// on deleted requests.
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch Pod")
		return ctrl.Result{}, err
	}

	// temporarily restrict controller to team-knada-hyka namespace
	if !isAirflowWorker(pod.Labels) || pod.Namespace != "team-knada-hyka" {
		return ctrl.Result{}, nil
	}

	allowListMap := extractAllowList(pod.Annotations)
	if len(allowListMap) == 0 {
		return ctrl.Result{}, nil
	}

	logger.Info("pod: %v, namespace: %v", req.Name, req.Namespace)

	fmt.Println(allowListMap)

	if err := r.alterNetPol(ctx, pod, allowListMap); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(r)
}

func (r *PodReconciler) alterNetPol(ctx context.Context, pod corev1.Pod, allowListMap map[string][]string) error {
	switch pod.Status.Phase {
	case corev1.PodPending:
		fmt.Println("creating netpol")
		return r.createNetPol(ctx, pod, allowListMap)
	case corev1.PodSucceeded:
		fallthrough
	case corev1.PodFailed:
		fmt.Println("removing netpol")
		return r.deleteNetPol(ctx, pod)
	}
	return nil
}

func (r *PodReconciler) createNetPol(ctx context.Context, pod corev1.Pod, allowListMap map[string][]string) error {
	netpol := &networkingV1.NetworkPolicy{}
	if err := r.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, netpol); err != nil {
		if apierrors.IsNotFound(err) {
			// ignoring if netpol already exists
			fmt.Println("netpol already exists")
			return nil
		}

		return err
	}

	egressRules, err := createEgressRules(ctx, allowListMap)
	if err != nil {
		return err
	}

	fmt.Println(egressRules)

	netpol = &networkingV1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Spec: networkingV1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"run_id":  pod.Labels["run_id"],
					"dag_id":  pod.Labels["dag_id"],
					"task_id": pod.Labels["task_id"],
				},
			},
			Egress: egressRules,
		},
	}

	if err := r.Create(ctx, netpol); err != nil {
		return err
	}

	return nil
}

func (r *PodReconciler) deleteNetPol(ctx context.Context, pod corev1.Pod) error {
	netpol := &networkingV1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}
	if err := r.Delete(ctx, netpol); err != nil {
		return err
	}

	return nil
}

func isAirflowWorker(podLabels map[string]string) bool {
	if component, ok := podLabels[airflowLabelKey]; ok {
		return component == workerLabelValue
	}

	return false
}

func extractAllowList(annotations map[string]string) map[string][]string {
	if allowList, ok := annotations[allowListAnnotationKey]; ok {
		hosts := strings.Split(allowList, ",")
		return createPortHostMap(hosts)
	}

	return map[string][]string{}
}

func createPortHostMap(hosts []string) map[string][]string {
	portHostMap := map[string][]string{}
	for _, h := range hosts {
		parts := strings.Split(h, ":")
		if len(parts) > 1 {
			portHostMap[parts[1]] = append(portHostMap[parts[1]], parts[0])
		} else {
			portHostMap["443"] = append(portHostMap["443"], parts[0])
		}
	}

	return portHostMap
}

func createEgressRules(ctx context.Context, portHostMap map[string][]string) ([]networkingV1.NetworkPolicyEgressRule, error) {
	egressRules := []networkingV1.NetworkPolicyEgressRule{}
	for port, hosts := range portHostMap {
		policyPeers, err := createPolicyPeers(ctx, hosts)
		if err != nil {
			return nil, err
		}
		egressRules = append(egressRules,
			networkingV1.NetworkPolicyEgressRule{
				To: policyPeers,
				Ports: []networkingV1.NetworkPolicyPort{
					{
						Port: &intstr.IntOrString{StrVal: port},
					},
				},
			})
	}

	return egressRules, nil
}

func createPolicyPeers(ctx context.Context, hosts []string) ([]networkingV1.NetworkPolicyPeer, error) {
	policyPeers := []networkingV1.NetworkPolicyPeer{}
	for _, h := range hosts {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", h)
		if err != nil {
			return nil, err
		}

		for _, ip := range ips {
			policyPeers = append(policyPeers, networkingV1.NetworkPolicyPeer{
				IPBlock: &networkingV1.IPBlock{
					CIDR: ip.String() + "/32",
				},
			})
		}

	}

	return policyPeers, nil
}
