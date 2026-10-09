package service

import (
	"fmt"
	"reflect"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type TelegramMessageRef struct {
	ChatID    int64 `json:"chat_id"`
	MessageID int   `json:"message_id"`
}

type TelegramNotifyI interface {
	SendAdmin(bot *tgbotapi.BotAPI, msg tgbotapi.Chattable) error
	SendAdminWithResults(bot *tgbotapi.BotAPI, msg tgbotapi.Chattable, fallback tgbotapi.Chattable) ([]TelegramMessageRef, error)
}

type TelegramNotifyS struct {
	admin TelegramAdminI
}

func NewTelegramNotifyS(admin TelegramAdminI) *TelegramNotifyS {
	return &TelegramNotifyS{admin: admin}
}

func (s *TelegramNotifyS) SendAdmin(bot *tgbotapi.BotAPI, msg tgbotapi.Chattable) error {
	_, err := s.SendAdminWithResults(bot, msg, nil)
	return err
}

func (s *TelegramNotifyS) SendAdminWithResults(bot *tgbotapi.BotAPI, msg tgbotapi.Chattable, fallback tgbotapi.Chattable) ([]TelegramMessageRef, error) {
	if bot == nil {
		return nil, fmt.Errorf("telegram bot is nil")
	}
	if s == nil || s.admin == nil {
		return nil, fmt.Errorf("telegram notify service is not initialized")
	}

	targets, targetsErr := s.admin.AdminTargets()

	// Preserve previous behavior (bot admins) if targets cannot be resolved.
	if targetsErr != nil {
		targets.SendToBotAdmins = true
		targets.SendToGroup = false
	}

	var out []TelegramMessageRef
	var sendErr error

	sendTo := func(chatID int64, channelUsername string) {
		targetMsg, err := retargetChattable(msg, chatID, channelUsername)
		if err != nil {
			sendErr = err
			return
		}

		sent, err := bot.Send(targetMsg)
		if err != nil && fallback != nil {
			fb, fbErr := retargetChattable(fallback, chatID, channelUsername)
			if fbErr != nil {
				sendErr = fbErr
				return
			}
			sent, err = bot.Send(fb)
		}
		if err != nil {
			sendErr = err
			return
		}
		out = append(out, TelegramMessageRef{ChatID: sent.Chat.ID, MessageID: sent.MessageID})
	}

	if targets.SendToBotAdmins {
		for _, chatID := range targets.AdminChatIDs {
			sendTo(chatID, "")
		}
	}

	if targets.SendToGroup {
		if targets.GroupChatID != 0 {
			sendTo(targets.GroupChatID, "")
		} else if targets.GroupChannelUsername != "" {
			sendTo(0, targets.GroupChannelUsername)
		} else {
			if sendErr == nil {
				sendErr = fmt.Errorf("group target is not configured")
			}
		}
	}

	if targetsErr != nil {
		return out, targetsErr
	}
	return out, sendErr
}

func retargetChattable(msg tgbotapi.Chattable, chatID int64, channelUsername string) (tgbotapi.Chattable, error) {
	// Most tgbotapi config types are value structs; we need to copy and override ChatID/ChannelUsername.
	switch m := msg.(type) {
	case tgbotapi.MessageConfig:
		m.ChatID = chatID
		m.ChannelUsername = channelUsername
		return m, nil
	case tgbotapi.PhotoConfig:
		m.ChatID = chatID
		m.ChannelUsername = channelUsername
		return m, nil
	case tgbotapi.DocumentConfig:
		m.ChatID = chatID
		m.ChannelUsername = channelUsername
		return m, nil
	case tgbotapi.LocationConfig:
		m.ChatID = chatID
		m.ChannelUsername = channelUsername
		return m, nil
	default:
		// As a fallback, try to set fields via reflection if it is a struct.
		rv := reflect.ValueOf(msg)
		if rv.Kind() != reflect.Struct {
			return nil, fmt.Errorf("unsupported telegram message type %T", msg)
		}
		copy := reflect.New(rv.Type()).Elem()
		copy.Set(rv)

		if f := copy.FieldByName("ChatID"); f.IsValid() && f.CanSet() && f.Kind() == reflect.Int64 {
			f.SetInt(chatID)
		}
		if f := copy.FieldByName("ChannelUsername"); f.IsValid() && f.CanSet() && f.Kind() == reflect.String {
			f.SetString(channelUsername)
		}

		ret, ok := copy.Interface().(tgbotapi.Chattable)
		if !ok {
			return nil, fmt.Errorf("unsupported telegram message type %T", msg)
		}
		return ret, nil
	}
}
