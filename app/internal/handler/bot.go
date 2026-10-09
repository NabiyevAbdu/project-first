package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/config"
)

type tgMessageRef struct {
	ChatID    int64 `json:"chat_id"`
	MessageID int   `json:"message_id"`
}

func splitTextRunes(s string, maxRunes int) []string {
	if maxRunes <= 0 {
		return []string{s}
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return []string{s}
	}

	parts := make([]string, 0, (len(r)/maxRunes)+1)
	for i := 0; i < len(r); i += maxRunes {
		end := i + maxRunes
		if end > len(r) {
			end = len(r)
		}
		parts = append(parts, string(r[i:end]))
	}
	return parts
}

func (bh *BotHandler) sendTextInParts(chatID int64, text string, replyMarkup any) (*tgbotapi.Message, error) {
	if bh == nil || bh.bot == nil {
		return nil, fmt.Errorf("telegram bot is not initialized")
	}

	parts := splitTextRunes(text, 3500)
	var first *tgbotapi.Message
	for i, p := range parts {
		msg := tgbotapi.NewMessage(chatID, p)
		if i == 0 && replyMarkup != nil {
			msg.ReplyMarkup = replyMarkup
		}
		sent, err := bh.bot.Send(msg)
		if err != nil {
			return first, err
		}
		if i == 0 {
			first = &sent
		}
	}
	return first, nil
}

func (bh *BotHandler) sendTextInPartsReply(chatID int64, text string, replyMarkup any, replyToMessageID int) (*tgbotapi.Message, error) {
	if bh == nil || bh.bot == nil {
		return nil, fmt.Errorf("telegram bot is not initialized")
	}

	parts := splitTextRunes(text, 3500)
	var first *tgbotapi.Message
	for i, p := range parts {
		msg := tgbotapi.NewMessage(chatID, p)
		if replyToMessageID > 0 {
			msg.ReplyToMessageID = replyToMessageID
		}
		if i == 0 && replyMarkup != nil {
			msg.ReplyMarkup = replyMarkup
		}
		sent, err := bh.bot.Send(msg)
		if err != nil {
			return first, err
		}
		if i == 0 {
			first = &sent
		}
	}
	return first, nil
}

func stripDeliveryBlurb(s string) string {
	if strings.TrimSpace(s) == "" {
		return strings.TrimSpace(s)
	}
	lower := strings.ToLower(s)
	idx := strings.Index(lower, "yetkazib berish")
	if idx < 0 {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[:idx])
}

func buildCaptionFromOrderText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	parts := make([]string, 0, 6)
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		parts = append(parts, l)
		if len(parts) >= 3 {
			break
		}
	}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "Umumiy summa:") {
			parts = append(parts, l)
			break
		}
	}

	cap := strings.TrimSpace(strings.Join(parts, "\n"))
	if cap == "" {
		cap = text
	}
	capParts := splitTextRunes(cap, 900)
	if len(capParts) > 0 {
		return capParts[0]
	}
	return cap
}

func (bh *BotHandler) buildOrderCaptionUz(order *core.Record) (string, error) {
	text, err := bh.buildOrderMessageUz(order)
	if err != nil {
		return "", err
	}
	text = strings.TrimPrefix(text, "Yangi buyurtma yaratildi\n\n")
	return buildCaptionFromOrderText(text), nil
}

func isGroupChat(chat *tgbotapi.Chat) bool {
	if chat == nil {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(chat.Type))
	return t == "group" || t == "supergroup"
}

func (bh *BotHandler) appendOrderMessageRef(orderID string, ref tgMessageRef) {
	if bh == nil || bh.app == nil || orderID == "" || ref.ChatID == 0 || ref.MessageID == 0 {
		return
	}
	record, err := bh.app.FindRecordById("orders", orderID)
	if err != nil || record == nil {
		return
	}

	var refs []tgMessageRef
	raw := strings.TrimSpace(record.GetString("tg_confirmation_messages"))
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &refs)
	}
	for _, r := range refs {
		if r.ChatID == ref.ChatID && r.MessageID == ref.MessageID {
			return
		}
	}
	refs = append(refs, ref)
	b, mErr := json.Marshal(refs)
	if mErr != nil {
		bh.logger.Error("Failed to marshal tg confirmation message refs", slog.Any("error", mErr))
		return
	}
	record.Set("tg_confirmation_messages", string(b))
	if saveErr := bh.app.Save(record); saveErr != nil {
		bh.logger.Error("Failed to save tg confirmation message refs", slog.Any("error", saveErr))
	}
}

func formatOrderCreatedUz(created string) string {
	if created == "" {
		return ""
	}
	loc, locErr := time.LoadLocation("Asia/Tashkent")
	if locErr != nil {
		loc = time.FixedZone("Asia/Tashkent", 5*60*60)
	}

	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		return t.In(loc).Format("02.01.2006 15:04")
	}
	if t, err := time.Parse("2006-01-02 15:04:05.000Z", created); err == nil {
		return t.In(loc).Format("02.01.2006 15:04")
	}
	if t, err := time.Parse("2006-01-02 15:04:05Z", created); err == nil {
		return t.In(loc).Format("02.01.2006 15:04")
	}
	return created
}

func statusToUz(status string) string {
	switch status {
	case "accepted":
		return "Qabul qilingan"
	case "confirmed":
		return "Tasdiqlangan"
	case "rejected":
		return "Rad etilgan"
	case "cancelled":
		return "Bekor qilingan"
	case "done":
		return "Tugallangan"
	case "delivered":
		return "Yetkazilgan"
	case "In progress":
		return "Jarayonda"
	case "Done":
		return "Tugallangan"
	case "Cancelled":
		return "Bekor qilingan"
	default:
		return status
	}
}

