package k8s

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/navikt/knep/pkg/bigquery"
	"gopkg.in/yaml.v2"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type Host struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type OracleHost struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	Scan []Host `json:"scan"`
}

type Hosts struct {
	Oracle []OracleHost `json:"oracle"`
}

type K8SClient struct {
	oracleScanHosts map[string]OracleHost
	client          *kubernetes.Clientset
	dynamicClient   *dynamic.DynamicClient
	bigqueryClient  *bigquery.BigQuery
	logger          *slog.Logger
}

func New(inCluster bool, onpremFirewallPath string, bigqueryClient *bigquery.BigQuery, logger *slog.Logger) (*K8SClient, error) {
	client, err := createClientset(inCluster)
	if err != nil {
		return nil, err
	}

	dynamicClient, err := createDynamicClient(inCluster)
	if err != nil {
		return nil, err
	}

	oracleScanHosts, err := getOracleScanHosts(onpremFirewallPath)
	if err != nil {
		return nil, err
	}

	return &K8SClient{
		oracleScanHosts: oracleScanHosts,
		client:          client,
		dynamicClient:   dynamicClient,
		bigqueryClient:  bigqueryClient,
		logger:          logger,
	}, nil
}

func createClientset(inCluster bool) (*kubernetes.Clientset, error) {
	config, err := createKubeConfig(inCluster)
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(config)
}

func createDynamicClient(inCluster bool) (*dynamic.DynamicClient, error) {
	config, err := createKubeConfig(inCluster)
	if err != nil {
		return nil, err
	}

	return dynamic.NewForConfig(config)
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
	// TODO: Virker ikke som at man får satt context på denne måten
	configOverrides := &clientcmd.ConfigOverrides{CurrentContext: "minikube"}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(configLoadingRules, configOverrides).ClientConfig()
}

func getOracleScanHosts(onpremFirewallPath string) (map[string]OracleHost, error) {
	dataBytes, err := os.ReadFile(onpremFirewallPath)
	if err != nil {
		return nil, err
	}

	var hostMap Hosts
	if err := yaml.Unmarshal(dataBytes, &hostMap); err != nil {
		return nil, err
	}

	oracleScanHosts := map[string]OracleHost{}
	for _, oracleHost := range hostMap.Oracle {
		if len(oracleHost.Scan) > 0 {
			oracleScanHosts[oracleHost.Host] = oracleHost
		}
	}

	return oracleScanHosts, nil
}
