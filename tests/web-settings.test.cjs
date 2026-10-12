const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");

test("video preferences survive refresh and reject invalid or unavailable storage", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const read = html.match(/    function readVideoPreference\([\s\S]*?\n    \}/)[0];
  const save = html.match(/    function saveVideoPreference\([\s\S]*?\n    \}/)[0];
  const initialization = html.match(/    let selectedBitRate = readVideoPreference[^\n]+\n    let selectedScale = readVideoPreference[^\n]+/)[0];
  const values = new Map();
  const localStorage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) };
  const load = () => {
    const context = vm.createContext({ localStorage });
    vm.runInContext(read + save + initialization, context);
    return context;
  };
  let context = load();
  assert.equal(vm.runInContext("selectedBitRate", context), 500000);
  assert.equal(vm.runInContext("selectedScale", context), 2);
  context.saveVideoPreference("screenbridge.bitrate", 2000000);
  context.saveVideoPreference("screenbridge.scale", 4);
  context = load();
  assert.equal(vm.runInContext("selectedBitRate", context), 2000000);
  assert.equal(vm.runInContext("selectedScale", context), 4);
  for (const rate of [250000]) {
    context.saveVideoPreference("screenbridge.bitrate", rate);
    context = load();
    assert.equal(vm.runInContext("selectedBitRate", context), rate);
  }
  values.set("screenbridge.bitrate", "125000");
  context = load();
  assert.equal(vm.runInContext("selectedBitRate", context), 500000);
  values.set("screenbridge.bitrate", "garbage");
  values.set("screenbridge.scale", "5");
  context = load();
  assert.equal(vm.runInContext("selectedBitRate", context), 500000);
  assert.equal(vm.runInContext("selectedScale", context), 2);
  localStorage.getItem = localStorage.setItem = () => { throw new Error("storage blocked"); };
  context = load();
  assert.equal(vm.runInContext("selectedBitRate", context), 500000);
  assert.doesNotThrow(() => context.saveVideoPreference("screenbridge.scale", 3));
  for (const [setting, key] of [["BitRate", "bitrate"], ["Scale", "scale"]]) {
    assert.ok(html.includes(`selected${setting} = value; saveVideoPreference("screenbridge.${key}", value); void restartVideo();`));
  }
});

test("bitrate menu includes 250k but excludes 125k and fits narrow screens", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const menu = html.match(/<div id="bitrate-options"[\s\S]*?<\/div>/)[0];
  const rates = Array.from(menu.matchAll(/data-bitrate="(\d+)"/g), match => Number(match[1]));
  assert.deepEqual(rates, [250000, 500000, 1000000, 2000000]);
  assert.match(html, /#bitrate-options\s*\{\s*width: min\(256px, calc\(100vw - 80px\)\);/);
});

test("upward menus mirror button order, corners and choice positions", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  assert.match(html, /\.virtual-keys:not\(\.opens-below\)\s*\{\s*flex-direction: column-reverse;/);
  assert.match(html, /\.virtual-keys:not\(\.opens-below\) > \[data-key="BACK"\]\s*\{[^}]*border-radius: 0 0 11px 11px;/);
  assert.match(html, /\.virtual-keys:not\(\.opens-below\) > #video-scale\s*\{\s*border-radius: 11px 11px 0 0;/);
  assert.match(html, /\.virtual-keys:not\(\.opens-below\) #bitrate-options\s*\{\s*top: 48px; bottom: auto;/);
  assert.match(html, /\.virtual-keys:not\(\.opens-below\) #scale-options\s*\{\s*top: 0; bottom: auto;/);
});

test("bitrate and scale choices are horizontal rows with equally sized options", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const menu = html.match(/#bitrate-options, #scale-options\s*\{([^}]+)\}/)[1];
  const option = html.match(/\.video-option\s*\{([^}]+)\}/)[1];
  assert.match(menu, /display: flex;/);
  assert.match(menu, /width: 192px;/);
  assert.match(option, /flex: 1;/);
  assert.match(option, /min-width: 0;/);
});

test("vertical menu opens down in the upper half and up in the lower half", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const menu = html.match(/\.virtual-keys\s*\{([^}]+)\}/)[1];
  assert.match(menu, /bottom: calc\(100% - var\(--floating-controls-top\) \+ 8px\);/);
  const setTop = html.match(/function setFloatingTop\(top\) \{[\s\S]*?\n    \}/)[0];
  const context = vm.createContext({
    virtualKeys: { hidden: false, offsetHeight: 290, classList: { toggle(name, value) { this[name] = value; } } },
    screenViewport: { clientHeight: 700, style: { setProperty() {} } },
    keysToggle: { offsetHeight: 48 }, floatingTop: null
  });
  vm.runInContext(setTop, context);
  context.setFloatingTop(0);
  assert.equal(context.floatingTop, 8);
  assert.equal(context.virtualKeys.classList["opens-below"], true);
  context.setFloatingTop(400);
  assert.equal(context.floatingTop, 400);
  assert.equal(context.virtualKeys.classList["opens-below"], false);
  context.setFloatingTop(1000);
  assert.equal(context.floatingTop, 644);
});