func formatAmount(v float64) string {
	rounded := math.Round(v)
	if math.Abs(v-rounded) < 1e-9 {
		return strconv.FormatInt(int64(rounded), 10)
	}
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

func extractLatLng(v any) (float64, float64, bool) {
	if v == nil {
		return 0, 0, false
	}

	// Handle PocketBase geoPoint which may be returned as a struct or pointer.
	rv := reflect.ValueOf(v)
	if rv.IsValid() {
		if rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				return 0, 0, false
			}
			rv = rv.Elem()
		}
		if rv.IsValid() && rv.Kind() == reflect.Struct {
			latField := rv.FieldByName("Lat")
			if !latField.IsValid() {
				latField = rv.FieldByName("Latitude")
			}
			lngField := rv.FieldByName("Lng")
			if !lngField.IsValid() {
				lngField = rv.FieldByName("Lon")
			}
			if !lngField.IsValid() {
				lngField = rv.FieldByName("Longitude")
			}

			if latField.IsValid() && lngField.IsValid() {
				latOk := latField.Kind() == reflect.Float64 || latField.Kind() == reflect.Float32
				lngOk := lngField.Kind() == reflect.Float64 || lngField.Kind() == reflect.Float32
				if latOk && lngOk {
					return latField.Convert(reflect.TypeOf(float64(0))).Float(), lngField.Convert(reflect.TypeOf(float64(0))).Float(), true
				}
			}
		}
	}

	switch vv := v.(type) {
	case map[string]any:
		lat, latOk := vv["lat"].(float64)
		lng, lngOk := vv["lng"].(float64)
		if latOk && lngOk {
			return lat, lng, true
		}
		lat2, lat2Ok := vv["latitude"].(float64)
		lng2, lng2Ok := vv["longitude"].(float64)
		if lat2Ok && lng2Ok {
			return lat2, lng2, true
		}
	case map[string]float64:
		lat, latOk := vv["lat"]
		lng, lngOk := vv["lng"]
		if latOk && lngOk {
			return lat, lng, true
		}
		lat2, lat2Ok := vv["latitude"]
		lng2, lng2Ok := vv["longitude"]
		if lat2Ok && lng2Ok {
			return lat2, lng2, true
		}
	case []any:
		if len(vv) >= 2 {
			lat, latOk := vv[0].(float64)
			lng, lngOk := vv[1].(float64)
			if latOk && lngOk {
				return lat, lng, true
			}
		}
	case string:
		p := strings.TrimSpace(vv)
		p = strings.Trim(p, "[]()")
		parts := strings.Split(p, ",")
		if len(parts) == 2 {
			latStr := strings.TrimSpace(parts[0])
			lngStr := strings.TrimSpace(parts[1])
			lat, latErr := strconv.ParseFloat(latStr, 64)
			lng, lngErr := strconv.ParseFloat(lngStr, 64)
			if latErr == nil && lngErr == nil {
				return lat, lng, true
			}
		}
	}

	return 0, 0, false
}

func (bh *BotHandler) buildOrderMessageUz(order *core.Record) (string, error) {
	if bh.app == nil {
		return "", fmt.Errorf("app is nil")
	}
	if order == nil {
		return "", fmt.Errorf("order is nil")
	}

	orderID := order.Id
	displayOrderID := order.GetString("order_id")
	if displayOrderID == "" {
		displayOrderID = "N/A"
	}
	userID := order.GetString("user")
	statusUz := statusToUz(order.GetString("status"))
	created := formatOrderCreatedUz(order.GetString("created"))

	userRecord, _ := bh.app.FindRecordById("_pb_users_auth_", userID)

	userName := ""
	userChatID := ""
	if userRecord != nil {
		userName = userRecord.GetString("name")
		userChatID = userRecord.GetString("chat_id")
	}

	phone := order.GetString("phone")
	receiverName := order.GetString("receiver_name")
	note := order.GetString("note")

	lines := ""
	overall := 0.0
	items, itemsErr := bh.app.FindRecordsByFilter(
		"orderItems",
		"order = {:order}",
		"created",
		500,
		0,
		dbx.Params{"order": orderID},
	)
	if itemsErr != nil {
		if strings.Contains(itemsErr.Error(), "unknown field \"order\"") {
			items, itemsErr = bh.app.FindRecordsByFilter(
				"orderItems",
				"cart = {:cart}",
				"created",
				500,
				0,
				dbx.Params{"cart": orderID},
			)
		}
		if itemsErr != nil {
			items = []*core.Record{}
		}
	}

	for i, item := range items {
		pID := item.GetString("product")
		q := item.GetFloat("quantity")
		unitPrice := item.GetFloat("price_unit")
		if unitPrice == 0 {
			unitPrice = item.GetFloat("unit_price")
		}

		productRecord, _ := bh.app.FindRecordById("products", pID)
		productName := ""
		productDescription := ""
		productCategoryName := ""
		if productRecord != nil {
			productName = productRecord.GetString("name")
			productDescription = stripDeliveryBlurb(productRecord.GetString("description"))
			categoryID := productRecord.GetString("category")
			if categoryID != "" {
				categoryRecord, _ := bh.app.FindRecordById("categories", categoryID)
				if categoryRecord != nil {
					productCategoryName = categoryRecord.GetString("name")
				}
			}
			if unitPrice == 0 {
				unitPrice = productRecord.GetFloat("price")
			}
		}

		lineTotal := unitPrice * q
		overall += lineTotal
		lines += fmt.Sprintf(
			"%d) %s\n- Kategoriya: %s\n- Narxi: %s\n- Soni: %s\n- Izoh: %s\n- Jami: %s\n\n",
			i+1,
			productName,
			productCategoryName,
			formatAmount(unitPrice),
			formatAmount(q),
			productDescription,
			formatAmount(lineTotal),
		)
	}

	addressBlock := ""
	var addressLat float64
	var addressLng float64
	hasAddressLocation := false
	deliveryAddressID := order.GetString("delivery_address")
	var addrRecord *core.Record
	if deliveryAddressID != "" {
		var addrErr error
		addrRecord, addrErr = bh.app.FindRecordById("addresses", deliveryAddressID)
		if addrErr != nil {
			bh.logger.Error("Failed to fetch delivery address", slog.String("orderId", orderID), slog.String("deliveryAddressId", deliveryAddressID), slog.Any("error", addrErr))
		}
	}
	if addrRecord != nil {
		addrText := addrRecord.GetString("address")
		addressLat, addressLng, hasAddressLocation = extractLatLng(addrRecord.Get("longlat"))
		addressBlock = "\n\nManzil"
		if addrText != "" {
			addressBlock += fmt.Sprintf("\n- Manzil: %s", addrText)
		}
		if hasAddressLocation {
			mapsLink := fmt.Sprintf("https://yandex.com/maps/?pt=%.6f,%.6f&z=16&l=map", addressLng, addressLat)
			addressBlock += fmt.Sprintf("\nXarita: %s", mapsLink)
		}
	}

	text := fmt.Sprintf(
		"Yangi buyurtma yaratildi\n\nBuyurtma raqami: %s\nSana: %s\nHolati: %s\n\nMijoz\n- F.I.Sh: %s\n- Telefon: %s\n- Qabul qiluvchi: %s\n- Izoh: %s\n- Chat ID: %s%s\n\nMahsulotlar\n%sUmumiy summa: %s",
		displayOrderID,
		created,
		statusUz,
		userName,
		phone,
		receiverName,
		note,
		userChatID,
		addressBlock,
		lines,
		formatAmount(overall),
	)

	return text, nil
}

