package controllers

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	networkingv1alpha3 "github.com/GoogleCloudPlatform/gke-fqdnnetworkpolicies-golang/api/v1alpha3"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

type allowIPFQDN struct {
	IP   map[int32][]string
	FQDN map[int32][]string
}

const (
	jupyterPodLabelKey           = "component"
	airflowPodLabelKey           = "dag_id"
	jupyterhubLabelValue         = "singleuser-server"
	allowListAnnotationKey       = "allowlist"
	defaultFQDNNetworkPolicyName = "default-allow-fqdn"
	conditionKneped              = "Kneped"
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

func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch Pod")
		return ctrl.Result{}, err
	}

	if !isRelevantPod(pod.Labels) {
		return ctrl.Result{}, nil
	}

	if err := r.defaultFQDNNetworkPolicyExists(ctx, pod.Namespace); err != nil {
		return ctrl.Result{}, nil
	}

	if _, ok := pod.Annotations[allowListAnnotationKey]; !ok {
		return ctrl.Result{}, nil
	}

	if err := r.alterNetpols(ctx, pod); err != nil {
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

func (r *PodReconciler) alterNetpols(ctx context.Context, pod corev1.Pod) error {
	switch pod.Status.Phase {
	case corev1.PodPending:
		return r.createNetpol(ctx, pod)
	case corev1.PodSucceeded:
		fallthrough
	case corev1.PodFailed:
		return r.deleteNetpol(ctx, pod)
	}
	return nil
}

func (r *PodReconciler) createNetpol(ctx context.Context, pod corev1.Pod) error {
	conditions := pod.Status.Conditions
	for _, condition := range conditions {
		if condition.Type == conditionKneped && condition.Status == corev1.ConditionTrue {
			return nil
		}
	}

	allowList := pod.Annotations[allowListAnnotationKey]
	trimmedList := strings.ReplaceAll(allowList, " ", "")
	hosts := strings.Split(trimmedList, ",")
	allowStruct, err := createPortHostMap(hosts)
	if err != nil {
		return err
	}

	podSelector, err := createPodSelector(pod)
	if err != nil {
		return err
	}

	objectMeta := metav1.ObjectMeta{
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}

	networkPolicy, err := createNetworkPolicy(objectMeta, podSelector, allowStruct.IP)
	if err != nil {
		return err
	}
	if err := r.Create(ctx, networkPolicy); err != nil {
		return err
	}

	fqdnNetworkPolicy, err := createFQDNNetworkPolicy(objectMeta, podSelector, allowStruct.FQDN)
	if err != nil {
		return err
	}
	if err := r.Create(ctx, fqdnNetworkPolicy); err != nil {
		return err
	}

	pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{
		Type:   conditionKneped,
		Status: corev1.ConditionTrue,
	})

	if err := r.SubResource("status").Update(ctx, &pod); err != nil {
		return err
	}

	return nil
}

func createNetworkPolicy(objectMeta metav1.ObjectMeta, podSelector metav1.LabelSelector, portHostMap map[int32][]string) (*networkingv1.NetworkPolicy, error) {
	egressRules := []networkingv1.NetworkPolicyEgressRule{}
	for port, hosts := range portHostMap {

		policyPeers := []networkingv1.NetworkPolicyPeer{}
		for _, host := range hosts {
			policyPeers = append(policyPeers, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{
					CIDR: host + "/32",
				},
			})
		}

		egressRules = append(egressRules,
			networkingv1.NetworkPolicyEgressRule{
				To: policyPeers,
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Port: &intstr.IntOrString{IntVal: port},
					},
				},
			})
	}

	return &networkingv1.NetworkPolicy{
		ObjectMeta: objectMeta,
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: podSelector,
			Egress:      egressRules,
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeEgress,
			},
		},
	}, nil
}