test("vertical menu matches the 48px menu toggle width", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  for (const selector of [/\.virtual-keys\s*\{([^}]+)\}/, /#keys-toggle\s*\{([^}]+)\}/, /#video-bitrate, #video-scale\s*\{([^}]+)\}/]) {
    assert.match(html.match(selector)[1], /width:\s*48px;/);
  }
});

test("active controls retain edge rounding without clipping popup menus", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  assert.match(html, /\.virtual-key\[data-key="BACK"\]\s*\{\s*border-radius: 11px 11px 0 0;/);
  assert.match(html, /#video-scale\s*\{\s*flex-basis: auto; border-radius: 0 0 11px 11px;/);
  assert.match(html.match(/#bitrate-options, #scale-options\s*\{([^}]+)\}/)[1], /overflow: hidden;/);
  assert.match(html.match(/\.virtual-keys\s*\{([^}]+)\}/)[1], /overflow: visible;/);
  assert.match(html, /--control-active: rgba\(20, 116, 132, \.85\)/);
});

test("vertical controls follow navigation, fullscreen, bitrate, scale order", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const nav = html.match(/<nav id="virtual-keys"[\s\S]*?<\/nav>/)[0];
  const markers = ['data-key="BACK"', 'data-key="HOME"', 'data-key="APP_SWITCH"', 'id="fullscreen-toggle"', 'id="video-bitrate"', 'id="video-scale"'];
  let previous = -1;
  for (const marker of markers) {
    const position = nav.indexOf(marker);
    assert.ok(position > previous, `${marker} is out of order`);
    previous = position;
  }
});

test("fullscreen button is hidden in Safari but remains visible in Chrome", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const visibility = html.match(/fullscreenToggle\.hidden = [^;]+;/)[0];
  for (const [userAgent, hidden] of [
    ["Mozilla/5.0 iPhone Version/18.0 Mobile Safari/604.1", true],
    ["Mozilla/5.0 Macintosh Version/18.0 Safari/605.1.15", true],
    ["Mozilla/5.0 Android Chrome/130.0 Mobile Safari/537.36", false],
    ["Mozilla/5.0 iPhone CriOS/130.0 Mobile Safari/604.1", false]
  ]) {
    const context = vm.createContext({ navigator: { userAgent }, fullscreenToggle: {} });
    vm.runInContext(visibility, context);
    assert.equal(context.fullscreenToggle.hidden, hidden);
  }
  assert.match(html, /#fullscreen-toggle\[hidden\]\s*\{\s*display:\s*none;\s*\}/);
});

test("fullscreen toggles the whole page and reports browser failures", async () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const toggle = html.match(/async function toggleFullscreen\(\) \{[\s\S]*?\n    \}/)[0];
  let entered = 0, exited = 0;
  const errors = [];
  const document = {
    fullscreenElement: null,
    documentElement: { requestFullscreen: async () => { entered++; } },
    exitFullscreen: async () => { exited++; }
  };
  const expanded = [];
  const context = vm.createContext({ document, setStatus: message => errors.push(message), setControlsExpanded: value => expanded.push(value) });
  vm.runInContext(toggle, context);
  await context.toggleFullscreen();
  assert.equal(entered, 1);
  document.fullscreenElement = document.documentElement;
  await context.toggleFullscreen();
  assert.equal(exited, 1);
  document.fullscreenElement = null;
  document.documentElement.requestFullscreen = async () => { throw new Error("Full screen denied"); };
  await context.toggleFullscreen();
  assert.deepEqual(errors, ["Full screen denied"]);
  assert.deepEqual(expanded, [false, false, false]);
});

