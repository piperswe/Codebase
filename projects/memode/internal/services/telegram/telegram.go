// Package telegram exposes the owner's Telegram account (a user session over
// MTProto, via gotd/td) to scripts: dialogs, message history, sending,
// replying, editing, deleting and marking read.
package telegram

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/projects/memode/internal/config"
)

// Service is the Telegram user-account service.
type Service struct {
	cfg config.Config

	mu     sync.Mutex
	api    *tg.Client
	ready  bool
	fatal  error
	peers  map[string]tg.InputPeerClass // chat id / @username / title -> peer
	titles map[string]string            // peer key -> display title
}

// New creates the Telegram service. The authorized MTProto session is cached
// in a file next to the database, so no database handle is needed here.
func New(cfg config.Config) *Service {
	return &Service{cfg: cfg, peers: map[string]tg.InputPeerClass{}, titles: map[string]string{}}
}

// Setup starts the MTProto client. First run authenticates interactively:
// Telegram sends a login code to the owner's other sessions, and a 2FA
// password is read from config or prompted. The authorized session is cached
// in a file next to the database, so later runs are non-interactive.
func (s *Service) Setup(ctx context.Context, ready func(bool)) {
	appID, err := strconv.Atoi(s.cfg.TelegramAPIID)
	if err != nil || appID == 0 {
		ready(false)
		return
	}
	storage := &session.FileStorage{Path: s.cfg.DBPath + ".telegram.session"}
	client := telegram.NewClient(appID, s.cfg.TelegramAPIHash, telegram.Options{
		SessionStorage: storage,
	})
	go func() {
		err := client.Run(ctx, func(ctx context.Context) error {
			flow := auth.NewFlow(authenticator{cfg: s.cfg}, auth.SendCodeOptions{})
			if err := client.Auth().IfNecessary(ctx, flow); err != nil {
				return err
			}
			s.mu.Lock()
			s.api = client.API()
			s.ready = true
			s.mu.Unlock()
			<-ctx.Done()
			return ctx.Err()
		})
		if err != nil && ctx.Err() == nil {
			s.mu.Lock()
			s.fatal = err
			s.ready = false
			s.mu.Unlock()
			fmt.Fprintf(os.Stderr, "memode telegram: %v\n", err)
		}
		ready(s.isReady())
	}()
}

func (s *Service) isReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

// client returns the MTProto API client, or an error while the session is not
// up yet.
func (s *Service) client() (*tg.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.api == nil {
		if s.fatal != nil {
			return nil, fmt.Errorf("telegram unavailable: %w", s.fatal)
		}
		return nil, fmt.Errorf("telegram session is not connected yet (first-run login may be pending)")
	}
	return s.api, nil
}

// Register adds the Lua API.
func (s *Service) Register(reg *codemode.Registry) {
	reg.Register(codemode.Function{Namespace: "telegram", Name: "dialogs", Desc: "list your chats. args: {limit?}. returns {chat (use it with the other functions), title, kind, unread, last_message_id}", Fn: s.fnDialogs})
	reg.Register(codemode.Function{Namespace: "telegram", Name: "history", Desc: "read messages of a chat, newest first. args: {chat, limit?, before_id?}", Fn: s.fnHistory})
	reg.Register(codemode.Function{Namespace: "telegram", Name: "search", Desc: "search messages globally or in one chat. args: {query, chat?, limit?}", Fn: s.fnSearch})
	reg.Register(codemode.Function{Namespace: "telegram", Name: "send", Write: true, Desc: "send a message. args: {chat, text, reply_to? (message id)}. returns {message_id, date}", Fn: s.fnSend})
	reg.Register(codemode.Function{Namespace: "telegram", Name: "edit", Write: true, Desc: "edit your own message. args: {chat, message_id, text}", Fn: s.fnEdit})
	reg.Register(codemode.Function{Namespace: "telegram", Name: "delete", Write: true, Desc: "delete messages. args: {chat, message_ids (list or single id), for_everyone? (default true)}", Fn: s.fnDelete})
	reg.Register(codemode.Function{Namespace: "telegram", Name: "mark_read", Write: true, Desc: "mark a chat as read. args: {chat}", Fn: s.fnMarkRead})
}

