package app

import (
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	_ "gitlab.yurtal.tech/company/blitz/business-card/back/artifacts/migrations"
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/config"
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/handler"
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/hook"
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/service"
)

func NewApp(config *config.Config) *pocketbase.PocketBase {
	app := pocketbase.New()

	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		logger := app.Logger()

		services := service.NewService(app, config)

		handlers := handler.NewHandler(logger, services, config, app)
		hooks := hook.New(logger, services, config)

		handlers.Register(e.Router)
		hooks.Register(app)

		return e.Next()
	})

	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		Automigrate: true,
	})

	return app
}
