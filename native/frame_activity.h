#pragma once

#include <algorithm>
#include <chrono>
#include <cstdint>
#include <cstdlib>
#include <vector>

// The probe is a small thumbnail, not a full-resolution CPU readback.
class FrameActivity {
 public:
  using Clock = std::chrono::steady_clock;
  bool shouldEncode(const std::vector<uint8_t>& probe, Clock::time_point now) {
    bool changed = previous_.size() != probe.size() || previous_.empty();
    if (!changed) {
      for (size_t i = 0; i < probe.size(); ++i) {
        if (std::abs(int(probe[i]) - int(previous_[i])) > 4) { changed = true; break; }
      }
    }
    if (changed || forced_) {
      previous_ = probe;
      lastChange_ = now;
    }
    const bool encode = forced_ || changed || now - lastChange_ < std::chrono::seconds(1) ||
                        now - lastEncoded_ >= std::chrono::seconds(1);
    forced_ = false;
    if (encode) lastEncoded_ = now;
    return encode;
  }
  void wake() { forced_ = true; }

 private:
  std::vector<uint8_t> previous_;
  Clock::time_point lastChange_{};
  Clock::time_point lastEncoded_{};
  bool forced_ = true;
};
