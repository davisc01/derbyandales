# Reads /boot/firmware/derbyandales.txt. Sourced by kiosk.sh and
# apply-settings.sh, never run on its own.
#
# The file is parsed rather than sourced. It is edited on a Mac with the SD card
# plugged in, so it may arrive with CRLF line endings or a stray character, and
# sourcing it would run whatever it contains as root at boot.

BOOT_DIR=/boot/firmware
[ -d "$BOOT_DIR" ] || BOOT_DIR=/boot
SETTINGS_FILE="$BOOT_DIR/derbyandales.txt"

SERVER=""
FALLBACK=""
RESOLUTION=""
REDUCED_MOTION="no"

read_settings() {
	[ -r "$SETTINGS_FILE" ] || return 0
	local line key value
	while IFS= read -r line || [ -n "$line" ]; do
		line=${line%$'\r'}
		line=${line%%#*}
		case "$line" in *=*) ;; *) continue ;; esac
		key=$(printf '%s' "${line%%=*}" | tr -d '[:space:]')
		value=$(printf '%s' "${line#*=}" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"//' -e 's/"$//')
		case "$key" in
		SERVER) SERVER=$value ;;
		FALLBACK) FALLBACK=$value ;;
		RESOLUTION) RESOLUTION=$value ;;
		REDUCED_MOTION) REDUCED_MOTION=$value ;;
		esac
	done <"$SETTINGS_FILE"

	SERVER=$(clean_url "$SERVER")
	FALLBACK=$(clean_url "$FALLBACK")
	# A typo here must not become a kernel argument; a bad value means the TV's
	# own preferred mode, which always works.
	case "$RESOLUTION" in
	'' | *[!0-9x]* | x* | *x | *x*x*) RESOLUTION="" ;;
	esac
	case "$REDUCED_MOTION" in
	[Yy]*) REDUCED_MOTION=yes ;;
	*) REDUCED_MOTION=no ;;
	esac
}

# clean_url keeps only an http(s) address with a host and optional port, and
# drops a trailing slash so "<server>/display" is never "//display".
clean_url() {
	local url=${1%/}
	if printf '%s' "$url" | grep -Eq '^https?://[A-Za-z0-9.-]+(:[0-9]+)?$'; then
		printf '%s' "$url"
	fi
}
