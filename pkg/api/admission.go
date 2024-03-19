package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/navikt/knep/pkg/hostmap"
	"github.com/navikt/knep/pkg/k8s"
	"github.com/navikt/knep/pkg/statswriter"
	"k8s.io/api/admission/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AdmissionHandler struct {
	k8sClient *k8s.K8SClient
	logger    *slog.Logger
}

func NewAdmissionHandler(ctx context.Context, inCluster bool, hostMap *hostmap.HostMap, statisticsChan chan statswriter.AllowListStatistics, logger *slog.Logger) (*AdmissionHandler, error) {
	k8sClient, err := k8s.New(inCluster, hostMap, statisticsChan, logger)
	if err != nil {
		logger.Error("creating k8s client", "error", err)
		return nil, err
	}

	return &AdmissionHandler{
		k8sClient: k8sClient,
		logger:    logger,
	}, nil
}

func (a *AdmissionHandler) Validate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		a.logger.Error("reading request body", "error", err)
		return
	}

	var review v1beta1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil {
		a.logger.Error("unmarshalling admission request", "error", err)
		return
	}

	a.logger.Info(fmt.Sprintf("admission request for %s/%s", review.Request.Namespace, review.Request.Name))
	if err := a.k8sClient.AlterNetpol(r.Context(), review.Request); err != nil {
		a.logger.Error("altering netpol", "error", err)
		review.Response = &v1beta1.AdmissionResponse{
			Allowed: false,
			UID:     review.Request.UID,
			Result: &v1.Status{
				Status:  "Failure",
				Message: err.Error(),
			},
		}
	} else {
		review.Response = &v1beta1.AdmissionResponse{
			Allowed: true,
			UID:     review.Request.UID,
		}
	}

	resp, err := json.Marshal(review)
	if err != nil {
		a.logger.Error("marshalling admission response", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}
