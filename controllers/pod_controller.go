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
	"strconv"
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

	networkingv1alpha3 "github.com/GoogleCloudPlatform/gke-fqdnnetworkpolicies-golang/api/v1alpha3"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	labelKey               = "component"
	workerLabelValue       = "worker"
	jupyterhubLabelValue   = "singleuser-server"
	allowListAnnotationKey = "allowlist"
	defaultNetpolName      = "airflow-worker-allow-fqdn"
)

//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=pods/finalizers,verbs=update
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies/finalizers,verbs=update
//+kubebuilder:rbac:groups=networking.gke.io,resources=fqdnnetworkpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.gke.io,resources=fqdnnetworkpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.gke.io,resources=fqdnnetworkpolicies/finalizers,verbs=update

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

	if !isRelevantPod(pod.Labels) {
		return ctrl.Result{}, nil
	}
	if err := r.defaultNetpolExists(ctx, pod.Namespace); err != nil {
		logger.Info("Ignoring namespace as default fqdn netpol does not exist")
		return ctrl.Result{}, nil
	}

	allowListMap := extractAllowList(pod.Annotations)
	if len(allowListMap) == 0 {
		return ctrl.Result{}, nil
	}

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
	logger := log.FromContext(ctx)
	switch pod.Status.Phase {
	case corev1.PodPending:
		logger.Info("Creating netpol")
		return r.createNetPol(ctx, pod, allowListMap)
	case corev1.PodSucceeded:
		fallthrough
	case corev1.PodFailed:
		logger.Info("Removing netpol")
		return r.deleteNetPol(ctx, pod)
	}
	return nil
}

func (r *PodReconciler) createNetPol(ctx context.Context, pod corev1.Pod, allowListMap map[string][]string) error {
	logger := log.FromContext(ctx)
	fqdnNetpol := &networkingv1alpha3.FQDNNetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}

	err := r.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, fqdnNetpol)
	if err == nil {
		logger.Info("FQDN netpol already exists")
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	egressRules, err := createEgressRules(ctx, allowListMap)
	if err != nil {
		return err
	}

	podSelector, err := createPodSelector(pod)
	if err != nil {
		return err
	}

	fqdnNetpol = &networkingv1alpha3.FQDNNetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Spec: networkingv1alpha3.FQDNNetworkPolicySpec{
			PodSelector: podSelector,
			Egress:      egressRules,
		},
	}

	if err := r.Create(ctx, fqdnNetpol); err != nil {
		return err
	}

	return nil
}

func (r *PodReconciler) deleteNetPol(ctx context.Context, pod corev1.Pod) error {
	logger := log.FromContext(ctx)

	fqdnNetpol := &networkingv1alpha3.FQDNNetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}
	if err := r.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, fqdnNetpol); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Netpol does not exists")
			return nil
		}

		return err
	}

	if err := r.Delete(ctx, fqdnNetpol); err != nil {
		return err
	}

	return nil
}

func isRelevantPod(podLabels map[string]string) bool {
	if component, ok := podLabels[labelKey]; ok {
		return component == workerLabelValue || component == jupyterhubLabelValue
	}

	return false
}

func createPodSelector(pod corev1.Pod) (metav1.LabelSelector, error) {
	switch pod.Labels[labelKey] {
	case workerLabelValue:
		return metav1.LabelSelector{
			MatchLabels: map[string]string{
				"run_id":  pod.Labels["run_id"],
				"dag_id":  pod.Labels["dag_id"],
				"task_id": pod.Labels["task_id"],
			},
		}, nil
	case jupyterhubLabelValue:
		return metav1.LabelSelector{
			MatchLabels: map[string]string{
				"component":                pod.Labels["component"],
				"hub.jupyter.org/username": pod.Labels["hub.jupyter.org/username"],
			},
		}, nil
	default:
		return metav1.LabelSelector{}, fmt.Errorf("invalid pod labels when creating network policy for pod %v", pod.Name)
	}
}

func (r *PodReconciler) defaultNetpolExists(ctx context.Context, namespace string) error {
	fqdnNetpol := &networkingv1alpha3.FQDNNetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      defaultNetpolName,
			Namespace: namespace,
		},
	}

	err := r.Get(ctx, types.NamespacedName{Name: defaultNetpolName, Namespace: namespace}, fqdnNetpol)
	return err
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

func createEgressRules(ctx context.Context, portHostMap map[string][]string) ([]networkingv1alpha3.FQDNNetworkPolicyEgressRule, error) {
	egressRules := []networkingv1alpha3.FQDNNetworkPolicyEgressRule{}
	for port, hosts := range portHostMap {
		portInt, err := strconv.Atoi(port)
		if err != nil {
			return nil, err
		}

		policyPeers := []networkingv1alpha3.FQDNNetworkPolicyPeer{
			{
				FQDNs: hosts,
			},
		}
		egressRules = append(egressRules,
			networkingv1alpha3.FQDNNetworkPolicyEgressRule{
				To: policyPeers,
				Ports: []networkingV1.NetworkPolicyPort{
					{
						Port: &intstr.IntOrString{IntVal: int32(portInt)},
					},
				},
			})
	}

	return egressRules, nil
}
