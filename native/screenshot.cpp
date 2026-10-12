/*
 * SurfaceFlinger capture and raw screencap WebP encoding for the Go server.
 * This translation unit is linked into the Go executable with Android NDK
 * cgo flags; it is not built or shipped as a separate helper program.
 */
#include "screenshot.h"

#include <android/gui/BnScreenCaptureListener.h>
#include <binder/ProcessState.h>
#include <gui/ScreenCaptureResults.h>
#include <ui/DisplayState.h>
#include <ui/GraphicBuffer.h>
#include <webp/encode.h>

#include <algorithm>
#include <chrono>
#include <cstdint>
#include <cstring>
#include <future>
#include <limits>
#include <mutex>
#include <vector>

namespace android {
// Android 12 private capture structures. Keep layout synchronized with
// frameworks/native/libs/gui/include/gui/LayerState.h.
struct CaptureArgs {
  const static int32_t UNSET_UID = -1;
  virtual ~CaptureArgs() = default;
  ui::PixelFormat pixelFormat{PIXEL_FORMAT_RGBA_8888};
  Rect sourceCrop;
  float frameScaleX{1};
  float frameScaleY{1};
  bool captureSecureLayers{false};
  int32_t uid{UNSET_UID};
  ui::Dataspace dataspace{ui::Dataspace::UNKNOWN};
  bool allowProtected{false};
  bool grayscale{false};
  virtual status_t write(Parcel& output) const;
  virtual status_t read(const Parcel& input);
};

struct DisplayCaptureArgs : CaptureArgs {
  sp<IBinder> displayToken;
  uint32_t width{0};
  uint32_t height{0};
  bool useIdentityTransform{false};
  status_t write(Parcel& output) const override;
  status_t read(const Parcel& input) override;
};

class ScreenshotClient {
 public:
  static status_t captureDisplay(
      const DisplayCaptureArgs& captureArgs,
      const sp<gui::IScreenCaptureListener>& captureListener);
};

class SurfaceComposerClient {
 public:
  static sp<IBinder> getInternalDisplayToken();
  static status_t getDisplayState(const sp<IBinder>& display,
                                 ui::DisplayState* state);
};
}  // namespace android

namespace {

using android::PIXEL_FORMAT_BGRA_8888;
using android::PIXEL_FORMAT_RGB_565;
using android::PIXEL_FORMAT_RGB_888;
using android::PIXEL_FORMAT_RGBA_8888;
using android::PIXEL_FORMAT_RGBX_8888;

class CaptureListener final : public android::gui::BnScreenCaptureListener {
 public:
  android::binder::Status onScreenCaptureCompleted(
      const android::gui::ScreenCaptureResults& result) override {
    resultsPromise.set_value(result);
    return android::binder::Status::ok();
  }

  std::future<android::gui::ScreenCaptureResults> takeFuture() {
    return resultsPromise.get_future();
  }

 private:
  std::promise<android::gui::ScreenCaptureResults> resultsPromise;
};

bool validQuality(int quality) { return quality >= 0 && quality <= 100; }

size_t bytesPerPixel(uint32_t format) {
  switch (format) {
    case PIXEL_FORMAT_RGBA_8888:
    case PIXEL_FORMAT_RGBX_8888:
    case PIXEL_FORMAT_BGRA_8888:
      return 4;
    case PIXEL_FORMAT_RGB_888:
      return 3;
    case PIXEL_FORMAT_RGB_565:
      return 2;
    default:
      return 0;
  }
}

void expandRgb565(uint16_t pixel, uint8_t* rgba) {
  const unsigned red = (pixel >> 11) & 0x1f;
  const unsigned green = (pixel >> 5) & 0x3f;
  const unsigned blue = pixel & 0x1f;
  rgba[0] = static_cast<uint8_t>((red * 255 + 15) / 31);
  rgba[1] = static_cast<uint8_t>((green * 255 + 31) / 63);
  rgba[2] = static_cast<uint8_t>((blue * 255 + 15) / 31);
  rgba[3] = 255;
}

uint32_t readU32(const uint8_t* data) {
  uint32_t value;
  std::memcpy(&value, data, sizeof(value));
  return value;
}

bool encodePixels(const uint8_t* pixels, uint32_t width, uint32_t height,
                  size_t stride, uint32_t format, int quality,
                  uint8_t** output, size_t* outputSize) {
  size_t size = 0;
  if (format == PIXEL_FORMAT_BGRA_8888) {
    size = WebPEncodeBGRA(pixels, static_cast<int>(width),
                          static_cast<int>(height), static_cast<int>(stride),
                          static_cast<float>(quality), output);
  } else {
    size = WebPEncodeRGBA(pixels, static_cast<int>(width),
                          static_cast<int>(height), static_cast<int>(stride),
                          static_cast<float>(quality), output);
  }
  if (size == 0 || *output == nullptr) {
    if (*output != nullptr) WebPFree(*output);
    *output = nullptr;
    return false;
  }
  *outputSize = size;
  return true;
}

}  // namespace

