#!/bin/bash
# The kiosk itself: one full-screen Chromium inside cage, started by
# derbyandales-kiosk.service as the kiosk user. If Chromium exits for any
# reason, cage exits with it and systemd starts both again.

set -u
. /opt/derbyandales-kiosk/settings.sh
read_settings

if [ -z "$SERVER" ] && [ -z "$FALLBACK" ]; then
	# The waiting page says so on the TV, which is where somebody will look.
	echo "derbyandales: no SERVER in $SETTINGS_FILE"
fi

PROFILE="$HOME/.config/derbyandales"
mkdir -p "$PROFILE/Default"

# The Pi is unplugged at the end of every night. Chromium takes that as a crash
# and offers to restore pages, in a bubble that would sit on the race display.
prefs="$PROFILE/Default/Preferences"
if [ -f "$prefs" ]; then
	sed -i -e 's/"exited_cleanly":false/"exited_cleanly":true/' \
		-e 's/"exit_type":"[^"]*"/"exit_type":"Normal"/' "$prefs"
fi

CHROMIUM=$(command -v chromium-browser || command -v chromium)

flags=(
	--kiosk
	--ozone-platform=wayland
	# Kept between boots: it holds the display's token, so the TV comes back
	# with the name and scene the coordinator gave it. Never --incognito.
	--user-data-dir="$PROFILE"
	--no-first-run
	--noerrdialogs
	--disable-session-crashed-bubble
	--disable-infobars
	--check-for-update-interval=31536000
	# No keyring prompt on a machine nobody is sitting at.
	--password-store=basic
	--overscroll-history-navigation=0
	--disable-pinch
	# The waiting page is a file:// page reading /healthz on a LAN address.
	# Newer Chromium asks permission for requests onto the local network, and
	# there is nobody at this screen to click Allow.
	--disable-features=Translate,LocalNetworkAccessChecks,PrivateNetworkAccessSendPreflights,PrivateNetworkAccessRespectPreflightResults
)
if [ "$REDUCED_MOTION" = yes ]; then
	# The display already has a still mode for this: rows appear rather than
	# slide, at the same pace. For a Pi 3 that stutters on the animation.
	flags+=(--force-prefers-reduced-motion)
fi

url="file:///opt/derbyandales-kiosk/waiting.html?server=$SERVER&fallback=$FALLBACK"
exec cage -- "$CHROMIUM" "${flags[@]}" "$url"