func (s *Service) fnDialogs(ctx context.Context, args map[string]any) (any, error) {
	api, err := s.client()
	if err != nil {
		return nil, err
	}
	limit := argInt(args, "limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{}, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("get dialogs: %w", err)
	}
	full, ok := res.(*tg.MessagesDialogs)
	if !ok {
		slice, ok := res.(*tg.MessagesDialogsSlice)
		if !ok {
			return []any{}, nil
		}
		full = &tg.MessagesDialogs{Dialogs: slice.Dialogs, Messages: slice.Messages, Chats: slice.Chats, Users: slice.Users}
	}
	s.cachePeers(full.Users, full.Chats)

	out := make([]map[string]any, 0, len(full.Dialogs))
	for _, d := range full.Dialogs {
		dialog, ok := d.(*tg.Dialog)
		if !ok {
			continue
		}
		key, title, kind := s.describe(full.Users, full.Chats, dialog.Peer)
		out = append(out, map[string]any{
			"chat": key, "title": title, "kind": kind,
			"unread": dialog.UnreadCount, "last_message_id": dialog.TopMessage,
		})
	}
	return out, nil
}

func (s *Service) fnHistory(ctx context.Context, args map[string]any) (any, error) {
	api, err := s.client()
	if err != nil {
		return nil, err
	}
	peer, err := s.resolvePeer(ctx, api, strArg(args, "chat"))
	if err != nil {
		return nil, err
	}
	limit := argInt(args, "limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	req := &tg.MessagesGetHistoryRequest{Peer: peer, Limit: limit}
	if before := argInt(args, "before_id", 0); before > 0 {
		req.OffsetID = before
	}
	res, err := api.MessagesGetHistory(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get history: %w", err)
	}
	return messagesToLua(res, nil), nil
}

func (s *Service) fnSearch(ctx context.Context, args map[string]any) (any, error) {
	api, err := s.client()
	if err != nil {
		return nil, err
	}
	query := strArg(args, "query")
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := argInt(args, "limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	req := &tg.MessagesSearchRequest{Q: query, Limit: limit, Filter: &tg.InputMessagesFilterEmpty{}}
	if chat := strArg(args, "chat"); chat != "" {
		peer, err := s.resolvePeer(ctx, api, chat)
		if err != nil {
			return nil, err
		}
		req.Peer = peer
	}
	res, err := api.MessagesSearch(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	return messagesToLua(res, nil), nil
}

func (s *Service) fnSend(ctx context.Context, args map[string]any) (any, error) {
	api, err := s.client()
	if err != nil {
		return nil, err
	}
	peer, err := s.resolvePeer(ctx, api, strArg(args, "chat"))
	if err != nil {
		return nil, err
	}
	text := strArg(args, "text")
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}
	req := &tg.MessagesSendMessageRequest{Peer: peer, Message: text}
	if replyTo := argInt(args, "reply_to", 0); replyTo > 0 {
		req.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: replyTo}
	}
	updates, err := api.MessagesSendMessage(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	id, date := sentMessageInfo(updates)
	return map[string]any{"message_id": id, "date": date}, nil
}

func (s *Service) fnEdit(ctx context.Context, args map[string]any) (any, error) {
	api, err := s.client()
	if err != nil {
		return nil, err
	}
	peer, err := s.resolvePeer(ctx, api, strArg(args, "chat"))
	if err != nil {
		return nil, err
	}
	id := argInt(args, "message_id", 0)
	if id <= 0 {
		return nil, fmt.Errorf("message_id is required")
	}
	text := strArg(args, "text")
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}
	if _, err := api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{Peer: peer, ID: id, Message: text}); err != nil {
		return nil, fmt.Errorf("edit: %w", err)
	}
	return "edited", nil
}

func (s *Service) fnDelete(ctx context.Context, args map[string]any) (any, error) {
	api, err := s.client()
	if err != nil {
		return nil, err
	}
	peer, err := s.resolvePeer(ctx, api, strArg(args, "chat"))
	if err != nil {
		return nil, err
	}
	ids, err := argIDs(args["message_ids"])
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("message_ids is required")
	}
	req := &tg.MessagesDeleteMessagesRequest{ID: ids}
	if revoke, ok := args["for_everyone"].(bool); !ok || revoke {
		// Channels require a channel-specific call with revoke semantics.
		if inputChannel, ok := peer.(*tg.InputPeerChannel); ok {
			if _, err := api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
				Channel: &tg.InputChannel{ChannelID: inputChannel.ChannelID, AccessHash: inputChannel.AccessHash},
				ID:      ids,
			}); err != nil {
				return nil, fmt.Errorf("delete: %w", err)
			}
			return "deleted", nil
		}
		req.Revoke = true
	}
	if _, err := api.MessagesDeleteMessages(ctx, req); err != nil {
		return nil, fmt.Errorf("delete: %w", err)
	}
	return "deleted", nil
}

