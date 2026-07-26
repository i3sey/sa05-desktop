package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// requestLimit bounds one request line, so a client cannot make the helper buffer without
// end.
const requestLimit = 64 * 1024

// callTimeout bounds how long one handler may run before the connection is dropped.
const callTimeout = 60 * time.Second

// Handler implements the operations the helper exposes.
type Handler interface {
	// Status reports the current tunnel state.
	Status(ctx context.Context) (Status, error)
	// TunUp brings the tunnel up; calling it while up re-applies the configuration.
	TunUp(ctx context.Context, request TunUp) (Status, error)
	// TunDown tears the tunnel down. Tearing down a stopped tunnel is not an error.
	TunDown(ctx context.Context) (Status, error)
}

// Authorizer decides whether a connecting peer may issue commands.
type Authorizer func(credentials PeerCredentials) error

// PeerCredentials identifies the process on the other end of the socket.
type PeerCredentials struct {
	UID uint32
	GID uint32
	PID int32
}

// Server serves the helper protocol on a listener.
type Server struct {
	Handler   Handler
	Authorize Authorizer
	Logger    *log.Logger

	mutex      sync.Mutex // serialises handlers: the tunnel is a single global resource
	activeConn sync.WaitGroup
	connMu     sync.Mutex
	conns      map[net.Conn]struct{}
}

// Serve accepts connections until ctx is cancelled or the listener fails.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			// Idle clients are blocked reading the next request. Closing their
			// connections is what lets shutdown finish instead of waiting for them to
			// disconnect on their own.
			s.closeConnections()
			s.activeConn.Wait()
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("приём соединения: %w", err)
		}
		s.trackConnection(connection)
		s.activeConn.Add(1)
		go func() {
			defer s.activeConn.Done()
			defer s.forgetConnection(connection)
			s.handleConnection(ctx, connection)
		}()
	}
}

func (s *Server) trackConnection(connection net.Conn) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.conns == nil {
		s.conns = map[net.Conn]struct{}{}
	}
	s.conns[connection] = struct{}{}
}

func (s *Server) forgetConnection(connection net.Conn) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	delete(s.conns, connection)
}

func (s *Server) closeConnections() {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	for connection := range s.conns {
		_ = connection.Close()
	}
}

func (s *Server) handleConnection(ctx context.Context, connection net.Conn) {
	defer connection.Close()

	if s.Authorize != nil {
		credentials, err := peerCredentials(connection)
		if err != nil {
			s.logf("не удалось получить учётные данные пира: %v", err)
			return
		}
		if err := s.Authorize(credentials); err != nil {
			s.logf("отказано в доступе (uid=%d pid=%d): %v",
				credentials.UID, credentials.PID, err)
			// Tell the client why, then drop: an unauthorized peer gets no further calls.
			_ = writeResponse(connection, Response{Error: err.Error()})
			return
		}
	}

	reader := bufio.NewReaderSize(connection, requestLimit)
	for {
		line, err := readLine(reader, requestLimit)
		if err != nil {
			return
		}
		var request Request
		if err := json.Unmarshal(line, &request); err != nil {
			_ = writeResponse(connection, Response{Error: "запрос не разобран"})
			continue
		}
		response := s.dispatch(ctx, request)
		if err := writeResponse(connection, response); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(ctx context.Context, request Request) Response {
	response := Response{ID: request.ID}
	if err := request.Validate(); err != nil {
		response.Error = err.Error()
		return response
	}
	if request.Method == MethodHello {
		if request.Hello.Version != ProtocolVersion {
			response.Error = fmt.Sprintf(
				"несовместимая версия протокола: клиент %d, хелпер %d",
				request.Hello.Version, ProtocolVersion)
			return response
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	// One tunnel, one operation at a time: concurrent tun_up/tun_down would race on the
	// interface and the routing table.
	s.mutex.Lock()
	defer s.mutex.Unlock()

	var (
		status Status
		err    error
	)
	switch request.Method {
	case MethodHello, MethodStatus:
		status, err = s.Handler.Status(callCtx)
	case MethodTunUp:
		status, err = s.Handler.TunUp(callCtx, *request.TunUp)
	case MethodTunDown:
		status, err = s.Handler.TunDown(callCtx)
	default:
		err = fmt.Errorf("неизвестный метод %q", request.Method)
	}
	if err != nil {
		response.Error = err.Error()
		return response
	}
	status.Version = ProtocolVersion
	response.OK = true
	response.Status = &status
	return response
}

func (s *Server) logf(format string, args ...any) {
	if s.Logger != nil {
		s.Logger.Printf(format, args...)
	}
}

func writeResponse(writer net.Conn, response Response) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if err := writer.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	_, err = writer.Write(append(encoded, '\n'))
	return err
}

// readLine reads one newline-terminated message, refusing anything longer than limit.
func readLine(reader *bufio.Reader, limit int) ([]byte, error) {
	line := make([]byte, 0, 256)
	for {
		chunk, isPrefix, err := reader.ReadLine()
		if err != nil {
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > limit {
			return nil, errors.New("запрос превышает лимит")
		}
		if !isPrefix {
			return line, nil
		}
	}
}
