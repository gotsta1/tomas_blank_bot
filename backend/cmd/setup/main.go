// Setup explicitly registers Telegram commands, permissions and webhook.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/gotsta1/tomas_blank_bot/backend/internal/config"
	"github.com/gotsta1/tomas_blank_bot/backend/internal/telegram"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		password := os.Getenv("ADMIN_PASSWORD")
		if len(password) < 12 || len(password) > 72 {
			log.Fatal("ADMIN_PASSWORD must be 12–72 bytes")
		}
		hash, e := bcrypt.GenerateFromPassword([]byte(password), 12)
		if e != nil {
			log.Fatal(e)
		}
		fmt.Println(string(hash))
		return
	}
	if e := run(); e != nil {
		log.Fatal(e)
	}
}
func run() error {
	c, e := config.Load()
	if e != nil {
		return e
	}
	if len(c.BaseURL) < 8 || c.BaseURL[:8] != "https://" {
		return fmt.Errorf("HTTPS BASE_URL is required for Telegram")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, e := pgxpool.New(ctx, c.DatabaseURL)
	if e != nil {
		return e
	}
	defer db.Close()
	tg := telegram.New(c.TelegramToken)
	var me telegram.User
	if e = tg.Call(ctx, "getMe", map[string]any{}, &me); e != nil {
		return fmt.Errorf("get bot identity: %w", e)
	}
	var member struct {
		Status   string `json:"status"`
		Invite   bool   `json:"can_invite_users"`
		Restrict bool   `json:"can_restrict_members"`
	}
	if e = tg.Call(ctx, "getChatMember", map[string]any{"chat_id": c.ChannelID, "user_id": me.ID}, &member); e != nil {
		return fmt.Errorf("check bot channel permissions: %w", e)
	}
	if member.Status != "administrator" || !member.Invite || !member.Restrict {
		return fmt.Errorf("bot needs administrator, can_invite_users and can_restrict_members permissions")
	}
	var existing string
	e = db.QueryRow(ctx, "SELECT value FROM runtime_settings WHERE key='invite_url'").Scan(&existing)
	if e != nil || existing == "" {
		var invite struct {
			URL string `json:"invite_link"`
		}
		if e = tg.Call(ctx, "createChatInviteLink", map[string]any{"chat_id": c.ChannelID, "creates_join_request": true, "name": "Subscription access"}, &invite); e != nil {
			return fmt.Errorf("create channel join-request link: %w", e)
		}
		if _, e = db.Exec(ctx, "INSERT INTO runtime_settings(key,value) VALUES('invite_url',$1) ON CONFLICT(key) DO UPDATE SET value=excluded.value", invite.URL); e != nil {
			return e
		}
	}
	commands := []map[string]string{}
	for _, key := range []string{"start", "buy", "status", "help"} {
		var description string
		if e = db.QueryRow(ctx, "SELECT text FROM bot_messages WHERE key=$1", "command_"+key).Scan(&description); e != nil {
			return e
		}
		commands = append(commands, map[string]string{"command": key, "description": description})
	}
	if e = tg.Call(ctx, "setMyCommands", map[string]any{"commands": commands}, nil); e != nil {
		return fmt.Errorf("register bot commands: %w", e)
	}
	if e = tg.Call(ctx, "setWebhook", map[string]any{"url": c.BaseURL + "/webhooks/telegram", "secret_token": c.TelegramSecret, "allowed_updates": []string{"message", "callback_query", "chat_join_request"}}, nil); e != nil {
		return fmt.Errorf("register Telegram webhook: %w", e)
	}
	fmt.Println("Telegram permissions verified; commands, invite link and webhook configured.")
	return nil
}
