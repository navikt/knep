package api

import (
	"context"
	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/navikt/knep/pkg/hostmap"
)

func New(ctx context.Context, incluster bool, onpremFirewallFilePath string, stats StatsSink, logger *slog.Logger) (*chi.Mux, error) {
	hostMap, err := hostmap.New(onpremFirewallFilePath)
	if err != nil {
		return nil, err
	}

	admissionHandler, err := NewAdmissionHandler(ctx, incluster, hostMap, stats, logger)
	if err != nil {
		return nil, err
	}

	router := chi.NewRouter()
	setupRoutes(router, admissionHandler)

	return router, nil
}

func setupRoutes(router *chi.Mux, admissionHandler *AdmissionHandler) {
	router.Post("/admission", admissionHandler.Validate)
}