func (bh *BotHandler) buildOrdersMessageUz(orders []*core.Record) (string, string, error) {
	if bh.app == nil {
		return "", "", fmt.Errorf("app is nil")
	}
	if len(orders) == 0 {
		return "", "", fmt.Errorf("orders is empty")
	}

	userID := orders[0].GetString("user")
	statusUz := statusToUz(orders[0].GetString("status"))
	created := formatOrderCreatedUz(orders[0].GetString("created"))

	userRecord, _ := bh.app.FindRecordById("_pb_users_auth_", userID)
	userName := ""
	userChatID := ""
	if userRecord != nil {
		userName = userRecord.GetString("name")
		userChatID = userRecord.GetString("chat_id")
	}

	lines := ""
	overall := 0.0
	itemIndex := 1
	for _, order := range orders {
		items, itemsErr := bh.app.FindRecordsByFilter(
			"orderItems",
			"order = {:order}",
			"created",
			500,
			0,
			dbx.Params{"order": order.Id},
		)
		if itemsErr != nil {
			if strings.Contains(itemsErr.Error(), "unknown field \"order\"") {
				items, itemsErr = bh.app.FindRecordsByFilter(
					"orderItems",
					"cart = {:cart}",
					"created",
					500,
					0,
					dbx.Params{"cart": order.Id},
				)
			}
			if itemsErr != nil {
				items = []*core.Record{}
			}
		}
		for _, item := range items {
			pID := item.GetString("product")
			q := item.GetFloat("quantity")
			unitPrice := item.GetFloat("price_unit")
			if unitPrice == 0 {
				unitPrice = item.GetFloat("unit_price")
			}
			productRecord, _ := bh.app.FindRecordById("products", pID)
			productName := ""
			if productRecord != nil {
				productName = productRecord.GetString("name")
				if unitPrice == 0 {
					unitPrice = productRecord.GetFloat("price")
				}
			}
			lineTotal := unitPrice * q
			overall += lineTotal
			lines += fmt.Sprintf("%d) %s — %v x %v = %v\n", itemIndex, productName, q, unitPrice, lineTotal)
			itemIndex++
		}
	}

	text := fmt.Sprintf(
		"Buyurtma\n\nSana: %s\nHolati: %s\n\nMijoz\n- F.I.Sh: %s\n- Chat ID: %s\n\nMahsulotlar\n%s\nUmumiy summa: %v",
		created,
		statusUz,
		userName,
		userChatID,
		lines,
		overall,
	)

	return text, userChatID, nil
}

func (bh *BotHandler) isAdminChat(chatID int64) bool {
	if bh.app == nil {
		return false
	}
	adminUsers, err := bh.app.FindRecordsByFilter(
		"_pb_users_auth_",
		"role = 'admin' && chat_id != ''",
		"",
		500,
		0,
		dbx.Params{},
	)
	if err != nil {
		bh.logger.Error("Failed to fetch admin users", slog.Any("error", err))
		return false
	}
	for _, admin := range adminUsers {
		chatIDStr := admin.GetString("chat_id")
		cid, parseErr := strconv.ParseInt(chatIDStr, 10, 64)
		if parseErr == nil && cid != 0 && cid == chatID {
			return true
		}
	}
	return false
}

func (bh *BotHandler) isAdminUser(userID int64) bool {
	return bh.isAdminChat(userID)
}

type BotHandler struct {
	logger *slog.Logger
	cfg    *config.Config
	bot    *tgbotapi.BotAPI
	app    *pocketbase.PocketBase
}

func (bh *BotHandler) clearOrderConfirmationButtons(orderID string) {
	if bh == nil || bh.bot == nil || bh.app == nil || orderID == "" {
		return
	}

	record, err := bh.app.FindRecordById("orders", orderID)
	if err != nil || record == nil {
		return
	}

	raw := record.GetString("tg_confirmation_messages")
	if strings.TrimSpace(raw) == "" {
		return
	}

	var refs []tgMessageRef
	if uErr := json.Unmarshal([]byte(raw), &refs); uErr != nil {
		bh.logger.Error("Failed to unmarshal tg confirmation message refs", slog.Any("error", uErr))
		return
	}
	if len(refs) == 0 {
		return
	}

	emptyMarkup := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
	for _, ref := range refs {
		if ref.ChatID == 0 || ref.MessageID == 0 {
			continue
		}
		edit := tgbotapi.NewEditMessageReplyMarkup(ref.ChatID, ref.MessageID, emptyMarkup)
		if _, sendErr := bh.bot.Send(edit); sendErr != nil {
			bh.logger.Error("Failed to remove inline keyboard", slog.Any("error", sendErr))
		}
	}

	record.Set("tg_confirmation_messages", "")
	if saveErr := bh.app.Save(record); saveErr != nil {
		bh.logger.Error("Failed to clear tg confirmation message refs", slog.Any("error", saveErr))
	}
}

func (bh *BotHandler) resetChatMenuButton(chatID int64) {
	if bh == nil || bh.bot == nil {
		return
	}
	_, err := bh.bot.MakeRequest("setChatMenuButton", tgbotapi.Params{
		"chat_id":     strconv.FormatInt(chatID, 10),
		"menu_button": `{"type":"commands"}`,
	})
	if err != nil {
		bh.logger.Error("Failed to reset chat menu button", slog.Any("error", err))
	}
}

func (bh *BotHandler) setChatMenuButtonWebApp(chatID int64) {
	if bh == nil || bh.bot == nil {
		return
	}
	url := "https://sevcooking.yurtal.tech/"
	menuButton := fmt.Sprintf(`{"type":"web_app","text":"Menu","web_app":{"url":"%s"}}`, url)
	_, err := bh.bot.MakeRequest("setChatMenuButton", tgbotapi.Params{
		"chat_id":     strconv.FormatInt(chatID, 10),
		"menu_button": menuButton,
	})
	if err != nil {
		bh.logger.Error("Failed to set chat menu button", slog.Any("error", err))
	}
}

func NewBotHandler(logger *slog.Logger, cfg *config.Config, app *pocketbase.PocketBase) (*BotHandler, error) {
	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot: %w", err)
	}

	bot.Debug = false

	logger.Info("Bot authorized", slog.String("username", bot.Self.UserName))

	bh := &BotHandler{
		logger: logger,
		cfg:    cfg,
		bot:    bot,
		app:    app,
	}

	// Hide the global chat menu button by default.
	// We'll enable a per-chat WebApp menu button only after the user shares their contact.
	if _, err := bh.bot.MakeRequest("setChatMenuButton", tgbotapi.Params{
		"menu_button": `{"type":"commands"}`,
	}); err != nil {
		bh.logger.Error("Failed to set global default chat menu button", slog.Any("error", err))
	}

	// Start polling in a goroutine
	go bh.StartPolling()

	return bh, nil
}

func (bh *BotHandler) RegisterBotRoutes(router *router.Router[*core.RequestEvent]) {
	api := router.Group("/api/v1")
	{
		bot := api.Group("/bot")
		{
			bot.POST("/webhook", bh.HandleWebhook)
		}
	}
}

