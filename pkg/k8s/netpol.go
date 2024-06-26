package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/navikt/knep/pkg/statswriter"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
)

var fqdnNetpolResource = schema.GroupVersionResource{
	Group:    "networking.gke.io",
	Version:  "v1alpha3",
	Resource: "fqdnnetworkpolicies",
}

const (
	allowListAnnotationKey      = "allowlist"
	jupyterPodLabelKey          = "component"
	jupyterhubLabelValue        = "singleuser-server"
	airflowPodLabelKey          = "dag_id"
	netpolCreatedTimeoutSeconds = 20
	numFQDNRetries              = 3
)

func (k *K8SClient) AlterNetpol(ctx context.Context, admissionRequest *v1beta1.AdmissionRequest) error {
	var alterNetpol func(ctx context.Context, pod corev1.Pod) error
	var pod corev1.Pod
	switch admissionRequest.Operation {
	case v1beta1.Create:
		alterNetpol = k.createNetpol
		if err := json.Unmarshal(admissionRequest.Object.Raw, &pod); err != nil {
			k.logger.Error("unmarshalling pod object", "error", err)
			return err
		}
	case v1beta1.Delete:
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

	k.statisticsChan <- statswriter.AllowListStatistics{
		HostMap: hostMap,
		Pod:     pod,
	}

	if err := k.createOrUpdateNetworkPolicy(ctx, objectMeta, podSelector, hostMap.IP); err != nil {
		return err
	}

	if err := k.createOrUpdateFQDNNetworkPolicyWithRetry(ctx, objectMeta, podSelector, hostMap.FQDN); err != nil {
		return err
	}

	return nil
}

func (k *K8SClient) createOrUpdateNetworkPolicy(ctx context.Context, objectMeta metav1.ObjectMeta, podSelector metav1.LabelSelector, portHostMap map[int32][]string) error {
	if len(portHostMap) == 0 {
		return nil
	}

	networkPolicy, err := k.createNetworkPolicy(objectMeta, podSelector, portHostMap)
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

func (k *K8SClient) createOrUpdateFQDNNetworkPolicyWithRetry(ctx context.Context, objectMeta metav1.ObjectMeta, podSelector metav1.LabelSelector, portHostMap map[int32][]string) error {
	if len(portHostMap) == 0 {
		return nil
	}

	fqdnNetworkPolicy, err := createFQDNNetworkPolicy(objectMeta, podSelector, portHostMap)
	if err != nil {
		return err
	}

	var fqdnErr error
	for i := 1; i <= numFQDNRetries; i++ {
		if fqdnErr = k.createOrUpdateFQDNNetworkPolicy(ctx, fqdnNetworkPolicy, objectMeta); fqdnErr == nil {
			break
		}
		time.Sleep(time.Duration(i) * time.Second)
	}
	if fqdnErr != nil {
		return fqdnErr
	}

	return k.ensureNetpolCreated(ctx, fqdnNetworkPolicy.GetNamespace(), fqdnNetworkPolicy.GetName())
}

func (k *K8SClient) createOrUpdateFQDNNetworkPolicy(ctx context.Context, fqdnNetworkPolicy *unstructured.Unstructured, objectMeta metav1.ObjectMeta) error {
	existing, err := k.dynamicClient.Resource(fqdnNetpolResource).Namespace(objectMeta.Namespace).Get(ctx, fqdnNetworkPolicy.GetName(), metav1.GetOptions{})
	if err == nil {
		existing.Object["spec"] = fqdnNetworkPolicy.Object["spec"]
		_, err := k.dynamicClient.Resource(fqdnNetpolResource).Namespace(objectMeta.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	} else if apierrors.IsNotFound(err) {
		_, err = k.dynamicClient.Resource(fqdnNetpolResource).Namespace(objectMeta.Namespace).Create(ctx, fqdnNetworkPolicy, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		return err
	}

	return nil
}

func (k *K8SClient) ensureNetpolCreated(ctx context.Context, namespace, name string) error {
	timeout := int64(netpolCreatedTimeoutSeconds)
	watcher, err := k.client.NetworkingV1().NetworkPolicies(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector:  fields.OneTermEqualSelector("metadata.name", name).String(),
		TimeoutSeconds: &timeout,
	})
	if err != nil {
		return err
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Added, watch.Modified:
			return nil
		}
	}

	k.logger.Info("netpol for corresponding fqdn netpol not created", "namespace", namespace, "fqdn", name)
	return nil
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

func (k *K8SClient) createNetworkPolicy(objectMeta metav1.ObjectMeta, podSelector metav1.LabelSelector, portHostMap map[int32][]string) (*networkingv1.NetworkPolicy, error) {
	egressRules := []networkingv1.NetworkPolicyEgressRule{}
	for port, hosts := range portHostMap {

		policyPeers := []networkingv1.NetworkPolicyPeer{}
		for _, host := range hosts {
			ip, cidr, err := parseIPHost(host)
			if err != nil {
				k.logger.Error("parsing IP host", "error", err, "host", host)
				continue
			}
			policyPeers = append(policyPeers, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{
					CIDR: ip + "/" + cidr,
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

func parseIPHost(host string) (string, string, error) {
	hostParts := strings.Split(host, "/")
	if len(hostParts) == 1 {
		return host, "32", nil
	} else if len(hostParts) == 2 {
		return hostParts[0], hostParts[1], nil
	}

	return "", "", fmt.Errorf("invalid ip host: %v", host)
}
