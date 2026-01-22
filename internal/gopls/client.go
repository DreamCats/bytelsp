package gopls

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	GoplsPath string
	Workdir   string
	RootURI   string
	Timeout   time.Duration
}

type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	reader *bufio.Reader
	writer *bufio.Writer

	writeMu sync.Mutex

	nextID  uint64
	pending map[uint64]chan *Message
	pendMu  sync.Mutex

	notifyMu sync.RWMutex
	notify   map[string][]func(json.RawMessage)

	closed chan struct{}
}

func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	goplsPath := cfg.GoplsPath
	if goplsPath == "" {
		goplsPath = "gopls"
	}

	cmd := exec.Command(goplsPath, "serve")
	if cfg.Workdir != "" {
		cmd.Dir = cfg.Workdir
	}
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("gopls stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("gopls stdout: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start gopls: %w", err)
	}

	client := &Client{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		reader:  bufio.NewReader(stdout),
		writer:  bufio.NewWriter(stdin),
		pending: make(map[uint64]chan *Message),
		notify:  make(map[string][]func(json.RawMessage)),
		closed:  make(chan struct{}),
	}

	go client.readLoop()
	return client, nil
}

func (c *Client) Close() error {
	select {
	case <-c.closed:
		return nil
	default:
	}

	close(c.closed)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = c.SendRequest(ctx, "shutdown", map[string]interface{}{})
	_ = c.SendNotification("exit", map[string]interface{}{})

	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.stdout != nil {
		_ = c.stdout.Close()
	}
	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
	return nil
}

func (c *Client) Initialize(ctx context.Context, rootURI string, workspaceFolders []string) error {
	params := map[string]interface{}{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"workspaceFolders": func() []map[string]interface{} {
			folders := make([]map[string]interface{}, 0, len(workspaceFolders))
			for _, f := range workspaceFolders {
				folders = append(folders, map[string]interface{}{
					"uri":  f,
					"name": filepath.Base(f),
				})
			}
			return folders
		}(),
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"diagnostic": map[string]interface{}{
					"dynamicRegistration":      false,
					"relatedDocumentSupport":   true,
					"multipleLanguagesSupport": false,
				},
				"hover":      map[string]interface{}{"dynamicRegistration": false},
				"definition": map[string]interface{}{"dynamicRegistration": false},
				"references": map[string]interface{}{"dynamicRegistration": false},
			},
			"workspace": map[string]interface{}{
				"workspaceFolders": true,
				"symbol":           map[string]interface{}{"dynamicRegistration": false},
			},
		},
	}

	_, err := c.SendRequest(ctx, "initialize", params)
	if err != nil {
		return err
	}
	return c.SendNotification("initialized", map[string]interface{}{})
}

func (c *Client) SendRequest(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := atomic.AddUint64(&c.nextID, 1)
	respCh := make(chan *Message, 1)

	c.pendMu.Lock()
	c.pending[id] = respCh
	c.pendMu.Unlock()

	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	if err := c.writeMessage(msg); err != nil {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return nil, err
	}

	select {
	case resp := <-respCh:
		if resp == nil {
			return nil, errors.New("connection closed")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("lsp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *Client) SendNotification(method string, params interface{}) error {
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return c.writeMessage(msg)
}

func (c *Client) OnNotification(method string, handler func(json.RawMessage)) {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	c.notify[method] = append(c.notify[method], handler)
}

func (c *Client) writeMessage(msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.writer, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	if _, err := c.writer.Write(data); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *Client) readLoop() {
	for {
		msg, err := c.readMessage()
		if err != nil {
			c.closePending(err)
			return
		}
		if msg.Method != "" && len(msg.ID) == 0 {
			c.dispatchNotification(msg.Method, msg.Params)
			continue
		}
		if len(msg.ID) > 0 {
			id, ok := parseID(msg.ID)
			if !ok {
				continue
			}
			c.pendMu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.pendMu.Unlock()
			if ch != nil {
				ch <- msg
			}
		}
	}
}

func (c *Client) closePending(err error) {
	c.pendMu.Lock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- &Message{Error: &ResponseError{Code: -1, Message: err.Error()}}
	}
	c.pendMu.Unlock()
}

func (c *Client) dispatchNotification(method string, params json.RawMessage) {
	c.notifyMu.RLock()
	handlers := append([]func(json.RawMessage){}, c.notify[method]...)
	c.notifyMu.RUnlock()
	for _, h := range handlers {
		h(params)
	}
}

func (c *Client) readMessage() (*Message, error) {
	contentLength := 0
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				v := strings.TrimSpace(parts[1])
				if n, err := strconv.Atoi(v); err == nil {
					contentLength = n
				}
			}
		}
	}
	if contentLength <= 0 {
		return nil, errors.New("missing content-length")
	}
	buf := make([]byte, contentLength)
	if _, err := io.ReadFull(c.reader, buf); err != nil {
		return nil, err
	}
	var msg Message
	if err := json.Unmarshal(buf, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func parseID(raw json.RawMessage) (uint64, bool) {
	var num uint64
	if err := json.Unmarshal(raw, &num); err == nil {
		return num, true
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		if f >= 0 {
			return uint64(f), true
		}
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		u, err := strconv.ParseUint(str, 10, 64)
		if err == nil {
			return u, true
		}
	}
	return 0, false
}
