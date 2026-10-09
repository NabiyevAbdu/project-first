package service

import (
	"github.com/pocketbase/pocketbase"
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/config"
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/model"
)

type AuthorizationI interface {
	AmoCRMTokenExchange(req *model.AmoCRMTokenExchangeRequest) (*model.AmoCRMTokenExchangeResponse, error)
}

type I interface {
	Authorization() AuthorizationI
	TelegramAdmin() TelegramAdminI
	TelegramNotify() TelegramNotifyI
}

type service struct {
	AuthorizationI
	TelegramAdminI
	TelegramNotifyI
}

func (s *service) Authorization() AuthorizationI {
	return s.AuthorizationI
}

func (s *service) TelegramAdmin() TelegramAdminI {
	return s.TelegramAdminI
}

func (s *service) TelegramNotify() TelegramNotifyI {
	return s.TelegramNotifyI
}

func NewService(app *pocketbase.PocketBase, cfg *config.Config) I {
	admin := NewTelegramAdminS(app, cfg)
	notify := NewTelegramNotifyS(admin)
	return &service{
		AuthorizationI:  NewAuthorizationS(app),
		TelegramAdminI:  admin,
		TelegramNotifyI: notify,
	}
}
