#include "gpu_capture.h"

namespace {
GLuint shader(GLenum kind, const char* source) {
  GLuint result = glCreateShader(kind);
  glShaderSource(result, 1, &source, nullptr);
  glCompileShader(result);
  GLint compiled = GL_FALSE;
  glGetShaderiv(result, GL_COMPILE_STATUS, &compiled);
  if (!compiled) { glDeleteShader(result); return 0; }
  return result;
}
constexpr int kProbeSize = 64;
}

bool GpuCapture::initialize(ANativeWindow* window, int width, int height) {
  width_ = width; height_ = height;
  display_ = eglGetDisplay(EGL_DEFAULT_DISPLAY);
  if (display_ == EGL_NO_DISPLAY || !eglInitialize(display_, nullptr, nullptr)) return false;
  const EGLint attributes[] = {EGL_SURFACE_TYPE, EGL_WINDOW_BIT,
      EGL_RENDERABLE_TYPE, EGL_OPENGL_ES2_BIT, EGL_RED_SIZE, 8, EGL_GREEN_SIZE, 8,
      EGL_BLUE_SIZE, 8, EGL_ALPHA_SIZE, 8, EGL_RECORDABLE_ANDROID, 1, EGL_NONE};
  EGLConfig config;
  EGLint count = 0;
  if (!eglChooseConfig(display_, attributes, &config, 1, &count) || count != 1) return false;
  const EGLint contextAttributes[] = {EGL_CONTEXT_CLIENT_VERSION, 2, EGL_NONE};
  context_ = eglCreateContext(display_, config, EGL_NO_CONTEXT, contextAttributes);
  surface_ = eglCreateWindowSurface(display_, config, window, nullptr);
  if (context_ == EGL_NO_CONTEXT || surface_ == EGL_NO_SURFACE ||
      !eglMakeCurrent(display_, surface_, surface_, context_)) return false;
  createImage_ = reinterpret_cast<PFNEGLCREATEIMAGEKHRPROC>(eglGetProcAddress("eglCreateImageKHR"));
  destroyImage_ = reinterpret_cast<PFNEGLDESTROYIMAGEKHRPROC>(eglGetProcAddress("eglDestroyImageKHR"));
  bindImage_ = reinterpret_cast<PFNGLEGLIMAGETARGETTEXTURE2DOESPROC>(eglGetProcAddress("glEGLImageTargetTexture2DOES"));
  presentationTime_ = reinterpret_cast<PFNEGLPRESENTATIONTIMEANDROIDPROC>(eglGetProcAddress("eglPresentationTimeANDROID"));
  if (!createImage_ || !destroyImage_ || !bindImage_ || !presentationTime_) { end(); return false; }
  const char* vertex = "attribute vec2 position; attribute vec2 texCoord; varying vec2 uv;"
                       "void main(){uv=texCoord;gl_Position=vec4(position,0.0,1.0);}";
  const char* fragment = "precision mediump float; varying vec2 uv; uniform sampler2D image;"
                         "void main(){gl_FragColor=texture2D(image,uv);}";
  GLuint vs = shader(GL_VERTEX_SHADER, vertex), fs = shader(GL_FRAGMENT_SHADER, fragment);
  if (!vs || !fs) { if (vs) glDeleteShader(vs); if (fs) glDeleteShader(fs); end(); return false; }
  program_ = glCreateProgram();
  glAttachShader(program_, vs); glAttachShader(program_, fs); glLinkProgram(program_);
  glDeleteShader(vs); glDeleteShader(fs);
  GLint linked = GL_FALSE;
  glGetProgramiv(program_, GL_LINK_STATUS, &linked);
  if (!linked) { end(); return false; }
  glUseProgram(program_);
  glUniform1i(glGetUniformLocation(program_, "image"), 0);
  position_ = glGetAttribLocation(program_, "position");
  texCoord_ = glGetAttribLocation(program_, "texCoord");
  glGenTextures(1, &source_);
  glBindTexture(GL_TEXTURE_2D, source_);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_LINEAR);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MAG_FILTER, GL_LINEAR);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_S, GL_CLAMP_TO_EDGE);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_T, GL_CLAMP_TO_EDGE);
  glGenTextures(1, &probeTexture_);
  glBindTexture(GL_TEXTURE_2D, probeTexture_);
  glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA, kProbeSize, kProbeSize, 0, GL_RGBA, GL_UNSIGNED_BYTE, nullptr);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_NEAREST);
  glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MAG_FILTER, GL_NEAREST);
  glGenFramebuffers(1, &probeFramebuffer_);
  glBindFramebuffer(GL_FRAMEBUFFER, probeFramebuffer_);
  glFramebufferTexture2D(GL_FRAMEBUFFER, GL_COLOR_ATTACHMENT0, GL_TEXTURE_2D, probeTexture_, 0);
  const bool ready = glCheckFramebufferStatus(GL_FRAMEBUFFER) == GL_FRAMEBUFFER_COMPLETE && glGetError() == GL_NO_ERROR;
  end();
  return ready;
}

