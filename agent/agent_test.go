package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/s16vc/brain-connector/protocol"
)

const waitLimit = 10 * time.Second

func testToken(t *testing.T) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	payload, err := json.Marshal(map[string]any{"v": 1, "kid": "k1", "cid": "connection-1", "ws": "ws-1", "n": base64.RawURLEncoding.EncodeToString(nonce)})
	if err != nil {
		t.Fatal(err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(privateKey, protocol.SigningInput(encodedPayload))
	return protocol.TokenPrefix + encodedPayload + "." + base64.RawURLEncoding.EncodeToString(signature)
}

type acceptedTunnel struct {
	authorization string
	path          string
	connectedAt   time.Time
	socket        *websocket.Conn
	session       *yamux.Session
}

type fakeBrainData struct {
	url      string
	upgrades atomic.Int64
	tunnels  chan *acceptedTunnel
	mu       sync.Mutex
	sessions []*yamux.Session
}

func startBrainData(t *testing.T) *fakeBrainData {
	t.Helper()
	brain := &fakeBrainData{tunnels: make(chan *acceptedTunnel, 16)}
	server := httptest.NewServer(http.HandlerFunc(brain.serveTunnel))
	t.Cleanup(server.Close)
	t.Cleanup(brain.closeSessions)
	brain.url = "ws" + strings.TrimPrefix(server.URL, "http")
	return brain
}

func (b *fakeBrainData) serveTunnel(w http.ResponseWriter, req *http.Request) {
	b.upgrades.Add(1)
	socket, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	config := yamux.DefaultConfig()
	config.LogOutput = io.Discard
	session, err := yamux.Client(websocket.NetConn(context.Background(), socket, websocket.MessageBinary), config)
	if err != nil {
		socket.CloseNow()
		return
	}
	b.mu.Lock()
	b.sessions = append(b.sessions, session)
	b.mu.Unlock()
	b.tunnels <- &acceptedTunnel{
		authorization: req.Header.Get("Authorization"),
		path:          req.URL.Path,
		connectedAt:   time.Now(),
		socket:        socket,
		session:       session,
	}
	<-session.CloseChan()
}

func (b *fakeBrainData) closeSessions() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, session := range b.sessions {
		_ = session.Close()
	}
}

func (b *fakeBrainData) nextTunnel(t *testing.T) *acceptedTunnel {
	t.Helper()
	select {
	case tunnel := <-b.tunnels:
		return tunnel
	case <-time.After(waitLimit):
		t.Fatal("the connector did not open a tunnel in time")
		return nil
	}
}

func (tunnel *acceptedTunnel) request(t *testing.T, address string) (net.Conn, protocol.Status, string) {
	t.Helper()
	stream, err := tunnel.session.OpenStream()
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	t.Cleanup(func() { stream.Close() })
	_ = stream.SetDeadline(time.Now().Add(waitLimit))
	if err := protocol.WriteRequest(stream, address); err != nil {
		t.Fatalf("write request: %v", err)
	}
	status, message, err := protocol.ReadReply(stream)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	return stream, status, message
}

func startEcho(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String()
}

func closedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func runAgent(t *testing.T, connector *Agent) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- connector.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	})
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func capturedLogger() (*slog.Logger, *lockedBuffer) {
	buffer := &lockedBuffer{}
	return slog.New(slog.NewTextHandler(buffer, nil)), buffer
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func echoes(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "ping" {
		t.Fatalf("echo = %q, %v", reply, err)
	}
}

func TestConnectsWithTheBearerToken(t *testing.T) {
	brain := startBrainData(t)
	token := testToken(t)
	runAgent(t, &Agent{Server: brain.url + "/", Token: token, Target: startEcho(t), AllowInsecure: true})
	tunnel := brain.nextTunnel(t)
	if tunnel.authorization != "Bearer "+token {
		t.Fatalf("authorization = %q", tunnel.authorization)
	}
	if tunnel.path != protocol.Route {
		t.Fatalf("path = %q, want %q", tunnel.path, protocol.Route)
	}
}

func TestDialsOnlyItsTarget(t *testing.T) {
	brain := startBrainData(t)
	echo := startEcho(t)
	var mu sync.Mutex
	var dialed []string
	runAgent(t, &Agent{
		Server:        brain.url,
		Token:         testToken(t),
		Target:        echo,
		AllowInsecure: true,
		DialTarget: func(ctx context.Context, network, address string) (net.Conn, error) {
			mu.Lock()
			dialed = append(dialed, address)
			mu.Unlock()
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		},
	})
	tunnel := brain.nextTunnel(t)

	conn, status, message := tunnel.request(t, echo)
	if status != protocol.StatusConnected || message != "" {
		t.Fatalf("target reply = %d %q", status, message)
	}
	echoes(t, conn)

	_, status, message = tunnel.request(t, "169.254.169.254:80")
	if status != protocol.StatusRefused || !strings.Contains(message, "only reaches "+echo) {
		t.Fatalf("other address reply = %d %q", status, message)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(dialed) != 1 || dialed[0] != echo {
		t.Fatalf("dialed %v, want only %s", dialed, echo)
	}
}

func TestReportsAnUnreachableTarget(t *testing.T) {
	brain := startBrainData(t)
	target := closedAddress(t)
	runAgent(t, &Agent{Server: brain.url, Token: testToken(t), Target: target, AllowInsecure: true})
	_, status, message := brain.nextTunnel(t).request(t, target)
	if status != protocol.StatusDialFailed || !strings.Contains(message, "could not reach "+target) {
		t.Fatalf("reply = %d %q", status, message)
	}
}

func TestReconnectsAfterADrop(t *testing.T) {
	brain := startBrainData(t)
	echo := startEcho(t)
	token := testToken(t)
	runAgent(t, &Agent{Server: brain.url, Token: token, Target: echo, AllowInsecure: true})
	first := brain.nextTunnel(t)
	first.socket.CloseNow()
	second := brain.nextTunnel(t)
	if second.authorization != "Bearer "+token {
		t.Fatalf("reconnect authorization = %q", second.authorization)
	}
	conn, status, _ := second.request(t, echo)
	if status != protocol.StatusConnected {
		t.Fatalf("reply after the reconnect = %d", status)
	}
	echoes(t, conn)
}

func TestWaitsWhenReplacedByAnotherConnector(t *testing.T) {
	brain := startBrainData(t)
	logger, logs := capturedLogger()
	replacedRetry := 2 * time.Second
	runAgent(t, &Agent{Server: brain.url, Token: testToken(t), Target: startEcho(t), AllowInsecure: true, Logger: logger, ReplacedRetry: replacedRetry})
	first := brain.nextTunnel(t)
	replacedAt := time.Now()
	_ = first.socket.Close(protocol.ReplacedCloseCode, protocol.ReplacedReason)
	eventually(t, func() bool { return strings.Contains(logs.String(), protocol.ReplacedReason) })
	second := brain.nextTunnel(t)
	if waited := second.connectedAt.Sub(replacedAt); waited < replacedRetry {
		t.Fatalf("reconnected %s after being replaced, want at least %s", waited, replacedRetry)
	}
}

func TestAReplacedConnectorWaitsFiveMinutesByDefault(t *testing.T) {
	if got := (&Agent{}).retryAfterReplaced(); got != 5*time.Minute {
		t.Fatalf("retry after replaced = %s", got)
	}
	watcher := &closeWatcher{}
	replacedErr := fmt.Errorf("failed to get reader: %w", websocket.CloseError{Code: protocol.ReplacedCloseCode, Reason: protocol.ReplacedReason})
	watcher.readErr.Store(&replacedErr)
	if !watcher.replaced() {
		t.Fatal("close code 4001 not recognised")
	}
	goingAway := error(websocket.CloseError{Code: websocket.StatusGoingAway})
	watcher = &closeWatcher{}
	watcher.readErr.Store(&goingAway)
	if watcher.replaced() {
		t.Fatal("a going-away close read as replaced")
	}
}

func TestRefusesAMalformedTokenBeforeDialling(t *testing.T) {
	brain := startBrainData(t)
	valid := testToken(t)
	malformed := map[string]string{
		"empty":               "",
		"not a token":         "x",
		"legacy random token": protocol.TokenPrefix + strings.Repeat("A", 43),
		"two dots":            valid + ".x",
		"no prefix":           strings.TrimPrefix(valid, protocol.TokenPrefix),
	}
	for name, token := range malformed {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
			defer cancel()
			err := (&Agent{Server: brain.url, Token: token, Target: "db.internal:5432", AllowInsecure: true}).Run(ctx)
			if !errors.Is(err, protocol.ErrTokenMalformed) {
				t.Fatalf("run error = %v", err)
			}
		})
	}
	if upgrades := brain.upgrades.Load(); upgrades != 0 {
		t.Fatalf("a malformed token reached BrainData %d times", upgrades)
	}
}

func TestValidate(t *testing.T) {
	token := testToken(t)
	refused := map[string]Agent{
		"bad token":   {Server: DefaultServer, Token: "x", Target: "db:5432"},
		"bad target":  {Server: DefaultServer, Token: token, Target: "db"},
		"plain ws":    {Server: "ws://data.team-brain.com", Token: token, Target: "db:5432"},
		"http scheme": {Server: "https://data.team-brain.com", Token: token, Target: "db:5432"},
		"not a url":   {Server: "::", Token: token, Target: "db:5432"},
	}
	for name, connector := range refused {
		if err := connector.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	accepted := map[string]Agent{
		"default server": {Server: DefaultServer, Token: token, Target: "db.internal:5432"},
		"insecure":       {Server: "ws://127.0.0.1:8090", Token: token, Target: "db:5432", AllowInsecure: true},
	}
	for name, connector := range accepted {
		if err := connector.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
