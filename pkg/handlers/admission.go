package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/navikt/knep/pkg/bigquery"
	"github.com/navikt/knep/pkg/config"
	"github.com/navikt/knep/pkg/k8s"
	"k8s.io/api/admission/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

type AdmissionHandler struct {
	cfg       config.Config
	decoder   runtime.Decoder
	bqClient  *bigquery.BigQuery
	k8sClient *k8s.K8SClient
	logger    *slog.Logger
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*AdmissionHandler, error) {
	bqClient, err := bigquery.New(ctx, cfg.StatsProjectID, cfg.StatsDatasetID, cfg.StatsTableID)
	if err != nil {
		logger.Error("creating bigquery client", "error", err)
		return nil, err
	}

	k8sClient, err := k8s.New(cfg.InCluster, bqClient, logger)
	if err != nil {
		logger.Error("creating k8s client", "error", err)
		return nil, err
	}

	return &AdmissionHandler{
		cfg:       cfg,
		k8sClient: k8sClient,
		logger:    logger,
	}, nil
}

func (a *AdmissionHandler) Validate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		a.logger.Error("reading request body", "error", err)
	}

	var review v1beta1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil {
		a.logger.Error("unmarshalling admission request", "error", err)
	}

	err = a.k8sClient.AlterNetpol(r.Context(), review.Request)
	if err == nil {
		review.Response = &v1beta1.AdmissionResponse{
			Allowed: true,
			UID:     review.Request.UID,
		}
	} else {
		a.logger.Error("altering netpol", "error", err)
		review.Response = &v1beta1.AdmissionResponse{
			Allowed: false,
			UID:     review.Request.UID,
			Result: &v1.Status{
				Status:  "Failure",
				Message: err.Error(),
			},
		}
	}

	resp, err := json.Marshal(review)
	if err != nil {
		a.logger.Error("marshalling admission response", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}
