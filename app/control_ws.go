package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	controlWSReadLimit   = 16 << 10
	controlWSWriteWait   = 10 * time.Second
	controlWSPongWait    = 70 * time.Second
	controlWSPingPeriod  = 60 * time.Second
	controlWSSuperseded  = 4001
	maxSupersededClients = 128
)

type controlWSRequest struct {
	ID     string         `json:"id"`
	Path   string         `json:"path"`
	Params map[string]any `json:"params"`
}

type controlWSResponse struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type bufferedResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newControlWSUpgrader() websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			parsed, err := url.Parse(origin)
			return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host) &&
				(parsed.Scheme == "http" || parsed.Scheme == "https")
		},
	}
}

var controlWSUpgrader = newControlWSUpgrader()

func (s *serverApp) handleControlWebSocket(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("clientId")
	if !validControlClientID(clientID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "a valid clientId is required"})
		return
	}
	conn, err := controlWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	if !s.claimControlClient(clientID, conn) {
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(controlWSSuperseded, "this client was replaced by a newer client"),
			time.Now().Add(controlWSWriteWait),
		)
		return
	}
	defer s.releaseControlClient(conn)
	if err := conn.WriteJSON(map[string]string{"type": "ready", "clientId": clientID}); err != nil {
		return
	}
	touchOwner := &touchSessionOwner{}
	defer s.cancelTouchOwner(touchOwner)
	conn.SetReadLimit(controlWSReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(controlWSPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(controlWSPongWait))
	})
	var writeMu sync.Mutex
	pingDone := make(chan struct{})
	defer close(pingDone)
	go func() {
		ticker := time.NewTicker(controlWSPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-pingDone:
				return
			case <-ticker.C:
				writeMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(controlWSWriteWait))
				writeMu.Unlock()
				if err != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, controlWSSuperseded) {
				s.logger.Printf("control WebSocket closed: %v", err)
			}
			return
		}
		response := s.handleControlWSMessageOwned(r.Context(), messageType, message, touchOwner)
		if response.ID == "" && response.OK {
			continue
		}
		writeMu.Lock()
		_ = conn.SetWriteDeadline(time.Now().Add(controlWSWriteWait))
		err = conn.WriteJSON(response)
		writeMu.Unlock()
		if err != nil {
			return
		}
	}
}

func (s *serverApp) claimControlClient(clientID string, conn *websocket.Conn) bool {
	// Serialize control takeovers with video offers so an older in-flight offer
	// cannot replace the stream after the newer client has taken control.
	s.videoOfferMu.Lock()
	defer s.videoOfferMu.Unlock()

	s.controlMu.Lock()
	if _, superseded := s.supersededClients[clientID]; superseded {
		s.controlMu.Unlock()
		return false
	}
	previous := s.controlConn
	previousClientID := s.controlClient
	if previousClientID != "" && previousClientID != clientID {
		s.rememberSupersededClient(previousClientID)
	}
	s.controlConn = conn
	s.controlClient = clientID
	s.controlMu.Unlock()

	if previous != nil && previous != conn {
		_ = previous.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(controlWSSuperseded, "replaced by a newer client"),
			time.Now().Add(controlWSWriteWait),
		)
		_ = previous.Close()
	}
	if previousClientID != clientID {
		if err := s.replaceVideoSessions(context.Background()); err != nil {
			s.logger.Printf("stop replaced video sessions: %v", err)
		}
	}
	return true
}

func (s *serverApp) rememberSupersededClient(clientID string) {
	if _, exists := s.supersededClients[clientID]; exists {
		return
	}
	if s.supersededClients == nil {
		s.supersededClients = make(map[string]struct{})
	}
	if len(s.supersededOrder) == maxSupersededClients {
		delete(s.supersededClients, s.supersededOrder[0])
		copy(s.supersededOrder, s.supersededOrder[1:])
		s.supersededOrder = s.supersededOrder[:len(s.supersededOrder)-1]
	}
	s.supersededClients[clientID] = struct{}{}
	s.supersededOrder = append(s.supersededOrder, clientID)
}

func (s *serverApp) releaseControlClient(conn *websocket.Conn) {
	s.controlMu.Lock()
	if s.controlConn == conn {
		s.controlConn = nil
	}
	s.controlMu.Unlock()
}

func (s *serverApp) isActiveControlClient(clientID string) bool {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	return s.controlClient == clientID
}

func validControlClientID(clientID string) bool {
	if len(clientID) != 32 {
		return false
	}
	for _, value := range clientID {
		if !(value >= '0' && value <= '9') && !(value >= 'a' && value <= 'f') {
			return false
		}
	}
	return true
}

func (s *serverApp) handleControlWSMessage(parent context.Context, messageType int, message []byte) controlWSResponse {
	return s.handleControlWSMessageOwned(parent, messageType, message, nil)
}

func (s *serverApp) handleControlWSMessageOwned(parent context.Context, messageType int, message []byte, owner *touchSessionOwner) controlWSResponse {
	response := controlWSResponse{}
	if messageType != websocket.TextMessage {
		response.Error = "text JSON messages are required"
		return response
	}
	decoder := json.NewDecoder(bytes.NewReader(message))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var request controlWSRequest
	if err := decoder.Decode(&request); err != nil {
		response.Error = "invalid control message"
		return response
	}
	response.ID = request.ID
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		response.Error = "invalid control message"
		return response
	}
	if request.ID == "" && !isUnacknowledgedTouchMove(request) || len(request.ID) > 64 {
		response.Error = "id is required and must not exceed 64 characters"
		return response
	}
	if !isWebSocketControlPath(request.Path) {
		response.Error = "unsupported control path"
		return response
	}
	if request.Path == "/touch" {
		if owner == nil {
			response.Error = "touch events require a WebSocket session"
			return response
		}
		return s.handleTouchControl(owner, request)
	}
	params := make(url.Values, len(request.Params))
	for key, rawValue := range request.Params {
		switch value := rawValue.(type) {
		case string:
			params.Set(key, value)
		case json.Number:
			params.Set(key, value.String())
		default:
			response.Error = "control parameters must be strings or numbers"
			return response
		}
	}

	ctx, cancel := context.WithTimeout(parent, s.config.HandlerTimeout)
	defer cancel()
	requestURL := &url.URL{Path: request.Path, RawQuery: params.Encode()}
	httpRequest := (&http.Request{
		Method: http.MethodGet,
		URL:    requestURL,
		Header: make(http.Header),
		Body:   http.NoBody,
	}).WithContext(ctx)
	writer := &bufferedResponseWriter{header: make(http.Header)}
	s.ServeHTTP(writer, httpRequest)
	if writer.status == 0 || writer.status < http.StatusBadRequest {
		response.OK = true
		response.Result = append(json.RawMessage(nil), writer.body.Bytes()...)
		return response
	}
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(writer.body.Bytes(), &failure) == nil && failure.Error != "" {
		response.Error = failure.Error
	} else {
		response.Error = "control action failed"
	}
	return response
}

func isWebSocketControlPath(path string) bool {
	switch path {
	case "/click", "/longpress", "/swipe", "/input", "/key":
		return true
	case "/touch":
		return true
	default:
		return false
	}
}

func isUnacknowledgedTouchMove(request controlWSRequest) bool {
	if request.Path != "/touch" || request.Params == nil {
		return false
	}
	action, ok := request.Params["action"].(string)
	return ok && action == "move"
}

func (w *bufferedResponseWriter) Header() http.Header { return w.header }

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}
