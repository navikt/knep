package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/navikt/knep/pkg/api"
	"github.com/navikt/knep/pkg/statswriter"
)

type Config struct {
	CertPath                string
	InCluster               bool
	WriteStatistics         bool
	OnpremHostMapFilePath   string
	ExternalHostMapFilePath string
	BigQuery                statswriter.BigQuery
}

var cfg = Config{}

func init() {
	flag.StringVar(&cfg.BigQuery.ProjectID, "stats-bigquery-project", os.Getenv("BIGQUERY_PROJECT"), "The GCP project where allowlist statistics should be written")
	flag.StringVar(&cfg.BigQuery.DatasetID, "stats-bigquery-dataset", os.Getenv("BIGQUERY_DATASET"), "The BigQuery dataset where allowlist statistics should be written")
	flag.StringVar(&cfg.BigQuery.TableID, "stats-bigquery-table", os.Getenv("BIGQUERY_TABLE"), "The BigQuery dataset where allowlist statistics should be written")
	flag.StringVar(&cfg.OnpremHostMapFilePath, "onprem-hostmap-file", os.Getenv("ONPREM_HOSTMAP_FILE"), "Path to the onprem hostmap map file")
	flag.StringVar(&cfg.ExternalHostMapFilePath, "external-hostmap-file", os.Getenv("EXTERNAL_HOSTMAP_FILE"), "Path to the external hostmap map file")
	flag.StringVar(&cfg.CertPath, "cert-path", os.Getenv("CERT_PATH"), "The path to the directory containing tls certificate and key")
	flag.BoolVar(&cfg.InCluster, "in-cluster", true, "Whether the app is running locally or in cluster")
	flag.BoolVar(&cfg.WriteStatistics, "write-statistics", true, "Whether to write allowlist statistics to BigQuery")
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()
	flag.Parse()

	statisticsChan := make(chan statswriter.AllowListStatistics, 100) // Channel can store 100 messages before becoming full

	if cfg.WriteStatistics {
		go statswriter.Run(ctx, cfg.BigQuery, statisticsChan, logger)
	}

	api, err := api.New(ctx, cfg.InCluster, cfg.OnpremHostMapFilePath, cfg.ExternalHostMapFilePath, statisticsChan, logger)
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
