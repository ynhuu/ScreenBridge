#include "h264.h"
#include "gpu_capture.h"
#include "frame_activity.h"

#include <android/gui/BnScreenCaptureListener.h>
#include <binder/ProcessState.h>
#include <gui/ScreenCaptureResults.h>
#include <ui/DisplayState.h>
#include <ui/GraphicBuffer.h>
#include <media/NdkMediaCodec.h>
#include <media/NdkMediaFormat.h>

#include <algorithm>
#include <chrono>
#include <cstdint>
#include <cstring>
#include <future>
#include <iterator>
#include <mutex>
#include <memory>
#include <new>
#include <cstdlib>
#include <vector>

namespace android {
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

using android::gui::ScreenCaptureResults;

class CaptureListener final : public android::gui::BnScreenCaptureListener {
 public:
  android::binder::Status onScreenCaptureCompleted(
      const ScreenCaptureResults& result) override {
    promise.set_value(result);
    return android::binder::Status::ok();
  }
  std::future<ScreenCaptureResults> future() { return promise.get_future(); }

 private:
  std::promise<ScreenCaptureResults> promise;
};

constexpr uint8_t kAnnexBStartCode[] = {0, 0, 0, 1};

void startBinderThreadPool() {
  static std::once_flag started;
  std::call_once(started, [] {
    android::ProcessState::self()->setThreadPoolMaxThreadCount(0);
    android::ProcessState::self()->startThreadPool();
  });
}

bool hasAnnexBStartCode(const uint8_t* data, size_t size) {
  return size >= 3 && data[0] == 0 && data[1] == 0 &&
         (data[2] == 1 || (size >= 4 && data[2] == 0 && data[3] == 1));
}

void appendNal(const uint8_t* data, size_t size, std::vector<uint8_t>* output) {
  if (size == 0) return;
  output->insert(output->end(), std::begin(kAnnexBStartCode),
                 std::end(kAnnexBStartCode));
  output->insert(output->end(), data, data + size);
}

bool appendAvcDecoderConfig(const uint8_t* data, size_t size,
                            std::vector<uint8_t>* output) {
  if (size < 7 || data[0] != 1) return false;
  size_t offset = 5;
  const uint8_t spsCount = data[offset++] & 0x1f;
  for (uint8_t i = 0; i < spsCount; ++i) {
    if (offset + 2 > size) return false;
    const size_t naluSize = (static_cast<size_t>(data[offset]) << 8) | data[offset + 1];
    offset += 2;
    if (naluSize == 0 || offset + naluSize > size) return false;
    appendNal(data + offset, naluSize, output);
    offset += naluSize;
  }
  if (offset >= size) return true;
  const uint8_t ppsCount = data[offset++];
  for (uint8_t i = 0; i < ppsCount; ++i) {
    if (offset + 2 > size) return false;
    const size_t naluSize = (static_cast<size_t>(data[offset]) << 8) | data[offset + 1];
    offset += 2;
    if (naluSize == 0 || offset + naluSize > size) return false;
    appendNal(data + offset, naluSize, output);
    offset += naluSize;
  }
  return true;
}

void appendH264(const uint8_t* data, size_t size,
                std::vector<uint8_t>* output) {
  if (data == nullptr || size == 0) return;
  if (hasAnnexBStartCode(data, size)) {
    size_t offset = 0;
    while (offset < size) {
      size_t prefix = 0;
      while (offset + 3 <= size) {
        if (data[offset] == 0 && data[offset + 1] == 0 && data[offset + 2] == 1) {
          prefix = 3;
          break;
        }
        if (offset + 4 <= size && data[offset] == 0 && data[offset + 1] == 0 &&
            data[offset + 2] == 0 && data[offset + 3] == 1) {
          prefix = 4;
          break;
        }
        ++offset;
      }
      if (prefix == 0) break;
      const size_t nalStart = offset + prefix;
      size_t next = nalStart;
      while (next < size && !hasAnnexBStartCode(data + next, size - next)) ++next;
      appendNal(data + nalStart, next - nalStart, output);
      offset = next;
    }
    return;
  }

  if (appendAvcDecoderConfig(data, size, output)) return;

  // MediaCodec commonly emits four-byte length-prefixed access units.
  size_t offset = 0;
  std::vector<uint8_t> parsed;
  while (offset + 4 <= size) {
    const size_t naluSize = (static_cast<size_t>(data[offset]) << 24) |
                            (static_cast<size_t>(data[offset + 1]) << 16) |
                            (static_cast<size_t>(data[offset + 2]) << 8) |
                            data[offset + 3];
    offset += 4;
    if (naluSize == 0 || offset + naluSize > size) {
      parsed.clear();
      break;
    }
    appendNal(data + offset, naluSize, &parsed);
    offset += naluSize;
  }
  if (offset == size && !parsed.empty()) {
    output->insert(output->end(), parsed.begin(), parsed.end());
  } else {
    appendNal(data, size, output);
  }
}

void appendFormatCsd(AMediaFormat* format, std::vector<uint8_t>* config) {
  for (const char* key : {"csd-0", "csd-1", "csd-2"}) {
    void* data = nullptr;
    size_t size = 0;
    if (AMediaFormat_getBuffer(format, key, &data, &size) && data != nullptr && size > 0) {
      std::vector<uint8_t> normalized;
      appendH264(static_cast<const uint8_t*>(data), size, &normalized);
      config->insert(config->end(), normalized.begin(), normalized.end());
    }
  }
}

int captureGraphicBuffer(const android::sp<android::IBinder>& token, int scaleDivisor,
                         android::sp<android::GraphicBuffer>* output,
                         uint32_t* displayWidthOut, uint32_t* displayHeightOut) {
  using namespace android;
  if (token == nullptr) return -10;
  ui::DisplayState displayState;
  if (SurfaceComposerClient::getDisplayState(token, &displayState) != NO_ERROR) return -10;
  const uint32_t displayWidth = static_cast<uint32_t>(displayState.layerStackSpaceRect.width);
  const uint32_t displayHeight = static_cast<uint32_t>(displayState.layerStackSpaceRect.height);
  if (displayWidth == 0 || displayHeight == 0) return -10;
  *displayWidthOut = displayWidth;
  *displayHeightOut = displayHeight;

  sp<CaptureListener> listener = new CaptureListener();
  auto resultFuture = listener->future();
  DisplayCaptureArgs args;
  args.displayToken = token;
  args.width = displayWidth / static_cast<uint32_t>(scaleDivisor);
  args.height = displayHeight / static_cast<uint32_t>(scaleDivisor);
  args.width &= ~1u;
  args.height &= ~1u;
  if (args.width == 0 || args.height == 0) return -10;
  const status_t captureStatus = ScreenshotClient::captureDisplay(args, listener);
  if (captureStatus != NO_ERROR) return -11;
  if (resultFuture.wait_for(std::chrono::seconds(2)) != std::future_status::ready) return -12;
  const ScreenCaptureResults result = resultFuture.get();
  if (result.result != NO_ERROR || !result.buffer) return -13;
  if (result.fence != nullptr && result.fence->wait(2000) != NO_ERROR) return -14;
  *output = result.buffer;
  return 0;
}

}  // namespace

