package cache

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// ValkeyProvider implements Provider backed by a Valkey/Redis-compatible server.
type ValkeyProvider struct {
	cfg ValkeyConfig
}

// ValkeyConfig holds connection parameters for the Valkey cluster.
type ValkeyConfig struct {
	Addr         string
	Username     string
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	MaxRetries   int
	TLS          bool
}

// NewValkeyProvider creates a Provider using the supplied configuration. It performs a ping
// against the target to fail fast when credentials or connectivity are incorrect.
func NewValkeyProvider(cfg ValkeyConfig) (*ValkeyProvider, error) {
	if cfg.Addr == "" {
		return nil, errors.New("valkey addr is required")
	}

	normaliseDurations(&cfg)
	provider := &ValkeyProvider{cfg: cfg}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.DialTimeout)
	defer cancel()
	if err := provider.ping(ctx); err != nil {
		return nil, err
	}

	return provider, nil
}

// Get fetches bytes by key, returning ErrCacheMiss when the key is absent.
func (p *ValkeyProvider) Get(ctx context.Context, key string) ([]byte, error) {
	var payload []byte
	err := p.withConn(ctx, func(vc *valkeyConn) error {
		if err := vc.writeCommand("GET", []byte(key)); err != nil {
			return err
		}

		reply, err := vc.readReply()
		if err != nil {
			return err
		}

		switch reply.typ {
		case replyNil:
			return ErrCacheMiss
		case replyBulkString:
			payload = reply.data
			return nil
		default:
			return fmt.Errorf("unexpected valkey reply type %q for GET", reply.typ)
		}
	})
	return payload, err
}

// Set stores bytes with the provided TTL.
func (p *ValkeyProvider) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return p.withConn(ctx, func(vc *valkeyConn) error {
		args := [][]byte{[]byte(key), value}
		if ttl > 0 {
			ms := strconv.FormatInt(ttl.Milliseconds(), 10)
			args = append(args, []byte("PX"), []byte(ms))
		}

		if err := vc.writeCommand("SET", args...); err != nil {
			return err
		}

		reply, err := vc.readReply()
		if err != nil {
			return err
		}
		if reply.typ != replySimpleString || string(reply.data) != "OK" {
			return fmt.Errorf("unexpected SET response: %s", reply.data)
		}
		return nil
	})
}

// SetNX stores the value only if the key does not exist.
func (p *ValkeyProvider) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	var ok bool
	err := p.withConn(ctx, func(vc *valkeyConn) error {
		args := [][]byte{[]byte(key), value}
		if ttl > 0 {
			ms := strconv.FormatInt(ttl.Milliseconds(), 10)
			args = append(args, []byte("PX"), []byte(ms))
		}
		args = append(args, []byte("NX"))
		if err := vc.writeCommand("SET", args...); err != nil {
			return err
		}

		reply, err := vc.readReply()
		if err != nil {
			return err
		}
		switch reply.typ {
		case replySimpleString:
			ok = true
			return nil
		case replyNil:
			ok = false
			return nil
		default:
			return fmt.Errorf("unexpected SETNX response type: %s", reply.typ)
		}
	})
	return ok, err
}

// Del removes a key from the cache.
func (p *ValkeyProvider) Del(ctx context.Context, key string) error {
	return p.withConn(ctx, func(vc *valkeyConn) error {
		if err := vc.writeCommand("DEL", []byte(key)); err != nil {
			return err
		}
		_, err := vc.readReply()
		return err
	})
}

// Close closes the underlying client (no-op for stateless provider).
func (p *ValkeyProvider) Close() error { return nil }

func (p *ValkeyProvider) ping(ctx context.Context) error {
	return p.withConn(ctx, func(vc *valkeyConn) error {
		if err := vc.writeCommand("PING"); err != nil {
			return err
		}
		reply, err := vc.readReply()
		if err != nil {
			return err
		}
		if reply.typ != replySimpleString || string(reply.data) != "PONG" {
			return fmt.Errorf("unexpected PING response: %s", reply.data)
		}
		return nil
	})
}

func (p *ValkeyProvider) withConn(ctx context.Context, fn func(*valkeyConn) error) error {
	var lastErr error
	retries := p.cfg.MaxRetries
	if retries <= 0 {
		retries = 1
	}
	for attempt := 0; attempt < retries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		vc, err := p.dial(ctx)
		if err != nil {
			lastErr = err
			if shouldRetry(err) && attempt < retries-1 {
				time.Sleep(backoff(attempt))
				continue
			}
			return err
		}

		err = p.bootstrap(vc)
		if err != nil {
			vc.close()
			lastErr = err
			if shouldRetry(err) && attempt < retries-1 {
				time.Sleep(backoff(attempt))
				continue
			}
			return err
		}

		err = fn(vc)
		vc.close()
		if err == nil {
			return nil
		}
		lastErr = err
		if shouldRetry(err) && attempt < retries-1 {
			time.Sleep(backoff(attempt))
			continue
		}
		return err
	}
	return lastErr
}

