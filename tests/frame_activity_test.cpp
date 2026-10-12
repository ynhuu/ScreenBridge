#include "frame_activity.h"
#include <cassert>

int main() {
  using namespace std::chrono;
  FrameActivity activity;
  const auto start = FrameActivity::Clock::time_point(seconds(100));
  std::vector<uint8_t> probe(16, 50);
  assert(activity.shouldEncode(probe, start));
  assert(activity.shouldEncode(probe, start + milliseconds(500)));
  assert(!activity.shouldEncode(probe, start + milliseconds(1200)));
  assert(activity.shouldEncode(probe, start + milliseconds(1500))); // idle heartbeat
  assert(!activity.shouldEncode(probe, start + milliseconds(1700)));
  activity.wake();
  assert(activity.shouldEncode(probe, start + milliseconds(1750))); // touch / PLI
  assert(activity.shouldEncode(probe, start + milliseconds(2000)));
  assert(!activity.shouldEncode(probe, start + milliseconds(2800)));
  probe[0] += 3;
  assert(!activity.shouldEncode(probe, start + milliseconds(2850))); // rounding noise
  probe[0] += 3;
  assert(activity.shouldEncode(probe, start + milliseconds(2900))); // visible change
}
