package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const dialTimeout = 3 * time.Second

// Client talks to the helper. It reconnects on demand, so the GUI can start before the
// helper does and a helper restart does not require restarting the client.
type Client struct {
	// Address is the unix socket path (Linux) or named pipe (Windows).
	Address string

	mutex      sync.Mutex
	connection net.Conn
	reader     *bufio.Reader
	nextID     atomic.Uint64
}

// NewClient returns a client for the default helper endpoint when address is empty.
func NewClient(address string) *Client {
	if address == "" {
		address = DefaultEndpoint()
	}
	return &Client{Address: address}
}

// Available reports whether the helper is reachable, without changing tunnel state.
func (c *Client) Available(ctx context.Context) bool {
	_, err := c.Hello(ctx)
	return err == nil
}

// Hello negotiates the protocol version. A mismatch is an error, so the GUI can tell the
// user to update the system component instead of half-applying a request.
func (c *Client) Hello(ctx context.Context) (Status, error) {
	return c.call(ctx, Request{
		Method: MethodHello,
		Hello:  &Hello{Version: ProtocolVersion},
	})
}

// Status reports the helper's view of the tunnel.
func (c *Client) Status(ctx context.Context) (Status, error) {
	return c.call(ctx, Request{Method: MethodStatus})
}

// TunUp asks the helper to route the machine's traffic into the client's SOCKS port.
func (c *Client) TunUp(ctx context.Context, request TunUp) (Status, error) {
	if err := request.Validate(); err != nil {
		return Status{}, err
	}
	return c.call(ctx, Request{Method: MethodTunUp, TunUp: &request})
}

// TunDown restores the machine's routing.
func (c *Client) TunDown(ctx context.Context) (Status, error) {
	return c.call(ctx, Request{Method: MethodTunDown})
}

// Close drops the connection; the next call reconnects.
func (c *Client) Close() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.closeLocked()
}

func (c *Client) closeLocked() {
	if c.connection != nil {
		c.connection.Close()
		c.connection = nil
		c.reader = nil
	}
}

func (c *Client) call(ctx context.Context, request Request) (Status, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	request.ID = c.nextID.Add(1)
	status, err := c.attempt(ctx, request)
	if err == nil {
		return status, nil
	}
	// One retry on a transport error: the helper may have restarted since the last call.
	var transport *transportError
	if !errors.As(err, &transport) {
		return Status{}, err
	}
	c.closeLocked()
	return c.attempt(ctx, request)
}

func (c *Client) attempt(ctx context.Context, request Request) (Status, error) {
	if err := c.ensureConnection(ctx); err != nil {
		return Status{}, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return Status{}, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(callTimeout)
	}
	if err := c.connection.SetDeadline(deadline); err != nil {
		return Status{}, &transportError{err}
	}
	if _, err := c.connection.Write(append(encoded, '\n')); err != nil {
		return Status{}, &transportError{err}
	}
	line, err := readLine(c.reader, requestLimit)
	if err != nil {
		return Status{}, &transportError{err}
	}

	var response Response
	if err := json.Unmarshal(line, &response); err != nil {
		return Status{}, fmt.Errorf("ответ хелпера не разобран: %w", err)
	}
	if !response.OK {
		if response.Error == "" {
			response.Error = "хелпер отклонил запрос"
		}
		return Status{}, errors.New(response.Error)
	}
	if response.Status == nil {
		return Status{}, errors.New("хелпер не вернул состояние")
	}
	return *response.Status, nil
}

func (c *Client) ensureConnection(ctx context.Context) error {
	if c.connection != nil {
		return nil
	}
	connection, err := dial(ctx, c.Address, dialTimeout)
	if err != nil {
		return &transportError{fmt.Errorf(
			"системный компонент SA05 недоступен (%s): %w", c.Address, err)}
	}
	c.connection = connection
	c.reader = bufio.NewReaderSize(connection, requestLimit)
	return nil
}

// transportError marks failures that a reconnect might fix, as opposed to a helper that
// answered and refused.
type transportError struct{ err error }

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }
