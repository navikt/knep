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

package main

import (
	"context"
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"gopkg.in/yaml.v2"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	networkingv1alpha3 "github.com/GoogleCloudPlatform/gke-fqdnnetworkpolicies-golang/api/v1alpha3"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"knada.io/knep/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	scheme        = runtime.NewScheme()
	setupLog      = ctrl.Log.WithName("setup")
	reconcilerlog = ctrl.Log.WithName("reconciler")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(networkingv1alpha3.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

func main() {
	ctx := context.Background()
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var statsProjectID string
	var statsDatasetID string
	var statsTableID string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&statsProjectID, "stats-bigquery-project", os.Getenv("BIGQUERY_PROJECT"), "The GCP project where allowlist statistics should be written")
	flag.StringVar(&statsDatasetID, "stats-bigquery-dataset", os.Getenv("BIGQUERY_DATASET"), "The BigQuery dataset where allowlist statistics should be written")
	flag.StringVar(&statsTableID, "stats-bigquery-table", os.Getenv("BIGQUERY_TABLE"), "The BigQuery dataset where allowlist statistics should be written")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "d98b17b9.knada.io",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	oracleScanHosts, err := getOracleScanHosts()
	if err != nil {
		setupLog.Error(err, "unable to get oracle scan hosts from config")
	}

	bq, err := controllers.NewBigQuery(ctx, statsProjectID, statsDatasetID, statsTableID)
	if err != nil {
		setupLog.Error(err, "unable to create bigquery client")
	}

	if err = (&controllers.PodReconciler{
		Client:          mgr.GetClient(),
		OracleScanHosts: oracleScanHosts,
		Scheme:          mgr.GetScheme(),
		BQClient:        bq,
		Log:             reconcilerlog,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getOracleScanHosts() (map[string]controllers.OracleHost, error) {
	dataBytes, err := os.ReadFile("/var/run/onprem-firewall.yaml")
	if err != nil {
		return nil, err
	}

	var hostMap controllers.Hosts
	if err := yaml.Unmarshal(dataBytes, &hostMap); err != nil {
		return nil, err
	}

	oracleScanHosts := map[string]controllers.OracleHost{}
	for _, oracleHost := range hostMap.Oracle {
		if len(oracleHost.Scan) > 0 {
			oracleScanHosts[oracleHost.Host] = oracleHost
		}
	}

	return oracleScanHosts, nil
}
