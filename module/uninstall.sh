#!/system/bin/sh

MODDIR=${0%/*}
SERVER="$MODDIR/bin/screenbridge"
STATE_DIR=/data/adb/screenbridge
PID_FILE="$STATE_DIR/server.pid"

if [ -f "$PID_FILE" ]; then
  pid=$(cat "$PID_FILE" 2>/dev/null)
  case "$pid" in
    ""|*[!0-9]*) ;;
    *)
      cmdline=$(tr '\000' ' ' < "/proc/$pid/cmdline" 2>/dev/null)
      case "$cmdline" in
        *"$SERVER"*) kill "$pid" 2>/dev/null; sleep 1 ;;
      esac
      ;;
  esac
fi

rm -rf "$STATE_DIR"
