#!/system/bin/sh

MODDIR=${0%/*}
STATE_DIR=/data/adb/screenbridge
CONFIG_FILE="$STATE_DIR/config.conf"
PID_FILE="$STATE_DIR/server.pid"
LOG_FILE="$STATE_DIR/server.log"
SERVER="$MODDIR/bin/screenbridge"

mkdir -p "$STATE_DIR"
[ -f "$CONFIG_FILE" ] || cp "$MODDIR/config.conf.example" "$CONFIG_FILE"

if [ -f "$PID_FILE" ]; then
  old_pid=$(cat "$PID_FILE" 2>/dev/null)
  if [ -n "$old_pid" ] && [ -r "/proc/$old_pid/cmdline" ]; then
    old_cmdline=$(tr '\000' ' ' < "/proc/$old_pid/cmdline" 2>/dev/null)
    case "$old_cmdline" in
      *"$SERVER"*) exit 0 ;;
    esac
  fi
  rm -f "$PID_FILE"
fi

nohup "$SERVER" -module-dir "$MODDIR" -state-dir "$STATE_DIR" \
  >> "$LOG_FILE" 2>&1 &
echo $! > "$PID_FILE"
