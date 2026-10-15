package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

func TestParseUINodes(t *testing.T) {
	input := `<hierarchy><node text="" content-desc="" bounds="[0,0][10,10]"/><node text="Sign in &amp; continue" content-desc="Continue" package="com.demo" bounds="[10,20][31,41]"/><node text="Back" bounds="[-2,-4][2,4]"/></hierarchy>`
	nodes, err := parseUINodes([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 useful nodes, got %d", len(nodes))
	}
	if nodes[0].Text != "Sign in & continue" || nodes[0].Description != "Continue" || nodes[0].Package != "com.demo" {
		t.Fatalf("unexpected first node: %#v", nodes[0])
	}
	if nodes[0].X == nil || *nodes[0].X != 20 || nodes[0].Y == nil || *nodes[0].Y != 30 {
		t.Fatalf("unexpected center point: %#v", nodes[0])
	}
	if nodes[1].X == nil || *nodes[1].X != 0 || nodes[1].Y == nil || *nodes[1].Y != 0 {
		t.Fatalf("negative bounds were not centered correctly: %#v", nodes[1])
	}
}

func TestLoadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.conf")
	if err := os.WriteFile(path, []byte("HOST=127.0.0.1\nPORT=8123\nBOOT_DELAY=0\nSCREENSHOT_QUALITY=35\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "127.0.0.1" || config.Port != 8123 || config.BootDelay != 0 || config.ScreenshotQuality != 35 {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestHealthAndEmbeddedControlPage(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	app := newServer(defaultConfig(), ".", t.TempDir(), logger)
	for _, path := range []string{"/health", "/"} {
		request := httptest.NewRequest("GET", path, nil)
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("%s returned %d", path, response.Code)
		}
		if path == "/health" && !strings.Contains(response.Body.String(), `"service":"screenbridge"`) {
			t.Fatalf("unexpected health response: %s", response.Body.String())
		}
		if path == "/" && (!strings.Contains(response.Body.String(), `new RTCPeerConnection`) ||
			strings.Contains(response.Body.String(), `id="video-fps"`) ||
			!strings.Contains(response.Body.String(), `data-bitrate="500000" aria-pressed="true">500k`) ||
			!strings.Contains(response.Body.String(), `data-scale="2" aria-pressed="true">1/2`) ||
			strings.Contains(response.Body.String(), `id="video-toggle"`)) {
			t.Fatal("embedded control page is missing")
		}
	}
}

func TestValidVideoBitRate(t *testing.T) {
	for _, rate := range []int{500_000, 1_000_000, 2_000_000} {
		if !validVideoBitRate(rate) {
			t.Errorf("%d should be supported", rate)
		}
	}
	for _, rate := range []int{-1, 0, 499_999, 800_000, 3_000_000} {
		if validVideoBitRate(rate) {
			t.Errorf("%d should not be supported", rate)
		}
	}
}

func TestValidVideoScale(t *testing.T) {
	for _, scale := range []int{2, 3, 4} {
		if !validVideoScale(scale) {
			t.Errorf("%d should be supported", scale)
		}
	}
	for _, scale := range []int{-1, 0, 1, 5} {
		if validVideoScale(scale) {
			t.Errorf("%d should not be supported", scale)
		}
	}
}

func TestVideoOfferRejectsUnsupportedScale(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	request := httptest.NewRequest("POST", "/webrtc/offer", strings.NewReader(`{"type":"offer","sdp":"v=0","scale":5}`))
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != 400 || !strings.Contains(response.Body.String(), "scale must be") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestVideoOfferRejectsUnsupportedBitRate(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	request := httptest.NewRequest("POST", "/webrtc/offer", strings.NewReader(`{"type":"offer","sdp":"v=0","bitRate":600000}`))
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != 400 || !strings.Contains(response.Body.String(), "bitRate must be") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestVideoOfferRejectsObsoleteFPSField(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	app := newServer(defaultConfig(), ".", t.TempDir(), logger)
	request := httptest.NewRequest("POST", "/webrtc/offer", strings.NewReader(`{"type":"offer","sdp":"v=0","fps":3}`))
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != 400 || !strings.Contains(response.Body.String(), "invalid WebRTC offer") {
		t.Fatalf("unexpected response for unsupported fps: %d %s", response.Code, response.Body.String())
	}
}

func TestControlWebSocketAcknowledgesMessages(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	app := newServer(defaultConfig(), ".", t.TempDir(), logger)
	server := httptest.NewServer(app)
	defer server.Close()
	websocketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/control"
	conn, response, err := websocket.DefaultDialer.Dial(websocketURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("connect to control WebSocket: %v (HTTP %d)", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(controlWSRequest{ID: "request-1", Path: "/unsupported"}); err != nil {
		t.Fatal(err)
	}
	var responseMessage controlWSResponse
	if err := conn.ReadJSON(&responseMessage); err != nil {
		t.Fatal(err)
	}
	if responseMessage.ID != "request-1" || responseMessage.OK || responseMessage.Error != "unsupported control path" {
		t.Fatalf("unexpected WebSocket response: %#v", responseMessage)
	}

	if err := conn.WriteJSON(controlWSRequest{ID: "request-2", Path: "/key", Params: map[string]any{"code": "NOT_A_KEY"}}); err != nil {
		t.Fatal(err)
	}
	if err := conn.ReadJSON(&responseMessage); err != nil {
		t.Fatal(err)
	}
	if responseMessage.ID != "request-2" || responseMessage.OK || !strings.Contains(responseMessage.Error, "code must be") {
		t.Fatalf("unexpected action validation response: %#v", responseMessage)
	}
}

func TestKeyCodeAllowlist(t *testing.T) {
	for _, code := range []string{"BACK", "HOME", "ENTER", "APP_SWITCH", "DEL", "POWER"} {
		if !allowedKeyCodes[code] {
			t.Fatalf("missing supported key code %s", code)
		}
	}
	if allowedKeyCodes["NOT_A_KEY"] {
		t.Fatal("unexpected key code NOT_A_KEY")
	}
}

type recordedTouchEvent struct {
	action        string
	width, height int
	x, y          int
}

type recordingTouchInjector struct {
	mu     sync.Mutex
	events []recordedTouchEvent
}

func (touch *recordingTouchInjector) add(event recordedTouchEvent) error {
	touch.mu.Lock()
	touch.events = append(touch.events, event)
	touch.mu.Unlock()
	return nil
}

func (touch *recordingTouchInjector) Down(width, height, x, y int) error {
	return touch.add(recordedTouchEvent{action: "down", width: width, height: height, x: x, y: y})
}

func (touch *recordingTouchInjector) Move(x, y int) error {
	return touch.add(recordedTouchEvent{action: "move", x: x, y: y})
}

func (touch *recordingTouchInjector) Up(x, y int) error {
	return touch.add(recordedTouchEvent{action: "up", x: x, y: y})
}

func (touch *recordingTouchInjector) Cancel() error {
	return touch.add(recordedTouchEvent{action: "cancel"})
}

func TestControlWebSocketStreamsTouchEvents(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	app := newServer(defaultConfig(), ".", t.TempDir(), logger)
	input := &recordingTouchInjector{}
	app.touch = input
	server := httptest.NewServer(app)
	defer server.Close()
	websocketURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(websocketURL, nil)
	if err != nil {
		t.Fatal(err)
	}

	writeTouch := func(id, action string, params map[string]any) {
		t.Helper()
		params["action"] = action
		if err := conn.WriteJSON(controlWSRequest{ID: id, Path: "/touch", Params: params}); err != nil {
			t.Fatal(err)
		}
	}
	readAck := func(wantID string) {
		t.Helper()
		var response controlWSResponse
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatal(err)
		}
		if response.ID != wantID || !response.OK {
			t.Fatalf("unexpected touch acknowledgement: %#v", response)
		}
	}

	writeTouch("down", "down", map[string]any{"x": 10, "y": 20, "width": 1080, "height": 1920})
	readAck("down")
	writeTouch("", "move", map[string]any{"x": 120, "y": 240})
	writeTouch("up", "up", map[string]any{"x": 120, "y": 240})
	readAck("up")

	input.mu.Lock()
	events := append([]recordedTouchEvent(nil), input.events...)
	input.mu.Unlock()
	if len(events) != 3 || events[0] != (recordedTouchEvent{action: "down", width: 1080, height: 1920, x: 10, y: 20}) ||
		events[1] != (recordedTouchEvent{action: "move", x: 120, y: 240}) ||
		events[2] != (recordedTouchEvent{action: "up", x: 120, y: 240}) {
		t.Fatalf("unexpected injected touch sequence: %#v", events)
	}
	_ = conn.Close()
}

func TestTouchSessionReleasedWhenWebSocketCloses(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	app := newServer(defaultConfig(), ".", t.TempDir(), logger)
	input := &recordingTouchInjector{}
	app.touch = input
	owner := &touchSessionOwner{}
	down := controlWSRequest{ID: "down", Path: "/touch", Params: map[string]any{
		"action": "down", "width": json.Number("100"), "height": json.Number("200"),
		"x": json.Number("5"), "y": json.Number("10"),
	}}
	if response := app.handleTouchControl(owner, down); !response.OK {
		t.Fatalf("touch down failed: %#v", response)
	}
	app.cancelTouchOwner(owner)
	if app.touchOwner != nil {
		t.Fatal("active touch owner was not cleared")
	}
	input.mu.Lock()
	defer input.mu.Unlock()
	if len(input.events) != 2 || input.events[1].action != "cancel" {
		t.Fatalf("disconnect did not release active touch: %#v", input.events)
	}
}
