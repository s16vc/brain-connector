package tokentest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/s16vc/brain-connector/protocol"
)

var rawURL = base64.RawURLEncoding

func NewKey(t testing.TB) ed25519.PrivateKey {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey
}

func EncodeJSON(t testing.TB, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return rawURL.EncodeToString(raw)
}

func Sign(privateKey ed25519.PrivateKey, encodedPayload string) string {
	signature := ed25519.Sign(privateKey, protocol.SigningInput(encodedPayload))
	return protocol.TokenPrefix + encodedPayload + "." + rawURL.EncodeToString(signature)
}

func Claims(overrides map[string]any) map[string]any {
	claims := map[string]any{"v": 1, "kid": "k1", "cid": "6f2c1d2e-0000-4000-8000-000000000001", "ws": "ws-1", "n": randomNonce()}
	for key, value := range overrides {
		if value == nil {
			delete(claims, key)
			continue
		}
		claims[key] = value
	}
	return claims
}

func Token(t testing.TB) string {
	t.Helper()
	return Sign(NewKey(t), EncodeJSON(t, Claims(nil)))
}

func randomNonce() string {
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	return rawURL.EncodeToString(nonce)
}