test("fullscreen button tracks browser-driven exits without sending a phone key", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const button = html.match(/<button id="fullscreen-toggle"[^>]*>/)[0];
  assert.doesNotMatch(button, /data-key=/);
  const update = html.match(/function updateFullscreenButton\(\) \{[\s\S]*?\n    \}/)[0];
  const attributes = {};
  const fullscreenToggle = { setAttribute: (key, value) => { attributes[key] = value; } };
  const document = { documentElement: {}, fullscreenElement: null };
  const context = vm.createContext({ document, fullscreenToggle });
  vm.runInContext(update, context);
  document.fullscreenElement = document.documentElement;
  context.updateFullscreenButton();
  assert.equal(attributes["aria-pressed"], "true");
  assert.equal(attributes["aria-label"], "退出全屏");
  document.fullscreenElement = null;
  context.updateFullscreenButton();
  assert.equal(attributes["aria-pressed"], "false");
  assert.equal(attributes["aria-label"], "进入全屏");
});

test("video options use explicit high-contrast colors regardless of inherited theme", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const menu = html.match(/#bitrate-options, #scale-options\s*\{([^}]+)\}/)[1];
  const option = html.match(/\.video-option\s*\{([^}]+)\}/)[1];
  assert.match(menu, /background:\s*#242832\s*;/);
  assert.match(menu, /color:\s*#f4f5f7\s*;/);
  assert.match(option, /color:\s*#f4f5f7\s*;/);
});

test("video buttons and options use semibold text for Safari readability", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  for (const selector of [/#video-bitrate, #video-scale\s*\{([^}]+)\}/, /\.video-option\s*\{([^}]+)\}/]) {
    assert.match(html.match(selector)[1], /font-weight:\s*600\s*;/);
  }
});

test("touch acknowledgment expires silently and clears pending requests", async () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const send = html.match(/function sendTouchEvent\(action, point\) \{[\s\S]*?\n    \}/)[0];
  let expire;
  const messages = [];
  const pending = new Map();
  const context = vm.createContext({
    controlSocket: { readyState: 1, send() {} }, WebSocket: { OPEN: 1 },
    controlRequestID: 0, pendingControlRequests: pending,
    displayWidth: 1080, displayHeight: 1920, devicePoint: point => point,
    setTimeout: callback => { expire = callback; return 1; }, clearTimeout() {},
    setStatus: message => messages.push(message)
  });
  vm.runInContext(send, context);
  context.sendTouchEvent("down", { x: 10, y: 20 });
  assert.equal(pending.size, 1);
  expire();
  await Promise.resolve();
  assert.equal(pending.size, 0);
  assert.deepEqual(messages, []);
});

function element(text = "") {
  return {
    textContent: text, hidden: true, disabled: false, dataset: {}, attributes: {}, events: {}, offsetHeight: 122,
    classList: { toggle(name, value) { this[name] = value; } },
    setAttribute(name, value) { this.attributes[name] = value; },
    addEventListener(name, listener) { this.events[name] = listener; }
  };
}

test("gesture markers keep fixed CSS sizes across preview resolutions and resizing", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const source = html.match(/function scaleGestureOverlay\(\) \{[\s\S]*?\n    \}/)[0];
  const startMarker = { ...element(), style: {} };
  const endMarker = { ...element(), style: {} };
  const line = { style: {} };
  let width = 272;
  const context = vm.createContext({
    layer: { getBoundingClientRect: () => ({ width }) },
    screenshotWidth: 544, startMarker, endMarker, line
  });
  vm.runInContext(source, context);
  for (const [previewWidth, videoWidth] of [[272, 544], [544, 544], [320, 360]]) {
    width = previewWidth;
    context.screenshotWidth = videoWidth;
    context.scaleGestureOverlay();
    const scale = previewWidth / videoWidth;
    for (const marker of [startMarker, endMarker]) {
      assert.equal(Number(marker.attributes.r) * scale, 10);
      assert.equal(parseFloat(marker.style.strokeWidth) * scale, 2.5);
    }
    assert.equal(parseFloat(line.style.strokeWidth) * scale, 4);
  }
  assert.match(html, /screen\.style\.height[^\n]*\n\s*scaleGestureOverlay\(\);/);
});

