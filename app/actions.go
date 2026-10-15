package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (s *serverApp) handleClick(w http.ResponseWriter, r *http.Request) {
	x, errX := parseIntParam(r, "x", true, 0)
	y, errY := parseIntParam(r, "y", true, 0)
	if errX != nil || errY != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "x and y must be non-negative integers"})
		return
	}
	output, err := runCombined(r.Context(), "/system/bin/input", "tap", strconv.Itoa(x), strconv.Itoa(y))
	if err != nil {
		writeCommandError(w, "input tap failed", output)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "x": x, "y": y})
}

func (s *serverApp) handleLongPress(w http.ResponseWriter, r *http.Request) {
	x, errX := parseIntParam(r, "x", true, 0)
	y, errY := parseIntParam(r, "y", true, 0)
	duration, errDuration := parseIntParam(r, "duration", false, 800)
	if errX != nil || errY != nil || errDuration != nil || duration < 600 || duration > 10000 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "x and y must be non-negative integers; duration must be from 600 to 10000 ms"})
		return
	}
	point := strconv.Itoa(x)
	output, err := runCombined(r.Context(), "/system/bin/input", "swipe", point, strconv.Itoa(y), point, strconv.Itoa(y), strconv.Itoa(duration))
	if err != nil {
		writeCommandError(w, "input long press failed", output)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "x": x, "y": y, "duration": duration})
}

func (s *serverApp) handleInput(w http.ResponseWriter, r *http.Request) {
	encodedText := ""
	for _, pair := range strings.Split(r.URL.RawQuery, "&") {
		key, value, found := strings.Cut(pair, "=")
		if found && key == "text" {
			encodedText = value
			break
		}
	}
	text := r.URL.Query().Get("text")
	if encodedText == "" || len(encodedText) > 2000 || text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "text is required and must not exceed 2000 encoded characters"})
		return
	}
	output, err := runCombined(r.Context(), "/system/bin/input", "text", text)
	if err != nil {
		writeCommandError(w, "input text failed", output)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *serverApp) handleSwipe(w http.ResponseWriter, r *http.Request) {
	x1, errX1 := parseIntParam(r, "x1", true, 0)
	y1, errY1 := parseIntParam(r, "y1", true, 0)
	x2, errX2 := parseIntParam(r, "x2", true, 0)
	y2, errY2 := parseIntParam(r, "y2", true, 0)
	duration, errDuration := parseIntParam(r, "duration", false, 300)
	if errX1 != nil || errY1 != nil || errX2 != nil || errY2 != nil || errDuration != nil || duration > 10000 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "x1, y1, x2, y2 and duration must be non-negative integers; maximum duration is 10000"})
		return
	}
	output, err := runCombined(r.Context(), "/system/bin/input", "swipe",
		strconv.Itoa(x1), strconv.Itoa(y1), strconv.Itoa(x2), strconv.Itoa(y2), strconv.Itoa(duration))
	if err != nil {
		writeCommandError(w, "input swipe failed", output)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "x1": x1, "y1": y1, "x2": x2, "y2": y2, "duration": duration})
}

var allowedKeyCodes = map[string]bool{
	"BACK": true, "HOME": true, "ENTER": true, "APP_SWITCH": true, "DEL": true, "POWER": true,
}

func (s *serverApp) handleKey(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(r.URL.Query().Get("code"))
	if !allowedKeyCodes[code] {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "code must be BACK, HOME, ENTER, APP_SWITCH, DEL or POWER"})
		return
	}
	output, err := runCombined(r.Context(), "/system/bin/input", "keyevent", "KEYCODE_"+code)
	if err != nil {
		writeCommandError(w, "input keyevent failed", output)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "code": code})
}

func (s *serverApp) handleDisplay(w http.ResponseWriter, r *http.Request) {
	output, err := runCombined(r.Context(), "/system/bin/wm", "size")
	if err != nil {
		writeCommandError(w, "unable to determine display size", output)
		return
	}
	matches := displayPattern.FindAllStringSubmatch(string(output), -1)
	if len(matches) == 0 {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "unable to determine display size"})
		return
	}
	width, _ := strconv.Atoi(matches[len(matches)-1][2])
	height, _ := strconv.Atoi(matches[len(matches)-1][3])
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "width": width, "height": height})
}

func (s *serverApp) handleCurrentApp(w http.ResponseWriter, r *http.Request) {
	output, _ := runCombined(r.Context(), "/system/bin/dumpsys", "activity", "activities")
	match := componentPattern.Find(output)
	if len(match) == 0 {
		output, _ = runCombined(r.Context(), "/system/bin/dumpsys", "window")
		match = componentPattern.Find(output)
	}
	if len(match) == 0 {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "unable to determine current app"})
		return
	}
	component := string(match)
	packageName, activity, found := strings.Cut(component, "/")
	if !found || activity == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "unable to determine current app"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "package": packageName, "activity": activity})
}

func (s *serverApp) handleApp(w http.ResponseWriter, r *http.Request) {
	packageName := r.URL.Query().Get("package")
	if !packageNamePattern.MatchString(packageName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid package name"})
		return
	}
	if output, err := runCombined(r.Context(), "/system/bin/pm", "path", packageName); err != nil {
		writeCommandErrorStatus(w, http.StatusNotFound, "package is not installed", output)
		return
	}

	action := "start"
	var output []byte
	var err error
	if r.URL.Path == "/start" {
		output, err = runCombined(r.Context(), "/system/bin/monkey", "-p", packageName, "-c", "android.intent.category.LAUNCHER", "1")
	} else {
		action = "stop"
		output, err = runCombined(r.Context(), "/system/bin/am", "force-stop", packageName)
	}
	if err != nil {
		writeCommandError(w, fmt.Sprintf("app %s failed", action), output)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": action, "package": packageName})
}

func writeCommandError(w http.ResponseWriter, message string, output []byte) {
	writeCommandErrorStatus(w, http.StatusInternalServerError, message, output)
}

func writeCommandErrorStatus(w http.ResponseWriter, status int, message string, output []byte) {
	detail := strings.TrimSpace(string(output))
	if len(detail) > 500 {
		detail = detail[:500]
	}
	value := map[string]any{"ok": false, "error": message}
	if detail != "" {
		value["detail"] = detail
	}
	writeJSON(w, status, value)
}
