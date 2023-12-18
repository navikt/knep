package api

import (
	"context"
	"log/slog"

	"github.com/go-chi/chi/v5"
)

func New(ctx context.Context, incluster bool, onpremFirewallFilePath string, stats StatsSink, logger *slog.Logger) (*chi.Mux, error) {
	admissionHandler, err := NewAdmissionHandler(ctx, incluster, onpremFirewallFilePath, stats, logger)
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