func (s *Service) fnMarkRead(ctx context.Context, args map[string]any) (any, error) {
	api, err := s.client()
	if err != nil {
		return nil, err
	}
	peer, err := s.resolvePeer(ctx, api, strArg(args, "chat"))
	if err != nil {
		return nil, err
	}
	if _, err := api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: peer}); err != nil {
		return nil, fmt.Errorf("mark read: %w", err)
	}
	return "read", nil
}

// messagesToLua flattens the MessagesClass response into script-friendly maps.
func messagesToLua(res tg.MessagesMessagesClass, _ any) []map[string]any {
	var msgs []tg.MessageClass
	switch m := res.(type) {
	case *tg.MessagesMessages:
		msgs = m.Messages
	case *tg.MessagesMessagesSlice:
		msgs = m.Messages
	case *tg.MessagesChannelMessages:
		msgs = m.Messages
	}
	out := make([]map[string]any, 0, len(msgs))
	for _, raw := range msgs {
		msg, ok := raw.(*tg.Message)
		if !ok {
			continue
		}
		entry := map[string]any{
			"message_id": msg.ID,
			"date":       int64(msg.Date),
			"text":       msg.Message,
			"outgoing":   msg.Out,
			"kind":       messageKind(msg),
		}
		if from, ok := msg.GetFromID(); ok {
			entry["sender_id"] = peerID(from)
		}
		if reply, ok := msg.GetReplyTo(); ok {
			if r, ok := reply.(*tg.MessageReplyHeader); ok {
				entry["reply_to"] = r.ReplyToMsgID
			}
		}
		out = append(out, entry)
	}
	return out
}

func messageKind(msg *tg.Message) string {
	switch msg.Media.(type) {
	case *tg.MessageMediaPhoto:
		return "photo"
	case *tg.MessageMediaDocument:
		return "document"
	case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive:
		return "location"
	case *tg.MessageMediaContact:
		return "contact"
	case *tg.MessageMediaPoll:
		return "poll"
	case nil:
		return "text"
	default:
		return "other"
	}
}

func sentMessageInfo(updates tg.UpdatesClass) (int, int64) {
	switch u := updates.(type) {
	case *tg.Updates:
		for _, upd := range u.Updates {
			if m, ok := upd.(*tg.UpdateNewMessage); ok {
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID, int64(msg.Date)
				}
			}
		}
	case *tg.UpdateShortSentMessage:
		return u.ID, int64(u.Date)
	}
	return 0, 0
}

func peerID(p tg.PeerClass) int64 {
	switch v := p.(type) {
	case *tg.PeerUser:
		return v.UserID
	case *tg.PeerChat:
		return v.ChatID
	case *tg.PeerChannel:
		return v.ChannelID
	}
	return 0
}

// cachePeers remembers how to address each chat seen in a dialogs listing.
func (s *Service) cachePeers(users []tg.UserClass, chats []tg.ChatClass) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range users {
		user, ok := u.(*tg.User)
		if !ok {
			continue
		}
		peer := &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}
		title := strings.TrimSpace(user.FirstName + " " + user.LastName)
		if username, ok := user.GetUsername(); ok && username != "" {
			title = strings.TrimSpace(title + " (@" + username + ")")
			s.peers["@"+strings.ToLower(username)] = peer
		}
		s.peers[strconv.FormatInt(user.ID, 10)] = peer
		s.titles[strconv.FormatInt(user.ID, 10)] = title
	}
	for _, c := range chats {
		switch chat := c.(type) {
		case *tg.Chat:
			peer := &tg.InputPeerChat{ChatID: chat.ID}
			s.peers[strconv.FormatInt(chat.ID, 10)] = peer
			s.titles[strconv.FormatInt(chat.ID, 10)] = chat.Title
		case *tg.Channel:
			peer := &tg.InputPeerChannel{ChannelID: chat.ID, AccessHash: chat.AccessHash}
			if username, ok := chat.GetUsername(); ok && username != "" {
				s.peers["@"+strings.ToLower(username)] = peer
			}
			s.peers[strconv.FormatInt(chat.ID, 10)] = peer
			s.titles[strconv.FormatInt(chat.ID, 10)] = chat.Title
		}
	}
}

