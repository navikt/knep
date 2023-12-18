package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/navikt/knep/pkg/api"
)

type Config struct {
	BindAddress string
	CertPath    string
	InCluster   bool
	Stats       api.StatsSink
}

var cfg = Config{}

func init() {
	flag.StringVar(&cfg.Stats.ProjectID, "stats-bigquery-project", os.Getenv("BIGQUERY_PROJECT"), "The GCP project where allowlist statistics should be written")
	flag.StringVar(&cfg.Stats.DatasetID, "stats-bigquery-dataset", os.Getenv("BIGQUERY_DATASET"), "The BigQuery dataset where allowlist statistics should be written")
	flag.StringVar(&cfg.Stats.TableID, "stats-bigquery-table", os.Getenv("BIGQUERY_TABLE"), "The BigQuery dataset where allowlist statistics should be written")
	flag.StringVar(&cfg.CertPath, "cert-path", os.Getenv("CERT_PATH"), "The path to the directory containing tls certificate and key")
	flag.BoolVar(&cfg.InCluster, "in-cluster", true, "Whether the app is running locally or in cluster")
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()
	flag.Parse()

	api, err := api.New(ctx, cfg.InCluster, cfg.Stats, logger)
	if err != nil {
		logger.Error("creating api", "error", err)
		os.Exit(1)
	}

	server := http.Server{
		Addr:    ":9443",
		Handler: api,
	}

	if err := server.ListenAndServeTLS(cfg.CertPath+"/tls.crt", cfg.CertPath+"/tls.key"); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
