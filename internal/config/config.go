package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	LogLevel    string
	UploadDir   string

	WeChatAppID                 string
	WeChatAppSecret             string
	WeChatCallSubscribeTemplateID string
	WeChatCallSubscribePage       string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL: getEnv("DATABASE_URL", "sqlite::memory:"),
		LogLevel:    strings.TrimSpace(getEnv("LOG_LEVEL", "info")),
		UploadDir:   getEnv("UPLOAD_DIR", "./uploads"),

		WeChatAppID:                   strings.TrimSpace(getEnv("WECHAT_APPID", "")),
		WeChatAppSecret:               strings.TrimSpace(getEnv("WECHAT_APPSECRET", "")),
		WeChatCallSubscribeTemplateID: strings.TrimSpace(getEnv("WECHAT_CALL_SUBSCRIBE_TEMPLATE_ID", "")),
		WeChatCallSubscribePage:       strings.TrimSpace(getEnv("WECHAT_CALL_SUBSCRIBE_PAGE", "pages/linkbridge/call/call")),
	}

	if strings.TrimSpace(cfg.HTTPAddr) == "" {
		return Config{}, fmt.Errorf("HTTP_ADDR must not be empty")
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return Config{}, fmt.Errorf("DATABASE_URL must not be empty")
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	v, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return defaultValue
	}
	return v
}
