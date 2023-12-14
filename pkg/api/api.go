package api

import (
	"context"
	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/navikt/knep/pkg/config"
	"github.com/navikt/knep/pkg/handlers"
)

func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*chi.Mux, error) {
	admissionHandler, err := handlers.New(ctx, cfg, logger)
	if err != nil {
		return nil, err
	}

	router := chi.NewRouter()
	setupRoutes(router, admissionHandler)

	return router, nil
}

func setupRoutes(router *chi.Mux, admissionHandler *handlers.AdmissionHandler) {
	router.Post("/admission", admissionHandler.Validate)
}
