package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/navikt/knep/pkg/api"
	"github.com/navikt/knep/pkg/config"
)

var cfg = config.Config{}

func init() {
	flag.StringVar(&cfg.BindAddress, "bind-address", ":9443", "The bind address")
	flag.StringVar(&cfg.StatsProjectID, "stats-bigquery-project", os.Getenv("BIGQUERY_PROJECT"), "The GCP project where allowlist statistics should be written")
	flag.StringVar(&cfg.StatsDatasetID, "stats-bigquery-dataset", os.Getenv("BIGQUERY_DATASET"), "The BigQuery dataset where allowlist statistics should be written")
	flag.StringVar(&cfg.StatsTableID, "stats-bigquery-table", os.Getenv("BIGQUERY_TABLE"), "The BigQuery dataset where allowlist statistics should be written")
	flag.BoolVar(&cfg.InCluster, "in-cluster", true, "Whether the app is running locally or in cluster")
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()
	flag.Parse()

	api, err := api.New(ctx, cfg, logger)
	if err != nil {
		logger.Error("creating api", "error", err)
		os.Exit(1)
	}

	server := http.Server{
		Addr:    cfg.BindAddress,
		Handler: api,
	}

	if err := server.ListenAndServeTLS("/run/secrets/tls/tls.crt", "/run/secrets/tls/tls.key"); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
