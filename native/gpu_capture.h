#pragma once

#include <EGL/egl.h>
#include <EGL/eglext.h>
#include <GLES2/gl2.h>
#include <GLES2/gl2ext.h>
#include <android/native_window.h>
#include <ui/GraphicBuffer.h>
#include <vector>

// Imports a captured GraphicBuffer as an EGL image. Only the 64x64 activity
// probe is read by the CPU; full-size RGBA -> YUV conversion is done by the GPU
// and the MediaCodec input Surface. The context is detached between cgo calls.
class GpuCapture {
 public:
  ~GpuCapture();
  bool initialize(ANativeWindow* window, int width, int height);
  bool begin(const android::sp<android::GraphicBuffer>& buffer, std::vector<uint8_t>* probe);
  bool submit(int64_t timestampUs);
  void end();

 private:
  void draw(int width, int height);
  EGLDisplay display_ = EGL_NO_DISPLAY;
  EGLContext context_ = EGL_NO_CONTEXT;
  EGLSurface surface_ = EGL_NO_SURFACE;
  EGLImageKHR image_ = EGL_NO_IMAGE_KHR;
  GLuint program_ = 0, source_ = 0, probeTexture_ = 0, probeFramebuffer_ = 0;
  GLint position_ = -1, texCoord_ = -1;
  int width_ = 0, height_ = 0;
  PFNEGLCREATEIMAGEKHRPROC createImage_ = nullptr;
  PFNEGLDESTROYIMAGEKHRPROC destroyImage_ = nullptr;
  PFNGLEGLIMAGETARGETTEXTURE2DOESPROC bindImage_ = nullptr;
  PFNEGLPRESENTATIONTIMEANDROIDPROC presentationTime_ = nullptr;
};
