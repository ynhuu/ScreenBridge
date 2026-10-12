#!/system/bin/sh

SKIPUNZIP=0

ui_print "- Installing ScreenBridge"
set_perm_recursive "$MODPATH" 0 0 0755 0644
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/uninstall.sh" 0 0 0755
set_perm "$MODPATH/bin/screenbridge" 0 0 0755
