package main

// Telegram approval bot.
//
// Sends the owner one message per unknown sender (name and number only, never
// message content) with Approve / Deny buttons, and long-polls for the answer.
//
// Security properties:
//   - Only a callback whose sender AND chat both equal TELEGRAM_OWNER_CHAT_ID
//     is honoured.
//   - Only ids that are currently pending can be decided, so a forged
//     callback_data cannot approve an arbitrary number.
//   - Redirects are never followed; the bot token stays in the URL path of
//     api.telegram.org only.
//
// Data leaving the machine: the sender's display name and number go to Telegram.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type telegramApprover struct {
	token   string
	ownerID int64
	store   *approvalStore
	baseURL string
	client  *http.Client
	offset  int64
}

func newTelegramApprover(token string, ownerID int64, store *approvalStore) *telegramApprover {
	return &telegramApprover{
		token:   token,
		ownerID: ownerID,
		store:   store,
		baseURL: "https://api.telegram.org",
		client: &http.Client{
			Timeout:       45 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// telegramFromEnv returns nil when the bot is not configured.
func telegramFromEnv(store *approvalStore) *telegramApprover {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	owner, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("TELEGRAM_OWNER_CHAT_ID")), 10, 64)
	if token == "" || err != nil || owner == 0 {
		return nil
	}
	return newTelegramApprover(token, owner, store)
}

func (t *telegramApprover) call(method string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/bot%s/%s", t.baseURL, t.token, method)
	resp, err := t.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		// Never print err: its text contains the URL and therefore the token.
		return nil, fmt.Errorf("telegram %s request failed", method)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("telegram %s: read failed", method)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
	}
	return data, nil
}

// Prompt asks the owner to approve id. label must come from approvalLabel.
func (t *telegramApprover) Prompt(id, label string) {
	text := "Neue WhatsApp-Nachricht von:\n" + label + "\n\nDer Inhalt wird absichtlich nicht angezeigt. Freigeben?"
	_, err := t.call("sendMessage", map[string]any{
		"chat_id": t.ownerID,
		"text":    text,
		"reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{
			{"text": "✅ Freigeben", "callback_data": "ok:" + id},
			{"text": "🚫 Ablehnen", "callback_data": "no:" + id},
		}}},
	})
	if err != nil {
		fmt.Printf("Approval prompt for %s not delivered: %v\n", id, err)
	}
}

type tgUpdates struct {
	OK     bool `json:"ok"`
	Result []struct {
		UpdateID      int64 `json:"update_id"`
		CallbackQuery *struct {
			ID   string `json:"id"`
			Data string `json:"data"`
			From struct {
				ID int64 `json:"id"`
			} `json:"from"`
			Message *struct {
				MessageID int64 `json:"message_id"`
				Chat      struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"callback_query"`
	} `json:"result"`
}

// pollOnce fetches pending updates and applies the owner's decisions.
func (t *telegramApprover) pollOnce() {
	raw, err := t.call("getUpdates", map[string]any{
		"offset": t.offset, "timeout": 25, "allowed_updates": []string{"callback_query"},
	})
	if err != nil {
		time.Sleep(5 * time.Second)
		return
	}
	var up tgUpdates
	if json.Unmarshal(raw, &up) != nil || !up.OK {
		return
	}
	for _, u := range up.Result {
		if u.UpdateID >= t.offset {
			t.offset = u.UpdateID + 1
		}
		cq := u.CallbackQuery
		if cq == nil || cq.Message == nil || cq.From.ID != t.ownerID || cq.Message.Chat.ID != t.ownerID {
			continue
		}
		action, id, ok := strings.Cut(cq.Data, ":")
		if !ok || (action != "ok" && action != "no") || !t.store.IsPending(id) {
			continue
		}
		t.store.Decide(id, action == "ok")
		verdict := "🚫 Abgelehnt"
		if action == "ok" {
			verdict = "✅ Freigegeben"
		}
		_, _ = t.call("answerCallbackQuery", map[string]any{"callback_query_id": cq.ID, "text": verdict})
		_, _ = t.call("editMessageReplyMarkup", map[string]any{
			"chat_id": t.ownerID, "message_id": cq.Message.MessageID,
			"reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{{"text": verdict + " (" + id + ")", "callback_data": "done"}}}},
		})
	}
}

// Run polls until the process exits.
func (t *telegramApprover) Run() {
	for {
		t.pollOnce()
	}
}
