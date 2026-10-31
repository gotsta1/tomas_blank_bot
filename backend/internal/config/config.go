package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL, Listen, BaseURL, AdminPasswordHash           string
	TelegramToken, TelegramSecret, LavaKey, LavaSecret        string
	ChannelID                                                 int64
	OfferID, ForeignCurrency, PlanName, PlanPrice, PlanPeriod string
	Worker                                                    bool
	Location                                                  *time.Location
}

func Load() (Config, error) {
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), Listen: env("LISTEN_ADDR", ":8080"), BaseURL: env("BASE_URL", "http://localhost:8080"), AdminPasswordHash: os.Getenv("ADMIN_PASSWORD_HASH"), TelegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"), TelegramSecret: os.Getenv("TELEGRAM_WEBHOOK_SECRET"), LavaKey: os.Getenv("LAVA_API_KEY"), LavaSecret: os.Getenv("LAVA_WEBHOOK_SECRET"), OfferID: os.Getenv("LAVA_OFFER_ID"), ForeignCurrency: env("LAVA_FOREIGN_CURRENCY", "USD"), PlanName: env("PLAN_NAME", "Подписка в клуб"), PlanPrice: os.Getenv("PLAN_PRICE_LABEL"), PlanPeriod: os.Getenv("PLAN_PERIOD_LABEL"), Worker: env("WORKER_ENABLED", "false") == "true"}
	var err error
	c.Location, err = time.LoadLocation(env("DISPLAY_TIMEZONE", "UTC"))
	if err != nil {
		return c, err
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return c, errors.New("invalid BASE_URL")
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.TelegramToken != "" {
		c.ChannelID, err = strconv.ParseInt(os.Getenv("TELEGRAM_CHANNEL_ID"), 10, 64)
		if err != nil || c.ChannelID >= 0 {
			return c, errors.New("negative TELEGRAM_CHANNEL_ID required")
		}
		if strings.IndexFunc(c.TelegramSecret, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
		}) >= 0 {
			return c, errors.New("invalid TELEGRAM_WEBHOOK_SECRET characters")
		}
		if len(c.TelegramSecret) < 32 {
			return c, errors.New("TELEGRAM_WEBHOOK_SECRET must be at least 32 characters")
		}
	}
	if c.LavaKey != "" && len(c.LavaSecret) < 32 {
		return c, errors.New("LAVA_WEBHOOK_SECRET must be at least 32 characters")
	}
	if c.ForeignCurrency != "USD" && c.ForeignCurrency != "EUR" {
		return c, errors.New("foreign currency must be USD or EUR")
	}
	return c, nil
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