void GpuCapture::draw(int width, int height) {
  const GLfloat positions[] = {-1,-1, 1,-1, -1,1, 1,1};
  const GLfloat coordinates[] = {0,1, 1,1, 0,0, 1,0};
  glViewport(0, 0, width, height);
  glUseProgram(program_);
  glActiveTexture(GL_TEXTURE0);
  glBindTexture(GL_TEXTURE_2D, source_);
  glEnableVertexAttribArray(position_); glEnableVertexAttribArray(texCoord_);
  glVertexAttribPointer(position_, 2, GL_FLOAT, GL_FALSE, 0, positions);
  glVertexAttribPointer(texCoord_, 2, GL_FLOAT, GL_FALSE, 0, coordinates);
  glDrawArrays(GL_TRIANGLE_STRIP, 0, 4);
  glDisableVertexAttribArray(position_); glDisableVertexAttribArray(texCoord_);
}

bool GpuCapture::begin(const android::sp<android::GraphicBuffer>& buffer, std::vector<uint8_t>* probe) {
  if (!eglMakeCurrent(display_, surface_, surface_, context_)) return false;
  const EGLint attributes[] = {EGL_IMAGE_PRESERVED_KHR, EGL_TRUE, EGL_NONE};
  image_ = createImage_(display_, EGL_NO_CONTEXT, EGL_NATIVE_BUFFER_ANDROID,
      reinterpret_cast<EGLClientBuffer>(buffer->getNativeBuffer()), attributes);
  if (image_ == EGL_NO_IMAGE_KHR) { end(); return false; }
  glBindTexture(GL_TEXTURE_2D, source_);
  bindImage_(GL_TEXTURE_2D, image_);
  glBindFramebuffer(GL_FRAMEBUFFER, probeFramebuffer_);
  draw(kProbeSize, kProbeSize);
  probe->resize(kProbeSize*kProbeSize*4);
  glReadPixels(0, 0, kProbeSize, kProbeSize, GL_RGBA, GL_UNSIGNED_BYTE, probe->data());
  if (glGetError() != GL_NO_ERROR) { end(); return false; }
  return true;
}

bool GpuCapture::submit(int64_t timestampUs) {
  glBindFramebuffer(GL_FRAMEBUFFER, 0);
  draw(width_, height_);
  const bool ready = glGetError() == GL_NO_ERROR &&
      presentationTime_(display_, surface_, timestampUs*1000) && eglSwapBuffers(display_, surface_);
  end();
  return ready;
}

void GpuCapture::end() {
  if (image_ != EGL_NO_IMAGE_KHR && destroyImage_) { destroyImage_(display_, image_); image_ = EGL_NO_IMAGE_KHR; }
  if (display_ != EGL_NO_DISPLAY) eglMakeCurrent(display_, EGL_NO_SURFACE, EGL_NO_SURFACE, EGL_NO_CONTEXT);
}

GpuCapture::~GpuCapture() {
  if (display_ == EGL_NO_DISPLAY) return;
  if (context_ != EGL_NO_CONTEXT && surface_ != EGL_NO_SURFACE && eglMakeCurrent(display_, surface_, surface_, context_)) {
    if (program_) glDeleteProgram(program_);
    if (source_) glDeleteTextures(1, &source_);
    if (probeTexture_) glDeleteTextures(1, &probeTexture_);
    if (probeFramebuffer_) glDeleteFramebuffers(1, &probeFramebuffer_);
    end();
  }
  if (surface_ != EGL_NO_SURFACE) eglDestroySurface(display_, surface_);
  if (context_ != EGL_NO_CONTEXT) eglDestroyContext(display_, context_);
  eglTerminate(display_);
}
