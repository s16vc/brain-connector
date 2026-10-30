package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/s16vc/brain-connector/protocol"
)

const (
	DefaultServer        = "wss://data.team-brain.com"
	DefaultReplacedRetry = 5 * time.Minute
	initialBackoff       = time.Second
	maximumBackoff       = time.Minute
	stableSession        = time.Minute
	targetDialLimit      = 10 * time.Second
	pipeLinger           = 5 * time.Second
	writeTimeout         = 30 * time.Second
	refusalBodyLimit     = 1024
)

var errReplaced = errors.New(protocol.ReplacedReason)

type Agent struct {
	Server        string
	Token         string
	Target        string
	AllowInsecure bool
	Logger        *slog.Logger
	DialTarget    func(ctx context.Context, network, address string) (net.Conn, error)
	ReplacedRetry time.Duration
}

func (a *Agent) Validate() error {
	if _, err := protocol.ParseToken(a.Token); err != nil {
		return fmt.Errorf("--token is not a connector token issued by Brain: %w", err)
	}
	if err := validTarget(a.Target); err != nil {
		return fmt.Errorf("--target must be host:port: %w", err)
	}
	endpoint, err := url.Parse(a.Server)
	if err != nil {
		return fmt.Errorf("--server is not a URL: %w", err)
	}
	switch endpoint.Scheme {
	case "wss":
	case "ws":
		if !a.AllowInsecure {
			return errors.New("--server must use wss://; ws:// is allowed only with --insecure")
		}
	default:
		return errors.New("--server must be a wss:// URL")
	}
	return nil
}

func validTarget(target string) error {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	if host == "" {
		return errors.New("the host is empty")
	}
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("the port %q is not a number from 1 to 65535", port)
	}
	return nil
}

func (a *Agent) Run(ctx context.Context) error {
	if err := a.Validate(); err != nil {
		return err
	}
	backoff := initialBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := a.serve(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(started) >= stableSession {
			backoff = initialBackoff
		}
		wait := backoff/2 + rand.N(backoff/2+1)
		if errors.Is(err, errReplaced) {
			wait = a.retryAfterReplaced()
			a.logger().Warn(protocol.ReplacedReason, slog.Duration("retry_in", wait))
		} else {
			a.logger().Warn("disconnected from BrainData", slog.String("error", err.Error()), slog.Duration("retry_in", wait))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		backoff = min(backoff*2, maximumBackoff)
	}
	return nil
}

func (a *Agent) logger() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.New(slog.DiscardHandler)
}

func (a *Agent) retryAfterReplaced() time.Duration {
	if a.ReplacedRetry > 0 {
		return a.ReplacedRetry
	}
	return DefaultReplacedRetry
}

func (a *Agent) serve(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, protocol.HandshakeTimeout)
	socket, response, err := websocket.Dial(dialCtx, strings.TrimRight(a.Server, "/")+protocol.Route, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + a.Token}},
	})
	cancel()
	if err != nil {
		if response != nil {
			return refusal(response)
		}
		return err
	}
	var streams sync.WaitGroup
	defer streams.Wait()
	sessionCtx, stop := context.WithCancel(ctx)
	defer stop()
	stream := &closeWatcher{Conn: websocket.NetConn(sessionCtx, socket, websocket.MessageBinary)}
	session, err := yamux.Server(stream, sessionConfig())
	if err != nil {
		_ = stream.Close()
		return err
	}
	defer session.Close()
	go func() {
		<-sessionCtx.Done()
		_ = session.Close()
	}()
	a.logger().Info("connected to BrainData", slog.String("target", a.Target))
	for {
		accepted, err := session.AcceptStream()
		if err != nil {
			if stream.replaced() {
				return errReplaced
			}
			return err
		}
		streams.Go(func() { a.handle(sessionCtx, accepted) })
	}
}

func refusal(response *http.Response) error {
	var answer struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if response.Body != nil {
		_ = json.NewDecoder(io.LimitReader(response.Body, refusalBodyLimit)).Decode(&answer)
	}
	if answer.Error.Message == "" {
		return fmt.Errorf("BrainData refused the tunnel with status %d", response.StatusCode)
	}
	return fmt.Errorf("BrainData refused the tunnel with status %d: %s", response.StatusCode, answer.Error.Message)
}

func sessionConfig() *yamux.Config {
	config := yamux.DefaultConfig()
	config.KeepAliveInterval = protocol.KeepAliveInterval
	config.ConnectionWriteTimeout = writeTimeout
	config.LogOutput = io.Discard
	return config
}

type closeWatcher struct {
	net.Conn
	readErr atomic.Pointer[error]
}

func (c *closeWatcher) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		c.readErr.CompareAndSwap(nil, &err)
	}
	return n, err
}

func (c *closeWatcher) replaced() bool {
	err := c.readErr.Load()
	return err != nil && websocket.CloseStatus(*err) == protocol.ReplacedCloseCode
}

func (a *Agent) handle(ctx context.Context, stream *yamux.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(protocol.HandshakeTimeout))
	requested, err := protocol.ReadRequest(stream)
	if err != nil {
		return
	}
	if !protocol.SameAddress(requested, a.Target) {
		a.logger().Warn("refused a connection to another address", slog.String("requested", requested))
		_ = protocol.WriteReply(stream, protocol.StatusRefused, fmt.Sprintf("this connector only reaches %s, not %s", a.Target, requested))
		return
	}
	dial := a.DialTarget
	if dial == nil {
		var dialer net.Dialer
		dial = dialer.DialContext
	}
	dialCtx, cancel := context.WithTimeout(ctx, targetDialLimit)
	target, err := dial(dialCtx, "tcp", a.Target)
	cancel()
	if err != nil {
		_ = protocol.WriteReply(stream, protocol.StatusDialFailed, fmt.Sprintf("the connector could not reach %s: %v", a.Target, err))
		return
	}
	defer target.Close()
	if err := protocol.WriteReply(stream, protocol.StatusConnected, ""); err != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	pipe(stream, target)
}

func pipe(left, right net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(left, right)
		closeWrite(left)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(right, left)
		closeWrite(right)
		done <- struct{}{}
	}()
	<-done
	linger := time.AfterFunc(pipeLinger, func() {
		_ = left.Close()
		_ = right.Close()
	})
	<-done
	linger.Stop()
}

func closeWrite(conn net.Conn) {
	if halfCloser, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = halfCloser.CloseWrite()
		return
	}
	_ = conn.Close()
}