function fixture(attribute = "bitrate", values = [500000, 1000000, 2000000], initial = values[0]) {
  const context = vm.createContext({});
  vm.runInContext(fs.readFileSync(path.join(__dirname, "../app/web/settings.js"), "utf8"), context);
  const button = element();
  const menu = element();
  const options = values.map(value => {
    const option = element(String(value));
    option.dataset[attribute] = String(value);
    return option;
  });
  menu.querySelectorAll = () => options;
  const changes = [];
  let opened = 0;
  const choice = context.createVideoChoice({ button, menu, attribute, label: "test", initial,
    container: { getBoundingClientRect: () => ({ top: 80 }) },
    viewport: { getBoundingClientRect: () => ({ top: 0 }) },
    onOpen: () => opened++, onChange: value => changes.push(value) });
  return { button, menu, options, changes, choice, opened: () => opened };
}

test("restored choices initialize labels without triggering another video request", () => {
  for (const [attribute, values] of [["bitrate", [500000, 1000000, 2000000]], ["scale", [2, 3, 4]]]) {
    const f = fixture(attribute, values, values[2]);
    assert.equal(f.button.textContent, String(values[2]));
    assert.equal(f.options[2].attributes["aria-pressed"], "true");
    assert.equal(f.options[0].attributes["aria-pressed"], "false");
    assert.deepEqual(f.changes, []);
  }
});

test("menus open below when there is insufficient room above", () => {
  const f = fixture();
  f.button.events.click();
  assert.equal(f.menu.hidden, false);
  assert.equal(f.menu.classList.below, true);
  assert.equal(f.button.attributes["aria-expanded"], "true");
  assert.equal(f.opened(), 1);
});

test("video settings update labels and selection, without duplicate restarts", () => {
  for (const [attribute, values] of [["bitrate", [500000, 1000000, 2000000]], ["scale", [2, 3, 4]]]) {
    const f = fixture(attribute, values);
    f.options[1].events.click();
    assert.deepEqual(f.changes, [values[1]]);
    assert.equal(f.button.textContent, String(values[1]));
    assert.equal(f.options[1].attributes["aria-pressed"], "true");
    assert.equal(f.options[0].attributes["aria-pressed"], "false");
    assert.equal(f.menu.hidden, true);
    f.options[1].events.click();
    assert.equal(f.changes.length, 1);
  }
});

test("disabled settings cannot open or change during reconnection", () => {
  const f = fixture();
  f.button.disabled = true;
  f.button.events.click();
  f.options[1].events.click();
  assert.equal(f.menu.hidden, true);
  assert.equal(f.changes.length, 0);
});

test("touch mapping respects rounded video dimensions and display bounds", () => {
  const context = vm.createContext({});
  vm.runInContext(fs.readFileSync(path.join(__dirname, "../app/web/settings.js"), "utf8"), context);
  for (const divisor of [2, 3, 4]) {
    const width = Math.floor(1080 / divisor) & ~1;
    const height = Math.floor(1920 / divisor) & ~1;
    const center = context.mapVideoPoint({ x: width / 2 - 0.5, y: height / 2 - 0.5 }, width, height, 1080, 1920);
    assert.equal(center.x, 540);
    assert.equal(center.y, 960);
    const outside = context.mapVideoPoint({ x: width, y: height }, width, height, 1080, 1920);
    assert.equal(outside.x, 1079);
    assert.equal(outside.y, 1919);
  }
  const rounded = context.mapVideoPoint({ x: 0, y: 318 }, 180, 320, 540, 962);
  assert.equal(rounded.y, 957);
});

