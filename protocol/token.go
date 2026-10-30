package protocol

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	TokenPrefix           = "bdc_"
	TokenVersion          = 1
	MaxTokenLength        = 1024
	MaxTokenPayloadLength = 700
	MaxTokenIDLength      = 64
	tokenSigningContext   = "brain-connector-token:v1\n"
)

var (
	ErrTokenMalformed = errors.New("connector token: malformed")
	ErrTokenVersion   = errors.New("connector token: unsupported version")
)

var strictRawURL = base64.RawURLEncoding.Strict()

type TokenClaims struct {
	KeyID        string
	ConnectionID string
	WorkspaceID  string
	Nonce        string
}

type Token struct {
	TokenClaims
	EncodedPayload string
	Signature      []byte
}

func SigningInput(encodedPayload string) []byte {
	return []byte(tokenSigningContext + encodedPayload)
}

func (t Token) SigningInput() []byte {
	return SigningInput(t.EncodedPayload)
}

func ParseToken(token string) (Token, error) {
	if len(token) > MaxTokenLength {
		return Token{}, ErrTokenMalformed
	}
	body, found := strings.CutPrefix(token, TokenPrefix)
	if !found || strings.Count(body, ".") != 1 {
		return Token{}, ErrTokenMalformed
	}
	payload, encodedSignature, _ := strings.Cut(body, ".")
	if payload == "" || len(payload) > MaxTokenPayloadLength {
		return Token{}, ErrTokenMalformed
	}
	rawPayload, err := strictRawURL.DecodeString(payload)
	if err != nil {
		return Token{}, ErrTokenMalformed
	}
	signature, err := strictRawURL.DecodeString(encodedSignature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Token{}, ErrTokenMalformed
	}
	claims, err := decodeClaims(rawPayload)
	if err != nil {
		return Token{}, err
	}
	return Token{TokenClaims: claims, EncodedPayload: payload, Signature: signature}, nil
}

func decodeClaims(raw []byte) (TokenClaims, error) {
	if !utf8.Valid(raw) {
		return TokenClaims{}, ErrTokenMalformed
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if opening, err := decoder.Token(); err != nil || opening != json.Delim('{') {
		return TokenClaims{}, ErrTokenMalformed
	}
	var claims TokenClaims
	var version json.Number
	seen := make(map[string]bool, 5)
	for decoder.More() {
		key, err := decoder.Token()
		name, isString := key.(string)
		if err != nil || !isString || seen[name] {
			return TokenClaims{}, ErrTokenMalformed
		}
		seen[name] = true
		value, err := decoder.Token()
		if err != nil {
			return TokenClaims{}, ErrTokenMalformed
		}
		var ok bool
		switch name {
		case "v":
			version, ok = value.(json.Number)
		case "kid":
			claims.KeyID, ok = value.(string)
		case "cid":
			claims.ConnectionID, ok = value.(string)
		case "ws":
			claims.WorkspaceID, ok = value.(string)
		case "n":
			claims.Nonce, ok = value.(string)
		}
		if !ok {
			return TokenClaims{}, ErrTokenMalformed
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return TokenClaims{}, ErrTokenMalformed
	}
	if _, err := decoder.Token(); err != io.EOF {
		return TokenClaims{}, ErrTokenMalformed
	}
	if len(seen) != 5 || !boundedID(claims.ConnectionID) || !boundedID(claims.WorkspaceID) || claims.KeyID == "" || claims.Nonce == "" {
		return TokenClaims{}, ErrTokenMalformed
	}
	if version != json.Number(strconv.Itoa(TokenVersion)) {
		return TokenClaims{}, ErrTokenVersion
	}
	return claims, nil
}

func boundedID(value string) bool {
	return value != "" && len(value) <= MaxTokenIDLength
}
