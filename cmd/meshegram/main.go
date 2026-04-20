// meshegram is a two-way Meshtastic ↔ Telegram bridge: it forwards text
// messages from a Meshtastic node into a Telegram chat and accepts /send
// and /channels commands back from whitelisted Telegram users.
package main

import (
	"context"
	"fmt"
	"html"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	pb "github.com/meshnet-gophers/meshtastic-go/meshtastic"
	"github.com/meshnet-gophers/meshtastic-go/transport"

	"gitlab.uscr.ru/public-projects/meshegram/internal/logx"
	"gitlab.uscr.ru/public-projects/meshegram/internal/mesh"
	"gitlab.uscr.ru/public-projects/meshegram/internal/tgclient"
)

const (
	botPollTimeout = 30 * time.Second
	// Conservative ceiling below Meshtastic's ~228-byte text payload MTU.
	meshTextMaxBytes = 220
	sendCommand      = "/send"
	channelsCommand  = "/channels"
	// How many recent mesh-to-Telegram message mappings to keep for
	// resolving reactions. Anything older is dropped silently.
	msgCacheCapacity = 100
)

type meInfo struct {
	username string
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		logx.Error.Fatalf("config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	httpClient, err := tgclient.New(cfg.proxyURL)
	if err != nil {
		logx.Error.Fatalf("telegram http client: %v", err)
	}

	b, err := bot.New(cfg.botToken, bot.WithHTTPClient(botPollTimeout, httpClient))
	if err != nil {
		logx.Error.Fatalf("bot: %v", err)
	}

	me, err := resolveMe(ctx, b)
	if err != nil {
		logx.Error.Fatalf("getMe: %v", err)
	}

	bridge := newBridgeState(msgCacheCapacity, cfg.defaultChannel)
	onPacket := func(pkt *pb.MeshPacket, state *transport.State) {
		handleIncomingPacket(ctx, b, cfg, bridge, pkt, state)
	}
	s := newSession(cfg.nodeAddress, cfg.hopLimit, cfg.reconnectInterval, onPacket)

	b.RegisterHandlerMatchFunc(
		func(*models.Update) bool { return true },
		makeHandler(cfg, s, me, bridge),
	)

	logx.Info.Printf("meshegram started: bot=@%s chat=%d node=%s default_channel=%d allowed_users=%d",
		me.username, cfg.chatID, cfg.nodeAddress, cfg.defaultChannel, cfg.allowedCount())

	go s.Run(ctx)
	b.Start(ctx)
}

func resolveMe(ctx context.Context, b *bot.Bot) (meInfo, error) {
	getMeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	u, err := b.GetMe(getMeCtx)
	if err != nil {
		return meInfo{}, err
	}
	if u.Username == "" {
		return meInfo{}, fmt.Errorf("bot has no username")
	}
	return meInfo{username: u.Username}, nil
}

func handleIncomingPacket(ctx context.Context, b *bot.Bot, cfg *config, bridge *bridgeState, pkt *pb.MeshPacket, state *transport.State) {
	if !cfg.channelAllowed(mesh.ChannelName(pkt.Channel, state)) {
		return
	}
	if mesh.IsReaction(pkt) {
		handleReaction(ctx, b, cfg, bridge.cache, pkt)
		return
	}
	text := mesh.TextPayload(pkt)
	if text == "" {
		return
	}
	if !cfg.messageAllowed(text) {
		return
	}
	msg := formatIncoming(cfg.nodeName, pkt, text, state)
	sent, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    cfg.chatID,
		Text:      msg,
		ParseMode: models.ParseModeHTML,
	})
	if err != nil {
		logx.Error.Printf("telegram send: %v", err)
		return
	}
	bridge.cache.Put(pkt.Id, sent.ID)
	bridge.SetLastChannel(pkt.Channel)
}

func handleReaction(ctx context.Context, b *bot.Bot, cfg *config, cache *msgCache, pkt *pb.MeshPacket) {
	dec, ok := pkt.PayloadVariant.(*pb.MeshPacket_Decoded)
	if !ok || dec.Decoded == nil {
		return
	}
	data := dec.Decoded
	emoji := strings.TrimSpace(string(data.Payload))
	if emoji == "" || data.ReplyId == 0 {
		return
	}
	tgMsgID, ok := cache.Get(data.ReplyId)
	if !ok {
		// Original message isn't in cache — either never forwarded or evicted.
		return
	}
	_, err := b.SetMessageReaction(ctx, &bot.SetMessageReactionParams{
		ChatID:    cfg.chatID,
		MessageID: tgMsgID,
		Reaction: []models.ReactionType{{
			Type:              models.ReactionTypeTypeEmoji,
			ReactionTypeEmoji: &models.ReactionTypeEmoji{Emoji: emoji},
		}},
	})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "REACTION_INVALID") {
		logx.Info.Printf("reaction %q is not in Telegram's allowed set, skipping (tg msg %d)", emoji, tgMsgID)
		return
	}
	logx.Error.Printf("set reaction %q on tg msg %d: %v", emoji, tgMsgID, err)
}

