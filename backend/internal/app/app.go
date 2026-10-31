package app

import (
	"context"
	"log/slog"

	"github.com/gotsta1/tomas_blank_bot/backend/internal/config"
	"github.com/gotsta1/tomas_blank_bot/backend/internal/lava"
	"github.com/gotsta1/tomas_blank_bot/backend/internal/telegram"
	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	DB       *pgxpool.Pool
	Config   config.Config
	Lava     *lava.Client
	Telegram *telegram.Client
	Log      *slog.Logger
}

func New(db *pgxpool.Pool, c config.Config, log *slog.Logger) *App {
	return &App{db, c, lava.New(c.LavaKey), telegram.New(c.TelegramToken), log}
}
func (a *App) Healthy(ctx context.Context) error { return a.DB.Ping(ctx) }
