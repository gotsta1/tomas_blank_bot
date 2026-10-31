package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gotsta1/tomas_blank_bot/backend/internal/app"
	"github.com/gotsta1/tomas_blank_bot/backend/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	c, e := config.Load()
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pc, e := pgxpool.ParseConfig(c.DatabaseURL)
	if e != nil {
		return e
	}
	pc.MaxConns = 15
	pc.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		return e
	}
	defer pool.Close()
	ping, cancel := context.WithTimeout(ctx, 10*time.Second)
	e = pool.Ping(ping)
	cancel()
	if e != nil {
		return e
	}
	a := app.New(pool, c, log)
	workerDone := make(chan struct{})
	if c.Worker {
		go func() { defer close(workerDone); a.RunWorker(ctx) }()
	} else {
		close(workerDone)
	}
	srv := &http.Server{Addr: c.Listen, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 90 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	errc := make(chan error, 1)
	go func() { log.Info("http server listening", "address", c.Listen); errc <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case e = <-errc:
		stop()
		if !errors.Is(e, http.ErrServerClosed) {
			return e
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	e = srv.Shutdown(shutdown)
	if e != nil {
		_ = srv.Close()
	}
	<-workerDone
	return e
}
