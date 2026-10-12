#pragma once

// The native screenshot prototype only needs these platform types in method
// declarations. Keep the headers independent of generated graphics AIDL code.
#include <cstdint>
#include <ui/PixelFormat.h>

namespace aidl::android::hardware::graphics::common {
struct ExtendableType {};
enum class BlendMode : int32_t;
enum class ChromaSiting : int32_t;
enum class Compression : int32_t;
enum class Interlaced : int32_t;
struct PlaneLayout {};
struct Smpte2086 {};
struct Cta861_3 {};
}  // namespace aidl::android::hardware::graphics::common

namespace android::ui {
using BlendMode = aidl::android::hardware::graphics::common::BlendMode;
using ChromaSiting = aidl::android::hardware::graphics::common::ChromaSiting;
using Compression = aidl::android::hardware::graphics::common::Compression;
using Interlaced = aidl::android::hardware::graphics::common::Interlaced;
using PlaneLayout = aidl::android::hardware::graphics::common::PlaneLayout;
using Smpte2086 = aidl::android::hardware::graphics::common::Smpte2086;
using Cta861_3 = aidl::android::hardware::graphics::common::Cta861_3;
using PixelFormat = android::PixelFormat;
enum class Dataspace : int32_t { UNKNOWN = 0, V0_SRGB = 142671872 };
}  // namespace android::ui
