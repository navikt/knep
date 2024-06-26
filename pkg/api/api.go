package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httplog/v2"
	"github.com/navikt/knep/pkg/hostmap"
	"github.com/navikt/knep/pkg/statswriter"
)

func New(ctx context.Context, incluster bool, onpremHostMapFilePath, externalHostMapFilePath string, statisticsChan chan statswriter.AllowListStatistics, log *slog.Logger) (*chi.Mux, error) {
	hostMap, err := hostmap.New(onpremHostMapFilePath, externalHostMapFilePath)
	if err != nil {
		return nil, err
	}

	admissionHandler, err := NewAdmissionHandler(ctx, incluster, hostMap, statisticsChan, log)
	if err != nil {
		return nil, err
	}

	logger := httplog.NewLogger("api", httplog.Options{
		JSON:             true,
		Concise:          true,
		RequestHeaders:   true,
		MessageFieldName: "message",
		TimeFieldFormat:  time.RFC850,
	})

	router := chi.NewRouter()
	router.Use(httplog.RequestLogger(logger))
	router.Use(middleware.Logger)
	router.Post("/admission", admissionHandler.Validate)

	return router, nil
}