// describe returns the script-facing key, title and kind for a dialog peer.
func (s *Service) describe(users []tg.UserClass, chats []tg.ChatClass, p tg.PeerClass) (string, string, string) {
	id := peerID(p)
	key := strconv.FormatInt(id, 10)
	switch peer := p.(type) {
	case *tg.PeerUser:
		kind := "user"
		for _, u := range users {
			if user, ok := u.(*tg.User); ok && user.ID == peer.UserID {
				title := strings.TrimSpace(user.FirstName + " " + user.LastName)
				if username, ok := user.GetUsername(); ok && username != "" {
					return "@" + username, title, kind
				}
				return key, title, kind
			}
		}
		return key, key, kind
	case *tg.PeerChat:
		return key, s.title(chats, id), "group"
	default:
		return key, s.title(chats, id), "channel"
	}
}

func (s *Service) title(chats []tg.ChatClass, id int64) string {
	for _, c := range chats {
		switch chat := c.(type) {
		case *tg.Chat:
			if chat.ID == id {
				return chat.Title
			}
		case *tg.Channel:
			if chat.ID == id {
				return chat.Title
			}
		}
	}
	return strconv.FormatInt(id, 10)
}

// resolvePeer maps a chat argument (numeric id, @username, or title substring)
// to an InputPeer, refreshing the dialog cache when necessary.
func (s *Service) resolvePeer(ctx context.Context, api *tg.Client, chat string) (tg.InputPeerClass, error) {
	if chat == "" {
		return nil, fmt.Errorf("chat is required")
	}
	key := strings.ToLower(strings.TrimSpace(chat))
	s.mu.Lock()
	if peer, ok := s.peers[key]; ok {
		s.mu.Unlock()
		return peer, nil
	}
	// Title substring match on cached dialog titles.
	var matches []string
	for id, title := range s.titles {
		if strings.Contains(strings.ToLower(title), key) {
			matches = append(matches, id)
		}
	}
	if len(matches) == 1 {
		peer := s.peers[matches[0]]
		s.mu.Unlock()
		return peer, nil
	}
	if len(matches) > 1 {
		sort.Strings(matches)
		var names []string
		for _, id := range matches {
			names = append(names, s.titles[id])
		}
		s.mu.Unlock()
		return nil, fmt.Errorf("chat %q is ambiguous: matches %s", chat, strings.Join(names, ", "))
	}
	s.mu.Unlock()

	// Refresh the cache once, then retry.
	if _, err := s.fnDialogs(ctx, map[string]any{"limit": float64(200)}); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if peer, ok := s.peers[key]; ok {
		return peer, nil
	}
	for id, title := range s.titles {
		if strings.Contains(strings.ToLower(title), key) {
			return s.peers[id], nil
		}
	}
	return nil, fmt.Errorf("chat %q not found among your dialogs", chat)
}

// authenticator performs first-run login: the phone comes from config, the
// login code and 2FA password are read from the terminal.
type authenticator struct{ cfg config.Config }

func (a authenticator) Phone(context.Context) (string, error) { return a.cfg.TelegramPhone, nil }

func (a authenticator) Password(context.Context) (string, error) {
	if a.cfg.TelegramPassword != "" {
		return a.cfg.TelegramPassword, nil
	}
	fmt.Fprint(os.Stderr, "memode telegram: two-factor password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line), err
}

func (a authenticator) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return nil
}

func (a authenticator) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("memode telegram: no account to sign up")
}

func (a authenticator) Code(_ context.Context, _ *tg.AuthSentCode) (string, error) {
	fmt.Fprint(os.Stderr, "memode telegram: login code sent to your Telegram app: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func strArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func argInt(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func argIDs(v any) ([]int, error) {
	switch t := v.(type) {
	case float64:
		return []int{int(t)}, nil
	case []any:
		var out []int
		for _, item := range t {
			if n, ok := item.(float64); ok {
				out = append(out, int(n))
			}
		}
		return out, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("message_ids must be a number or a list of numbers")
	}
}
