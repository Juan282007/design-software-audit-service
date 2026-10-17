package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/code-sena/design-software-audit-service/internal/domain/model"
)

type cursorCodec struct{ secret []byte }
type cursorPayload struct {
	Version    int    `json:"v"`
	ReceivedAt string `json:"r"`
	ID         string `json:"i"`
}

func (c cursorCodec) Encode(cursor model.AuditCursor) (string, error) {
	b, err := json.Marshal(cursorPayload{Version: 1, ReceivedAt: cursor.ReceivedAt.UTC().Format(time.RFC3339Nano), ID: string(cursor.ID)})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(payload))
	return "v1." + payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (c cursorCodec) Decode(raw string) (model.AuditCursor, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return model.AuditCursor{}, errors.New("invalid cursor")
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return model.AuditCursor{}, errors.New("invalid cursor")
	}
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(parts[1]))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return model.AuditCursor{}, errors.New("invalid cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return model.AuditCursor{}, errors.New("invalid cursor")
	}
	var payload cursorPayload
	if json.Unmarshal(b, &payload) != nil || payload.Version != 1 {
		return model.AuditCursor{}, errors.New("invalid cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, payload.ReceivedAt)
	if err != nil {
		return model.AuditCursor{}, errors.New("invalid cursor")
	}
	id, err := model.ParseUUID(payload.ID)
	if err != nil {
		return model.AuditCursor{}, errors.New("invalid cursor")
	}
	return model.AuditCursor{ReceivedAt: at.UTC(), ID: id}, nil
}