func createFQDNNetworkPolicy(objectMeta metav1.ObjectMeta, podSelector metav1.LabelSelector, portHostMap map[int32][]string) (*networkingv1alpha3.FQDNNetworkPolicy, error) {
	egressRules := []networkingv1alpha3.FQDNNetworkPolicyEgressRule{}
	for port, hosts := range portHostMap {
		policyPeers := []networkingv1alpha3.FQDNNetworkPolicyPeer{
			{
				FQDNs: hosts,
			},
		}

		egressRules = append(egressRules,
			networkingv1alpha3.FQDNNetworkPolicyEgressRule{
				To: policyPeers,
				Ports: []networkingv1.NetworkPolicyPort{
					{
						Port: &intstr.IntOrString{IntVal: port},
					},
				},
			})
	}

	objectMeta.Name = objectMeta.Name + "-fqdn"

	return &networkingv1alpha3.FQDNNetworkPolicy{
		ObjectMeta: objectMeta,
		Spec: networkingv1alpha3.FQDNNetworkPolicySpec{
			PodSelector: podSelector,
			Egress:      egressRules,
		},
	}, nil
}

func (r *PodReconciler) deleteNetpol(ctx context.Context, pod corev1.Pod) error {
	fqdnNetworkPolicy := &networkingv1alpha3.FQDNNetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name + "-fqdn",
			Namespace: pod.Namespace,
		},
	}
	if err := r.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, fqdnNetworkPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return err
	}

	if err := r.Delete(ctx, fqdnNetworkPolicy); err != nil {
		return err
	}

	networkPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}
	if err := r.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, networkPolicy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return err
	}

	if err := r.Delete(ctx, networkPolicy); err != nil {
		return err
	}

	return nil
}

func isRelevantPod(podLabels map[string]string) bool {
	// Check if Jupyter
	if component, ok := podLabels[jupyterPodLabelKey]; ok && component == jupyterhubLabelValue {
		return true
	}

	// Check if Airflow
	if _, ok := podLabels[airflowPodLabelKey]; ok {
		return true
	}

	return false
}

func createPodSelector(pod corev1.Pod) (metav1.LabelSelector, error) {
	if component, ok := pod.Labels[jupyterPodLabelKey]; ok && component == jupyterhubLabelValue {
		return metav1.LabelSelector{
			MatchLabels: map[string]string{
				"component":                pod.Labels["component"],
				"hub.jupyter.org/username": pod.Labels["hub.jupyter.org/username"],
			},
		}, nil
	}

	if _, ok := pod.Labels[airflowPodLabelKey]; ok {
		return metav1.LabelSelector{
			MatchLabels: map[string]string{
				"run_id":  pod.Labels["run_id"],
				"dag_id":  pod.Labels["dag_id"],
				"task_id": pod.Labels["task_id"],
			},
		}, nil
	}

	return metav1.LabelSelector{}, fmt.Errorf("invalid pod labels when creating network policy for pod %v", pod.Name)
}

func (r *PodReconciler) defaultFQDNNetworkPolicyExists(ctx context.Context, namespace string) error {
	fqdnNetworkPolicy := &networkingv1alpha3.FQDNNetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      defaultFQDNNetworkPolicyName,
			Namespace: namespace,
		},
	}

	err := r.Get(ctx, types.NamespacedName{Name: defaultFQDNNetworkPolicyName, Namespace: namespace}, fqdnNetworkPolicy)
	return err
}

func createPortHostMap(hosts []string) (allowIPFQDN, error) {
	ipRegex := regexp.MustCompile(`((25[0-5]|(2[0-4]|1\d|[1-9]|)\d)\.?\b){4}`)
	allow := allowIPFQDN{
		IP:   make(map[int32][]string),
		FQDN: make(map[int32][]string),
	}

	for _, hostPort := range hosts {
		parts := strings.Split(hostPort, ":")
		host := parts[0]
		portInt := int32(443)
		if len(parts) > 1 {
			port := parts[1]
			tmp, err := strconv.Atoi(port)
			if err != nil {
				return allowIPFQDN{}, err
			}
			portInt = int32(tmp)
		}

		if ipRegex.MatchString(host) {
			allow.IP[portInt] = append(allow.IP[portInt], host)
		} else {
			allow.FQDN[portInt] = append(allow.FQDN[portInt], host)
		}
	}

	return allow, nil
}