struct UiaH264Encoder {
  AMediaCodec* codec = nullptr;
  android::sp<android::IBinder> displayToken;
  ANativeWindow* window = nullptr;
  std::unique_ptr<GpuCapture> gpu;
  FrameActivity activity;
  std::vector<uint8_t> probe;
  uint32_t width = 0;
  uint32_t height = 0;
  uint32_t displayWidth = 0, displayHeight = 0;
  int scaleDivisor = 2;
  int64_t lastPresentationTimeUs = -1;
  std::vector<uint8_t> codecConfig;
  std::mutex mutex;
  ~UiaH264Encoder() {
    gpu.reset();
    if (window) ANativeWindow_release(window);
    if (codec) { AMediaCodec_stop(codec); AMediaCodec_delete(codec); }
  }
};

extern "C" int uia_h264_create(int scaleDivisor, int frameRate, int bitRate,
                               UiaH264Encoder** output, uint32_t* widthOut,
                               uint32_t* heightOut, uint32_t* displayWidthOut,
                               uint32_t* displayHeightOut) {
  if (output == nullptr || widthOut == nullptr || heightOut == nullptr ||
      displayWidthOut == nullptr || displayHeightOut == nullptr ||
      (scaleDivisor != 2 && scaleDivisor != 3 && scaleDivisor != 4) ||
      frameRate < 1 || frameRate > 30 || bitRate < 100000 || bitRate > 12000000) {
    return -1;
  }
  *output = nullptr;

  startBinderThreadPool();
  android::ui::DisplayState displayState;
  auto token = android::SurfaceComposerClient::getInternalDisplayToken();
  if (token == nullptr ||
      android::SurfaceComposerClient::getDisplayState(token, &displayState) != android::NO_ERROR) {
    return -2;
  }
  uint32_t width = static_cast<uint32_t>(displayState.layerStackSpaceRect.width) /
                   static_cast<uint32_t>(scaleDivisor);
  uint32_t height = static_cast<uint32_t>(displayState.layerStackSpaceRect.height) /
                    static_cast<uint32_t>(scaleDivisor);
  width &= ~1u;
  height &= ~1u;
  if (width < 16 || height < 16) return -2;

  std::unique_ptr<UiaH264Encoder> encoder(new (std::nothrow) UiaH264Encoder());
  if (encoder == nullptr) return -3;
  encoder->width = width;
  encoder->height = height;
  encoder->scaleDivisor = scaleDivisor;
  encoder->displayToken = token;
  encoder->displayWidth = static_cast<uint32_t>(displayState.layerStackSpaceRect.width);
  encoder->displayHeight = static_cast<uint32_t>(displayState.layerStackSpaceRect.height);
  encoder->codec = AMediaCodec_createEncoderByType("video/avc");
  if (encoder->codec == nullptr) {
    return -4;
  }

  AMediaFormat* format = AMediaFormat_new();
  if (format == nullptr) {
    return -5;
  }
  AMediaFormat_setString(format, AMEDIAFORMAT_KEY_MIME, "video/avc");
  AMediaFormat_setInt32(format, AMEDIAFORMAT_KEY_WIDTH, static_cast<int32_t>(width));
  AMediaFormat_setInt32(format, AMEDIAFORMAT_KEY_HEIGHT, static_cast<int32_t>(height));
  AMediaFormat_setInt32(format, AMEDIAFORMAT_KEY_COLOR_FORMAT, 0x7f000789);  // COLOR_FormatSurface
  AMediaFormat_setInt32(format, AMEDIAFORMAT_KEY_BIT_RATE, bitRate);
  // MediaCodecInfo.EncoderCapabilities.BITRATE_MODE_CBR = 2.
  AMediaFormat_setInt32(format, AMEDIAFORMAT_KEY_BITRATE_MODE, 2);
  AMediaFormat_setInt32(format, AMEDIAFORMAT_KEY_FRAME_RATE, frameRate);
  AMediaFormat_setInt32(format, AMEDIAFORMAT_KEY_I_FRAME_INTERVAL, 2);

  media_status_t status = AMediaCodec_configure(
      encoder->codec, format, nullptr, nullptr, AMEDIACODEC_CONFIGURE_FLAG_ENCODE);
  AMediaFormat_delete(format);
  if (status != AMEDIA_OK) {
    return -6;
  }
  if (AMediaCodec_createInputSurface(encoder->codec, &encoder->window) != AMEDIA_OK) return -8;
  encoder->gpu = std::make_unique<GpuCapture>();
  if (!encoder->gpu->initialize(encoder->window, width, height)) return -9;
  if (AMediaCodec_start(encoder->codec) != AMEDIA_OK) {
    return -7;
  }
  *widthOut = width;
  *heightOut = height;
  *displayWidthOut = static_cast<uint32_t>(displayState.layerStackSpaceRect.width);
  *displayHeightOut = static_cast<uint32_t>(displayState.layerStackSpaceRect.height);
  *output = encoder.release();
  return 0;
}

