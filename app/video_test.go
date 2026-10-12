package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
)

type failingVideoEncoder struct{ closed bool }

func TestValidVideoBitRates(t *testing.T) {
	for _, rate := range []int{250_000, 500_000, 1_000_000, 2_000_000} {
		if !validVideoBitRate(rate) {
			t.Errorf("supported bit rate rejected: %d", rate)
		}
	}
	for _, rate := range []int{-1, 0, 125_000, 124_999, 300_000, 2_000_001} {
		if validVideoBitRate(rate) {
			t.Errorf("unsupported bit rate accepted: %d", rate)
		}
	}
}

func (*failingVideoEncoder) DisplaySize() (int, int) { return 1080, 1920 }

func (*failingVideoEncoder) Wake(bool) error { return nil }

func TestVideoFeedbackRequestsKeyFrames(t *testing.T) {
	requests := make(chan struct{}, 1)
	var last time.Time
	now := time.Now()
	queueKeyFrameRequest([]rtcp.Packet{&rtcp.ReceiverReport{}}, requests, now, &last)
	if len(requests) != 0 {
		t.Fatal("receiver reports must not request keyframes")
	}
	queueKeyFrameRequest([]rtcp.Packet{&rtcp.PictureLossIndication{}}, requests, now, &last)
	if len(requests) != 1 {
		t.Fatal("PLI did not request a keyframe")
	}
	<-requests
	queueKeyFrameRequest([]rtcp.Packet{&rtcp.FullIntraRequest{}}, requests, now.Add(100*time.Millisecond), &last)
	if len(requests) != 0 {
		t.Fatal("feedback flood was not throttled")
	}
	queueKeyFrameRequest([]rtcp.Packet{&rtcp.FullIntraRequest{}}, requests, now.Add(time.Second), &last)
	if len(requests) != 1 {
		t.Fatal("FIR did not request a keyframe")
	}
	queueKeyFrameRequest([]rtcp.Packet{&rtcp.PictureLossIndication{}}, requests, now.Add(2*time.Second), &last)
	if len(requests) != 1 {
		t.Fatal("requests were not coalesced")
	}
}

type wakeVideoEncoder struct {
	failingVideoEncoder
	wakes []bool
}

func (e *wakeVideoEncoder) Wake(keyFrame bool) error { e.wakes = append(e.wakes, keyFrame); return nil }

func TestIdleCaptureWakesImmediately(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	encoder := &wakeVideoEncoder{}
	session := &videoSession{encoder: encoder, stop: make(chan struct{}), keyFrames: make(chan struct{}, 1), activity: make(chan struct{}, 1)}
	session.keyFrames <- struct{}{}
	if !app.waitVideoCapture(session, time.Hour) {
		t.Fatal("keyframe did not wake capture")
	}
	session.activity <- struct{}{}
	if !app.waitVideoCapture(session, time.Hour) {
		t.Fatal("touch did not wake capture")
	}
	if len(encoder.wakes) != 2 || !encoder.wakes[0] || encoder.wakes[1] {
		t.Fatal("incorrect wake requests")
	}
	close(session.stop)
	if app.waitVideoCapture(session, time.Hour) {
		t.Fatal("stopped session continued capturing")
	}
}

func TestActiveCaptureDoesNotAllocateTimers(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	session := &videoSession{stop: make(chan struct{}), keyFrames: make(chan struct{}, 1), activity: make(chan struct{}, 1)}
	allocations := testing.AllocsPerRun(100, func() {
		if !app.waitVideoCapture(session, 0) {
			t.Fatal("active capture stopped")
		}
	})
	if allocations != 0 {
		t.Fatalf("active capture allocated %.0f objects", allocations)
	}
}

func TestStoppedCaptureIgnoresPendingActivity(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	encoder := &wakeVideoEncoder{}
	session := &videoSession{encoder: encoder, stop: make(chan struct{}), activity: make(chan struct{}, 1)}
	session.activity <- struct{}{}
	close(session.stop)
	if app.waitVideoCapture(session, 0) || len(encoder.wakes) != 0 {
		t.Fatal("stopped capture processed pending activity")
	}
}

func TestVideoShutdownRejectsOffers(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	app.closeVideoSessions()
	request := httptest.NewRequest(http.MethodPost, "/webrtc/offer", strings.NewReader(`{"type":"offer","sdp":"v=0"}`))
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("shutdown accepted offer: %d", response.Code)
	}
}

func TestVideoRTPTimestampsMatchCaptureTime(t *testing.T) {
	packetizer := rtp.NewPacketizer(1200, 96, 0, &codecs.H264Payloader{}, rtp.NewFixedSequencer(0), 90000)
	var clock videoRTPClock
	frame := append([]byte{0, 0, 0, 1, 0x65}, make([]byte, 4000)...)
	origin := 5 * time.Hour
	for _, elapsed := range []time.Duration{0, time.Second, 1033 * time.Millisecond, 1100 * time.Millisecond, 40 * time.Hour} {
		packets := packetizeVideoFrame(packetizer, &clock, frame, origin+elapsed)
		if len(packets) < 2 {
			t.Fatal("frame was not fragmented")
		}
		ticks := uint64(elapsed/time.Second)*90000 + uint64(elapsed%time.Second)*90000/uint64(time.Second)
		for _, packet := range packets {
			if got := packet.Timestamp - clock.base; got != uint32(ticks) {
				t.Fatalf("capture %s: RTP ticks %d, want %d", elapsed, got, uint32(ticks))
			}
		}
	}
}

