package protocol_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/s16vc/brain-connector/internal/tokentest"
	"github.com/s16vc/brain-connector/protocol"
)

var rawURL = base64.RawURLEncoding

func TestParseTokenReadsABrainToken(t *testing.T) {
	privateKey := tokentest.NewKey(t)
	nonce := strings.Repeat("A", 43)
	encodedPayload := tokentest.EncodeJSON(t, tokentest.Claims(map[string]any{"n": nonce}))
	parsed, err := protocol.ParseToken(tokentest.Sign(privateKey, encodedPayload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.KeyID != "k1" || parsed.ConnectionID != "6f2c1d2e-0000-4000-8000-000000000001" || parsed.WorkspaceID != "ws-1" || parsed.Nonce != nonce {
		t.Fatalf("claims = %+v", parsed.TokenClaims)
	}
	if parsed.EncodedPayload != encodedPayload {
		t.Fatalf("payload = %q", parsed.EncodedPayload)
	}
	if !bytes.Equal(parsed.SigningInput(), []byte("brain-connector-token:v1\n"+encodedPayload)) {
		t.Fatalf("signing input = %q", parsed.SigningInput())
	}
	if !ed25519.Verify(privateKey.Public().(ed25519.PublicKey), parsed.SigningInput(), parsed.Signature) {
		t.Fatal("the parsed signature does not verify over the signing input")
	}
}

func TestParseTokenRefusesMalformedTokens(t *testing.T) {
	privateKey := tokentest.NewKey(t)
	sign := func(overrides map[string]any) string {
		return tokentest.Sign(privateKey, tokentest.EncodeJSON(t, tokentest.Claims(overrides)))
	}
	signRaw := func(payload string) string { return tokentest.Sign(privateKey, rawURL.EncodeToString([]byte(payload))) }
	valid := sign(nil)
	payload, signature, _ := strings.Cut(strings.TrimPrefix(valid, protocol.TokenPrefix), ".")
	cases := []struct {
		name  string
		token string
		want  error
	}{
		{"missing dot", strings.Replace(valid, ".", "", 1), protocol.ErrTokenMalformed},
		{"two dots", valid + ".x", protocol.ErrTokenMalformed},
		{"oversize", sign(map[string]any{"n": strings.Repeat("n", 900)}), protocol.ErrTokenMalformed},
		{"payload over 700 characters", sign(map[string]any{"n": strings.Repeat("n", 520)}), protocol.ErrTokenMalformed},
		{"short signature", protocol.TokenPrefix + payload + "." + rawURL.EncodeToString(make([]byte, 32)), protocol.ErrTokenMalformed},
		{"padded signature", valid + "=", protocol.ErrTokenMalformed},
		{"trailing newline", valid + "\n", protocol.ErrTokenMalformed},
		{"trailing carriage return", valid + "\r\n", protocol.ErrTokenMalformed},
		{"newline inside the payload", protocol.TokenPrefix + payload[:10] + "\n" + payload[10:] + "." + signature, protocol.ErrTokenMalformed},
		{"version 2", sign(map[string]any{"v": 2}), protocol.ErrTokenVersion},
		{"version as a string", sign(map[string]any{"v": "1"}), protocol.ErrTokenMalformed},
		{"missing nonce", sign(map[string]any{"n": nil}), protocol.ErrTokenMalformed},
		{"empty nonce", sign(map[string]any{"n": ""}), protocol.ErrTokenMalformed},
		{"empty key id", sign(map[string]any{"kid": ""}), protocol.ErrTokenMalformed},
		{"key id as a number", sign(map[string]any{"kid": 7}), protocol.ErrTokenMalformed},
		{"empty connection id", sign(map[string]any{"cid": ""}), protocol.ErrTokenMalformed},
		{"connection id over 64", sign(map[string]any{"cid": strings.Repeat("c", 65)}), protocol.ErrTokenMalformed},
		{"empty workspace id", sign(map[string]any{"ws": ""}), protocol.ErrTokenMalformed},
		{"workspace id over 64", sign(map[string]any{"ws": strings.Repeat("w", 65)}), protocol.ErrTokenMalformed},
		{"unknown field", sign(map[string]any{"admin": true}), protocol.ErrTokenMalformed},
		{"duplicate field", signRaw(`{"v":1,"kid":"k1","cid":"c1","ws":"ws-1","ws":"ws-2","n":"x"}`), protocol.ErrTokenMalformed},
		{"trailing data", signRaw(`{"v":1,"kid":"k1","cid":"c1","ws":"ws-1","n":"x"}{}`), protocol.ErrTokenMalformed},
		{"not an object", signRaw(`[1,"k1"]`), protocol.ErrTokenMalformed},
		{"invalid utf-8", signRaw("{\"v\":1,\"kid\":\"k\xff\",\"cid\":\"c1\",\"ws\":\"ws-1\",\"n\":\"x\"}"), protocol.ErrTokenMalformed},
		{"padded payload", tokentest.Sign(privateKey, payload+"="), protocol.ErrTokenMalformed},
		{"no prefix", strings.TrimPrefix(valid, protocol.TokenPrefix), protocol.ErrTokenMalformed},
		{"legacy random token", protocol.TokenPrefix + rawURL.EncodeToString(make([]byte, 32)), protocol.ErrTokenMalformed},
		{"a jwt", "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJ4In0.c2ln", protocol.ErrTokenMalformed},
		{"empty", "", protocol.ErrTokenMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := protocol.ParseToken(tc.token); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
	if len(cases[2].token) <= protocol.MaxTokenLength {
		t.Fatalf("the oversize case is only %d characters", len(cases[2].token))
	}
	if len(cases[3].token) > protocol.MaxTokenLength {
		t.Fatalf("the long payload case is %d characters, over the token limit", len(cases[3].token))
	}
}
