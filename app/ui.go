package main

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"regexp"
	"strconv"
)

var boundsPattern = regexp.MustCompile(`-?\d+`)

func (s *serverApp) handleDump(w http.ResponseWriter, r *http.Request) {
	if !s.dumpMu.TryLock() {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "UI dump already in progress"})
		return
	}
	defer s.dumpMu.Unlock()
	xmlData, err := s.dumpUI(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "uiautomator dump failed", "detail": err.Error()})
		return
	}
	if r.URL.Path == "/ui" {
		writeBytes(w, http.StatusOK, "application/xml; charset=utf-8", xmlData)
		return
	}
	nodes, err := parseUINodes(xmlData)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "UI filtering failed", "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(nodes), "nodes": nodes})
}

type uiNode struct {
	Text        string `json:"text,omitempty"`
	Description string `json:"description,omitempty"`
	Package     string `json:"package,omitempty"`
	X           *int   `json:"x,omitempty"`
	Y           *int   `json:"y,omitempty"`
}

func parseUINodes(data []byte) ([]uiNode, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	nodes := make([]uiNode, 0, 32)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nodes, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "node" {
			continue
		}
		var node uiNode
		var bounds string
		for _, attribute := range start.Attr {
			switch attribute.Name.Local {
			case "text":
				node.Text = attribute.Value
			case "content-desc":
				node.Description = attribute.Value
			case "package":
				node.Package = attribute.Value
			case "bounds":
				bounds = attribute.Value
			}
		}
		if node.Text == "" && node.Description == "" {
			continue
		}
		coordinates := boundsPattern.FindAllString(bounds, -1)
		if len(coordinates) == 4 {
			x1, errX1 := strconv.Atoi(coordinates[0])
			y1, errY1 := strconv.Atoi(coordinates[1])
			x2, errX2 := strconv.Atoi(coordinates[2])
			y2, errY2 := strconv.Atoi(coordinates[3])
			if errX1 == nil && errY1 == nil && errX2 == nil && errY2 == nil {
				x, y := (x1+x2)/2, (y1+y2)/2
				node.X, node.Y = &x, &y
			}
		}
		nodes = append(nodes, node)
	}
}
