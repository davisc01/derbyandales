#!/bin/bash
# Runs as root before the kiosk starts (ExecStartPre=+). Brings the kernel
# command line in line with derbyandales.txt, so a setting changed on the Mac
# with the SD card plugged in takes effect with no keyboard and no SSH.
#
# Only the screen mode needs this: it is a kernel argument, and it only takes
# effect from boot. When the line changes we reboot once; on the next boot it
# matches and nothing happens, so this cannot loop.

set -uf  # -f: split the command line on spaces without globbing it
. /opt/derbyandales-kiosk/settings.sh
read_settings

CMDLINE="$BOOT_DIR/cmdline.txt"
[ -w "$CMDLINE" ] || exit 0

current=$(tr -d '\r\n' <"$CMDLINE")

# Drop the arguments this script owns and keep everything else exactly as it
# was. cmdline.txt also holds the root partition; losing that is a Pi that does
# not boot.
kept=""
for arg in $current; do
	case "$arg" in
	video=HDMI-A-1:* | video=HDMI-A-2:* | consoleblank=* | vt.global_cursor_default=*) ;;
	*) kept="$kept $arg" ;;
	esac
done

# A blanked console or a blinking cursor would show for a second at every boot
# and every Chromium restart.
want="${kept# } consoleblank=0 vt.global_cursor_default=0"
if [ -n "$RESOLUTION" ]; then
	# Both ports, because a Pi 4 or 5 has two and either may be the one in use.
	want="$want video=HDMI-A-1:${RESOLUTION}@60 video=HDMI-A-2:${RESOLUTION}@60"
fi

if [ "$want" != "$current" ]; then
	echo "derbyandales: kernel command line changed; rebooting to apply"
	printf '%s\n' "$want" >"$CMDLINE.new" && sync && mv "$CMDLINE.new" "$CMDLINE" && sync || exit 0
	systemctl --no-block reboot
	# Fail so the kiosk does not start against the old mode.
	exit 1
fi
exit 0