func TestH264VideoNegotiationValidation(t *testing.T) {
	header := "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n"
	video := "m=video 9 UDP/TLS/RTP/SAVPF 96\r\na=sendonly\r\na=rtpmap:96 H264/90000\r\n"
	if !hasSendingH264Video(header + video) {
		t.Fatal("valid H.264 video rejected")
	}
	for _, description := range []string{
		"invalid", header + "m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=rtpmap:111 opus/48000/2\r\n",
		header + strings.ReplaceAll(video, "sendonly", "recvonly"),
		header + strings.ReplaceAll(video, "sendonly", "inactive"),
		header + strings.ReplaceAll(video, "video 9", "video 0"),
		header + strings.ReplaceAll(video, "H264", "VP8"),
		header + "a=recvonly\r\n" + strings.ReplaceAll(video, "a=sendonly\r\n", ""),
	} {
		if hasSendingH264Video(description) {
			t.Fatalf("unusable video accepted: %q", description)
		}
	}
}

func TestAudioOnlyOfferDoesNotKickOldVideo(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	client, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	session := &videoSession{peer: old, stop: make(chan struct{}), done: make(chan struct{})}
	app.videoSessions["old"] = session
	body, _ := json.Marshal(videoOfferRequest{Type: "offer", SDP: offer.SDP})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/webrtc/offer", bytes.NewReader(body)))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "H.264 video track") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	select {
	case <-session.stop:
		t.Fatal("audio-only offer kicked old video")
	default:
	}
}

func (*failingVideoEncoder) NextFrame() ([]byte, time.Duration, error) {
	return nil, 0, errors.New("capture failed")
}
func (e *failingVideoEncoder) Close() { e.closed = true }

func TestVideoCaptureFailureReleasesResources(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	encoder := &failingVideoEncoder{}
	session := &videoSession{id: "test", peer: peer, encoder: encoder,
		stop: make(chan struct{}), connected: make(chan struct{}), done: make(chan struct{})}
	close(session.connected)
	app.videoSessions[session.id] = session
	go app.runVideoSession(session)
	select {
	case <-session.done:
	case <-time.After(3 * time.Second):
		t.Fatal("session did not finish")
	}
	if !encoder.closed || peer.ConnectionState() != webrtc.PeerConnectionStateClosed {
		t.Fatal("video resources leaked")
	}
	app.videoMu.Lock()
	defer app.videoMu.Unlock()
	if len(app.videoSessions) != 0 {
		t.Fatal("failed session still registered")
	}
}

func TestVideoCloseRejectsMalformedRequests(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	for _, body := range []string{`{}`, `{"sessionId":"x","unknown":1}`, `{"sessionId":"x"} {}`, `{"sessionId":3}`} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/webrtc/close", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("accepted malformed request: %s", body)
		}
	}
}

func TestVideoCloseHonorsContext(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	app.videoSessions["test"] = &videoSession{peer: peer, stop: make(chan struct{}), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/webrtc/close", strings.NewReader(`{"sessionId":"test"}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("unexpected response: %d", response.Code)
	}
}

func TestNewVideoSessionReleasesOldEncoder(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	encoder := &failingVideoEncoder{}
	session := &videoSession{id: "old", peer: peer, encoder: encoder,
		stop: make(chan struct{}), connected: make(chan struct{}), done: make(chan struct{})}
	app.videoSessions[session.id] = session
	go app.runVideoSession(session)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.replaceVideoSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if !encoder.closed || peer.ConnectionState() != webrtc.PeerConnectionStateClosed {
		t.Fatal("old encoder/peer not released")
	}
	app.videoMu.Lock()
	defer app.videoMu.Unlock()
	if len(app.videoSessions) != 0 {
		t.Fatal("old session still registered")
	}
}

func TestVideoTakeoverHonorsTimeout(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	app.videoSessions["old"] = &videoSession{peer: peer, stop: make(chan struct{}), done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := app.replaceVideoSessions(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvalidOfferDoesNotKickOldVideo(t *testing.T) {
	app := newServer(defaultConfig(), ".", t.TempDir(), log.New(io.Discard, "", 0))
	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	session := &videoSession{peer: peer, stop: make(chan struct{}), done: make(chan struct{})}
	app.videoSessions["old"] = session
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/webrtc/offer", strings.NewReader(`{"type":"offer","sdp":"invalid-sdp"}`))
	app.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid SDP returned %d", response.Code)
	}
	select {
	case <-session.stop:
		t.Fatal("invalid offer kicked old session")
	default:
	}
}
