# ScreenBridge

通过浏览器查看和控制 Android 手机的远程屏幕工具，使用 Go + NDK 开发，以 Magisk 模块形式部署。

网页和原生库内嵌于单个可执行文件，手机无需额外安装控制 App。

## v1.0.1 更新

- 修复不同浏览器运行模式下的画面位置问题：普通浏览器使用动态视口高度 `100dvh`（不支持时回退 `100vh`），iOS 主屏幕应用使用 `100vh`。
- 保持视频连接、码率、分辨率和触控逻辑不变。

## 功能

- **实时画面**：H.264 硬件编码，通过 WebRTC 传输到浏览器。
- **触屏控制**：在网页预览中点击、长按、拖动。
- **系统操作**：返回、主页、最近任务、按键和文本输入。
- **画面设置**：CBR 码率支持 `250k`、`500k`、`1m`、`2m`，默认 `500k`；宽高缩放支持 `1/2`、`1/3`、`1/4`。码率与大小会在浏览器中保存，刷新后首次连接直接使用保存的设置。
- **HTTP 接口**：获取 WebP 截图、UI 节点、屏幕尺寸和前台应用，执行常用控制操作。

## 使用要求

- 推荐通过 Magisk 安装，需要 root。
- 当前构建目标为 **Android API 32 / arm64-v8a**，已在 Pixel 2 对应系统验证；其他设备和系统需实测。
- 浏览器需支持 WebRTC、H.264 和 WebSocket；电脑与手机应处于可直连的局域网。
- 视频依赖私有 SurfaceFlinger 接口，连续触控依赖 `/dev/uinput`，可用性受系统权限和 SELinux 策略影响。

ADB shell 可探索独立运行及部分系统命令功能，但完整免 root 使用尚未验证。项目没有专门的 Shizuku 接入。

> **安全提醒**：目前没有登录鉴权，HTTP 和控制 WebSocket 不加密。能访问服务的人可能查看并控制手机。仅在可信网络使用，不要将端口直接暴露到公网。

## 安装与使用

1. 在 Magisk 中安装 `dist/ScreenBridge-v1.0.1.zip`。
2. 重启手机，服务默认延迟 10 秒启动。
3. 电脑与手机连接同一网络，在浏览器打开：

   ```text
   http://手机IP:7913/
   ```

在画面中按下、移动和抬起即可操作触屏。展开浮动控制菜单可发送系统按键、调整码率和画面大小；调整视频设置会重新连接视频。

四方块菜单按钮点击后展开等宽竖列菜单：按钮在屏幕上半部时向下展开，下半部时向上展开，顺序为返回、主页、最近任务、全屏、码率、大小。全屏按钮可切换浏览器全屏，保留触控和浮动按钮。Android Chrome 支持此操作；Safari 中隐藏该按钮，其他不支持网页全屏的浏览器会禁用该按钮。

服务同时只保留一个视频会话，新连接会替换旧视频，但不会关闭旧客户端的控制连接。

## 配置

配置文件：`/data/adb/screenbridge/config.conf`。首次启动自动创建，修改后需重启服务。

```ini
HOST=0.0.0.0
PORT=7913
BOOT_DELAY=10
DUMP_TIMEOUT=15
HANDLER_TIMEOUT=20
SCREENSHOT_QUALITY=80
```

`HOST`、`PORT` 设置监听地址和端口；三项时间配置单位为秒；`SCREENSHOT_QUALITY` 为截图质量（0–100），不影响视频。配置使用 `KEY=VALUE` 格式，值无需引号。

日志：`/data/adb/screenbridge/server.log`。PID 文件：`/data/adb/screenbridge/server.pid`。卸载模块时会清理此状态目录。

## 接口

以下 HTTP 接口使用 `GET`，参数放在 URL 查询字符串中。坐标以手机原始屏幕为准。

| 接口 | 参数或用途 |
| --- | --- |
| `/health` | 服务状态 |
| `/display` | 屏幕宽高 |
| `/current-app` | 前台应用包名和 Activity |
| `/ui`、`/dump` | UI XML、筛选后的节点 JSON |
| `/screenshot` | WebP 截图；`quality=0–100`、`scale=1/2/4` |
| `/click` | `x`、`y` |
| `/longpress` | `x`、`y`、`duration`（600–10000 ms，默认 800） |
| `/swipe` | `x1`、`y1`、`x2`、`y2`、`duration`（0–10000 ms，默认 300） |
| `/input` | `text`，需 URL 编码，字符支持受系统 `input text` 限制 |
| `/key` | `code=BACK/HOME/ENTER/APP_SWITCH/DEL` |
| `/start`、`/stop` | `package`，应用包名 |

```sh
curl 'http://PHONE_IP:7913/dump'
curl 'http://PHONE_IP:7913/click?x=540&y=960'
curl 'http://PHONE_IP:7913/screenshot?quality=80&scale=2' -o screen.webp
```

控制 WebSocket：`ws://PHONE_IP:7913/ws/control`。

```json
{"id":"1","path":"/key","params":{"code":"HOME"}}
```

视频信令使用 `POST /webrtc/offer`，提交 `type`、`sdp` 和可选的 `bitRate`、`scale`；`POST /webrtc/close` 通过 `sessionId` 关闭会话。

## 构建

准备 Go、Android SDK/NDK、SDK build-tools 中的 AIDL，以及 `adb`、`curl`、`make`、`tar`、`zip`、`unzip`。测试另需 Node.js 和支持 C++17 的编译器。

连接目标 API 32 arm64 手机，构建脚本会下载所需头文件与 libwebp，并从手机提取系统链接库。多设备时使用 `ANDROID_SERIAL` 指定目标；SDK/NDK 未被找到时设置 `ANDROID_SDK_ROOT` 和 `ANDROID_NDK_HOME`。

```sh
make check
go test -race ./...
make
```

产物：`bin/screenbridge` 和 `dist/ScreenBridge-v1.0.1.zip`。Magisk 模块 ID 为 `screenbridge`。主机测试不能替代手机与浏览器实测。

## 项目结构

| 目录 | 内容 |
| --- | --- |
| `app/` | Go 服务、测试和内嵌网页 `web/` |
| `native/` | 屏幕捕获、GPU 处理、编码和触控 |
| `module/` | Magisk 安装、启动脚本及配置示例 |
| `tests/` | 网页和原生测试 |
| `tools/` | 构建脚本和辅助文件 |
| `third_party/` | 第三方源码 |

## 常见问题

- **网页打不开**：确认手机 IP、端口和网络，等待启动延迟结束后检查日志。
- **有网页但没有视频**：检查系统兼容性、浏览器 H.264 支持及 WebRTC UDP 通信；视频没有备用捕获或编码管线。
- **触控不生效**：检查 root、`/dev/uinput` 权限和 SELinux 策略；视频正常不代表触控可用。
- **旧页面视频断开**：可能被新的客户端接管。

查看日志：

```sh
adb shell "su -c 'tail -n 80 /data/adb/screenbridge/server.log'"
```
