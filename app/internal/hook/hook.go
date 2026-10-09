package hook

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
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/config"
	"gitlab.yurtal.tech/company/blitz/business-card/back/internal/service"
)

type Hook struct {
	logger  *slog.Logger
	service service.I
	cfg     *config.Config
	bot     *tgbotapi.BotAPI
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

func splitTextRunes(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = 1
	}
	r := []rune(text)
	if len(r) <= maxLen {
		return []string{text}
	}
	var parts []string
	for len(r) > 0 {
		end := maxLen
		if end > len(r) {
			end = len(r)
		}
		parts = append(parts, string(r[:end]))
		r = r[end:]
	}
	return parts
}

func sendTextInPartsReply(bot *tgbotapi.BotAPI, chatID int64, text string, replyToMessageID int) (int, error) {
	parts := splitTextRunes(text, 3500)
	lastReplyTo := replyToMessageID
	for _, part := range parts {
		msg := tgbotapi.NewMessage(chatID, part)
		if lastReplyTo > 0 {
			msg.ReplyToMessageID = lastReplyTo
		}
		sent, err := bot.Send(msg)
		if err != nil {
			return lastReplyTo, err
		}
		lastReplyTo = sent.MessageID
	}
	return lastReplyTo, nil
}

func formatAmountRu(v float64) string {
	n := int64(math.Round(v))
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = strings.TrimPrefix(s, "-")
	}
	var out []byte
	for i, j := len(s), 0; i > 0; i, j = i-1, j+1 {
		out = append(out, s[i-1])
		if (j+1)%3 == 0 && i-1 > 0 {
			out = append(out, ' ')
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	res := string(out)
	if neg {
		res = "-" + res
	}
	return res
}

func formatQtyRu(v float64) string {
	rounded := math.Round(v)
	if math.Abs(v-rounded) < 1e-9 {
		return strconv.FormatInt(int64(rounded), 10)
	}
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

func (h *Hook) Register(app *pocketbase.PocketBase) {
	app.OnRecordAfterUpdateSuccess("orders").BindFunc(func(e *core.RecordEvent) error {
		if h.bot == nil {
			return e.Next()
		}

		status := e.Record.GetString("status")
		oldStatus := ""
		if old := e.Record.Original(); old != nil {
			oldStatus = old.GetString("status")
		}
		if oldStatus != "" && oldStatus == status {
			return e.Next()
		}

		orderID := e.Record.Id

		// Reload record to ensure we have the generated order_id
		freshRecord, err := app.FindRecordById("orders", orderID)
		if err != nil {
			h.logger.Error("Failed to reload order record", slog.String("id", orderID), slog.Any("error", err))
			freshRecord = e.Record
		}

		displayOrderID := freshRecord.GetString("order_id")
		if displayOrderID == "" {
			for i := 0; i < 5; i++ {
				time.Sleep(100 * time.Millisecond)
				retryRecord, retryErr := app.FindRecordById("orders", orderID)
				if retryErr != nil || retryRecord == nil {
					continue
				}
				if oid := retryRecord.GetString("order_id"); oid != "" {
					freshRecord = retryRecord
					displayOrderID = oid
					break
				}
			}
		}
		if displayOrderID == "" {
			displayOrderID = "N/A"
		}

		supportInfoUz := ""
		supportInfoRu := ""
		if status == "cancelled" {
			contacts, _ := app.FindRecordsByFilter("contacts", "", "", 1, 0, nil)
			if len(contacts) > 0 {
				c := contacts[0]
				tg := c.GetString("telegram")
				ph := c.GetString("phone")
				links := ""
				if tg != "" {
					links += fmt.Sprintf("Telegram: @%s\n", strings.TrimPrefix(tg, "@"))
				}
				if ph != "" {
					links += fmt.Sprintf("Tel: %s\n", ph)
				}
				links = strings.TrimSpace(links)
				if links != "" {
					supportInfoUz = fmt.Sprintf("Iltimos, qo'llab-quvvatlash xizmatiga murojaat qiling:\n%s", links)
					supportInfoRu = fmt.Sprintf("Пожалуйста, обратитесь в службу поддержки:\n%s", links)
				}
			}
		}

		userID := e.Record.GetString("user")
		userRecord, userErr := app.FindRecordById("_pb_users_auth_", userID)
		if userErr != nil {
			h.logger.Error("Failed to fetch order user for status notification", slog.String("userId", userID), slog.Any("error", userErr))
		} else if userRecord != nil {
			lang := strings.ToLower(strings.TrimSpace(userRecord.GetString("language")))
			if lang != "ru" {
				lang = "uz"
			}

			supportInfo := supportInfoUz
			if lang == "ru" {
				supportInfo = supportInfoRu
			}

			if msgText, ok := buildUserStatusNotificationText(lang, oldStatus, status, displayOrderID, supportInfo); ok {
				userChatID := userRecord.GetString("chat_id")
				cid, parseErr := strconv.ParseInt(userChatID, 10, 64)
				if parseErr == nil && cid != 0 {
					userMsg := tgbotapi.NewMessage(cid, msgText)
					if _, sendErr := h.bot.Send(userMsg); sendErr != nil {
						h.logger.Error("Failed to send user status notification", slog.Any("error", sendErr))
					}
				}
			}
		}
		if status != "accepted" {
			return e.Next()
		}
		if oldStatus == "accepted" {
			return e.Next()
		}
		created := e.Record.GetString("created")
		receipt := e.Record.GetString("receipt")
		createdFormatted := created
		loc, locErr := time.LoadLocation("Asia/Tashkent")
		if locErr != nil {
			loc = time.FixedZone("Asia/Tashkent", 5*60*60)
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			createdFormatted = t.In(loc).Format("02.01.2006 15:04")
		} else if t, err := time.Parse("2006-01-02 15:04:05.000Z", created); err == nil {
			createdFormatted = t.In(loc).Format("02.01.2006 15:04")
		} else if t, err := time.Parse("2006-01-02 15:04:05Z", created); err == nil {
			createdFormatted = t.In(loc).Format("02.01.2006 15:04")
		}

		statusUz := status
		switch status {
		case "accepted":
			statusUz = "Qabul qilingan"
		case "confirmed":
			statusUz = "Tasdiqlangan"
		case "rejected":
			statusUz = "Rad etilgan"
		case "cancelled":
			statusUz = "Bekor qilingan"
		case "delivered":
			statusUz = "Yetkazilgan"
		case "In progress":
			statusUz = "Jarayonda"
		case "Done":
			statusUz = "Tugallangan"
		case "Cancelled":
			statusUz = "Bekor qilingan"
		}

		orderUserRecord, orderUserErr := app.FindRecordById("_pb_users_auth_", userID)
		if orderUserErr != nil {
			h.logger.Error("Failed to fetch order user", slog.String("userId", userID), slog.Any("error", orderUserErr))
		}

		var userName string
		if orderUserRecord != nil {
			userName = orderUserRecord.GetString("name")
		}

		phone := e.Record.GetString("phone")
		receiverName := e.Record.GetString("receiver_name")
		note := e.Record.GetString("note")

		lines := ""
		overall := 0.0
		items, itemsErr := app.FindRecordsByFilter(
			"orderItems",
			"order = {:order}",
			"created",
			500,
			0,
			dbx.Params{"order": orderID},
		)
		if itemsErr != nil {
			if strings.Contains(itemsErr.Error(), "unknown field \"order\"") {
				items, itemsErr = app.FindRecordsByFilter(
					"orderItems",
					"cart = {:cart}",
					"created",
					500,
					0,
					dbx.Params{"cart": orderID},
				)
			}
			if itemsErr != nil {
				h.logger.Error("Failed to find order items", slog.String("orderId", orderID), slog.Any("error", itemsErr))
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

			productRecord, productErr := app.FindRecordById("products", pID)
			if productErr != nil {
				h.logger.Error("Failed to fetch order item product", slog.String("productId", pID), slog.Any("error", productErr))
				continue
			}

			productName := ""
			productDescription := ""
			productCategoryName := ""
			if productRecord != nil {
				productName = productRecord.GetString("name")
				productDescription = stripDeliveryBlurb(productRecord.GetString("description"))
				categoryID := productRecord.GetString("category")
				if categoryID != "" {
					categoryRecord, categoryErr := app.FindRecordById("categories", categoryID)
					if categoryErr == nil && categoryRecord != nil {
						productCategoryName = categoryRecord.GetString("name")
					}
				}
				if unitPrice == 0 {
					unitPrice = productRecord.GetFloat("price")
				}
			}

			lineTotal := unitPrice * q
			overall += lineTotal
			_ = productCategoryName
			_ = productDescription
			lines += fmt.Sprintf("• %s ×%s — %s сум\n", productName, formatQtyRu(q), formatAmountRu(lineTotal))
		}

		addressBlock := ""
		var addressLat float64
		var addressLng float64
		hasAddressLocation := false
		deliveryAddressID := e.Record.GetString("delivery_address")
		var addrRecord *core.Record
		if deliveryAddressID != "" {
			var addrErr error
			addrRecord, addrErr = app.FindRecordById("addresses", deliveryAddressID)
			if addrErr != nil {
				h.logger.Error("Failed to fetch delivery address", slog.String("orderId", orderID), slog.String("deliveryAddressId", deliveryAddressID), slog.Any("error", addrErr))
			}
		}
		addrText := ""
		mapsLink := ""
		if addrRecord != nil {
			addrText = addrRecord.GetString("address")
			addressLat, addressLng, hasAddressLocation = extractLatLng(addrRecord.Get("longlat"))
			if hasAddressLocation {
				mapsLink = fmt.Sprintf("https://yandex.com/maps/?pt=%.6f,%.6f&z=16&l=map", addressLng, addressLat)
			}
		}
		if addrText != "" || mapsLink != "" {
			addressBlock = "\n\n📍 Адрес:"
			if addrText != "" {
				addressBlock += "\n" + addrText
			}
			if mapsLink != "" {
				addressBlock += "\n🗺 " + mapsLink
			}
		}

		datePart := createdFormatted
		timePart := ""
		if parts := strings.SplitN(createdFormatted, " ", 2); len(parts) == 2 {
			datePart = parts[0]
			timePart = parts[1]
		}
		dateLine := fmt.Sprintf("📅 %s", datePart)
		if timePart != "" {
			dateLine += fmt.Sprintf(" ⏰ %s", timePart)
		}

		clientName := userName
		if clientName == "" {
			clientName = receiverName
		}
		phoneLine := ""
		if strings.TrimSpace(phone) != "" {
			phoneLine = "\n📞 " + phone
		}
		noteLine := ""
		if strings.TrimSpace(note) != "" {
			noteLine = "\n💬 " + note
		}

		orderBlock := "🛒 Заказ:\n"
		if strings.TrimSpace(lines) == "" {
			orderBlock += "• —\n"
		} else {
			orderBlock += lines
		}

		text := fmt.Sprintf(
			"🆕 Новый заказ #%s\n%s\n📌 Статус: %s\n\n👤 Клиент: %s%s%s%s\n\n%s\n💰 Итого: %s сум",
			displayOrderID,
			dateLine,
			statusUz,
			clientName,
			phoneLine,
			noteLine,
			addressBlock,
			orderBlock,
			formatAmountRu(overall),
		)

		replyMarkup := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Tasdiqlash", "order:confirm:"+orderID),
				tgbotapi.NewInlineKeyboardButtonData("Tasdiqlamaslik", "order:cancel:"+orderID),
			),
		)

		// Route all admin notifications via service to respect contacts.confirmation_type and GROUP_URL.
		confirmationRefs := []service.TelegramMessageRef{}
		receiptMessageRefs := []service.TelegramMessageRef{}
		caption := text
		captionParts := splitTextRunes(caption, 900)
		if len(captionParts) > 1 {
			caption = captionParts[0]
		}

		// Send receipt first so the photo is on top.
		if receipt != "" {
			imgPath := filepath.Join(app.DataDir(), "storage", e.Record.Collection().Id, orderID, receipt)
			if _, statErr := os.Stat(imgPath); statErr == nil {
				ext := strings.ToLower(filepath.Ext(receipt))
				imgBytes, readErr := os.ReadFile(imgPath)
				sendAsPhoto := ext == ".jpg" || ext == ".jpeg" || ext == ".png"

				if ext == ".heic" || ext == ".heif" || !sendAsPhoto {
					var doc tgbotapi.DocumentConfig
					if readErr == nil {
						doc = tgbotapi.NewDocument(0, tgbotapi.FileBytes{Name: receipt, Bytes: imgBytes})
					} else {
						h.logger.Error("Failed to read receipt file", slog.Any("error", readErr), slog.String("path", imgPath))
						doc = tgbotapi.NewDocument(0, tgbotapi.FilePath(imgPath))
					}
					doc.Caption = caption
					doc.ReplyMarkup = replyMarkup
					refs, mediaErr := h.service.TelegramNotify().SendAdminWithResults(h.bot, doc, nil)
					if mediaErr != nil {
						h.logger.Error("Failed to send admin order receipt document", slog.Any("error", mediaErr))
					} else {
						receiptMessageRefs = refs
						confirmationRefs = refs
					}
				} else {
					var photo tgbotapi.PhotoConfig
					var docFallback tgbotapi.DocumentConfig
					if readErr == nil {
						name := receipt
						if ext == ".jpeg" {
							name = "receipt.jpg"
						}
						photo = tgbotapi.NewPhoto(0, tgbotapi.FileBytes{Name: name, Bytes: imgBytes})
						docFallback = tgbotapi.NewDocument(0, tgbotapi.FileBytes{Name: receipt, Bytes: imgBytes})
					} else {
						h.logger.Error("Failed to read receipt file", slog.Any("error", readErr), slog.String("path", imgPath))
						photo = tgbotapi.NewPhoto(0, tgbotapi.FilePath(imgPath))
						docFallback = tgbotapi.NewDocument(0, tgbotapi.FilePath(imgPath))
					}
					photo.Caption = caption
					photo.ReplyMarkup = replyMarkup
					refs, mediaErr := h.service.TelegramNotify().SendAdminWithResults(h.bot, photo, docFallback)
					if mediaErr != nil {
						h.logger.Error("Failed to send admin order photo notification", slog.Any("error", mediaErr))
					} else {
						receiptMessageRefs = refs
						confirmationRefs = refs
					}
				}
			} else {
				h.logger.Error("Receipt file not found", slog.String("path", imgPath), slog.Any("error", statErr))
			}
		}

		if len(confirmationRefs) == 0 {
			msg := tgbotapi.NewMessage(0, text)
			msg.ReplyMarkup = replyMarkup
			refs, sendErr := h.service.TelegramNotify().SendAdminWithResults(h.bot, msg, nil)
			if sendErr != nil {
				h.logger.Error("Failed to send admin order notification", slog.Any("error", sendErr))
			}
			confirmationRefs = refs
		}

		if len(confirmationRefs) > 0 {
			b, mErr := json.Marshal(confirmationRefs)
			if mErr != nil {
				h.logger.Error("Failed to marshal tg confirmation message refs", slog.Any("error", mErr))
			} else {
				freshRecord.Set("tg_confirmation_messages", string(b))
				if saveErr := app.Save(freshRecord); saveErr != nil {
					h.logger.Error("Failed to save tg confirmation message refs", slog.Any("error", saveErr))
				}
			}
		}

		if hasAddressLocation {
			refs := confirmationRefs
			if len(receiptMessageRefs) > 0 {
				refs = receiptMessageRefs
			}
			for _, ref := range refs {
				locMsg := tgbotapi.NewLocation(ref.ChatID, addressLat, addressLng)
				locMsg.ReplyToMessageID = ref.MessageID
				if _, sendErr := h.bot.Send(locMsg); sendErr != nil {
					h.logger.Error("Failed to send admin order location", slog.Any("error", sendErr))
				}
			}
		}

		return e.Next()
	})
}

func buildUserStatusNotificationText(lang, oldStatus, newStatus, orderID, supportInfo string) (string, bool) {
	switch {
	case newStatus == "accepted":
		if lang == "ru" {
			return fmt.Sprintf("🇷🇺 Ваш заказ принят.\nНомер заказа: %s", orderID), true
		}
		return fmt.Sprintf("🇺🇿 Buyurtmangiz qabul qilindi.\nBuyurtma raqami: %s", orderID), true
	case oldStatus == "accepted" && newStatus == "confirmed":
		if lang == "ru" {
			return fmt.Sprintf("🇷🇺 Ваш заказ подтверждён.\nНомер заказа: %s", orderID), true
		}
		return fmt.Sprintf("🇺🇿 Buyurtmangiz tasdiqlandi.\nBuyurtma raqami: %s", orderID), true
	case newStatus == "cancelled":
		if lang == "ru" {
			if strings.TrimSpace(supportInfo) != "" {
				return fmt.Sprintf("❌ Ваш заказ отменён: %s\n%s", orderID, supportInfo), true
			}
			return fmt.Sprintf("❌ Ваш заказ отменён: %s", orderID), true
		}
		if strings.TrimSpace(supportInfo) != "" {
			return fmt.Sprintf("❌ Buyurtmangiz bekor qilindi: %s\n%s", orderID, supportInfo), true
		}
		return fmt.Sprintf("❌ Buyurtmangiz bekor qilindi: %s", orderID), true
	case newStatus == "done":
		if lang == "ru" {
			return fmt.Sprintf("🇷🇺 Ваш заказ успешно выполнен.\nСпасибо, что выбрали нас! Хорошего дня.\nНомер заказа: %s", orderID), true
		}
		return fmt.Sprintf("🇺🇿 Buyurtmangiz muvaffaqiyatli yakunlandi.\nBizni tanlaganingiz uchun rahmat! Yoqimli kun tilaymiz.\nBuyurtma raqami: %s", orderID), true
	default:
		return "", false
	}
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

func New(logger *slog.Logger, service service.I, cfg *config.Config) *Hook {
	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		logger.Error("Failed to create bot", slog.Any("error", err))
	}

	return &Hook{
		logger:  logger,
		service: service,
		cfg:     cfg,
		bot:     bot,
	}
}
