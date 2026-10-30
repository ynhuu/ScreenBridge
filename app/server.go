package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	packageNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+)+$`)
	componentPattern   = regexp.MustCompile(`[A-Za-z0-9._]+/[A-Za-z0-9._$]+`)
	displayPattern     = regexp.MustCompile(`(?m)(Physical|Override) size:\s*(\d+)x(\d+)`)
)

type serverApp struct {
	config            Config
	moduleDir         string
	stateDir          string
	logger            *log.Logger
	dumpMu            sync.Mutex
	touchMu           sync.Mutex
	touchOwner        *touchSessionOwner
	touch             touchInjector
	controlMu         sync.Mutex
	controlConn       *websocket.Conn
	controlClient     string
	supersededClients map[string]struct{}
	supersededOrder   []string
	videoMu           sync.Mutex
	videoOfferMu      sync.Mutex
	videoClosing      bool // guarded by videoOfferMu
	videoSessions     map[string]*videoSession
	apiLevel          string
	deviceABI         string
}

func newServer(config Config, moduleDir, stateDir string, logger *log.Logger) *serverApp {
	app := &serverApp{
		config:            config,
		moduleDir:         moduleDir,
		stateDir:          stateDir,
		logger:            logger,
		touch:             newTouchInjector(),
		videoSessions:     make(map[string]*videoSession),
		supersededClients: make(map[string]struct{}),
		apiLevel:          commandString("/system/bin/getprop", "ro.build.version.sdk"),
		deviceABI:         commandString("/system/bin/getprop", "ro.product.cpu.abi"),
	}
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		logger.Printf("create state directory: %v", err)
	}
	return app
}

func (s *serverApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/ws/control" {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
			return
		}
		s.handleControlWebSocket(w, r)
		return
	}
	if r.URL.Path == "/webrtc/offer" && r.Method == http.MethodPost {
		ctx, cancel := context.WithTimeout(r.Context(), s.config.HandlerTimeout)
		defer cancel()
		s.handleVideoOffer(w, r.WithContext(ctx))
		return
	}
	if r.URL.Path == "/webrtc/close" && r.Method == http.MethodPost {
		ctx, cancel := context.WithTimeout(r.Context(), s.config.HandlerTimeout)
		defer cancel()
		s.handleVideoClose(w, r.WithContext(ctx))
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.config.HandlerTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	if isWebSocketControlPath(r.URL.Path) || r.URL.Path == "/start" || r.URL.Path == "/stop" {
		s.notifyVideoActivity()
	}

	switch r.URL.Path {
	case "/health":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "screenbridge"})
	case "/":
		serveWebAsset(w, "/")
	case "/assets/settings.js", "/favicon.ico", "/apple-touch-icon.png":
		serveWebAsset(w, r.URL.Path)
	case "/ui", "/dump":
		s.handleDump(w, r)
	case "/click":
		s.handleClick(w, r)
	case "/longpress":
		s.handleLongPress(w, r)
	case "/input":
		s.handleInput(w, r)
	case "/swipe":
		s.handleSwipe(w, r)
	case "/key":
		s.handleKey(w, r)
	case "/screenshot":
		s.handleScreenshot(w, r)
	case "/webrtc/offer", "/webrtc/close":
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
	case "/display":
		s.handleDisplay(w, r)
	case "/current-app":
		s.handleCurrentApp(w, r)
	case "/start", "/stop":
		s.handleApp(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{
			"ok": false, "error": "not found",
			"endpoints": []string{"/", "/health", "/ui", "/dump", "/click", "/longpress", "/input", "/swipe", "/key", "/screenshot", "/display", "/current-app", "/start", "/stop", "WS /ws/control", "POST /webrtc/offer", "POST /webrtc/close"},
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte(`{"ok":false,"error":"JSON encoding failed"}`)
		status = http.StatusInternalServerError
	}
	writeBytes(w, status, "application/json; charset=utf-8", data)
}

func writeBytes(w http.ResponseWriter, status int, contentType string, data []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func parseIntParam(r *http.Request, key string, required bool, fallback int) (int, error) {
	value := r.URL.Query().Get(key)
	if value == "" && !required {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return parsed, nil
}

func commandString(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := runCombined(ctx, name, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
