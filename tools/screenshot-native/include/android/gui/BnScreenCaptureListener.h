#pragma once

#include <binder/IInterface.h>
#include <android/gui/IScreenCaptureListener.h>

namespace android::gui {
class BnScreenCaptureListener
    : public ::android::BnInterface<IScreenCaptureListener> {
public:
  explicit BnScreenCaptureListener();
  ::android::status_t onTransact(uint32_t code, const ::android::Parcel& data,
                                 ::android::Parcel* reply,
                                 uint32_t flags) override;
};
}  // namespace android::gui
