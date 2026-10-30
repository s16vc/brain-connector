package protocol_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/s16vc/brain-connector/protocol"
)

var rawURL = base64.RawURLEncoding

func signedToken(t *testing.T, privateKey ed25519.PrivateKey, encodedPayload string) string {
	t.Helper()
	signature := ed25519.Sign(privateKey, protocol.SigningInput(encodedPayload))
	return protocol.TokenPrefix + encodedPayload + "." + rawURL.EncodeToString(signature)
}

func encodedJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return rawURL.EncodeToString(raw)
}

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey
}

func claims(overrides map[string]any) map[string]any {
	payload := map[string]any{"v": 1, "kid": "k1", "cid": "6f2c1d2e-0000-4000-8000-000000000001", "ws": "ws-1", "n": strings.Repeat("A", 43)}
	for key, value := range overrides {
		if value == nil {
			delete(payload, key)
			continue
		}
		payload[key] = value
	}
	return payload
}

func TestParseTokenReadsABrainToken(t *testing.T) {
	privateKey := newKey(t)
	encodedPayload := encodedJSON(t, claims(nil))
	token := signedToken(t, privateKey, encodedPayload)
	parsed, err := protocol.ParseToken(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.KeyID != "k1" || parsed.ConnectionID != "6f2c1d2e-0000-4000-8000-000000000001" || parsed.WorkspaceID != "ws-1" || parsed.Nonce != strings.Repeat("A", 43) {
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
	privateKey := newKey(t)
	sign := func(payload map[string]any) string { return signedToken(t, privateKey, encodedJSON(t, payload)) }
	signRaw := func(payload string) string { return signedToken(t, privateKey, rawURL.EncodeToString([]byte(payload))) }
	valid := sign(claims(nil))
	cases := []struct {
		name  string
		token string
		want  error
	}{
		{"missing dot", strings.Replace(valid, ".", "", 1), protocol.ErrTokenMalformed},
		{"two dots", valid + ".x", protocol.ErrTokenMalformed},
		{"oversize", sign(claims(map[string]any{"n": strings.Repeat("n", 900)})), protocol.ErrTokenMalformed},
		{"payload over 700 characters", sign(claims(map[string]any{"n": strings.Repeat("n", 520)})), protocol.ErrTokenMalformed},
		{"short signature", protocol.TokenPrefix + encodedJSON(t, claims(nil)) + "." + rawURL.EncodeToString(make([]byte, 32)), protocol.ErrTokenMalformed},
		{"padded signature", valid + "=", protocol.ErrTokenMalformed},
		{"version 2", sign(claims(map[string]any{"v": 2})), protocol.ErrTokenVersion},
		{"version as a string", sign(claims(map[string]any{"v": "1"})), protocol.ErrTokenMalformed},
		{"missing nonce", sign(claims(map[string]any{"n": nil})), protocol.ErrTokenMalformed},
		{"empty key id", sign(claims(map[string]any{"kid": ""})), protocol.ErrTokenMalformed},
		{"empty connection id", sign(claims(map[string]any{"cid": ""})), protocol.ErrTokenMalformed},
		{"connection id over 64", sign(claims(map[string]any{"cid": strings.Repeat("c", 65)})), protocol.ErrTokenMalformed},
		{"workspace id over 64", sign(claims(map[string]any{"ws": strings.Repeat("w", 65)})), protocol.ErrTokenMalformed},
		{"unknown field", sign(claims(map[string]any{"admin": true})), protocol.ErrTokenMalformed},
		{"duplicate field", signRaw(`{"v":1,"kid":"k1","cid":"c1","ws":"ws-1","ws":"ws-2","n":"x"}`), protocol.ErrTokenMalformed},
		{"trailing data", signRaw(`{"v":1,"kid":"k1","cid":"c1","ws":"ws-1","n":"x"}{}`), protocol.ErrTokenMalformed},
		{"not an object", signRaw(`[1,"k1"]`), protocol.ErrTokenMalformed},
		{"invalid utf-8", signRaw("{\"v\":1,\"kid\":\"k\xff\",\"cid\":\"c1\",\"ws\":\"ws-1\",\"n\":\"x\"}"), protocol.ErrTokenMalformed},
		{"padded payload", signedToken(t, privateKey, encodedJSON(t, claims(nil))+"="), protocol.ErrTokenMalformed},
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
