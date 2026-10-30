package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

const (
	videoCaptureScale     = 2
	videoBitRate          = 1_000_000
	maximumVideoFrameRate = 30
)

type videoOfferRequest struct {
	Type     string `json:"type"`
	SDP      string `json:"sdp"`
	BitRate  int    `json:"bitRate"`
	Scale    int    `json:"scale"`
	ClientID string `json:"clientId"`
}

type videoOfferResponse struct {
	OK            bool   `json:"ok"`
	SessionID     string `json:"sessionId"`
	Type          string `json:"type"`
	SDP           string `json:"sdp"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Scale         int    `json:"scale"`
	BitRate       int    `json:"bitRate"`
	DisplayWidth  int    `json:"displayWidth"`
	DisplayHeight int    `json:"displayHeight"`
}

type videoCloseRequest struct {
	SessionID string `json:"sessionId"`
}

type videoSession struct {
	id            string
	peer          *webrtc.PeerConnection
	track         *webrtc.TrackLocalStaticRTP
	encoder       h264Encoder
	stop          chan struct{}
	connected     chan struct{}
	done          chan struct{}
	closeOnce     sync.Once
	connectedOnce sync.Once
	keyFrames     chan struct{}
	activity      chan struct{}
	display       atomic.Pointer[webrtc.DataChannel]
	info          videoDisplayInfo
	bitRate       int
}

type videoDisplayInfo struct {
	Width         int `json:"width"`
	Height        int `json:"height"`
	DisplayWidth  int `json:"displayWidth"`
	DisplayHeight int `json:"displayHeight"`
	Scale         int `json:"scale"`
}

func validVideoBitRate(bitRate int) bool {
	return bitRate == 1_000_000 || bitRate == 2_000_000 || bitRate == 5_000_000
}

func validVideoScale(scale int) bool {
	return scale == 2 || scale == 3 || scale == 4
}

func (s *serverApp) handleVideoOffer(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request videoOfferRequest
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid WebRTC offer"})
		return
	}
	if request.Type != "offer" || request.SDP == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "a WebRTC offer is required"})
		return
	}
	var trailing any
	if request.Scale == 0 {
		request.Scale = videoCaptureScale
	}
	if !validVideoScale(request.Scale) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "scale must be 2, 3, or 4"})
		return
	}
	if request.BitRate == 0 {
		request.BitRate = videoBitRate
	}
	if !validVideoBitRate(request.BitRate) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bitRate must be 1000000, 2000000, or 5000000"})
		return
	}
	if !validControlClientID(request.ClientID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "a valid clientId is required"})
		return
	}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid WebRTC offer"})
		return
	}

	// Serialize takeovers: validate the new peer before releasing the old codec.
	s.videoOfferMu.Lock()
	defer s.videoOfferMu.Unlock()
	if r.Context().Err() != nil {
		return
	}
	if s.videoClosing {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "video service is shutting down"})
		return
	}
	if !s.isActiveControlClient(request.ClientID) {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "this client has been replaced by a newer connection"})
		return
	}
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "WebRTC peer initialization failed"})
		return
	}
	var encoder h264Encoder
	started := false
	defer func() {
		if !started {
			if encoder != nil {
				encoder.Close()
			}
			_ = peer.Close()
		}
	}()
	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000},
		"screen", "screenbridge-screen",
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "H.264 track initialization failed"})
		return
	}
	sender, err := peer.AddTrack(track)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "H.264 track setup failed"})
		return
	}

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: request.SDP}
	if err = peer.SetRemoteDescription(offer); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "WebRTC offer was rejected"})
		return
	}
	answer, err := peer.CreateAnswer(nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "WebRTC answer creation failed"})
		return
	}
	if !hasSendingH264Video(answer.SDP) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "a receiving H.264 video track is required"})
		return
	}
	gatheringComplete := webrtc.GatheringCompletePromise(peer)
	if err = peer.SetLocalDescription(answer); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "WebRTC ICE setup failed"})
		return
	}
	select {
	case <-gatheringComplete:
	case <-r.Context().Done():
		return
	case <-time.After(12 * time.Second):
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"ok": false, "error": "WebRTC ICE gathering timed out"})
		return
	}
	if err = r.Context().Err(); err != nil {
		return
	}
	localDescription := peer.LocalDescription()
	if localDescription == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "WebRTC answer is unavailable"})
		return
	}
	sessionID, err := newVideoSessionID()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "video session initialization failed"})
		return
	}
	if err := s.replaceVideoSessions(r.Context()); err != nil {
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"ok": false, "error": "previous video stream did not stop in time"})
		return
	}
	var width, height int
	encoder, width, height, err = newH264Encoder(request.Scale, maximumVideoFrameRate, request.BitRate)
	if err != nil {
		s.logger.Printf("initialize H.264 encoder: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "H.264 encoder initialization failed"})
		return
	}
	if r.Context().Err() != nil {
		return
	}
	displayWidth, displayHeight := encoder.DisplaySize()
	session := &videoSession{
		id: sessionID, peer: peer, track: track,
		encoder: encoder, stop: make(chan struct{}), connected: make(chan struct{}), done: make(chan struct{}),
		keyFrames: make(chan struct{}, 1), activity: make(chan struct{}, 1),
		info:    videoDisplayInfo{Width: width, Height: height, DisplayWidth: displayWidth, DisplayHeight: displayHeight, Scale: request.Scale},
		bitRate: request.BitRate,
	}
	peer.OnDataChannel(func(channel *webrtc.DataChannel) {
		if channel.Label() == "display" {
			session.display.Store(channel)
		}
	})
	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			session.connectedOnce.Do(func() { close(session.connected) })
		}
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			go session.close()
		}
	})
	s.videoMu.Lock()
	s.videoSessions[sessionID] = session
	s.videoMu.Unlock()
	started = true
	go s.runVideoSession(session)
	go readVideoFeedback(sender, session.keyFrames)

	writeJSON(w, http.StatusOK, videoOfferResponse{
		OK: true, SessionID: sessionID, Type: "answer", SDP: localDescription.SDP,
		Width: width, Height: height, Scale: request.Scale,
		BitRate:      request.BitRate,
		DisplayWidth: displayWidth, DisplayHeight: displayHeight,
	})
}

func hasSendingH264Video(description string) bool {
	var parsed sdp.SessionDescription
	if parsed.Unmarshal([]byte(description)) != nil {
		return false
	}
	for _, media := range parsed.MediaDescriptions {
		if media.MediaName.Media != "video" || media.MediaName.Port.Value == 0 {
			continue
		}
		canSend := true
		for _, attributes := range [][]sdp.Attribute{parsed.Attributes, media.Attributes} {
			for _, attribute := range attributes {
				switch attribute.Key {
				case "inactive", "recvonly":
					canSend = false
				case "sendonly", "sendrecv":
					canSend = true
				}
			}
		}
		if !canSend {
			continue
		}
		for _, attribute := range media.Attributes {
			fields := strings.Fields(attribute.Value)
			if attribute.Key == "rtpmap" && len(fields) == 2 && strings.EqualFold(fields[1], "H264/90000") {
				for _, format := range media.MediaName.Formats {
					if format == fields[0] {
						return true
					}
				}
			}
		}
	}
	return false
}

func (s *serverApp) replaceVideoSessions(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.videoMu.Lock()
	sessions := make([]*videoSession, 0, len(s.videoSessions))
	for _, session := range s.videoSessions {
		sessions = append(sessions, session)
	}
	s.videoMu.Unlock()
	for _, session := range sessions {
		session.close()
	}
	for _, session := range sessions {
		select {
		case <-session.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func (s *serverApp) handleVideoClose(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request videoCloseRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "sessionId is required"})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid video close request"})
		return
	}
	s.videoMu.Lock()
	session := s.videoSessions[request.SessionID]
	s.videoMu.Unlock()
	if session != nil {
		session.close()
		select {
		case <-session.done:
		case <-r.Context().Done():
			writeJSON(w, http.StatusGatewayTimeout, map[string]any{"ok": false, "error": "video close timed out"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *serverApp) runVideoSession(session *videoSession) {
	defer func() {
		session.close()
		session.encoder.Close()
		s.removeVideoSession(session.id, session)
		close(session.done)
	}()
	select {
	case <-session.stop:
		return
	case <-session.connected:
	case <-time.After(30 * time.Second):
		s.logger.Printf("WebRTC video session %s did not connect", session.id)
		return
	}
	packetizer := rtp.NewPacketizer(1200, 96, 0, &codecs.H264Payloader{}, rtp.NewRandomSequencer(), 90000)
	var clock videoRTPClock
	var lastInfo videoDisplayInfo
	var delay time.Duration
	frameInterval := time.Second / time.Duration(maximumVideoFrameRate)
	var nextFrameAt time.Time
	for {
		if !s.waitVideoCapture(session, delay) {
			return
		}
		if !waitUntilVideoFrame(session, nextFrameAt) {
			return
		}
		frameStartedAt := time.Now()
		frame, timestamp, err := session.encoder.NextFrame()
		delay = 0
		if errors.Is(err, errH264DisplayChanged) {
			session.encoder.Close()
			encoder, width, height, createErr := newH264Encoder(session.info.Scale, maximumVideoFrameRate, session.bitRate)
			if createErr != nil {
				s.logger.Printf("resize video encoder: %v", createErr)
				return
			}
			session.encoder = encoder
			displayWidth, displayHeight := encoder.DisplaySize()
			session.info = videoDisplayInfo{Width: width, Height: height, DisplayWidth: displayWidth, DisplayHeight: displayHeight, Scale: session.info.Scale}
			continue
		}
		if channel := session.display.Load(); channel != nil && channel.ReadyState() == webrtc.DataChannelStateOpen && session.info != lastInfo {
			data, _ := json.Marshal(session.info)
			if sendErr := channel.SendText(string(data)); sendErr != nil {
				s.logger.Printf("send display metadata: %v", sendErr)
				return
			}
			lastInfo = session.info
		}
		if errors.Is(err, errH264Idle) {
			delay = 200 * time.Millisecond
			continue
		}
		if errors.Is(err, errH264NoFrame) {
			delay = time.Millisecond
			continue
		}
		if err != nil {
			s.logger.Printf("video frame capture failed: %v", err)
			return
		}
		for _, packet := range packetizeVideoFrame(packetizer, &clock, frame, timestamp) {
			if err := session.track.WriteRTP(packet); err != nil {
				s.logger.Printf("send H.264 video packet: %v", err)
				return
			}
		}
		nextFrameAt = frameStartedAt.Add(frameInterval)
	}
}

func waitUntilVideoFrame(session *videoSession, deadline time.Time) bool {
	if deadline.IsZero() {
		return true
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		select {
		case <-session.stop:
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-session.stop:
		return false
	case <-timer.C:
		return true
	}
}

type videoRTPClock struct {
	origin      time.Duration
	base        uint32
	initialized bool
}

func packetizeVideoFrame(packetizer rtp.Packetizer, clock *videoRTPClock, frame []byte, capturedAt time.Duration) []*rtp.Packet {
	packets := packetizer.Packetize(frame, 0)
	if len(packets) == 0 {
		return nil
	}
	if !clock.initialized {
		clock.origin, clock.base, clock.initialized = capturedAt, packets[0].Timestamp, true
	}
	elapsed := capturedAt - clock.origin
	if elapsed < 0 {
		return nil
	}
	// Split seconds/remainder to avoid overflowing Duration on long sessions.
	ticks := uint64(elapsed/time.Second)*90000 + uint64(elapsed%time.Second)*90000/uint64(time.Second)
	for _, packet := range packets {
		packet.Timestamp = clock.base + uint32(ticks)
	}
	return packets
}

func (s *serverApp) waitVideoCapture(session *videoSession, delay time.Duration) bool {
	// Active capture does not allocate a timer. Stop always wins at entry.
	select {
	case <-session.stop:
		return false
	default:
	}
	keyFrame := false
	select {
	case <-session.stop:
		return false
	case <-session.keyFrames:
		keyFrame = true
	case <-session.activity:
	default:
		if delay <= 0 {
			return true
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-session.stop:
			return false
		case <-timer.C:
			return true
		case <-session.keyFrames:
			keyFrame = true
		case <-session.activity:
		}
	}
	if err := session.encoder.Wake(keyFrame); err != nil {
		s.logger.Printf("wake video encoder: %v", err)
	}
	return true
}

func readVideoFeedback(sender *webrtc.RTPSender, requests chan struct{}) {
	var lastRequest time.Time
	for {
		packets, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		queueKeyFrameRequest(packets, requests, time.Now(), &lastRequest)
	}
}

func queueKeyFrameRequest(packets []rtcp.Packet, requests chan struct{}, now time.Time, last *time.Time) {
	for _, packet := range packets {
		switch packet.(type) {
		case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
			if !last.IsZero() && now.Sub(*last) < 500*time.Millisecond {
				return
			}
			select {
			case requests <- struct{}{}:
				*last = now
			default:
			}
			return
		}
	}
}

func (s *serverApp) notifyVideoActivity() {
	s.videoMu.Lock()
	defer s.videoMu.Unlock()
	for _, session := range s.videoSessions {
		select {
		case session.activity <- struct{}{}:
		default:
		}
	}
}

func (s *serverApp) removeVideoSession(id string, session *videoSession) {
	s.videoMu.Lock()
	if s.videoSessions[id] == session {
		delete(s.videoSessions, id)
	}
	s.videoMu.Unlock()
}

func (session *videoSession) close() {
	session.closeOnce.Do(func() {
		close(session.stop)
		_ = session.peer.Close()
	})
}

func newVideoSessionID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func (s *serverApp) closeVideoSessions() {
	s.videoOfferMu.Lock()
	defer s.videoOfferMu.Unlock()
	s.videoClosing = true
	_ = s.replaceVideoSessions(context.Background())
}
