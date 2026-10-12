//go:build android && arm64 && cgo

package native

/*
#cgo CFLAGS: -I${SRCDIR} -I${SRCDIR}/include -I${SRCDIR}/generated/include -I${SRCDIR}/_build/deps/libwebp/src
#cgo CXXFLAGS: -std=c++17 -fno-rtti -DLIBBINDER_EXPORTED= -I${SRCDIR}/../tools/screenshot-native/include -I${SRCDIR} -I${SRCDIR}/include -I${SRCDIR}/generated/include -I${SRCDIR}/_build/deps/libwebp/src
#cgo android,arm64 LDFLAGS: -L${SRCDIR}/libs -Wl,-rpath-link,${SRCDIR}/libs -L${SRCDIR}/_build/deps/webp-build/src/.libs -L${SRCDIR}/_build/deps/webp-build/sharpyuv/.libs -Wl,--no-as-needed -lgui -lbinder -lui -lutils -lbase -lcutils -lsync -llog -lmediandk -lEGL -lGLESv2 -landroid -lwebp -lsharpyuv -lc++_static -lm
#include "screenshot.h"
#include "h264.h"
#include "touch.h"
*/
import "C"

import (
	"errors"
	"runtime"
	"strconv"
	"time"
	"unsafe"
)

func CaptureWebP(quality, scale int) ([]byte, error) {
	var output *C.uint8_t
	var outputSize C.size_t
	status := C.uia_capture_webp(C.int(quality), C.int(scale), &output, &outputSize)
	if status != 0 {
		return nil, errors.New("native SurfaceFlinger capture failed with status " + strconv.Itoa(int(status)))
	}
	defer C.uia_free_buffer(unsafe.Pointer(output))
	if output == nil || outputSize == 0 {
		return nil, errors.New("native capture returned an empty image")
	}
	return C.GoBytes(unsafe.Pointer(output), C.int(outputSize)), nil
}

func EncodeScreencap(raw []byte, quality int) ([]byte, int, int, error) {
	if len(raw) == 0 {
		return nil, 0, 0, errors.New("screencap returned no data")
	}
	var output *C.uint8_t
	var outputSize C.size_t
	var width, height C.uint32_t
	status := C.uia_encode_screencap(
		(*C.uint8_t)(unsafe.Pointer(&raw[0])), C.size_t(len(raw)), C.int(quality),
		&output, &outputSize, &width, &height,
	)
	runtime.KeepAlive(raw)
	if status != 0 {
		return nil, 0, 0, errors.New("native WebP encoding failed with status " + strconv.Itoa(int(status)))
	}
	defer C.uia_free_buffer(unsafe.Pointer(output))
	if output == nil || outputSize == 0 {
		return nil, 0, 0, errors.New("native WebP encoder returned an empty image")
	}
	return C.GoBytes(unsafe.Pointer(output), C.int(outputSize)), int(width), int(height), nil
}

var ErrH264NoFrame = errors.New("MediaCodec has no output frame ready")
var ErrH264Idle = errors.New("screen is unchanged")
var ErrH264DisplayChanged = errors.New("display size changed")

type H264Encoder struct {
	handle        *C.UiaH264Encoder
	DisplayWidth  int
	DisplayHeight int
}

func NewH264Encoder(scale, frameRate, bitRate int) (*H264Encoder, int, int, error) {
	var handle *C.UiaH264Encoder
	var width, height C.uint32_t
	var displayWidth, displayHeight C.uint32_t
	status := C.uia_h264_create(C.int(scale), C.int(frameRate), C.int(bitRate), &handle, &width, &height, &displayWidth, &displayHeight)
	if status != 0 || handle == nil {
		return nil, 0, 0, errors.New("MediaCodec H.264 encoder initialization failed: " + strconv.Itoa(int(status)))
	}
	return &H264Encoder{handle: handle, DisplayWidth: int(displayWidth), DisplayHeight: int(displayHeight)}, int(width), int(height), nil
}

func (encoder *H264Encoder) NextFrame() ([]byte, time.Duration, error) {
	if encoder == nil || encoder.handle == nil {
		return nil, 0, errors.New("H.264 encoder is closed")
	}
	var output *C.uint8_t
	var outputSize C.size_t
	var keyFrame C.int
	var presentationTime C.int64_t
	status := C.uia_h264_encode_next(encoder.handle, &output, &outputSize, &keyFrame, &presentationTime)
	if status == 1 {
		return nil, 0, ErrH264NoFrame
	}
	if status == 2 {
		return nil, 0, ErrH264Idle
	}
	if status == 3 {
		return nil, 0, ErrH264DisplayChanged
	}
	if status != 0 {
		return nil, 0, errors.New("H.264 frame capture/encode failed: " + strconv.Itoa(int(status)))
	}
	defer C.uia_h264_free_buffer(unsafe.Pointer(output))
	if output == nil || outputSize == 0 {
		return nil, 0, errors.New("MediaCodec returned an empty H.264 access unit")
	}
	return C.GoBytes(unsafe.Pointer(output), C.int(outputSize)), time.Duration(presentationTime) * time.Microsecond, nil
}

func (encoder *H264Encoder) Close() {
	if encoder == nil || encoder.handle == nil {
		return
	}
	C.uia_h264_destroy(encoder.handle)
	encoder.handle = nil
}

func (encoder *H264Encoder) Wake(keyFrame bool) error {
	if encoder == nil || encoder.handle == nil {
		return errors.New("H.264 encoder is closed")
	}
	request := C.int(0)
	if keyFrame {
		request = 1
	}
	if status := C.uia_h264_wake(encoder.handle, request); status != 0 {
		return errors.New("H.264 wake failed: " + strconv.Itoa(int(status)))
	}
	return nil
}

func TouchDown(width, height, x, y int) error {
	status := C.uia_touch_down(C.int(width), C.int(height), C.int(x), C.int(y))
	return touchStatusError(status)
}

func TouchMove(x, y int) error {
	return touchStatusError(C.uia_touch_move(C.int(x), C.int(y)))
}

func TouchUp(x, y int) error {
	return touchStatusError(C.uia_touch_up(C.int(x), C.int(y)))
}

func TouchCancel() error { return touchStatusError(C.uia_touch_cancel()) }

func touchStatusError(status C.int) error {
	if status == 0 {
		return nil
	}
	return errors.New("NDK uinput touchscreen failed with status " + strconv.Itoa(int(status)))
}