func (bh *BotHandler) HandleWebhook(e *core.RequestEvent) error {
	var update tgbotapi.Update
	if err := e.BindBody(&update); err != nil {
		bh.logger.Error("Failed to parse update", slog.Any("error", err))
		return e.JSON(400, map[string]string{"error": "invalid update"})
	}

	// Handle message
	if update.Message != nil {
		bh.handleMessage(update.Message)
	}

	// Handle callback query (button press)
	if update.CallbackQuery != nil {
		bh.handleCallbackQuery(update.CallbackQuery)
	}

	return e.JSON(200, map[string]string{"ok": "true"})
}

func (bh *BotHandler) handleMessage(message *tgbotapi.Message) {
	if message == nil {
		return
	}

	isGroup := isGroupChat(message.Chat)

	if message.Contact != nil {
		bh.handleSharedContact(message)
		return
	}

	if !bh.isAdminChat(message.Chat.ID) {
		if message.Audio != nil || message.Voice != nil || message.Video != nil || message.VideoNote != nil || message.Animation != nil {
			msg := tgbotapi.NewMessage(message.Chat.ID, "Bu formatdagi fayllarni yuborish mumkin emas")
			if _, err := bh.bot.Send(msg); err != nil {
				bh.logger.Error("Failed to send document blocked message", slog.Any("error", err))
			}
			return
		}

		if message.Document != nil {
			name := strings.ToLower(message.Document.FileName)
			mt := strings.ToLower(message.Document.MimeType)
			ext := strings.ToLower(filepath.Ext(name))
			isImage := strings.HasPrefix(mt, "image/")
			if !isImage {
				switch ext {
				case ".jpg", ".jpeg", ".png", ".webp", ".heic", ".heif", ".gif", ".bmp", ".tiff":
					isImage = true
				}
			}
			if !isImage {
				msg := tgbotapi.NewMessage(message.Chat.ID, "Bu formatdagi fayllarni yuborish mumkin emas")
				if _, err := bh.bot.Send(msg); err != nil {
					bh.logger.Error("Failed to send document blocked message", slog.Any("error", err))
				}
				return
			}
		}
	}

	if !isGroup && !bh.ensureContactShared(message) {
		return
	}

	if message.IsCommand() {
		switch message.Command() {
		case "start":
			if isGroup {
				break
			}
			bh.sendWelcomeMessage(message.Chat.ID)
		case "help":
			if isGroup {
				break
			}
			bh.sendHelpMessage(message.Chat.ID)
		case "orders":
			if !bh.isAdminChat(message.Chat.ID) && !isGroup {
				msg := tgbotapi.NewMessage(message.Chat.ID, "Ruxsat yo'q")
				bh.bot.Send(msg)
				break
			}
			if bh.app == nil {
				bh.logger.Error("PocketBase app is nil; cannot fetch orders")
				break
			}

			orders, err := bh.app.FindRecordsByFilter(
				"orders",
				"status = 'confirmed'",
				"-created",
				500,
				0,
				dbx.Params{},
			)
			if err != nil {
				bh.logger.Error("Failed to fetch confirmed orders", slog.Any("error", err))
				break
			}
			if len(orders) == 0 {
				msg := tgbotapi.NewMessage(message.Chat.ID, "Tasdiqlangan buyurtmalar yo'q")
				bh.bot.Send(msg)
				break
			}

			for _, order := range orders {
				text, buildErr := bh.buildOrderMessageUz(order)
				if buildErr != nil {
					bh.logger.Error("Failed to build order message", slog.Any("error", buildErr))
					continue
				}
				text = strings.TrimPrefix(text, "Yangi buyurtma yaratildi\n\n")

				orderID := order.Id
				keyboard := tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("✅ Yetkazib berildi", "order:deliver:"+orderID),
						tgbotapi.NewInlineKeyboardButtonData("❌ Bekor qilindi", "order:admin_cancel:"+orderID),
					),
				)

				replyToID := 0
				sendFullTextAsCaption := false
				receipt := order.GetString("receipt")
				if receipt != "" {
					imgPath := filepath.Join(bh.app.DataDir(), "storage", order.Collection().Id, orderID, receipt)
					if _, statErr := os.Stat(imgPath); statErr == nil {
						ext := strings.ToLower(filepath.Ext(receipt))
						imgBytes, readErr := os.ReadFile(imgPath)
						sendAsPhoto := ext == ".jpg" || ext == ".jpeg" || ext == ".png"
						captionParts := splitTextRunes(text, 900)
						sendFullTextAsCaption = len(captionParts) == 1
						caption := buildCaptionFromOrderText(text)
						if sendFullTextAsCaption {
							caption = text
						}
						if ext == ".heic" || ext == ".heif" || !sendAsPhoto {
							var doc tgbotapi.DocumentConfig
							if readErr == nil {
								doc = tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FileBytes{Name: receipt, Bytes: imgBytes})
							} else {
								bh.logger.Error("Failed to read receipt file", slog.Any("error", readErr), slog.String("path", imgPath))
								doc = tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FilePath(imgPath))
							}
							doc.Caption = caption
							doc.ReplyMarkup = keyboard
							sent, sendErr := bh.bot.Send(doc)
							if sendErr != nil {
								bh.logger.Error("Failed to send order receipt document", slog.Any("error", sendErr))
							} else {
								replyToID = sent.MessageID
								bh.appendOrderMessageRef(orderID, tgMessageRef{ChatID: sent.Chat.ID, MessageID: sent.MessageID})
							}
						} else {
							var photo tgbotapi.PhotoConfig
							var docFallback tgbotapi.DocumentConfig
							if readErr == nil {
								name := receipt
								if ext == ".jpeg" {
									name = "receipt.jpg"
								}
								photo = tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileBytes{Name: name, Bytes: imgBytes})
								docFallback = tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FileBytes{Name: receipt, Bytes: imgBytes})
							} else {
								bh.logger.Error("Failed to read receipt file", slog.Any("error", readErr), slog.String("path", imgPath))
								photo = tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FilePath(imgPath))
								docFallback = tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FilePath(imgPath))
							}
							photo.Caption = caption
							photo.ReplyMarkup = keyboard
							sent, sendErr := bh.bot.Send(photo)
							if sendErr != nil {
								bh.logger.Error("Failed to send order photo", slog.Any("error", sendErr))
								docFallback.Caption = caption
								docFallback.ReplyMarkup = keyboard
								docSent, docErr := bh.bot.Send(docFallback)
								if docErr != nil {
									bh.logger.Error("Failed to send order receipt document fallback", slog.Any("error", docErr))
								} else {
									replyToID = docSent.MessageID
									bh.appendOrderMessageRef(orderID, tgMessageRef{ChatID: docSent.Chat.ID, MessageID: docSent.MessageID})
								}
							} else {
								replyToID = sent.MessageID
								bh.appendOrderMessageRef(orderID, tgMessageRef{ChatID: sent.Chat.ID, MessageID: sent.MessageID})
							}
						}
					} else {
						bh.logger.Error("Receipt file not found", slog.String("path", imgPath), slog.Any("error", statErr))
					}
				}

				if replyToID == 0 {
					sentBase, baseErr := bh.sendTextInParts(message.Chat.ID, text, keyboard)
					if baseErr != nil {
						bh.logger.Error("Failed to send order message", slog.Any("error", baseErr))
					} else if sentBase != nil {
						replyToID = sentBase.MessageID
						bh.appendOrderMessageRef(orderID, tgMessageRef{ChatID: sentBase.Chat.ID, MessageID: sentBase.MessageID})
					}
				} else {
					if !sendFullTextAsCaption {
						if _, baseErr := bh.sendTextInPartsReply(message.Chat.ID, text, nil, replyToID); baseErr != nil {
							bh.logger.Error("Failed to send order message", slog.Any("error", baseErr))
						}
					}
				}

				deliveryAddressID := order.GetString("delivery_address")
				if deliveryAddressID != "" {
					addrRecord, addrErr := bh.app.FindRecordById("addresses", deliveryAddressID)
					if addrErr != nil {
						bh.logger.Error("Failed to fetch delivery address", slog.String("orderId", orderID), slog.String("deliveryAddressId", deliveryAddressID), slog.Any("error", addrErr))
					} else if addrRecord != nil {
						lat, lng, ok := extractLatLng(addrRecord.Get("longlat"))
						if ok {
							locMsg := tgbotapi.NewLocation(message.Chat.ID, lat, lng)
							if replyToID > 0 {
								locMsg.ReplyToMessageID = replyToID
							}
							if _, sendErr := bh.bot.Send(locMsg); sendErr != nil {
								bh.logger.Error("Failed to send order location", slog.Any("error", sendErr))
							}
						}
					}
				}
			}
		case "cancelled":
			if !bh.isAdminChat(message.Chat.ID) && !isGroup {
				msg := tgbotapi.NewMessage(message.Chat.ID, "Ruxsat yo'q")
				bh.bot.Send(msg)
				break
			}
			if bh.app == nil {
				bh.logger.Error("PocketBase app is nil; cannot fetch cancelled orders")
				break
			}

			orders, err := bh.app.FindRecordsByFilter(
				"orders",
				"status = 'cancelled'",
				"-created",
				500,
				0,
				dbx.Params{},
			)
			if err != nil {
				bh.logger.Error("Failed to fetch cancelled orders", slog.Any("error", err))
				break
			}
			if len(orders) == 0 {
				msg := tgbotapi.NewMessage(message.Chat.ID, "Bekor qilingan buyurtmalar yo'q")
				bh.bot.Send(msg)
				break
			}

			for _, order := range orders {
				text, buildErr := bh.buildOrderMessageUz(order)
				if buildErr != nil {
					bh.logger.Error("Failed to build order message", slog.Any("error", buildErr))
					continue
				}
				text = strings.TrimPrefix(text, "Yangi buyurtma yaratildi\n\n")

				orderID := order.Id

				replyToID := 0
				sendFullTextAsCaption := false
				receipt := order.GetString("receipt")
				if receipt != "" {
					imgPath := filepath.Join(bh.app.DataDir(), "storage", order.Collection().Id, orderID, receipt)
					if _, statErr := os.Stat(imgPath); statErr == nil {
						ext := strings.ToLower(filepath.Ext(receipt))
						imgBytes, readErr := os.ReadFile(imgPath)
						sendAsPhoto := ext == ".jpg" || ext == ".jpeg" || ext == ".png"
						captionParts := splitTextRunes(text, 900)
						sendFullTextAsCaption = len(captionParts) == 1
						caption := buildCaptionFromOrderText(text)
						if sendFullTextAsCaption {
							caption = text
						}
						if ext == ".heic" || ext == ".heif" || !sendAsPhoto {
							var doc tgbotapi.DocumentConfig
							if readErr == nil {
								doc = tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FileBytes{Name: receipt, Bytes: imgBytes})
							} else {
								bh.logger.Error("Failed to read receipt file", slog.Any("error", readErr), slog.String("path", imgPath))
								doc = tgbotapi.NewDocument(message.Chat.ID, tgbotapi.FilePath(imgPath))
							}
							doc.Caption = caption
							sent, sendErr := bh.bot.Send(doc)
							if sendErr != nil {
								bh.logger.Error("Failed to send order receipt document", slog.Any("error", sendErr))
							} else {
								replyToID = sent.MessageID
							}
						} else {
							var photo tgbotapi.PhotoConfig
							if readErr == nil {
								name := receipt
								if ext == ".jpeg" {
									name = "receipt.jpg"
								}
								photo = tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FileBytes{Name: name, Bytes: imgBytes})
							} else {
								bh.logger.Error("Failed to read receipt file", slog.Any("error", readErr), slog.String("path", imgPath))
								photo = tgbotapi.NewPhoto(message.Chat.ID, tgbotapi.FilePath(imgPath))
							}
							photo.Caption = caption
							sent, sendErr := bh.bot.Send(photo)
							if sendErr != nil {
								bh.logger.Error("Failed to send order photo", slog.Any("error", sendErr))
							} else {
								replyToID = sent.MessageID
							}
						}
					} else {
						bh.logger.Error("Receipt file not found", slog.String("path", imgPath), slog.Any("error", statErr))
					}
				}

				if replyToID > 0 {
					if !sendFullTextAsCaption {
						if _, baseErr := bh.sendTextInPartsReply(message.Chat.ID, text, nil, replyToID); baseErr != nil {
							bh.logger.Error("Failed to send order message", slog.Any("error", baseErr))
						}
					}
				} else {
					if _, baseErr := bh.sendTextInParts(message.Chat.ID, text, nil); baseErr != nil {
						bh.logger.Error("Failed to send order message", slog.Any("error", baseErr))
					}
				}

				deliveryAddressID := order.GetString("delivery_address")
				if deliveryAddressID != "" {
					addrRecord, addrErr := bh.app.FindRecordById("addresses", deliveryAddressID)
					if addrErr != nil {
						bh.logger.Error("Failed to fetch delivery address", slog.String("orderId", orderID), slog.String("deliveryAddressId", deliveryAddressID), slog.Any("error", addrErr))
					} else if addrRecord != nil {
						lat, lng, ok := extractLatLng(addrRecord.Get("longlat"))
						if ok {
							locMsg := tgbotapi.NewLocation(message.Chat.ID, lat, lng)
							if replyToID > 0 {
								locMsg.ReplyToMessageID = replyToID
							}
							if _, sendErr := bh.bot.Send(locMsg); sendErr != nil {
								bh.logger.Error("Failed to send order location", slog.Any("error", sendErr))
							}
						}
					}
				}
			}
		default:
			if isGroup {
				break
			}
			msg := tgbotapi.NewMessage(message.Chat.ID, "Unknown command. Type /help for available commands.")
			bh.bot.Send(msg)
		}
	} else if message.Text != "" {
		if isGroup {
			return
		}
		bh.sendWelcomeMessage(message.Chat.ID)
	}
}