func (p *ValkeyProvider) dial(ctx context.Context) (*valkeyConn, error) {
	dialer := net.Dialer{Timeout: deadlineOr(ctx, p.cfg.DialTimeout)}
	var (
		conn net.Conn
		err  error
	)
	if p.cfg.TLS {
		host := hostForTLS(p.cfg.Addr)
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
		conn, err = tls.DialWithDialer(&dialer, "tcp", p.cfg.Addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", p.cfg.Addr)
	}
	if err != nil {
		return nil, err
	}
	vc := &valkeyConn{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
		cfg:    p.cfg,
	}
	return vc, nil
}

func (p *ValkeyProvider) bootstrap(vc *valkeyConn) error {
	if p.cfg.Password != "" {
		cmd := []string{"AUTH"}
		if p.cfg.Username != "" {
			cmd = append(cmd, p.cfg.Username, p.cfg.Password)
		} else {
			cmd = append(cmd, p.cfg.Password)
		}
		if err := vc.writeStrings(cmd...); err != nil {
			return err
		}
		reply, err := vc.readReply()
		if err != nil {
			return err
		}
		if reply.typ != replySimpleString || !strings.EqualFold(string(reply.data), "OK") {
			return fmt.Errorf("auth failed: %s", reply.data)
		}
	}
	if p.cfg.DB > 0 {
		if err := vc.writeCommand("SELECT", []byte(strconv.Itoa(p.cfg.DB))); err != nil {
			return err
		}
		reply, err := vc.readReply()
		if err != nil {
			return err
		}
		if reply.typ != replySimpleString || !strings.EqualFold(string(reply.data), "OK") {
			return fmt.Errorf("select failed: %s", reply.data)
		}
	}
	return nil
}

// replyType enumerates the subset of RESP types needed by the provider.
type replyType string

const (
	replySimpleString replyType = "+"
	replyBulkString   replyType = "$"
	replyError        replyType = "-"
	replyInteger      replyType = ":"
	replyNil          replyType = "_"
)

type respReply struct {
	typ  replyType
	data []byte
}

// valkeyConn wraps a network connection with RESP helpers.
type valkeyConn struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	cfg    ValkeyConfig
}

func (vc *valkeyConn) close() {
	_ = vc.conn.Close()
}

func (vc *valkeyConn) writeCommand(command string, args ...[]byte) error {
	parts := make([][]byte, 0, len(args)+1)
	parts = append(parts, []byte(command))
	parts = append(parts, args...)
	return vc.write(parts...)
}

func (vc *valkeyConn) writeStrings(parts ...string) error {
	chunks := make([][]byte, 0, len(parts))
	for _, p := range parts {
		chunks = append(chunks, []byte(p))
	}
	return vc.write(chunks...)
}

func (vc *valkeyConn) write(parts ...[]byte) error {
	if err := vc.conn.SetWriteDeadline(time.Now().Add(writeTimeout(vc.cfg))); err != nil {
		return err
	}
	if _, err := vc.writer.WriteString(fmt.Sprintf("*%d\r\n", len(parts))); err != nil {
		return err
	}
	for _, part := range parts {
		if _, err := vc.writer.WriteString(fmt.Sprintf("$%d\r\n", len(part))); err != nil {
			return err
		}
		if _, err := vc.writer.Write(part); err != nil {
			return err
		}
		if _, err := vc.writer.WriteString("\r\n"); err != nil {
			return err
		}
	}
	return vc.writer.Flush()
}

func (vc *valkeyConn) readReply() (respReply, error) {
	if err := vc.conn.SetReadDeadline(time.Now().Add(readTimeout(vc.cfg))); err != nil {
		return respReply{}, err
	}
	prefix, err := vc.reader.ReadByte()
	if err != nil {
		return respReply{}, err
	}
	switch prefix {
	case '+':
		line, err := vc.readLine()
		return respReply{typ: replySimpleString, data: line}, err
	case '-':
		line, err := vc.readLine()
		if err != nil {
			return respReply{}, err
		}
		return respReply{}, errors.New(string(line))
	case ':':
		line, err := vc.readLine()
		return respReply{typ: replyInteger, data: line}, err
	case '$':
		line, err := vc.readLine()
		if err != nil {
			return respReply{}, err
		}
		size, err := strconv.Atoi(string(line))
		if err != nil {
			return respReply{}, err
		}
		if size == -1 {
			return respReply{typ: replyNil}, nil
		}
		buf := make([]byte, size)
		if _, err := ioReadFull(vc.reader, buf); err != nil {
			return respReply{}, err
		}
		if err := vc.expectCRLF(); err != nil {
			return respReply{}, err
		}
		return respReply{typ: replyBulkString, data: buf}, nil
	default:
		return respReply{}, fmt.Errorf("unexpected RESP prefix %q", prefix)
	}
}

func (vc *valkeyConn) readLine() ([]byte, error) {
	line, err := vc.reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return []byte(line), nil
}

func (vc *valkeyConn) expectCRLF() error {
	b1, err := vc.reader.ReadByte()
	if err != nil {
		return err
	}
	b2, err := vc.reader.ReadByte()
	if err != nil {
		return err
	}
	if b1 != '\r' || b2 != '\n' {
		return fmt.Errorf("invalid line termination")
	}
	return nil
}

func normaliseDurations(cfg *ValkeyConfig) {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 2 * time.Second
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 500 * time.Millisecond
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 500 * time.Millisecond
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 1
	}
}

func readTimeout(cfg ValkeyConfig) time.Duration {
	return cfg.ReadTimeout
}

func writeTimeout(cfg ValkeyConfig) time.Duration {
	return cfg.WriteTimeout
}

func deadlineOr(ctx context.Context, d time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return time.Millisecond
		}
		if d == 0 || remaining < d {
			return remaining
		}
	}
	if d <= 0 {
		return time.Millisecond
	}
	return d
}

func backoff(attempt int) time.Duration {
	base := 25 * time.Millisecond
	return time.Duration(1<<attempt) * base
}

func shouldRetry(err error) bool {
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}

func hostForTLS(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func ioReadFull(r *bufio.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := r.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
