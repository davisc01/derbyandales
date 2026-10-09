#!/bin/bash
# Turns a fresh Raspberry Pi OS Lite install into the race display: on power-up
# it opens the Derby and Ales display full screen, with no keyboard or mouse.
#
#   sudo ./setup.sh
#
# Safe to run again: it replaces its own files and leaves derbyandales.txt
# alone once that exists, because by then somebody has filled it in.

set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
	echo "Run this with sudo: sudo ./setup.sh" >&2
	exit 1
fi

HERE=$(cd "$(dirname "$0")" && pwd)
DEST=/opt/derbyandales-kiosk
KIOSK_USER=kiosk

. "$HERE/settings.sh"

echo "== Installing cage, Chromium and fonts"
apt-get update
# Raspberry Pi OS Bookworm ships its tuned build as chromium-browser; Trixie
# renamed it chromium. Prefer whichever has a real candidate.
browser=chromium
if apt-cache policy chromium-browser 2>/dev/null | grep -q 'Candidate: [0-9]'; then
	browser=chromium-browser
fi
# The display asks for system fonts that Pi OS Lite does not have; Roboto and
# DejaVu are what it falls back to, rather than whatever fontconfig picks.
DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
	cage "$browser" curl fonts-roboto fonts-dejavu-core

echo "== Creating the $KIOSK_USER user"
if ! id "$KIOSK_USER" >/dev/null 2>&1; then
	useradd --create-home --shell /usr/sbin/nologin "$KIOSK_USER"
fi
# video/render for the GPU, input so cage sees a keyboard plugged in to debug.
usermod -aG video,render,input "$KIOSK_USER"

echo "== Installing the kiosk to $DEST"
mkdir -p "$DEST"
install -m 0644 "$HERE/settings.sh" "$HERE/waiting.html" "$DEST/"
install -m 0755 "$HERE/kiosk.sh" "$HERE/apply-settings.sh" "$DEST/"
if [ -f "$HERE/logo.png" ]; then
	install -m 0644 "$HERE/logo.png" "$DEST/"
fi

if [ ! -f "$SETTINGS_FILE" ]; then
	echo "== Writing $SETTINGS_FILE"
	cat >"$SETTINGS_FILE" <<'EOF'
# Derby and Ales race display settings.
#
# This file is on the SD card's boot partition: put the card in the Mac and it
# shows up as "bootfs". Edit it in a plain-text editor (TextEdit: Format > Make
# Plain Text), eject, and put the card back.

# The Mac running Derby and Ales. Its fixed address at the venue, port 8080.
SERVER=http://192.168.1.42:8080

# Tried when SERVER does not answer. The Mac's name as shown on its Status page.
FALLBACK=http://race-mac.local:8080

# Leave blank to use the TV's own resolution. On a Pi 3 that stutters, try
# 1280x720: the display scales to fit, so it looks the same with far less to
# draw. The Pi reboots once by itself to apply a change here.
RESOLUTION=

# yes: results appear in place instead of the cars sliding across. Nothing is
# left out, it just doesn't move. Another fix for a Pi 3 that stutters.
REDUCED_MOTION=no
EOF
fi

echo "== Installing the service"
# A login session of its own, so cage can open the screen through logind.
cat >/etc/pam.d/derbyandales-kiosk <<'EOF'
auth     required pam_unix.so nullok
account  required pam_unix.so
session  required pam_unix.so
session  required pam_systemd.so
EOF

cat >/etc/systemd/system/derbyandales-kiosk.service <<EOF
[Unit]
Description=Derby and Ales race display
After=systemd-user-sessions.service network-online.target systemd-logind.service
Wants=network-online.target
Conflicts=getty@tty1.service
After=getty@tty1.service

[Service]
User=$KIOSK_USER
PAMName=derbyandales-kiosk
WorkingDirectory=/home/$KIOSK_USER
# + runs it as root: it may edit cmdline.txt and reboot.
ExecStartPre=+$DEST/apply-settings.sh
ExecStart=$DEST/kiosk.sh
Restart=always
RestartSec=2
TTYPath=/dev/tty1
TTYReset=yes
TTYVHangup=yes
TTYVTDisallocate=yes
StandardInput=tty-fail
StandardOutput=journal
StandardError=journal
UtmpIdentifier=tty1
UtmpMode=user

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl disable getty@tty1.service >/dev/null 2>&1 || true
systemctl enable derbyandales-kiosk.service

read_settings
echo
echo "Done. The display will look for the race server at:"
echo "  ${SERVER:-(not set)}"
[ -n "$FALLBACK" ] && echo "  then ${FALLBACK}"
echo
echo "Edit $SETTINGS_FILE if that is wrong, then: sudo reboot"