func (bh *BotHandler) ensureContactShared(message *tgbotapi.Message) bool {
	if message == nil || message.Chat == nil {
		return true
	}
	if isGroupChat(message.Chat) {
		return true
	}
	if message.From == nil {
		return true
	}
	if bh.isAdminChat(message.Chat.ID) {
		return true
	}

	if bh.app == nil {
		return true
	}

	userChatID := strconv.FormatInt(message.From.ID, 10)
	users, err := bh.app.FindRecordsByFilter(
		"_pb_users_auth_",
		"chat_id = {:chat_id} && phone != ''",
		"",
		1,
		0,
		dbx.Params{"chat_id": userChatID},
	)
	if err == nil && len(users) > 0 {
		bh.setChatMenuButtonWebApp(message.Chat.ID)
		return true
	}

	bh.resetChatMenuButton(message.Chat.ID)
	bh.sendContactRequest(message.Chat.ID)
	return false
}

func (bh *BotHandler) sendContactRequest(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "Botdan foydalanish uchun iltimos kontaktingizni ulashing.")
	msg.ParseMode = tgbotapi.ModeHTML

	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.KeyboardButton{Text: "📞 Kontaktni ulashish", RequestContact: true},
		),
	)
	msg.ReplyMarkup = tgbotapi.ReplyKeyboardMarkup{
		Keyboard:        kb.Keyboard,
		ResizeKeyboard:  true,
		OneTimeKeyboard: true,
	}

	if _, err := bh.bot.Send(msg); err != nil {
		bh.logger.Error("Failed to send contact request", slog.Any("error", err))
	}
}

