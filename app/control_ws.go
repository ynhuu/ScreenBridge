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
	controlWSReadLimit  = 16 << 10
	controlWSWriteWait  = 10 * time.Second
	controlWSPongWait   = 70 * time.Second
	controlWSPingPeriod = 60 * time.Second
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
	conn, err := controlWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
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
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
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