extern "C" int uia_capture_webp(int quality, int scaleDivisor,
                                uint8_t** output, size_t* outputSize) {
  if (output == nullptr || outputSize == nullptr || !validQuality(quality) ||
      (scaleDivisor != 1 && scaleDivisor != 2 && scaleDivisor != 4)) {
    return 2;
  }
  *output = nullptr;
  *outputSize = 0;

  static std::once_flag binderStarted;
  std::call_once(binderStarted, [] {
    android::ProcessState::self()->setThreadPoolMaxThreadCount(0);
    android::ProcessState::self()->startThreadPool();
  });

  using namespace android;
  const sp<IBinder> displayToken =
      SurfaceComposerClient::getInternalDisplayToken();
  if (displayToken == nullptr) return 10;

  ui::DisplayState displayState;
  if (SurfaceComposerClient::getDisplayState(displayToken, &displayState) !=
      NO_ERROR) {
    return 10;
  }
  const uint32_t displayWidth = static_cast<uint32_t>(
      displayState.layerStackSpaceRect.width);
  const uint32_t displayHeight = static_cast<uint32_t>(
      displayState.layerStackSpaceRect.height);
  if (displayWidth == 0 || displayHeight == 0) return 10;

  sp<CaptureListener> listener = new CaptureListener();
  auto resultsFuture = listener->takeFuture();
  DisplayCaptureArgs captureArgs;
  captureArgs.displayToken = displayToken;
  captureArgs.width = displayWidth / static_cast<uint32_t>(scaleDivisor);
  captureArgs.height = displayHeight / static_cast<uint32_t>(scaleDivisor);
  if (captureArgs.width == 0 || captureArgs.height == 0) return 10;

  const status_t status = ScreenshotClient::captureDisplay(captureArgs, listener);
  if (status != NO_ERROR) return 11;
  if (resultsFuture.wait_for(std::chrono::seconds(5)) !=
      std::future_status::ready) {
    return 12;
  }

  const gui::ScreenCaptureResults results = resultsFuture.get();
  if (results.result != NO_ERROR || !results.buffer) return 13;
  if (results.fence != nullptr &&
      results.fence->waitForever("uia-go-ndk-capture") != NO_ERROR) {
    return 14;
  }

  const sp<GraphicBuffer> buffer = results.buffer;
  const uint32_t width = buffer->getWidth();
  const uint32_t height = buffer->getHeight();
  const uint32_t stride = buffer->getStride();
  const uint64_t pixelCount = static_cast<uint64_t>(width) * height;
  if (width == 0 || height == 0 || stride < width ||
      pixelCount > 64u * 1024u * 1024u ||
      stride > static_cast<uint32_t>(std::numeric_limits<int>::max() / 4)) {
    return 15;
  }

  void* pixels = nullptr;
  if (buffer->lock(GraphicBuffer::USAGE_SW_READ_OFTEN, &pixels) != NO_ERROR ||
      pixels == nullptr) {
    return 16;
  }

  const uint32_t format = static_cast<uint32_t>(buffer->getPixelFormat());
  const bool encoded = encodePixels(static_cast<const uint8_t*>(pixels), width,
                                    height, static_cast<size_t>(stride) * 4,
                                    format, quality, output, outputSize);
  buffer->unlock();
  return encoded ? 0 : 17;
}

extern "C" int uia_encode_screencap(const uint8_t* input, size_t inputSize,
                                    int quality, uint8_t** output,
                                    size_t* outputSize, uint32_t* widthOut,
                                    uint32_t* heightOut) {
  if (input == nullptr || output == nullptr || outputSize == nullptr ||
      !validQuality(quality) || inputSize < 12) {
    return 1;
  }
  *output = nullptr;
  *outputSize = 0;

  const uint32_t width = readU32(input);
  const uint32_t height = readU32(input + 4);
  const uint32_t format = readU32(input + 8);
  const uint64_t pixelCount = static_cast<uint64_t>(width) * height;
  const size_t sourceBpp = bytesPerPixel(format);
  if (width == 0 || height == 0 || pixelCount > 64u * 1024u * 1024u ||
      sourceBpp == 0 || pixelCount > SIZE_MAX / sourceBpp) {
    return 2;
  }
  const size_t sourceSize = static_cast<size_t>(pixelCount) * sourceBpp;
  if (inputSize < 12 + sourceSize || pixelCount > SIZE_MAX / 4) return 3;

  const uint8_t* source = input + 12;
  std::vector<uint8_t> rgba;
  const uint8_t* pixels = source;
  if (format != PIXEL_FORMAT_RGBA_8888 && format != PIXEL_FORMAT_BGRA_8888) {
    rgba.resize(static_cast<size_t>(pixelCount) * 4);
    for (size_t i = 0; i < static_cast<size_t>(pixelCount); ++i) {
      if (format == PIXEL_FORMAT_RGBX_8888) {
        std::memcpy(rgba.data() + i * 4, source + i * 4, 3);
        rgba[i * 4 + 3] = 255;
      } else if (format == PIXEL_FORMAT_RGB_888) {
        std::memcpy(rgba.data() + i * 4, source + i * 3, 3);
        rgba[i * 4 + 3] = 255;
      } else {
        uint16_t pixel;
        std::memcpy(&pixel, source + i * 2, sizeof(pixel));
        expandRgb565(pixel, rgba.data() + i * 4);
      }
    }
    pixels = rgba.data();
  }

  if (!encodePixels(pixels, width, height, static_cast<size_t>(width) * 4,
                    format, quality, output, outputSize)) {
    return 4;
  }
  if (widthOut != nullptr) *widthOut = width;
  if (heightOut != nullptr) *heightOut = height;
  return 0;
}

extern "C" void uia_free_buffer(void* buffer) { WebPFree(buffer); }
