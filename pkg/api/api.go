package api

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"
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
	router.Use(func(ctx *gin.Context) {
		admissionHandler.logger.Info(fmt.Sprintf("%v %v %v", ctx.Request.URL.Path, ctx.Request.Method, ctx.Writer.Status()))
	})
	router.Post("/admission", admissionHandler.Validate)
}