extern "C" int uia_h264_encode_next(UiaH264Encoder* encoder, uint8_t** output,
                                    size_t* outputSize, int* isKeyFrame,
                                    int64_t* presentationTimeOut) {
  if (encoder == nullptr || output == nullptr || outputSize == nullptr || isKeyFrame == nullptr || presentationTimeOut == nullptr) return -1;
  *output = nullptr;
  *outputSize = 0;
  *isKeyFrame = 0;
  *presentationTimeOut = 0;
  std::lock_guard<std::mutex> lock(encoder->mutex);

  android::sp<android::GraphicBuffer> buffer;
  uint32_t displayWidth = 0, displayHeight = 0;
  const int captureStatus = captureGraphicBuffer(encoder->displayToken, encoder->scaleDivisor, &buffer,
                                                &displayWidth, &displayHeight);
  if (captureStatus != 0) return captureStatus;
  const auto capturedAt = std::chrono::steady_clock::now();
  const uint32_t width = buffer->getWidth();
  const uint32_t height = buffer->getHeight();
  if (width != encoder->width || height != encoder->height ||
      displayWidth != encoder->displayWidth || displayHeight != encoder->displayHeight) return 3;
  if (!encoder->gpu->begin(buffer, &encoder->probe)) return -21;
  const bool idle = !encoder->activity.shouldEncode(encoder->probe, capturedAt);

  const int64_t elapsedUs = std::chrono::duration_cast<std::chrono::microseconds>(
      capturedAt.time_since_epoch()).count();
  const int64_t presentationTimeUs = std::max(elapsedUs, encoder->lastPresentationTimeUs + 1);
  if (idle) {
    encoder->gpu->end();
  } else {
    if (!encoder->gpu->submit(presentationTimeUs)) return -23;
    encoder->lastPresentationTimeUs = presentationTimeUs;
  }

  for (int attempt = 0; attempt < 12; ++attempt) {
    AMediaCodecBufferInfo info{};
    const ssize_t outputIndex = AMediaCodec_dequeueOutputBuffer(encoder->codec, &info, idle ? 0 : 15000);
    if (outputIndex == AMEDIACODEC_INFO_OUTPUT_FORMAT_CHANGED) {
      AMediaFormat* outputFormat = AMediaCodec_getOutputFormat(encoder->codec);
      if (outputFormat != nullptr) {
        appendFormatCsd(outputFormat, &encoder->codecConfig);
        AMediaFormat_delete(outputFormat);
      }
      continue;
    }
    if (outputIndex < 0) {
      if (outputIndex == AMEDIACODEC_INFO_TRY_AGAIN_LATER) return idle ? 2 : 1;
      continue;
    }

    size_t encodedCapacity = 0;
    uint8_t* encoded = AMediaCodec_getOutputBuffer(encoder->codec,
                                                   static_cast<size_t>(outputIndex),
                                                   &encodedCapacity);
    if (encoded == nullptr || info.offset < 0 || info.size < 0 ||
        static_cast<size_t>(info.offset) + static_cast<size_t>(info.size) > encodedCapacity) {
      AMediaCodec_releaseOutputBuffer(encoder->codec, static_cast<size_t>(outputIndex), false);
      continue;
    }
    if ((info.flags & AMEDIACODEC_BUFFER_FLAG_CODEC_CONFIG) != 0) {
      appendH264(encoded + info.offset, static_cast<size_t>(info.size), &encoder->codecConfig);
      AMediaCodec_releaseOutputBuffer(encoder->codec, static_cast<size_t>(outputIndex), false);
      continue;
    }

    const bool keyFrame = (info.flags & 1) != 0;
    std::vector<uint8_t> accessUnit;
    if (keyFrame && !encoder->codecConfig.empty()) {
      accessUnit.insert(accessUnit.end(), encoder->codecConfig.begin(), encoder->codecConfig.end());
    }
    appendH264(encoded + info.offset, static_cast<size_t>(info.size), &accessUnit);
    AMediaCodec_releaseOutputBuffer(encoder->codec, static_cast<size_t>(outputIndex), false);
    if (accessUnit.empty()) continue;
    auto* copy = static_cast<uint8_t*>(std::malloc(accessUnit.size()));
    if (copy == nullptr) return -24;
    std::memcpy(copy, accessUnit.data(), accessUnit.size());
    *output = copy;
    *outputSize = accessUnit.size();
    *isKeyFrame = keyFrame ? 1 : 0;
    *presentationTimeOut = info.presentationTimeUs;
    return 0;
  }
  return idle ? 2 : 1;
}

extern "C" int uia_h264_wake(UiaH264Encoder* encoder, int keyFrame) {
  if (!encoder) return -1;
  std::lock_guard<std::mutex> lock(encoder->mutex);
  encoder->activity.wake();
  if (!keyFrame) return 0;
  AMediaFormat* parameters = AMediaFormat_new();
  if (!parameters) return -25;
  AMediaFormat_setInt32(parameters, "request-sync", 0);
  const auto status = AMediaCodec_setParameters(encoder->codec, parameters);
  AMediaFormat_delete(parameters);
  return status == AMEDIA_OK ? 0 : -26;
}

extern "C" void uia_h264_destroy(UiaH264Encoder* encoder) { delete encoder; }

extern "C" void uia_h264_free_buffer(void* buffer) { std::free(buffer); }