func formatIncoming(nodeName string, pkt *pb.MeshPacket, text string, state *transport.State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📡 <b>%s</b> · <code>#%s</code>\n",
		html.EscapeString(nodeName),
		html.EscapeString(mesh.ChannelName(pkt.Channel, state)),
	)
	fmt.Fprintf(&b, "👤 %s\n", html.EscapeString(mesh.NodeLabel(pkt.From, state)))
	fmt.Fprintf(&b, "<i>%s · SNR %.1f dB · RSSI %d dBm</i>\n",
		formatHops(pkt), pkt.RxSnr, pkt.RxRssi)
	if pkt.ViaMqtt {
		b.WriteString("<i>via MQTT</i>\n")
	}
	fmt.Fprintf(&b, "\n<blockquote>%s</blockquote>", html.EscapeString(text))
	return b.String()
}

func formatHops(pkt *pb.MeshPacket) string {
	if start, ok := mesh.HopStart(pkt); ok && start >= pkt.HopLimit {
		taken := start - pkt.HopLimit
		if taken == 0 {
			return "прямой сигнал"
		}
		return fmt.Sprintf("%d %s", taken, hopPlural(taken))
	}
	return fmt.Sprintf("TTL %d", pkt.HopLimit)
}

// hopPlural picks a Russian-grammar-correct word form for the given count.
func hopPlural(n uint32) string {
	mod100 := n % 100
	mod10 := n % 10
	switch {
	case mod100 >= 11 && mod100 <= 14:
		return "хопов"
	case mod10 == 1:
		return "хоп"
	case mod10 >= 2 && mod10 <= 4:
		return "хопа"
	default:
		return "хопов"
	}
}

func makeHandler(cfg *config, s *session, me meInfo, bridge *bridgeState) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		msg := update.Message
		if msg == nil || msg.From == nil {
			return
		}

		isPrivate := msg.Chat.Type == models.ChatTypePrivate
		cmd, body, ok := extractCommand(msg, me)

		if !isPrivate && !ok {
			return
		}
		if !ok {
			cmd = sendCommand
			body = strings.TrimSpace(msg.Text)
		}

		if !isAllowed(cfg, msg) {
			if isPrivate {
				reply(ctx, b, msg, fmt.Sprintf("⛔ Нет доступа. Ваш ID: %d", msg.From.ID))
			} else {
				logx.Info.Printf("rejected %s from user %d (%s) in chat %d",
					cmd, msg.From.ID, msg.From.Username, msg.Chat.ID)
			}
			return
		}

		switch cmd {
		case sendCommand:
			handleSend(ctx, b, msg, cfg, s, bridge, body, isPrivate)
		case channelsCommand:
			handleChannels(ctx, b, msg, s)
		}
	}
}

func handleSend(ctx context.Context, b *bot.Bot, msg *models.Message, cfg *config, s *session, bridge *bridgeState, body string, isPrivate bool) {
	chanName, text := splitChannelPrefix(body)
	if text == "" {
		hint := "ℹ️ Пришлите текст для отправки в mesh."
		if !isPrivate {
			hint = "ℹ️ Использование: /send [#channel] текст сообщения"
		}
		reply(ctx, b, msg, hint)
		return
	}

	channelIdx := cfg.defaultChannel
	if !isPrivate {
		// In the bridged group without an explicit #channel we reply into the
		// channel of the most recent incoming mesh message.
		channelIdx = bridge.LastChannel()
	}
	if chanName != "" {
		idx, err := s.ChannelIndexByName(chanName)
		if err != nil {
			reply(ctx, b, msg, fmt.Sprintf("❌ Канал #%s не найден", chanName))
			return
		}
		channelIdx = idx
	}

	meshText := text
	if cfg.prependAuthor {
		if author := authorLabel(msg); author != "" {
			meshText = author + ": " + text
		}
	}
	meshText = truncateUTF8(meshText, meshTextMaxBytes)

	if err := s.SendText(ctx, channelIdx, meshText); err != nil {
		logx.Error.Printf("mesh send failed: %v", err)
		reply(ctx, b, msg, "❌ Не удалось отправить: "+err.Error())
		return
	}
	reply(ctx, b, msg, "✅ Отправлено в mesh")
}

