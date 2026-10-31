package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/gotsta1/tomas_blank_bot/backend/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, e := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal(e)
	}
	defer p.Close()
	if e = migrations.Run(ctx, p); e != nil {
		log.Fatal(e)
	}
}
