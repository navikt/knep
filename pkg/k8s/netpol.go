package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var fqdnNetpolResource = schema.GroupVersionResource{
	Group:    "networking.gke.io",
	Version:  "v1alpha3",
	Resource: "fqdnnetworkpolicies",
}

const (
	allowListAnnotationKey       = "allowlist"
	jupyterPodLabelKey           = "component"
	jupyterhubLabelValue         = "singleuser-server"
	airflowPodLabelKey           = "dag_id"
	defaultFQDNNetworkPolicyName = "default-allow-fqdn"
	netpolCreatedTimeoutSeconds  = 10
)

func (k *K8SClient) AlterNetpol(ctx context.Context, admissionRequest *v1beta1.AdmissionRequest) error {
	var alterNetpol func(ctx context.Context, pod corev1.Pod) error
	var pod corev1.Pod
	switch admissionRequest.Operation {
	case v1beta1.Create:
		fmt.Printf("create request for pod %v in namespace %v", pod.Name, pod.Namespace)
		alterNetpol = k.createNetpol
		if err := json.Unmarshal(admissionRequest.Object.Raw, &pod); err != nil {
			k.logger.Error("unmarshalling pod object", "error", err)
			return err
		}
	case v1beta1.Delete:
		fmt.Printf("delete request for pod %v in namespace %v", pod.Name, pod.Namespace)
		alterNetpol = k.deleteNetpol
		if err := json.Unmarshal(admissionRequest.OldObject.Raw, &pod); err != nil {
			k.logger.Error("unmarshalling pod object", "error", err)
			return err
		}
	default:
		k.logger.Info("unsupported request operation %v", "operation", admissionRequest.Operation)
		return nil
	}

	if !isRelevantPod(pod.Labels) {
		return nil
	}

	if err := k.defaultFQDNNetworkPolicyExists(ctx, pod.Namespace); err != nil {
		return nil
	}

	if _, ok := pod.Annotations[allowListAnnotationKey]; !ok {
		return nil
	}

	return alterNetpol(ctx, pod)
}

func (k *K8SClient) createNetpol(ctx context.Context, pod corev1.Pod) error {
	allowList := pod.Annotations[allowListAnnotationKey]
	trimmedList := strings.ReplaceAll(allowList, " ", "")
	hosts := strings.Split(trimmedList, ",")
	hostMap, err := k.hostMap.CreatePortHostMap(hosts)
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
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "knep",
		},
	}

	if err := k.createOrUpdateNetworkPolicy(ctx, objectMeta, podSelector, hostMap.IP); err != nil {
		return err
	}

	if err := k.createOrReplaceFQDNNetworkPolicy(ctx, objectMeta, podSelector, hostMap.FQDN); err != nil {
		return err
	}

	if err := k.bigqueryClient.PersistAllowlistStats(ctx, hostMap, pod); err != nil {
		k.logger.Error("persisting allowlist stats", "error", err)
	}

	return nil
}

func (k *K8SClient) createOrUpdateNetworkPolicy(ctx context.Context, objectMeta metav1.ObjectMeta, podSelector metav1.LabelSelector, portHostMap map[int32][]string) error {
	if len(portHostMap) == 0 {
		return nil
	}

	networkPolicy, err := createNetworkPolicy(objectMeta, podSelector, portHostMap)
	if err != nil {
		return err
	}

	_, err = k.client.NetworkingV1().NetworkPolicies(objectMeta.Namespace).Create(ctx, networkPolicy, metav1.CreateOptions{})
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		_, err := k.client.NetworkingV1().NetworkPolicies(objectMeta.Namespace).Update(ctx, networkPolicy, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

func (k *K8SClient) createOrReplaceFQDNNetworkPolicy(ctx context.Context, objectMeta metav1.ObjectMeta, podSelector metav1.LabelSelector, portHostMap map[int32][]string) error {
	if len(portHostMap) == 0 {
		return nil
	}

	fqdnNetworkPolicy, err := createFQDNNetworkPolicy(objectMeta, podSelector, portHostMap)
	if err != nil {
		return err
	}

	_, err = k.dynamicClient.Resource(fqdnNetpolResource).Namespace(objectMeta.Namespace).Get(ctx, fqdnNetworkPolicy.GetName(), metav1.GetOptions{})
	if err == nil {
		if err := k.dynamicClient.Resource(fqdnNetpolResource).Namespace(objectMeta.Namespace).Delete(ctx, fqdnNetworkPolicy.GetName(), metav1.DeleteOptions{}); err != nil {
			return err
		}
	} else if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	_, err = k.dynamicClient.Resource(fqdnNetpolResource).Namespace(objectMeta.Namespace).Create(ctx, fqdnNetworkPolicy, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	fmt.Println("fqdn netpol namespace", fqdnNetworkPolicy.GetNamespace())
	fmt.Println("fqdn netpol name", fqdnNetworkPolicy.GetName())

	return nil // k.ensureNetpolCreated(ctx, fqdnNetworkPolicy.GetNamespace(), fqdnNetworkPolicy.GetName())
}

func (k *K8SClient) ensureNetpolCreated(ctx context.Context, namespace, name string) error {
	for i := 0; i < netpolCreatedTimeoutSeconds; i++ {
		_, err := k.client.NetworkingV1().NetworkPolicies(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			return nil
		}
		fmt.Println("error:", err)
		time.Sleep(time.Second)
	}
	return nil
	// return fmt.Errorf("netpol for corresponding fqdn netpol not created in %v seconds", netpolCreatedTimeoutSeconds)
}

func (k *K8SClient) deleteNetpol(ctx context.Context, pod corev1.Pod) error {
	err := k.dynamicClient.Resource(fqdnNetpolResource).Namespace(pod.Namespace).Delete(ctx, pod.Name+"-fqdn", metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	err = k.client.NetworkingV1().NetworkPolicies(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
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

func createFQDNNetworkPolicy(objectMeta metav1.ObjectMeta, podSelector metav1.LabelSelector, portHostMap map[int32][]string) (*unstructured.Unstructured, error) {
	egressRules := []map[string]any{}
	for port, hosts := range portHostMap {
		egressRules = append(egressRules, map[string]any{
			"to": []map[string][]string{
				{
					"fqdns": hosts,
				},
			},
			"ports": []map[string]any{
				{
					"port": port,
				},
			},
		})
	}

	fqdnNetpol := &unstructured.Unstructured{}
	fqdnNetpol.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "networking.gke.io/v1alpha3",
		"kind":       "FQDNNetworkPolicy",
		"metadata": map[string]any{
			"name":      objectMeta.Name + "-fqdn",
			"namespace": objectMeta.Namespace,
			"labels":    objectMeta.Labels,
		},
		"spec": map[string]any{
			"podSelector": map[string]any{
				"matchLabels": podSelector.MatchLabels,
			},
			"egress": egressRules,
			"policyTypes": []string{
				"Egress",
			},
		},
	})

	return fqdnNetpol, nil
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

func (k *K8SClient) defaultFQDNNetworkPolicyExists(ctx context.Context, namespace string) error {
	_, err := k.dynamicClient.Resource(fqdnNetpolResource).Namespace(namespace).Get(ctx, defaultFQDNNetworkPolicyName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	return nil
}