test("video settings stay disabled throughout restart and connection setup", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const update = html.match(/function updateVideoSettings\(\) \{[\s\S]*?\n    \}/)[0];
  const context = vm.createContext({ videoConnecting: false, videoRestartPending: false,
    videoBitRateButton: {}, videoScaleButton: {} });
  vm.runInContext(update, context);
  for (const [connecting, restarting] of [[false, false], [true, false], [false, true], [true, true]]) {
    context.videoConnecting = connecting;
    context.videoRestartPending = restarting;
    context.updateVideoSettings();
    assert.equal(context.videoBitRateButton.disabled, connecting || restarting);
    assert.equal(context.videoScaleButton.disabled, connecting || restarting);
  }
});

function requestContext(fetch) {
  const context = vm.createContext({ fetch, AbortController, setTimeout, clearTimeout });
  vm.runInContext(fs.readFileSync(path.join(__dirname, "../app/web/settings.js"), "utf8"), context);
  return context;
}

test("video requests time out instead of leaving settings disabled indefinitely", async () => {
  const context = requestContext((url, { signal }) => new Promise((resolve, reject) => {
    signal.addEventListener("abort", () => reject(signal.reason), { once: true });
  }));
  await assert.rejects(context.videoRequest("/webrtc/offer", {}, { timeout: 10 }), /视频请求超时/);
});

test("video requests support explicit cancellation", async () => {
  const context = requestContext((url, { signal }) => new Promise((resolve, reject) => {
    signal.addEventListener("abort", () => reject(signal.reason), { once: true });
  }));
  const controller = new AbortController();
  const pending = context.videoRequest("/webrtc/offer", {}, { signal: controller.signal });
  controller.abort();
  await assert.rejects(pending, { name: "AbortError" });
});

test("video requests surface HTTP failures", async () => {
  const context = requestContext(async () => ({ ok: false, status: 400, json: async () => ({ error: "invalid offer" }) }));
  await assert.rejects(context.videoRequest("/webrtc/offer", {}), /invalid offer/);
});

test("rotation metadata validates dimensions before updating the preview", () => {
  const context = vm.createContext({});
  vm.runInContext(fs.readFileSync(path.join(__dirname, "../app/web/settings.js"), "utf8"), context);
  const landscape = context.videoDisplayInfo({ width: 960, height: 540, displayWidth: 1920, displayHeight: 1080, scale: 2 });
  assert.equal(landscape.displayWidth, 1920);
  assert.equal(landscape.width, 960);
  for (const invalid of [ { ...landscape, width: 0 }, { ...landscape, height: Infinity }, { ...landscape, scale: 1 } ]) {
    assert.throws(() => context.videoDisplayInfo(invalid), /尺寸信息无效/);
  }
});

test("rotation waits for the decoder resize before changing touch coordinates", () => {
  const html = fs.readFileSync(path.join(__dirname, "../app/web/index.html"), "utf8");
  const apply = html.match(/function applyPendingDisplayInfo\(\) \{[\s\S]*?\n    \}/)[0];
  const info = { width: 960, height: 540, displayWidth: 1920, displayHeight: 1080, scale: 2 };
  const applied = [];
  const context = vm.createContext({ pendingDisplayInfo: info, videoEnabled: true,
    video: { videoWidth: 540, videoHeight: 960 }, applyVideoDisplayInfo: value => applied.push(value) });
  vm.runInContext(apply, context);
  context.applyPendingDisplayInfo();
  assert.equal(applied.length, 0);
  context.video.videoWidth = 960;
  context.video.videoHeight = 540;
  context.applyPendingDisplayInfo();
  assert.equal(applied.length, 1);
  assert.equal(context.pendingDisplayInfo, null);
});