func normalizePhoneNumber(phone string) string {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if len(digits) == 9 {
		return "998" + digits
	}
	if strings.HasPrefix(digits, "8") && len(digits) == 10 {
		return "998" + digits[1:]
	}
	if strings.HasPrefix(digits, "998") && len(digits) == 12 {
		return digits
	}
	return digits
}

func (bh *BotHandler) handleSharedContact(message *tgbotapi.Message) {
	if message == nil || message.Chat == nil || message.Contact == nil {
		return
	}
	if message.From == nil {
		bh.sendContactRequest(message.Chat.ID)
		return
	}
	if bh.app == nil {
		return
	}

	if message.Contact.UserID != 0 && int64(message.Contact.UserID) != message.From.ID {
		bh.sendContactRequest(message.Chat.ID)
		return
	}

	chatIDStr := strconv.FormatInt(message.From.ID, 10)
	phone := normalizePhoneNumber(message.Contact.PhoneNumber)
	if phone == "" {
		bh.sendContactRequest(message.Chat.ID)
		return
	}

	fullName := strings.TrimSpace(strings.TrimSpace(message.Contact.FirstName) + " " + strings.TrimSpace(message.Contact.LastName))
	if fullName == "" {
		fullName = strings.TrimSpace(strings.TrimSpace(message.From.FirstName) + " " + strings.TrimSpace(message.From.LastName))
	}

	emailPattern := phone + "@"
	users, err := bh.app.FindRecordsByFilter(
		"_pb_users_auth_",
		"(chat_id = {:chat_id} || phone = {:phone} || email ~ {:emailPattern})",
		"",
		1,
		0,
		dbx.Params{"chat_id": chatIDStr, "phone": phone, "emailPattern": emailPattern},
	)
	if err != nil {
		bh.logger.Error("Failed to find user by telegram contact", slog.Any("error", err))
		return
	}

	var userRecord *core.Record
	if len(users) > 0 {
		userRecord = users[0]
	} else {
		collection, colErr := bh.app.FindCollectionByNameOrId("_pb_users_auth_")
		if colErr != nil {
			bh.logger.Error("Failed to find users collection", slog.Any("error", colErr))
			return
		}

		userRecord = core.NewRecord(collection)

		password := "password@1234" // needed to be fixed
		userRecord.Set("email", phone+"@telegram.local")
		userRecord.Set("password", password)
		userRecord.Set("passwordConfirm", password)
	}

	if fullName != "" {
		userRecord.Set("name", fullName)
	}
	userRecord.Set("phone", phone)
	userRecord.Set("chat_id", chatIDStr)

	if saveErr := bh.app.Save(userRecord); saveErr != nil {
		bh.logger.Error("Failed to save user from telegram contact", slog.Any("error", saveErr))
		return
	}

	bh.setChatMenuButtonWebApp(message.Chat.ID)

	ack := tgbotapi.NewMessage(message.Chat.ID, "Rahmat! Endi botdan foydalanishingiz mumkin.")
	ack.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
	if _, err := bh.bot.Send(ack); err != nil {
		bh.logger.Error("Failed to send contact confirmation", slog.Any("error", err))
	}

	bh.sendWelcomeMessage(message.Chat.ID)
}

func (bh *BotHandler) sendWelcomeMessage(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, bh.getWelcomeText())
	msg.ParseMode = tgbotapi.ModeHTML

	type tgWebAppInfo struct {
		URL string `json:"url"`
	}
	type tgInlineKeyboardButton struct {
		Text   string        `json:"text"`
		WebApp *tgWebAppInfo `json:"web_app,omitempty"`
	}
	type tgInlineKeyboardMarkup struct {
		InlineKeyboard [][]tgInlineKeyboardButton `json:"inline_keyboard"`
	}

	msg.ReplyMarkup = tgInlineKeyboardMarkup{
		InlineKeyboard: [][]tgInlineKeyboardButton{
			{
				{
					Text:   "🔗 Ochish / Открить / Open",
					WebApp: &tgWebAppInfo{URL: "https://sevcooking.yurtal.tech/"},
				},
			},
		},
	}

	if _, err := bh.bot.Send(msg); err != nil {
		bh.logger.Error("Failed to send message", slog.Any("error", err))
	}
}

func (bh *BotHandler) sendHelpMessage(chatID int64) {
	helpText := `Available commands:
/start - Show welcome message
/help - Show this help message`
	msg := tgbotapi.NewMessage(chatID, helpText)
	if _, err := bh.bot.Send(msg); err != nil {
		bh.logger.Error("Failed to send help message", slog.Any("error", err))
	}
}

