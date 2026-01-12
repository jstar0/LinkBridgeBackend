package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"linkbridge-backend/internal/config"
	"linkbridge-backend/internal/httpserver"
	"linkbridge-backend/internal/logging"
	"linkbridge-backend/internal/storage"
	"linkbridge-backend/internal/wechat"
	"linkbridge-backend/internal/ws"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		_, _ = os.Stderr.WriteString("config error: " + err.Error() + "\n")
		os.Exit(1)
	}

	logger, err := logging.New(cfg.LogLevel)
	if err != nil {
		_, _ = os.Stderr.WriteString("log init error: " + err.Error() + "\n")
		os.Exit(1)
	}

	logger.Info("starting", "httpAddr", cfg.HTTPAddr, "database", storage.RedactedDatabaseURL(cfg.DatabaseURL))

	store, err := storage.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}

	tokenValidator := &storeTokenValidator{store: store}
	callStore := &storeCallStore{store: store}
	wsManager := ws.NewManager(logger, tokenValidator, callStore)
	go runBurnMessageSweeper(ctx, logger, store, wsManager)
	go runActivityReminderSweeper(ctx, logger, store, cfg.WeChatAppID, cfg.WeChatAppSecret, cfg.WeChatActivitySubscribeTemplateID, cfg.WeChatActivitySubscribePage)
	handler := httpserver.NewHandler(logger, store, wsManager, cfg.UploadDir, httpserver.HandlerOptions{
		WeChatAppID:                       cfg.WeChatAppID,
		WeChatAppSecret:                   cfg.WeChatAppSecret,
		WeChatCallSubscribeTemplateID:     cfg.WeChatCallSubscribeTemplateID,
		WeChatCallSubscribePage:           cfg.WeChatCallSubscribePage,
		WeChatActivitySubscribeTemplateID: cfg.WeChatActivitySubscribeTemplateID,
		WeChatActivitySubscribePage:       cfg.WeChatActivitySubscribePage,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ErrorLog:          logging.StdLogger(logger),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	logger.Info("listening", "httpAddr", cfg.HTTPAddr)

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "error", err)
			os.Exit(1)
		}
	}

	wsManager.CloseAll()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "error", err)
	}

	if err := store.Close(); err != nil {
		logger.Error("db close error", "error", err)
	}

	logger.Info("stopped")
}

func runBurnMessageSweeper(ctx context.Context, logger *slog.Logger, store *storage.Store, wsManager *ws.Manager) {
	if store == nil || wsManager == nil {
		return
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nowMs := time.Now().UnixMilli()
			due, err := store.ExpireBurnMessages(ctx, nowMs, 200)
			if err != nil {
				logger.Warn("expire burn messages failed", "error", err)
				continue
			}
			for _, row := range due {
				wsManager.SendToUsers([]string{row.SenderID, row.RecipientID}, ws.Envelope{
					Type:      "message.burn.deleted",
					SessionID: row.SessionID,
					Payload: map[string]any{
						"messageId": row.MessageID,
					},
				})
			}
		}
	}
}

func runActivityReminderSweeper(ctx context.Context, logger *slog.Logger, store *storage.Store, appID, appSecret, templateID, page string) {
	if store == nil || logger == nil {
		return
	}

	appID = strings.TrimSpace(appID)
	appSecret = strings.TrimSpace(appSecret)
	templateID = strings.TrimSpace(templateID)
	page = strings.TrimSpace(page)
	if appID == "" || appSecret == "" || templateID == "" {
		return
	}
	if page == "" {
		page = "pages/chat/index"
	}

	wechatClient := wechat.NewClient(logger, appID, appSecret)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nowMs := time.Now().UnixMilli()
			due, err := store.ListDueActivityReminders(ctx, nowMs, 50)
			if err != nil {
				logger.Warn("list due activity reminders failed", "error", err)
				continue
			}
			if len(due) == 0 {
				continue
			}

			accessToken, err := wechatClient.GetAccessToken(ctx)
			if err != nil {
				logger.Warn("wechat get access token failed (activity reminder)", "error", err)
				continue
			}

			for _, r := range due {
				// Best-effort: one attempt per reminder; failures can be retried by re-subscribing.
				binding, err := store.GetWeChatBindingByUserID(ctx, r.UserID)
				if err != nil {
					_ = store.MarkActivityReminderFailed(ctx, r.ActivityID, r.UserID, "wechat binding not found", nowMs)
					continue
				}

				activity, err := store.GetActivityByID(ctx, r.ActivityID)
				if err != nil {
					_ = store.MarkActivityReminderFailed(ctx, r.ActivityID, r.UserID, "activity not found", nowMs)
					continue
				}

				caller, err := store.GetUserByID(ctx, activity.CreatorID)
				if err != nil {
					_ = store.MarkActivityReminderFailed(ctx, r.ActivityID, r.UserID, "creator not found", nowMs)
					continue
				}

				startAtMs := r.RemindAtMs
				if activity.StartAtMs != nil && *activity.StartAtMs > 0 {
					startAtMs = *activity.StartAtMs
				}
				startAtText := time.UnixMilli(startAtMs).Format("2006-01-02 15:04:05")

				title := strings.TrimSpace(activity.Title)
				if title == "" {
					title = "活动"
				}
				creatorName := strings.TrimSpace(caller.DisplayName)
				if creatorName == "" {
					creatorName = "发起者"
				}

				content := fmt.Sprintf("%s 即将开始，点击进入活动群聊", title)

				// Default deep link goes directly to the group chat session (more useful than the creator page).
				targetPage := page
				sep := "?"
				if strings.Contains(targetPage, "?") {
					sep = "&"
				}
				targetPage = fmt.Sprintf(
					"%s%ssessionId=%s&peerName=%s",
					targetPage,
					sep,
					url.QueryEscape(activity.SessionID),
					url.QueryEscape(title),
				)

				data := map[string]any{
					"time2":  map[string]any{"value": startAtText},
					"thing4": map[string]any{"value": title},
					"thing5": map[string]any{"value": creatorName},
					"thing6": map[string]any{"value": content},
				}

				err = wechatClient.SendSubscribeMessage(ctx, accessToken, wechat.SubscribeSendRequest{
					ToUser:     binding.OpenID,
					TemplateID: templateID,
					Page:       targetPage,
					Data:       data,
				})
				if err != nil {
					logger.Warn("wechat activity reminder send failed", "error", err)
					_ = store.MarkActivityReminderFailed(ctx, r.ActivityID, r.UserID, err.Error(), nowMs)
					continue
				}

				_ = store.MarkActivityReminderSent(ctx, r.ActivityID, r.UserID, nowMs)
			}
		}
	}
}

type storeTokenValidator struct {
	store *storage.Store
}

func (v *storeTokenValidator) ValidateToken(ctx context.Context, token string) (string, error) {
	nowMs := time.Now().UnixMilli()
	authToken, err := v.store.ValidateToken(ctx, token, nowMs)
	if err != nil {
		return "", err
	}
	return authToken.UserID, nil
}

type storeCallStore struct {
	store *storage.Store
}

func (s *storeCallStore) GetCallByID(ctx context.Context, callID string) (callerID, calleeID, status string, err error) {
	call, err := s.store.GetCallByID(ctx, callID)
	if err != nil {
		return "", "", "", err
	}
	return call.CallerID, call.CalleeID, call.Status, nil
}
