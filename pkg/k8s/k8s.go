package k8s

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/navikt/knep/pkg/bigquery"
	"github.com/navikt/knep/pkg/hostmap"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type K8SClient struct {
	hostMap        *hostmap.HostMap
	client         *kubernetes.Clientset
	dynamicClient  *dynamic.DynamicClient
	bigqueryClient *bigquery.BigQuery
	logger         *slog.Logger
}

func New(inCluster bool, hostMap *hostmap.HostMap, bigqueryClient *bigquery.BigQuery, logger *slog.Logger) (*K8SClient, error) {
	config, err := createKubeConfig(inCluster)
	if err != nil {
		return nil, err
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &K8SClient{
		hostMap:        hostMap,
		client:         client,
		dynamicClient:  dynamicClient,
		bigqueryClient: bigqueryClient,
		logger:         logger,
	}, nil
}

func createKubeConfig(inCluster bool) (*rest.Config, error) {
	if inCluster {
		return rest.InClusterConfig()
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = filepath.Join(homedir.HomeDir(), ".kube", "config")
	}

	configLoadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(configLoadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
}
