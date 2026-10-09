package handler

import (
	"log/slog"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/config"
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/service"
)

type Handler struct {
	logger     *slog.Logger
	service    service.I
	cfg        *config.Config
	botHandler *BotHandler
}

func (h *Handler) Register(router *router.Router[*core.RequestEvent]) {
	api := router.Group("/api/v1")
	{
		auth := api.Group("/auth")
		{
			auth.GET("/", h.AuthHandler)
		}

	}

	// Register bot routes
	if h.botHandler != nil {
		h.botHandler.RegisterBotRoutes(router)
	}
}

func NewHandler(logger *slog.Logger, service service.I, cfg *config.Config, app *pocketbase.PocketBase) *Handler {
	botHandler, err := NewBotHandler(logger, cfg, app)
	if err != nil {
		logger.Error("Failed to initialize bot handler", slog.Any("error", err))
	}

	return &Handler{
		logger:     logger,
		service:    service,
		cfg:        cfg,
		botHandler: botHandler,
	}
}