func handleChannels(ctx context.Context, b *bot.Bot, msg *models.Message, s *session) {
	channels := s.Channels()
	if len(channels) == 0 {
		reply(ctx, b, msg, "ℹ️ Нет информации о каналах (нода не на связи?).")
		return
	}
	var out strings.Builder
	out.WriteString("<b>Каналы на ноде:</b>\n")
	for _, ch := range channels {
		name := ""
		if ch.Settings != nil {
			name = ch.Settings.Name
		}
		if name == "" && ch.Role == pb.Channel_PRIMARY {
			name = "Default"
		}
		role := strings.ToLower(ch.Role.String())
		fmt.Fprintf(&out, "• <code>#%s</code> — index %d, %s\n",
			html.EscapeString(name), ch.Index, role)
	}
	replyHTML(ctx, b, msg, out.String())
}

// extractCommand returns (command, body, true) if msg starts with a bot_command
// entity we handle (/send or /channels, optionally @botname-suffixed).
func extractCommand(msg *models.Message, me meInfo) (cmd, body string, ok bool) {
	if msg == nil || len(msg.Entities) == 0 || msg.Text == "" {
		return "", "", false
	}
	for _, ent := range msg.Entities {
		if ent.Type != models.MessageEntityTypeBotCommand || ent.Offset != 0 {
			continue
		}
		end := ent.Offset + ent.Length
		if end > len(msg.Text) {
			return "", "", false
		}
		raw := msg.Text[ent.Offset:end]
		suffix := "@" + me.username
		base := raw
		if strings.HasSuffix(strings.ToLower(raw), strings.ToLower(suffix)) {
			base = raw[:len(raw)-len(suffix)]
		}
		switch strings.ToLower(base) {
		case sendCommand, channelsCommand:
			return strings.ToLower(base), strings.TrimSpace(msg.Text[end:]), true
		}
	}
	return "", "", false
}

// splitChannelPrefix returns ("channelName", "rest") if body starts with
// "#name " and ("", body) otherwise.
func splitChannelPrefix(body string) (string, string) {
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "#") || len(body) < 2 {
		return "", body
	}
	rest := body[1:]
	if idx := strings.IndexAny(rest, " \t\n"); idx > 0 {
		return rest[:idx], strings.TrimSpace(rest[idx+1:])
	}
	return rest, ""
}

func isAllowed(cfg *config, msg *models.Message) bool {
	if msg == nil {
		return false
	}
	// Anonymous admins in the bridged chat come through as GroupAnonymousBot
	// with SenderChat pointing at the group itself. Trust these — the sender
	// is by definition already an admin of the chat we bridge.
	if msg.SenderChat != nil && msg.SenderChat.ID == cfg.chatID {
		return true
	}
	u := msg.From
	if u == nil {
		return false
	}
	if _, ok := cfg.allowedIDs[u.ID]; ok {
		return true
	}
	if u.Username != "" {
		if _, ok := cfg.allowedUsernames[strings.ToLower(u.Username)]; ok {
			return true
		}
	}
	return false
}

func authorLabel(msg *models.Message) string {
	if msg == nil {
		return ""
	}
	if msg.SenderChat != nil && msg.SenderChat.Title != "" {
		return msg.SenderChat.Title
	}
	u := msg.From
	if u == nil {
		return ""
	}
	switch {
	case u.Username != "":
		return "@" + u.Username
	case u.FirstName != "" && u.LastName != "":
		return u.FirstName + " " + u.LastName
	case u.FirstName != "":
		return u.FirstName
	default:
		return ""
	}
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	out := make([]byte, 0, max)
	for _, r := range s {
		sz := utf8.RuneLen(r)
		if len(out)+sz > max {
			break
		}
		out = utf8.AppendRune(out, r)
	}
	return string(out)
}

func reply(ctx context.Context, b *bot.Bot, msg *models.Message, text string) {
	sendReply(ctx, b, msg, &bot.SendMessageParams{Text: text})
}

func replyHTML(ctx context.Context, b *bot.Bot, msg *models.Message, text string) {
	sendReply(ctx, b, msg, &bot.SendMessageParams{Text: text, ParseMode: models.ParseModeHTML})
}

func sendReply(ctx context.Context, b *bot.Bot, msg *models.Message, params *bot.SendMessageParams) {
	params.ChatID = msg.Chat.ID
	if msg.Chat.Type != models.ChatTypePrivate {
		params.ReplyParameters = &models.ReplyParameters{MessageID: msg.ID}
	}
	if _, err := b.SendMessage(ctx, params); err != nil {
		logx.Error.Printf("reply to chat %d: %v", msg.Chat.ID, err)
	}
}
