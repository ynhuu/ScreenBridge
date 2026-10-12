#ifndef UIA_TOUCH_H_
#define UIA_TOUCH_H_

#ifdef __cplusplus
extern "C" {
#endif

int uia_touch_down(int width, int height, int x, int y);
int uia_touch_move(int x, int y);
int uia_touch_up(int x, int y);
int uia_touch_cancel(void);

#ifdef __cplusplus
}
#endif

#endif  // UIA_TOUCH_H_
