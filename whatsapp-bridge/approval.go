package main

// Contact approval (prompt-injection defence).
//
// An incoming WhatsApp message is untrusted text. Anything an LLM reads can
// carry instructions. So the user approves each sender once, out of band
// (Telegram bot, see telegram.go), seeing only the sender's name and number,
// never the message. Until then the sender's messages are stored but are not
// forwarded (webhook), not auto-downloaded, and the MCP server hides them.
//
// The allowlist lives in store/allowlist.json as {"approved": ["<id>", ...]}.
// whatsapp-mcp-server/approvals.py reads the same file. Fail closed: a missing
// or corrupt file approves nobody.
//
// Groups: approval is per participant, not per group.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

const allowlistPath = "store/allowlist.json"

// approvalGate is nil only when approval is switched off
// (WHATSAPP_REQUIRE_APPROVAL=false) or in tests that do not exercise it.
var approvalGate *approvalStore

// approvalNotify asks the owner about a new sender; nil means "nobody to ask".
var approvalNotify func(id, label string)

func approvalRequired() bool {
	return getEnvBool("WHATSAPP_REQUIRE_APPROVAL", true)
}

type approvalFile struct {
	Approved []string          `json:"approved"`
	Denied   []string          `json:"denied,omitempty"`
	Pending  map[string]string `json:"pending,omitempty"` // id -> label (sanitised)
}

type approvalStore struct {
	mu       sync.Mutex
	path     string
	approved map[string]bool
	denied   map[string]bool
	pending  map[string]string
}

func newApprovalStore(path string) (*approvalStore, error) {
	s := &approvalStore{path: path, approved: map[string]bool{}, denied: map[string]bool{}, pending: map[string]string{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err // empty store: fail closed
	}
	var f approvalFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return s, fmt.Errorf("corrupt %s: %w", path, err)
	}
	for _, id := range f.Approved {
		s.approved[normalizeID(id)] = true
	}
	for _, id := range f.Denied {
		s.denied[normalizeID(id)] = true
	}
	for id, label := range f.Pending {
		s.pending[normalizeID(id)] = label
	}
	return s, nil
}

// normalizeID reduces a JID or user part to its bare id: "49123:7@s.whatsapp.net" -> "49123".
func normalizeID(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, "@"); i >= 0 {
		v = v[:i]
	}
	if i := strings.Index(v, ":"); i >= 0 {
		v = v[:i]
	}
	return v
}

func isDirectJID(jid string) bool {
	return strings.HasSuffix(jid, "@s.whatsapp.net") || strings.HasSuffix(jid, "@lid")
}

func isGroupJID(jid string) bool { return strings.HasSuffix(jid, "@g.us") }

// identityFor returns the id that must be approved for a message in chatJID
// sent by sender: the contact for direct chats, the participant for groups.
func identityFor(chatJID, sender string) (string, bool) {
	switch {
	case isDirectJID(chatJID):
		return normalizeID(chatJID), true
	case isGroupJID(chatJID):
		id := normalizeID(sender)
		return id, id != ""
	}
	return "", false
}

// Allowed mirrors the filter in approvals.py. Own messages in a group pass;
// in a direct chat they follow the contact's approval.
func (s *approvalStore) Allowed(chatJID, sender string, isFromMe bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if isGroupJID(chatJID) && isFromMe {
		return true
	}
	id, ok := identityFor(chatJID, sender)
	return ok && s.approved[id]
}

// Observe records an unknown sender as pending and calls notify exactly once
// per sender. Approved, denied and already-pending senders are ignored.
func (s *approvalStore) Observe(chatJID, sender, pushName, groupName string, notify func(id, label string)) {
	id, ok := identityFor(chatJID, sender)
	if !ok {
		return
	}
	s.mu.Lock()
	if s.approved[id] || s.denied[id] {
		s.mu.Unlock()
		return
	}
	if _, already := s.pending[id]; already {
		s.mu.Unlock()
		return
	}
	label := approvalLabel(id, pushName, groupName)
	s.pending[id] = label
	_ = s.saveLocked()
	s.mu.Unlock()
	if notify != nil {
		notify(id, label)
	}
}

// IsPending reports whether id was actually requested (and not yet decided).
func (s *approvalStore) IsPending(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pending[normalizeID(id)]
	return ok
}

// Decide records the user's choice and clears the pending request.
func (s *approvalStore) Decide(id string, approve bool) {
	id = normalizeID(id)
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
	if approve {
		s.approved[id] = true
		delete(s.denied, id)
	} else {
		s.denied[id] = true
		delete(s.approved, id)
	}
	_ = s.saveLocked()
}

func (s *approvalStore) saveLocked() error {
	f := approvalFile{Approved: sortedKeys(s.approved), Denied: sortedKeys(s.denied), Pending: s.pending}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// small lists; simple insertion sort keeps the file stable
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// approvalLabel builds the text the user sees when asked to approve. The push
// name and group name are chosen by the sender, so they are treated as hostile:
// stripped of control/format characters, collapsed to one line and truncated.
// The number is always included. The label goes only to the user, never to the
// agent.
func approvalLabel(id, pushName, groupName string) string {
	label := cleanText(pushName, 40)
	if label == "" {
		label = "(kein Name)"
	}
	label += " +" + cleanText(id, 20)
	if g := cleanText(groupName, 30); g != "" {
		label += " in Gruppe \"" + g + "\""
	}
	return label
}

func cleanText(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == ' ', r == ' ':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(out); len(r) > max {
		out = string(r[:max]) + "…"
	}
	return out
}
