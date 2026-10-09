# Raspberry Pi race display

Plug a Pi into the TV and into power, and it opens the race display full screen
by itself, with no keyboard or mouse. Once set up, the Pi needs nothing done to
it. The coordinator picks what the TV shows from the **Displays** page, the same
as for any other screen.

On power-up the Pi shows "Waiting for the race server" until Derby and Ales
answers on the Mac, then switches to the display. You can start the Pi and the
Mac in either order. If the Mac drops out partway through the night, the display
shows that it lost contact and picks back up when the Mac returns.

## Which Pi

A **Pi 4** is the safe choice. A **Pi 3** runs it but hasn't been tested against
the animations; its graphics are weak and the cars sliding across now-racing may
stutter. Rehearse on it with `-demo` before trusting it. If it stutters, there
are two settings below that fix it. Failing that, the same SD card boots in a
Pi 4. A Pi 5 is more than you need.

Use wired ethernet if the venue router has a free port. The Pi 3 only has
2.4 GHz wifi.

## Setting it up (once)

1. **Flash the card.** In Raspberry Pi Imager, choose **Raspberry Pi OS Lite
   (64-bit)**. Before writing it, open the settings (the gear, or "Edit
   settings") and set:
   - a hostname, e.g. `derby-tv`
   - a username and password
   - the venue wifi (skip if using ethernet)
   - **Enable SSH** under Services
2. **Boot the Pi** on the same network as the Mac and wait a minute.
3. **Copy the kiosk over and run setup**, from the repo on the Mac:
   ```sh
   make pi
   scp -r dist/pi derby-tv.local:
   ssh derby-tv.local 'cd pi && sudo ./setup.sh'
   ```
   This takes a few minutes. Most of that is installing Chromium.
4. **Set the Mac's address**, either over SSH (`sudo nano
   /boot/firmware/derbyandales.txt`) or by putting the SD card in the Mac (see
   below).
5. `ssh derby-tv.local sudo reboot`. The TV should show the waiting page, then
   the display once the app is running on the Mac.
6. On the Mac, open **Displays**. The Pi shows up as a new display. Rename it
   (e.g. "Main TV") and give it its scene. It remembers both from then on,
   across reboots.

## Settings: `derbyandales.txt`

The settings file is on the SD card's boot partition, which a Mac can read. Put
the card in the Mac and it appears as **bootfs**. Open `derbyandales.txt` in a
plain-text editor (in TextEdit, use Format > Make Plain Text), edit it, eject the
card and put it back in the Pi.

| Setting | What it does |
|---|---|
| `SERVER` | The Mac's fixed address at the venue, e.g. `http://192.168.1.42:8080`. Use the `http://` one shown on the Mac's Status page, not `https://`. |
| `FALLBACK` | Tried when `SERVER` doesn't answer: the Mac's `.local` name, e.g. `http://race-mac.local:8080`. |
| `RESOLUTION` | Blank uses the TV's own resolution. `1280x720` gives a Pi 3 less than half the pixels to draw, and looks the same because the display scales to fit. The Pi reboots once by itself to apply it. |
| `REDUCED_MOTION` | `yes` makes results appear in place instead of the cars sliding across. Nothing is left out; it just doesn't move. |

If a Pi 3 stutters, try `RESOLUTION=1280x720` first, then add
`REDUCED_MOTION=yes`.

The Pi manages the `video=`, `consoleblank=` and `vt.global_cursor_default=`
entries in `cmdline.txt` itself. Don't edit those by hand: they're rewritten
from `derbyandales.txt` at every boot.

## When something's wrong

The TV itself says what it's waiting for, and after a minute it suggests what to
check. To look more closely, SSH in:

```sh
ssh derby-tv.local
journalctl -u derbyandales-kiosk -b      # this boot's log
sudo systemctl restart derbyandales-kiosk
```

To use the Pi as a normal computer again, run `sudo systemctl disable
derbyandales-kiosk` and reboot.

**About pulling the plug.** At the end of the night the Pi is usually just
unplugged. The kiosk hides Chromium's "restore pages?" bubble that this would
otherwise cause. A card that has been unplugged hundreds of times can still
corrupt, so keep a spare flashed. Raspberry Pi OS's read-only overlay would
prevent corruption, but don't use it here: it also wipes the display's saved
identity at every boot, so the Pi would show up as a new display each night and
need setting up again.

## What's installed

`setup.sh` installs `cage` (a compositor that runs one app full screen) and
Chromium, creates a `kiosk` user, and adds `derbyandales-kiosk.service`, which
starts them on the TV at boot and restarts them if Chromium exits. The files
live in `/opt/derbyandales-kiosk/`. Running `setup.sh` again updates them and
leaves `derbyandales.txt` alone.
