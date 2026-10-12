#include "touch.h"

#include <errno.h>
#include <fcntl.h>
#include <linux/input.h>
#include <linux/uinput.h>
#include <stdio.h>
#include <string.h>
#include <sys/time.h>
#include <sys/ioctl.h>
#include <unistd.h>

#include <array>
#include <mutex>

namespace {

class VirtualTouchscreen {
 public:
  ~VirtualTouchscreen() { destroy(); }

  int down(int width, int height, int x, int y) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (active_) return -3;
    if (fd_ < 0 || width != width_ || height != height_) {
      destroy();
      const int status = create(width, height);
      if (status != 0) return status;
    }
    const std::array<input_event, 4> events = {
        event(EV_ABS, ABS_X, x), event(EV_ABS, ABS_Y, y),
        event(EV_KEY, BTN_TOUCH, 1), event(EV_SYN, SYN_REPORT, 0)};
    const int status = writeEvents(events.data(), events.size());
    if (status == 0) {
      active_ = true;
      x_ = x;
      y_ = y;
    }
    return status;
  }

  int move(int x, int y) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (fd_ < 0 || !active_) return -4;
    const std::array<input_event, 3> events = {
        event(EV_ABS, ABS_X, x), event(EV_ABS, ABS_Y, y),
        event(EV_SYN, SYN_REPORT, 0)};
    const int status = writeEvents(events.data(), events.size());
    if (status == 0) {
      x_ = x;
      y_ = y;
    }
    return status;
  }

  int up(int x, int y) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (fd_ < 0 || !active_) return -4;
    const int status = release(x, y);
    if (status == 0) active_ = false;
    return status;
  }

  int cancel() {
    std::lock_guard<std::mutex> lock(mutex_);
    if (fd_ < 0 || !active_) return 0;
    const int status = release(x_, y_);
    if (status == 0) active_ = false;
    return status;
  }

 private:
  static input_event event(uint16_t type, uint16_t code, int32_t value) {
    input_event result{};
    gettimeofday(&result.time, nullptr);
    result.type = type;
    result.code = code;
    result.value = value;
    return result;
  }

  int create(int width, int height) {
    if (width < 16 || height < 16 || width > 16384 || height > 16384) return -1;
    const int fd = open("/dev/uinput", O_WRONLY | O_CLOEXEC);
    if (fd < 0) return -10;

    const bool configured =
        ioctl(fd, UI_SET_EVBIT, EV_KEY) == 0 &&
        ioctl(fd, UI_SET_KEYBIT, BTN_TOUCH) == 0 &&
        ioctl(fd, UI_SET_EVBIT, EV_ABS) == 0 &&
        ioctl(fd, UI_SET_ABSBIT, ABS_X) == 0 &&
        ioctl(fd, UI_SET_ABSBIT, ABS_Y) == 0 &&
        ioctl(fd, UI_SET_PROPBIT, INPUT_PROP_DIRECT) == 0;
    if (!configured) {
      close(fd);
      return -11;
    }

    uinput_user_dev device{};
    snprintf(device.name, UINPUT_MAX_NAME_SIZE, "ScreenBridge virtual touchscreen");
    device.id.bustype = BUS_VIRTUAL;
    device.id.vendor = 0x1209;
    device.id.product = 0x7913;
    device.id.version = 1;
    device.absmin[ABS_X] = 0;
    device.absmax[ABS_X] = width - 1;
    device.absmin[ABS_Y] = 0;
    device.absmax[ABS_Y] = height - 1;
    if (write(fd, &device, sizeof(device)) != static_cast<ssize_t>(sizeof(device)) ||
        ioctl(fd, UI_DEV_CREATE) < 0) {
      close(fd);
      return -12;
    }

    fd_ = fd;
    width_ = width;
    height_ = height;
    // Give EventHub a moment to discover the device before its first contact.
    usleep(50000);
    return 0;
  }

  int release(int x, int y) {
    const std::array<input_event, 4> events = {
        event(EV_ABS, ABS_X, x), event(EV_ABS, ABS_Y, y),
        event(EV_KEY, BTN_TOUCH, 0), event(EV_SYN, SYN_REPORT, 0)};
    return writeEvents(events.data(), events.size());
  }

  int writeEvents(const input_event* events, size_t count) {
    const size_t bytes = count * sizeof(input_event);
    const ssize_t written = write(fd_, events, bytes);
    return written == static_cast<ssize_t>(bytes) ? 0 : -13;
  }

  void destroy() {
    if (fd_ < 0) return;
    if (active_) {
      (void)release(x_, y_);
      active_ = false;
    }
    (void)ioctl(fd_, UI_DEV_DESTROY);
    close(fd_);
    fd_ = -1;
    width_ = 0;
    height_ = 0;
  }

  std::mutex mutex_;
  int fd_ = -1;
  int width_ = 0;
  int height_ = 0;
  int x_ = 0;
  int y_ = 0;
  bool active_ = false;
};

VirtualTouchscreen gTouchscreen;

}  // namespace

extern "C" int uia_touch_down(int width, int height, int x, int y) {
  return gTouchscreen.down(width, height, x, y);
}

extern "C" int uia_touch_move(int x, int y) { return gTouchscreen.move(x, y); }

extern "C" int uia_touch_up(int x, int y) { return gTouchscreen.up(x, y); }

extern "C" int uia_touch_cancel(void) { return gTouchscreen.cancel(); }
