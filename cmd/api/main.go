package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"linkbridge-backend/internal/config"
	"linkbridge-backend/internal/httpserver"
	"linkbridge-backend/internal/logging"
	"linkbridge-backend/internal/storage"
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
	wsManager := ws.NewManager(logger, tokenValidator)
	handler := httpserver.NewHandler(logger, store, wsManager, cfg.UploadDir)

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