func (bh *BotHandler) handleCallbackQuery(query *tgbotapi.CallbackQuery) {
	if query == nil {
		return
	}

	callbackText := ""

	if query.Message == nil {
		callback := tgbotapi.NewCallback(query.ID, callbackText)
		if _, err := bh.bot.Request(callback); err != nil {
			bh.logger.Error("Failed to answer callback query", slog.Any("error", err))
		}
		return
	}

	data := query.Data
	if data != "" {
		parts := strings.SplitN(data, ":", 3)
		if len(parts) == 3 && parts[0] == "order" {
			isAdmin := false
			if query.Message != nil && isGroupChat(query.Message.Chat) {
				isAdmin = true
			} else {
				isAdmin = bh.isAdminUser(query.From.ID)
			}

			if !isAdmin {
				callbackText = "Ruxsat yo'q"
				bh.logger.Warn(
					"Non-admin attempted to use admin callback",
					slog.Int64("chat_id", query.Message.Chat.ID),
					slog.Int64("user_id", query.From.ID),
				)
			} else {
				orderID := parts[2]
				switch parts[1] {
				case "confirm":
					confirmMarkup := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("Ha ✅", "order:yes:"+orderID),
							tgbotapi.NewInlineKeyboardButtonData("Yo'q ❌", "order:no:"+orderID),
						),
					)

					edit := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, confirmMarkup)
					if _, err := bh.bot.Send(edit); err != nil {
						bh.logger.Error("Failed to edit confirmation keyboard", slog.Any("error", err))
					}
				case "cancel":
					confirmMarkup := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("Ha ✅", "order:cancel_yes:"+orderID),
							tgbotapi.NewInlineKeyboardButtonData("Yo'q ❌", "order:cancel_no:"+orderID),
						),
					)

					edit := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, confirmMarkup)
					if _, err := bh.bot.Send(edit); err != nil {
						bh.logger.Error("Failed to edit cancellation confirmation keyboard", slog.Any("error", err))
					}
				case "deliver":
					confirmMarkup := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("Ha ✅", "order:deliver_yes:"+orderID),
							tgbotapi.NewInlineKeyboardButtonData("Yo'q ❌", "order:deliver_no:"+orderID),
						),
					)

					edit := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, confirmMarkup)
					if _, err := bh.bot.Send(edit); err != nil {
						bh.logger.Error("Failed to edit delivery confirmation keyboard", slog.Any("error", err))
					}
				case "admin_cancel":
					confirmMarkup := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("Ha ✅", "order:admin_cancel_yes:"+orderID),
							tgbotapi.NewInlineKeyboardButtonData("Yo'q ❌", "order:admin_cancel_no:"+orderID),
						),
					)

					edit := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, confirmMarkup)
					if _, err := bh.bot.Send(edit); err != nil {
						bh.logger.Error("Failed to edit admin cancellation confirmation keyboard", slog.Any("error", err))
					}
				case "yes":
					if bh.app == nil {
						bh.logger.Error("PocketBase app is nil; cannot update order")
						break
					}

					record, err := bh.app.FindRecordById("orders", orderID)
					if err != nil {
						bh.logger.Error("Failed to find order", slog.String("orderId", orderID), slog.Any("error", err))
						break
					}

					record.Set("status", "confirmed")
					if err := bh.app.Save(record); err != nil {
						bh.logger.Error("Failed to update order status", slog.String("orderId", record.Id), slog.Any("error", err))
					}

					baseText := query.Message.Text
					if query.Message.Caption != "" {
						baseText = query.Message.Caption
					}
					captionText := ""
					text := ""
					if query.Message.Caption != "" {
						captionText = baseText + "\n\n✅ Buyurtma tasdiqlandi"
					} else {
						text = baseText + "\n\n✅ Buyurtma tasdiqlandi"
					}

					emptyMarkup := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
					if query.Message.Caption != "" {
						editCaption := tgbotapi.NewEditMessageCaption(query.Message.Chat.ID, query.Message.MessageID, captionText)
						editCaption.ReplyMarkup = &emptyMarkup
						if _, err := bh.bot.Send(editCaption); err != nil {
							bh.logger.Error("Failed to edit message caption", slog.Any("error", err))
							editMarkup := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, emptyMarkup)
							if _, mkErr := bh.bot.Send(editMarkup); mkErr != nil {
								bh.logger.Error("Failed to remove inline keyboard", slog.Any("error", mkErr))
							}
						}
					} else {
						editText := tgbotapi.NewEditMessageText(query.Message.Chat.ID, query.Message.MessageID, text)
						editText.ReplyMarkup = &emptyMarkup
						if _, err := bh.bot.Send(editText); err != nil {
							bh.logger.Error("Failed to edit message text", slog.Any("error", err))
							editMarkup := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, emptyMarkup)
							if _, mkErr := bh.bot.Send(editMarkup); mkErr != nil {
								bh.logger.Error("Failed to remove inline keyboard", slog.Any("error", mkErr))
							}
						}
					}
					bh.clearOrderConfirmationButtons(orderID)
				case "no":
					revertMarkup := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("Tasdiqlash", "order:confirm:"+orderID),
							tgbotapi.NewInlineKeyboardButtonData("Tasdiqlamaslik", "order:cancel:"+orderID),
						),
					)
					edit := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, revertMarkup)
					if _, err := bh.bot.Send(edit); err != nil {
						bh.logger.Error("Failed to revert keyboard", slog.Any("error", err))
					}
				case "deliver_yes":
					if bh.app == nil {
						bh.logger.Error("PocketBase app is nil; cannot update order")
						break
					}

					record, err := bh.app.FindRecordById("orders", orderID)
					if err != nil {
						bh.logger.Error("Failed to find order", slog.String("orderId", orderID), slog.Any("error", err))
						break
					}

					record.Set("status", "done")
					if err := bh.app.Save(record); err != nil {
						bh.logger.Error("Failed to update order status", slog.String("orderId", record.Id), slog.Any("error", err))
					}

					baseText := query.Message.Text
					if query.Message.Caption != "" {
						baseText = query.Message.Caption
					}
					captionText := ""
					text := ""
					if query.Message.Caption != "" {
						captionText = baseText + "\n\n✅ Yetkazib berildi"
					} else {
						text = baseText + "\n\n✅ Yetkazib berildi"
					}

					emptyMarkup := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
					if query.Message.Caption != "" {
						editCaption := tgbotapi.NewEditMessageCaption(query.Message.Chat.ID, query.Message.MessageID, captionText)
						editCaption.ReplyMarkup = &emptyMarkup
						if _, err := bh.bot.Send(editCaption); err != nil {
							bh.logger.Error("Failed to edit message caption", slog.Any("error", err))
							editMarkup := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, emptyMarkup)
							if _, mkErr := bh.bot.Send(editMarkup); mkErr != nil {
								bh.logger.Error("Failed to remove inline keyboard", slog.Any("error", mkErr))
							}
						}
					} else {
						editText := tgbotapi.NewEditMessageText(query.Message.Chat.ID, query.Message.MessageID, text)
						editText.ReplyMarkup = &emptyMarkup
						if _, err := bh.bot.Send(editText); err != nil {
							bh.logger.Error("Failed to edit message text", slog.Any("error", err))
							editMarkup := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, emptyMarkup)
							if _, mkErr := bh.bot.Send(editMarkup); mkErr != nil {
								bh.logger.Error("Failed to remove inline keyboard", slog.Any("error", mkErr))
							}
						}
					}
					bh.clearOrderConfirmationButtons(orderID)
				case "deliver_no":
					revertMarkup := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("✅ Yetkazib berildi", "order:deliver:"+orderID),
							tgbotapi.NewInlineKeyboardButtonData("❌ Bekor qilindi", "order:admin_cancel:"+orderID),
						),
					)
					edit := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, revertMarkup)
					if _, err := bh.bot.Send(edit); err != nil {
						bh.logger.Error("Failed to revert keyboard", slog.Any("error", err))
					}
				case "admin_cancel_yes":
					if bh.app == nil {
						bh.logger.Error("PocketBase app is nil; cannot update order")
						break
					}

					record, err := bh.app.FindRecordById("orders", orderID)
					if err != nil {
						bh.logger.Error("Failed to find order", slog.String("orderId", orderID), slog.Any("error", err))
						break
					}

					record.Set("status", "cancelled")
					if err := bh.app.Save(record); err != nil {
						bh.logger.Error("Failed to update order status", slog.String("orderId", record.Id), slog.Any("error", err))
					}

					baseText := query.Message.Text
					if query.Message.Caption != "" {
						baseText = query.Message.Caption
					}
					captionText := ""
					text := ""
					if query.Message.Caption != "" {
						captionText = baseText + "\n\n❌ Bu buyurtma bekor qilindi"
					} else {
						text = baseText + "\n\n❌ Bu buyurtma bekor qilindi"
					}

					emptyMarkup := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
					if query.Message.Caption != "" {
						editCaption := tgbotapi.NewEditMessageCaption(query.Message.Chat.ID, query.Message.MessageID, captionText)
						editCaption.ReplyMarkup = &emptyMarkup
						if _, err := bh.bot.Send(editCaption); err != nil {
							bh.logger.Error("Failed to edit message caption", slog.Any("error", err))
							editMarkup := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, emptyMarkup)
							if _, mkErr := bh.bot.Send(editMarkup); mkErr != nil {
								bh.logger.Error("Failed to remove inline keyboard", slog.Any("error", mkErr))
							}
						}
					} else {
						editText := tgbotapi.NewEditMessageText(query.Message.Chat.ID, query.Message.MessageID, text)
						editText.ReplyMarkup = &emptyMarkup
						if _, err := bh.bot.Send(editText); err != nil {
							bh.logger.Error("Failed to edit message text", slog.Any("error", err))
							editMarkup := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, emptyMarkup)
							if _, mkErr := bh.bot.Send(editMarkup); mkErr != nil {
								bh.logger.Error("Failed to remove inline keyboard", slog.Any("error", mkErr))
							}
						}
					}
					bh.clearOrderConfirmationButtons(orderID)
				case "admin_cancel_no":
					revertMarkup := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("✅ Yetkazib berildi", "order:deliver:"+orderID),
							tgbotapi.NewInlineKeyboardButtonData("❌ Bekor qilindi", "order:admin_cancel:"+orderID),
						),
					)
					edit := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, revertMarkup)
					if _, err := bh.bot.Send(edit); err != nil {
						bh.logger.Error("Failed to revert keyboard", slog.Any("error", err))
					}
				case "cancel_yes":
					if bh.app == nil {
						bh.logger.Error("PocketBase app is nil; cannot update order")
						break
					}

					record, err := bh.app.FindRecordById("orders", orderID)
					if err != nil {
						bh.logger.Error("Failed to find order", slog.String("orderId", orderID), slog.Any("error", err))
						break
					}

					record.Set("status", "cancelled")
					if err := bh.app.Save(record); err != nil {
						bh.logger.Error("Failed to update order status", slog.String("orderId", record.Id), slog.Any("error", err))
					}

					baseText := query.Message.Text
					if query.Message.Caption != "" {
						baseText = query.Message.Caption
					}
					captionText := ""
					text := ""
					if query.Message.Caption != "" {
						captionText = baseText + "\n\n❌ Bu buyurtma bekor qilindi"
					} else {
						text = baseText + "\n\n❌ Bu buyurtma bekor qilindi"
					}

					emptyMarkup := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
					if query.Message.Caption != "" {
						editCaption := tgbotapi.NewEditMessageCaption(query.Message.Chat.ID, query.Message.MessageID, captionText)
						editCaption.ReplyMarkup = &emptyMarkup
						if _, err := bh.bot.Send(editCaption); err != nil {
							bh.logger.Error("Failed to edit message caption", slog.Any("error", err))
							editMarkup := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, emptyMarkup)
							if _, mkErr := bh.bot.Send(editMarkup); mkErr != nil {
								bh.logger.Error("Failed to remove inline keyboard", slog.Any("error", mkErr))
							}
						}
					} else {
						editText := tgbotapi.NewEditMessageText(query.Message.Chat.ID, query.Message.MessageID, text)
						editText.ReplyMarkup = &emptyMarkup
						if _, err := bh.bot.Send(editText); err != nil {
							bh.logger.Error("Failed to edit message text", slog.Any("error", err))
							editMarkup := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, emptyMarkup)
							if _, mkErr := bh.bot.Send(editMarkup); mkErr != nil {
								bh.logger.Error("Failed to remove inline keyboard", slog.Any("error", mkErr))
							}
						}
					}
					bh.clearOrderConfirmationButtons(orderID)
				case "cancel_no":
					revertMarkup := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonData("Tasdiqlash", "order:confirm:"+orderID),
							tgbotapi.NewInlineKeyboardButtonData("Tasdiqlamaslik", "order:cancel:"+orderID),
						),
					)
					edit := tgbotapi.NewEditMessageReplyMarkup(query.Message.Chat.ID, query.Message.MessageID, revertMarkup)
					if _, err := bh.bot.Send(edit); err != nil {
						bh.logger.Error("Failed to revert keyboard", slog.Any("error", err))
					}
				}
			}
		}
	}

	// Acknowledge the callback query
	callback := tgbotapi.NewCallback(query.ID, callbackText)
	if _, err := bh.bot.Request(callback); err != nil {
		bh.logger.Error("Failed to answer callback query", slog.Any("error", err))
	}
}

func (bh *BotHandler) getWelcomeText() string {
	return `🇺🇿 <b>Assolomu aleykum!</b> 👋

Mazali taomlarimizga buyurtma berishni boshlash uchun quyidagi "Ochish" tugmasini bosing.

Agar sizda biron bir savol bo'lsa, iltimos, bizning qo'llab-quvvatlashimizga yozing.


🇷🇺 <b>Здравствуйте!</b> 👋

Чтобы заказать наши блюда, перейдите на платформу доставки, нажав на кнопку ниже "Открыть".

Если у вас возникнут вопросы, напишите нам в поддержку.
`
}

func (bh *BotHandler) StartPolling() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bh.bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			bh.handleMessage(update.Message)
		}

		if update.CallbackQuery != nil {
			bh.handleCallbackQuery(update.CallbackQuery)
		}
	}
}
