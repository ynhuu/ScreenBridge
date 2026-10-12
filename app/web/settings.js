"use strict";

async function videoRequest(path, body, { signal, timeout = 25000, keepalive = false } = {}) {
  const controller = new AbortController();
  const abort = () => controller.abort(signal.reason);
  if (signal?.aborted) abort();
  else signal?.addEventListener("abort", abort, { once: true });
  const timer = setTimeout(() => controller.abort(), timeout);
  try {
    const response = await fetch(path, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body), signal: controller.signal, keepalive
    });
    const result = await response.json();
    if (!response.ok || result.ok === false) throw new Error(result.error || `视频请求失败（HTTP ${response.status}）`);
    return result;
  } catch (error) {
    if (controller.signal.aborted && !signal?.aborted) throw new Error("视频请求超时");
    throw error;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
  }
}

function videoDisplayInfo(info) {
  const result = {};
  for (const key of ["width", "height", "displayWidth", "displayHeight", "scale"]) {
    result[key] = Number(info[key]);
    if (!Number.isInteger(result[key]) || result[key] <= 0) throw new Error("视频流尺寸信息无效");
  }
  if (![2, 3, 4].includes(result.scale)) throw new Error("视频流尺寸信息无效");
  return result;
}

// H.264 dimensions are rounded down to even pixels. Use the actual display
// dimensions rather than multiplying by the requested scale divisor.
function mapVideoPoint(point, width, height, displayWidth, displayHeight) {
  return {
    x: Math.max(0, Math.min(displayWidth - 1, Math.round((point.x + 0.5) * displayWidth / width - 0.5))),
    y: Math.max(0, Math.min(displayHeight - 1, Math.round((point.y + 0.5) * displayHeight / height - 0.5)))
  };
}

// Native select popups ignore text alignment on some mobile browsers.
// Keep video settings in a centered, absolutely positioned menu.
function createVideoChoice({ button, menu, container, viewport, attribute, label, initial, onOpen, onChange }) {
  const options = Array.from(menu.querySelectorAll(`[data-${attribute}]`));
  let selected = initial;
  const initialOption = options.find(option => Number(option.dataset[attribute]) === initial);
  if (initialOption) {
    button.textContent = initialOption.textContent;
    button.setAttribute("aria-label", `${label}：${initialOption.textContent}`);
    options.forEach(option => option.setAttribute("aria-pressed", String(option === initialOption)));
  }

  function setOpen(open) {
    if (open && button.disabled) return;
    if (open) onOpen();
    menu.hidden = !open;
    button.setAttribute("aria-expanded", String(open));
    if (open) {
      const availableAbove = container.getBoundingClientRect().top - viewport.getBoundingClientRect().top;
      menu.classList.toggle("below", availableAbove < menu.offsetHeight + 8);
    }
  }

  button.addEventListener("click", () => setOpen(menu.hidden));
  options.forEach(option => option.addEventListener("click", () => {
    if (button.disabled) return;
    const value = Number(option.dataset[attribute]);
    setOpen(false);
    if (!Number.isFinite(value) || value === selected) return;
    selected = value;
    button.textContent = option.textContent;
    button.setAttribute("aria-label", `${label}：${option.textContent}`);
    options.forEach(item => item.setAttribute("aria-pressed", String(item === option)));
    onChange(value);
  }));

  return { setOpen };
}
