package service

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/config"
)

type TelegramAdminTargets struct {
	ConfirmationType string

	SendToBotAdmins bool
	SendToGroup     bool

	AdminChatIDs []int64

	GroupChatID          int64
	GroupChannelUsername string
}

type TelegramAdminI interface {
	AdminTargets() (TelegramAdminTargets, error)
}

type TelegramAdminS struct {
	app *pocketbase.PocketBase
	cfg *config.Config
}

func NewTelegramAdminS(app *pocketbase.PocketBase, cfg *config.Config) *TelegramAdminS {
	return &TelegramAdminS{app: app, cfg: cfg}
}

func (s *TelegramAdminS) AdminTargets() (TelegramAdminTargets, error) {
	var out TelegramAdminTargets

	confirmationType := "bot"
	if s.app != nil {
		contacts, _ := s.app.FindRecordsByFilter("contacts", "", "", 1, 0, dbx.Params{})
		if len(contacts) > 0 {
			ct := strings.TrimSpace(strings.ToLower(contacts[0].GetString("confirmation_type")))
			switch ct {
			case "bot", "group", "both":
				confirmationType = ct
			}
		}
	}

	out.ConfirmationType = confirmationType
	out.SendToBotAdmins = confirmationType == "bot" || confirmationType == "both" || confirmationType == ""
	out.SendToGroup = confirmationType == "group" || confirmationType == "both"

	if s.app != nil {
		admins, err := s.app.FindRecordsByFilter(
			"_pb_users_auth_",
			"role = 'admin' && chat_id != ''",
			"",
			500,
			0,
			dbx.Params{},
		)
		if err == nil {
			for _, admin := range admins {
				cidStr := strings.TrimSpace(admin.GetString("chat_id"))
				cid, parseErr := strconv.ParseInt(cidStr, 10, 64)
				if parseErr == nil && cid != 0 {
					out.AdminChatIDs = append(out.AdminChatIDs, cid)
				}
			}
		}
	}

	chatID, username, parseErr := parseTelegramGroupTarget(strings.TrimSpace(s.cfg.GroupURL))
	if parseErr != nil {
		return out, parseErr
	}
	out.GroupChatID = chatID
	out.GroupChannelUsername = username

	if out.SendToGroup && out.GroupChatID == 0 && out.GroupChannelUsername == "" {
		return out, fmt.Errorf("confirmation_type requires group notifications but GROUP_URL is empty or unsupported")
	}

	return out, nil
}

func parseTelegramGroupTarget(groupURL string) (int64, string, error) {
	if groupURL == "" {
		return 0, "", nil
	}

	// Numeric chat id (e.g. -1001234567890)
	if id, err := strconv.ParseInt(groupURL, 10, 64); err == nil {
		return id, "", nil
	}

	// Username format
	if strings.HasPrefix(groupURL, "@") {
		return 0, groupURL, nil
	}

	if strings.HasPrefix(groupURL, "http://") || strings.HasPrefix(groupURL, "https://") {
		u, err := url.Parse(groupURL)
		if err != nil {
			return 0, "", fmt.Errorf("invalid GROUP_URL: %w", err)
		}
		path := strings.Trim(strings.TrimSpace(u.Path), "/")
		if path == "" {
			return 0, "", fmt.Errorf("invalid GROUP_URL: missing path")
		}
		if strings.HasPrefix(path, "+") || strings.HasPrefix(path, "joinchat/") {
			return 0, "", fmt.Errorf("GROUP_URL invite links (t.me/+) cannot be used by the bot to send messages; use a public @username or numeric chat id")
		}
		// t.me/<username>
		return 0, "@" + path, nil
	}

	// Bare username without @
	return 0, "@" + groupURL, nil
}
