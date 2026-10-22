# scterm

scterm shows the screen of an Android device on a Linux computer, plays its sound, and lets you
control it with the keyboard and mouse. It shows the device in the terminal, in a web browser
(`--web`), or in a window of its own (`--window`). It is its own client for the
[scrcpy](https://github.com/Genymobile/scrcpy) protocol and bundles the scrcpy server, so it
needs adb but not the scrcpy program. With the scterm Android app it can also reach a device
over the network without adb: this is peer mode.

- [Requirements](#requirements)
- [Install](#install)
- [Quick start](#quick-start)
- [Displays](#displays)
- [Controls](#controls)
- [Peer mode](#peer-mode)
- [Updating](#updating)
- [Troubleshooting](#troubleshooting)
- [Reference](#reference)
- [How it works](#how-it-works)
- [Development](#development)
- [Credits](#credits)
- [License](#license)

## Requirements

To run scterm you need:

- Linux. On x86-64 (amd64) scterm needs a CPU with AVX2, also when it is built from source.
  Release binaries are for amd64 only.
- `adb` from the Android platform-tools on your `PATH` (often packaged as `android-tools` or
  `adb`). Peer mode does not use it.
- An Android device with USB debugging or wireless debugging turned on. The bundled server
  works with Android 5.0 and later. Sound needs Android 11 or later.
- The FFmpeg libraries libavcodec, libavutil, libswscale and libswresample, and
  libpulse-simple. Release binaries are built on Ubuntu 26.04 and need the versions of these
  libraries they were built with. If the release binary does not start because of a library,
  build from source. If it stops with `SIGILL: illegal instruction` (exit status 2), the CPU
  lacks AVX2, and a build from source stops the same way.
- For sound in the terminal display: a PulseAudio server (PipeWire's PulseAudio service also
  works). Without one, the terminal display has no sound; the browser display plays sound
  without it. scterm also uses `pactl`, when it is installed, to follow the output device the
  desktop selects. Without `pactl`, it plays on the server's default output, and when you pick
  another output, the sound moves only if the server moves it (PipeWire usually does).
- For `--window`: a Chromium-family browser or Firefox.

To build from source you also need Go 1.27 or later, a C compiler, `pkg-config`, and the
development packages of the libraries above. On Debian or Ubuntu:

```sh
sudo apt-get install gcc pkg-config libavcodec-dev libavutil-dev libswscale-dev \
  libswresample-dev libpulse-dev
```

Only Linux is supported: the terminal code is Linux-only. On aarch64 the C code has NEON paths,
but aarch64 builds are untested. 32-bit targets do not build.

## Install

### Release binary

Download `scterm` from the
[latest release](https://github.com/pwnedbygary/scrcpy-terminal/releases/latest), then install
it in a directory on your `PATH`:

```sh
install -m 755 scterm ~/.local/bin/scterm
scterm version
```

### From source

```sh
git clone https://github.com/pwnedbygary/scrcpy-terminal.git
cd scrcpy-terminal
go build -o scterm .
./scterm version
```

A build from source reports its version as `scterm dev`. Keep the binary in the checkout if you
want `scterm update` to update it (see [Updating](#updating)).

## Quick start

1. On the device, turn on USB debugging. On most devices: open Settings, then About phone, tap
   Build number seven times, then turn on USB debugging in Developer options.
2. Connect the device with a USB cable. Unlock it and accept the "Allow USB debugging?" prompt.
3. Run `scterm`.

The device appears in the terminal. The bottom line starts with `Alt+Q quit · Alt+/ controls`:
Alt+Q quits, and Alt+/ opens the Controls menu, which lists every action. Ctrl+C does not quit:
it goes to the device.

To use the device over Wi-Fi, connect it with `adb pair` and `adb connect` first. With several
devices connected, choose one with `-s SERIAL`, using the serial that `adb devices` shows.

## Displays

### Terminal

This is the default display. Each character cell shows two pixels, one above the other, so the
terminal needs 24-bit colour. The picture keeps its shape. scterm redraws only the rows that
change, and redraws the whole screen after a resize and every 300 frames
(`--repaint-interval`), which clears any garbling.

The mouse needs a terminal that reports it in SGR mode, as most current terminals do. While
scterm runs, the mouse belongs to the device: it is "grabbed". F12, Ctrl+G or Alt+G gives the
mouse back to the terminal, for example to select text, and gives it to the device again; keys
still go to the device. In Zellij, scterm starts with the mouse and typing left to Zellij
(F-keys, Alt chords and Esc still reach the device), and the bottom line says
`typing goes to Zellij · F12 gives control`.

The bottom line starts with the two hints. After them come where input goes (when it is not
grabbed), the volume of the sound on this computer, the device name, the picture size, the frame
rate of the video stream (the one `--no-tui` prints), and a graph of the time between the frames
drawn. A message, such as a saved screenshot or an error, takes the place of these details
for a few seconds. When it exits, scterm prints the messages again: the last 50 different ones,
with a count of any older ones. After a first press on Quit
in the action bar, the menu or the on-screen keyboard, the line says `press Quit again to quit`
right after the hints until the second press, or for 2 seconds.

If the device is lost during the session, scterm ends with a line that says why, for example
`scterm: lost the video stream from Pixel 8: …`, and exit status 2.

### Browser

```sh
scterm --web
```

scterm prints the address, `http://127.0.0.1:6969/` by default. Open it in a web browser.
`--web-port` changes the port (0 picks a free one). scterm listens on the port before it
connects to the device, so a port that is in use, or that it cannot listen on, stops scterm at
once, with a line that says why, and for the usual causes, listed under Troubleshooting, what
to do. The terminal stays a normal terminal and shows log lines; Ctrl+C there ends scterm.
Several browsers can watch at once. Closing a tab does not end the session, but Quit on the
page does.

Sound plays in the browser, and the speakers of the computer that runs scterm stay silent
(`--audio-dup` plays it in both places, and on the device too; on Android 11 and 12 it then
plays on the device only, and scterm says why). When the browser holds sound back until you use
the page, a chip on the page says *Tap for sound*: any tap, click or key starts it. Opened over
plain http from another computer, the page plays sound through a compatibility path with a
little more delay, and the chip says *Sound: compatibility mode*. A `localhost` address, for
example through an SSH tunnel, gives the low-delay path.

A card on the picture says what the page is waiting for: *Connecting…*, *Waiting for video
from …*, *Connection lost. Reconnecting…* (the page reconnects by itself), or *Session ended*
with the reason and a *Reconnect* button. Once the session has ended, the keys are the
browser's again, so F5 reloads the page; Alt+/ still opens the Controls sheet, and Alt+S still
saves the last picture.

On a phone or tablet, a tap or a swipe on the picture goes to the device and does not bring up
the action bar. The ⋯ button next to ☰ shows the bar until you press ⋯ again or touch the
picture. The bar is one row: swipe sideways on its buttons for the rest of them (a swipe that
starts between buttons passes through the bar, to the device where the picture is under it).
The page keeps its buttons clear of a notch. A pinch outside the picture zooms the page; a pinch
on the picture does nothing.

> **Warning:** by default only this computer can open the page. With `--web-addr 0.0.0.0`, or
> any address that is not a loopback address, anyone who can reach the port can see the screen
> and control the device. There is no password and no encryption. In the page's address, scterm
> accepts only an IP address, `localhost` or a name that ends in `.localhost`, the name given to
> `--web-addr`, or a name in `SCTERM_WEB_HOSTS`. With an address that is not a loopback address,
> it also accepts this computer's name, with or without `.local`. Any other name gets an error
> that says what to do. This stops a website that points its own name at this computer (DNS
> rebinding) from seeing and controlling the device. The WebSocket connection also refuses a page
> opened on another address. scterm also tells the browser not to show the page inside another
> page, where a website could trick you into clicking or typing on it. These checks do not stop
> anyone who can reach the port and uses this computer's address, or, with an address that is not
> a loopback address, someone on your network who can answer for this computer's name. scterm
> accepts a name in `SCTERM_WEB_HOSTS` or given to `--web-addr` with any address, a loopback
> address included, so the checks do not stop someone who can answer for that name either. For a
> `.local` name, that is anyone on your network. Use such addresses and names only on a network
> you trust, or keep the default address, list no names in `SCTERM_WEB_HOSTS`, and use an SSH
> tunnel: `ssh -L 6969:127.0.0.1:6969 HOST`.

### Window

```sh
scterm --window
```

`--window` is `--web` in a browser window of its own. scterm prefers a Chromium-family browser
(Chromium, Google Chrome, Brave, Microsoft Edge, Vivaldi). It opens it as an app window, with
no tabs or address bar, and a private profile in `~/.cache/scterm/chromium`. With a new
profile, the first window takes a few seconds to show the picture. Without Chromium, scterm
opens Firefox with a temporary profile, in a normal window. `SCTERM_CHROME` chooses the browser,
and `SCTERM_FIREFOX_KIOSK=1` opens Firefox full screen without toolbars.

A Chromium window opens at the size of the picture (the device's screen, scaled down to
`--max-size`), at most 1000 pixels tall, and `--window-size WxH` sets another size. With
`--peer`, scterm does not know the device's screen size, so the window opens at a default size.
Sound starts without a tap. Closing the window ends scterm about 3 seconds later; reloading
the page keeps the session. Quitting scterm closes the window.

When the environment names no display, as in a Zellij pane, scterm finds the Wayland or X
socket itself and says which one it uses. When the browser exits within 15 seconds without
showing the picture, scterm prints the browser's error lines and the address it still serves.

## Controls

The terminal, the browser and the window share one set of actions. Almost every action has a
button on the action bar, which appears when you use the mouse and fades a few seconds after
you stop, but not while the mouse rests on it. On a touch screen, the browser's ⋯ button shows
it.
Most actions also have an Alt+letter chord and an F-key; the Alt chords exist because phone
keyboards have Alt but no F-keys. Alt+/ opens the Controls menu (terminal) or the Controls
sheet (browser), which lists the actions with their keys: click a row, or in the terminal press
its letter.

The last two columns say where each action is: on the action bar and in the Controls menu in
the terminal, on the action bar and in the Controls sheet in the browser.

| Action | Button | Alt | F-key | Terminal | Browser |
| --- | --- | --- | --- | --- | --- |
| Back | Back | Alt+B | — | bar, menu | bar, sheet |
| Home | Home | Alt+H | F1 | bar, menu | bar, sheet |
| Recents | Rec | Alt+T | F3 | bar, menu | bar, sheet |
| Menu key | Menu | — | F2 | bar, menu | bar, sheet |
| Controls | Controls | Alt+/ | — | — | bar, ☰ button |
| Volume up | Vol+ | Alt+U | F6 | bar, menu | bar, sheet |
| Volume down | Vol- | Alt+D | F5 | bar, menu | bar, sheet |
| Mute device | Mute | — | F7 | bar | bar, sheet |
| Rotate | Rot | Alt+R | F8 | bar, menu | bar, sheet |
| Notifications | Notif | Alt+N | F9 | bar, menu | bar, sheet |
| Settings shade | Shade | Alt+E | F10 | bar, menu | bar, sheet |
| Collapse panels | Collapse | Alt+C | F11 | bar, menu | bar, sheet |
| Power | Power | Alt+P | F4 | bar, menu | bar, sheet |
| Keyboard | Keys | Alt+I | — | bar, menu | bar, sheet |
| Screenshot | Shot | Alt+S | — | bar, menu | bar, sheet |
| Refresh video | Redraw | Alt+K | — | bar, menu | bar, sheet |
| Grab | Grab | Alt+G | F12 | bar, menu | bar, sheet |
| Quit | Quit | Alt+Q | — | bar, menu | bar, sheet |

- Esc is Back too, and the arrow keys are the D-pad.
- Controls is the menu or the sheet itself. In the browser, the ☰ button always opens it.
- Mute device mutes the device's own sound. It has no Alt chord; in the terminal it is on F7,
  the action bar and the on-screen keyboard.
- Quit: Alt+Q quits at once, and so does Ctrl+Q while the terminal's Controls menu is open. A
  Quit button, the Quit row and the QUIT key of the on-screen keyboard ask for a second press
  within two seconds.
- Grab, in the terminal, gives the mouse to the device or back to the terminal (in Zellij, the
  keys too). In the browser the mouse always goes to the device, and Grab decides where some
  Ctrl keys go (see [Typing](#typing)).
- Refresh video asks the device for a new keyframe, which repaints a damaged picture.
- In the browser, Alt+F1 to Alt+F12 do the same as F1 to F12. During a session, F5 and F11 do
  not reload the page or switch to full screen. The *Full screen* button, last on the action
  bar and also in the Controls sheet, fills the screen where the browser allows it, and Esc
  then leaves full screen instead of pressing Back.

### Mouse and touch

| Input | Terminal | Browser |
| --- | --- | --- |
| Click | Tap | Tap |
| Drag | Swipe | Swipe |
| Wheel | Scroll | Scroll |
| Right click | Back | Back |
| Middle click | Home | Home |
| One finger | — | Tap, swipe, long press |
| Three-finger tap | — | Show or hide your phone's on-screen keyboard |

In the terminal, the mouse reaches the device only while it is grabbed. A click on the action
bar, the Controls menu or the on-screen keyboard runs that control and does not tap the device.
In the browser, a long press is a long touch on the device, not Back, and only one finger
reaches the device. A pinch on the picture does nothing; a pinch outside it zooms the page.

### Typing

Letters, digits and space go to the device as key events, so games see them. Other characters
are typed as text. Enter, Backspace, Tab, Delete, the arrows, Home, End, Page Up and Page Down
go as keys.

In the terminal, Ctrl with a letter goes to the device as that key with Ctrl, except that
Ctrl+G toggles Grab and Ctrl+K and Ctrl+O show or hide the on-screen keyboard. Terminals send
Ctrl+I, Ctrl+M and Ctrl+[ as Tab, Enter and Esc. Ctrl+C goes to the device and does not stop
scterm. Alt with a letter that has no action does nothing.

A paste in the terminal goes to the device as one paste, through the device's clipboard
(which it replaces), with its line breaks kept as line breaks; nothing in it acts as a key
except Ctrl+C, which cancels it (so a paste holding a Ctrl+C character is not sent). After a
Ctrl+C, scterm drops what comes up to the end of the paste, or until the terminal sends nothing
for a quarter of a second, so the rest of a paste that pauses that long after its Ctrl+C is
typed as keys. A paired device that does not share its clipboard gets the text typed instead,
up to 4,096 bytes, and a paired device takes a paste only while this computer has control of
it (`--takeover`). A paste longer than 262,130 bytes is not sent; scterm says when one of
these stops a paste. If the terminal starts a paste and never ends it, keys do nothing until
Ctrl+C cancels it; after two seconds the bottom line says so. Over adb, the server types
accented letters but not the euro sign, CJK characters or emoji; a paste carries them.

In the browser, Ctrl with a letter also goes to the device as that key with Ctrl, except:

- Ctrl+C stays with the browser (copy), and Ctrl+V is not sent. Text pasted into the page is
  typed on the device.
- With Grab off (the default), Ctrl+W, Ctrl+T, Ctrl+N, Ctrl+R and Ctrl+Q stay with the
  browser. With Grab on, the first four go to the device and Ctrl+Q ends the session. A browser
  may still keep some of these keys for itself.

Alt with a letter that has no action types that letter.

### On-screen keyboard

In the terminal, Alt+I, Ctrl+K, Ctrl+O or the Keys button opens a keyboard over the bottom of
the picture. It has letters, digits, the D-pad, Android's own keys, F1 to F12 and a row of
actions. Click a key, or move with the arrow keys and press Enter. Shift, Ctrl and Alt apply to
the next key. Clicks above the keyboard still tap the device. Esc or F12 closes it.

In the browser, Alt+I, the Keys button or a three-finger tap shows the on-screen keyboard of
the phone or tablet you browse from, and what you type there goes to the device. If a phone
does not show it after Alt+I, tap the screen once.

### Screenshots

Alt+S, or the Shot button, saves a screenshot: the device's picture at the stream's resolution
(the size the device sends, which `--max-size` limits), as `scterm-YYYYMMDD-HHMMSS.png`. In the
browser, the page downloads it. In the terminal, scterm saves it in the current directory,
without the bars around the picture, and the bottom line names the file. A second screenshot in
the same second is named `scterm-YYYYMMDD-HHMMSS-2.png`, the next one `-3`, and so on. In the
terminal, a press while a screenshot is still being taken or saved takes no new one, and scterm
ignores an Alt+S that comes within 0.2 seconds of the one before it. With the usual repeat
rates, holding Alt+S down there therefore saves one screenshot or two: the key's first repeat
comes later than 0.2 seconds, the others sooner. With repeats 0.2 seconds or more apart, each
repeat takes another, unless one is still being taken or saved. In the browser, holding it down
saves one. When the device sends no new frames, as when its screen does not change or is off,
scterm saves the last frame it received. If scterm cannot get the frame within a second, it
saves what the terminal shows instead, at the terminal's resolution (one pixel for each half
cell), and the bottom line says so.

### Sound on this computer

Alt+M mutes the sound on this computer, and Alt+- and Alt+= turn it down and up in steps of
10%, from 0% to 200%. They never change the device's volume; Volume up and Volume down do that.
In the terminal they act on scterm's PulseAudio stream, and in the browser on the page's own
volume.

## Peer mode

The scterm Android app can serve its device's screen to paired devices. scterm connects to it
over the network, without adb, and shows it in any of the displays. The app sends scrcpy's own
stream format inside mutual TLS, so everything after the connection works as with adb. To
install and use the app, see [android/README.md](android/README.md).

scterm's `--peer` is tested against the app's own target code running on a computer. It has
not yet been run against a phone.

### Pairing

Pair each device once, in one of two ways:

- **Nearby**, on the same network. On the device, tap *Start serving*, then *Invite a
  device…*. Run `scterm pair`. scterm looks for up to 30 seconds for a device that shows an
  invitation, and asks which one when it finds several. Then both screens show a six-digit
  code: check that they match, and accept on both.
- **Typed invitation**, from any network that can reach the device, such as a VPN. The device
  shows its invitation as an address and a code. Give both to `scterm pair`, in quotes:
  `scterm pair "192.168.1.20:27300 04HM-ASW9-NF6Y-Y093"`. A `scterm://pair` link also works.

An invitation works once, for 10 minutes. When pairing succeeds, scterm says what the device
allows this computer to do, and how to connect to it.

### Connecting

```sh
scterm --peer pixel                       # by the start of its name, or of a word in it
scterm --peer=                            # the only paired device; asks when there are several
scterm --peer "Pixel 8" --window --takeover
```

`NAME` is the device's name in any case, the start of the name or of a word in it, or the start
of its identity (at least four characters). When several devices match, scterm asks which one,
or names them when there is no terminal to ask on.

- The device decides what this computer may do (view, hear, control, share the clipboard), and
  can change that at any time. When it ends the session, scterm prints its reason.
- One device controls at a time. Without `--takeover`, scterm watches while another device has
  control, and says so; `--takeover` takes control.
- scterm sends `--no-video`, `--no-audio`, `--no-control`, `--max-size`, `--max-fps`, `-b` and
  `--audio-codec` to the device, and the device decides what it can do. `--audio-source`,
  `--audio-bit-rate` and the device side of `--audio-dup` have no effect in peer mode.
- What works depends on how the device serves. The full-control helper works like adb. Screen
  capture on a standard install has no sound and takes one finger.

### Managing pairings

- `scterm peers` lists the paired devices without connecting: name, short identity, address,
  and what each one allows.
- `scterm peers --check` also connects to each device, authenticated, without starting a stream
  or taking control, and prints *Ready* or the problem. *Ready* means viewing works; another
  device may still have control. It checks two devices at a time, for at most five seconds
  each. It also finds a device whose address changed on the local network, and checks its
  identity before it saves the new address.
- `scterm forget NAME` forgets a device. The whole name, in any case, or the identity forgets
  it at once. Part of a name asks for confirmation, and is refused when there is no terminal to
  ask on. The device keeps this computer in its own list until you remove it there.

This computer's identity and the paired devices are in `~/.config/scterm` (see
[Files](#files)).

## Updating

`scterm update` updates scterm in place:

- A release binary asks GitHub for the latest release. When that is newer, scterm shows the
  new version, its release page and the start of its release notes, and on a terminal asks
  before it goes on (`-y` or `--yes` skips the question; without a terminal it goes on). It
  downloads the binary next to itself, with a progress line on a terminal, and checks it
  against the SHA-256 digest and the size that GitHub publishes for it (it says when there is
  no digest). Then it runs the new binary's `version` command, to check that it starts on this
  system and is the expected version, and only then replaces itself. The previous binary stays
  beside it, with `.old` added to its name, and scterm prints the `mv` command that goes back
  to it. When scterm cannot make that copy (it cannot read its own binary, or there is no
  room), it does not update. When it may not write to its own directory, it prints the
  `sudo install` command that finishes the update. Releases contain a linux/amd64 binary only.
- A build from source (`scterm dev`) runs `git pull --ff-only` in its checkout. Unless the
  binary was built from the new commit without local changes, scterm rebuilds it with
  `go build`, checks that it starts, and replaces itself. The binary must be in the checkout,
  or one or two directories below it.

Ctrl+C, SIGTERM or SIGHUP while a release binary downloads or replaces itself stops the update
and removes the files it made, so the binary and any `.old` stay as they were; scterm then
ends as the signal would have ended it. Once the new binary is in place, scterm first keeps
the previous one as `.old` and says so. Ctrl+C at the question stops at once.

`scterm update --check` only says whether a newer release exists, and downloads nothing. When
the newer release has no linux/amd64 binary, it says that there is nothing to update to.

`scterm version` prints the version: `scterm 2.1.0`, for example, or `scterm dev`. A session
prints the same line first.

## Troubleshooting

Start with `scterm doctor`. It checks what scterm needs on this computer: on x86-64, a CPU with
AVX2; adb, each device it lists, and the Android version of the device scterm would use (the one
`ANDROID_SERIAL` names, or the only ready one); what the sound of
the terminal display needs; the browser and the display for `--window`; whether `--web` can
listen on its default address, `127.0.0.1:6969`; the names in `SCTERM_WEB_HOSTS`; and the
identity and pairings of peer mode. It prints one line for each check, which starts with
`ok`, `warn` or `FAIL`, and under it a line that says what to do, when there is something to do.
It exits with status 1 when a check fails, and 0 when there are only warnings. It runs nothing
on the other devices, which another program may be using, and it does not
connect to paired devices: `scterm peers --check` does that.

### The device is not found

scterm uses a device that `adb devices` lists as `device`. When there is none, it says what to
do:

| `adb devices` shows | What to do |
| --- | --- |
| no device | Turn on USB debugging and connect the device, or use `adb pair` and `adb connect` |
| `unauthorized` | Unlock the device and accept "Allow USB debugging?" |
| `no permissions` | Add a udev rule for the device, or join the `plugdev` group; then replug it |
| `offline` | Replug the device, or run `adb kill-server` |
| `authorizing`, `connecting` | Wait a moment and run scterm again |
| `detached` | Run `adb -s SERIAL attach` |
| `recovery`, `sideload`, ... | Start Android normally |
| two or more devices | Choose one with `-s SERIAL`, or set `ANDROID_SERIAL` |

When `adb` itself is missing, install the Android platform-tools, or use [peer mode](#peer-mode).

### The session does not start

When the server on the device stops before it connects, scterm prints the server's last lines,
which usually say why. When the device does not connect within 15 seconds, scterm says which
connection it was waiting for.

### No sound

- Devices with Android 10 or older send no sound. On Android 11, unlock the device before you
  start scterm. When the device sends no sound, scterm says so, with the reason the device
  gives, if any, for example
  `scterm: no sound from Pixel 8: Audio disabled: it is not supported before Android 11`.
  In the terminal display, the bottom line then says `audio off`. In the browser display, the
  line at the bottom of the page says `audio off` in place of the device volume.
- In the terminal display, sound needs a PulseAudio server. Without one, scterm prints
  `scterm: audio disabled: …` before it starts, and the bottom line says `audio off`. The
  browser display still plays the sound in the browser, and shows the device volume.
- scterm plays on the output your desktop uses, and with `pactl` installed it follows it when
  it changes. `SCT_AUDIO_DEVICE` sets a fixed sink (see `pactl list sinks short`).
- Only one scterm plays sound at a time. When a session that plays sound on this computer
  starts, scterm stops the sound of any other scterm here, running or left behind by a crash,
  and says how many sound streams it silenced. A session that plays no sound here leaves them
  alone: with `--no-audio`, with `--web` or `--window` but without `--audio-dup` (the browser
  plays the sound), or in peer mode when the device does not let this computer hear. A session
  without a sound stream (`--no-audio`, or a paired device that does not let this computer
  hear) does not connect to the sound server at all. scterm decides both when it starts,
  before the device says whether it sends sound: a device that turns its sound off (see
  above) still stops the other scterms' sound, and opens a sound stream here that stays
  silent.
- The default `--audio-source output` mutes the device while scterm runs. To hear the sound on
  the device too, use `--audio-source playback --audio-dup` (Android 13 and later).
- In the browser, the sound chip says what is wrong: *Tap for sound*, *Sound off · Alt+M* (the
  page's volume is muted), *Sound failed · Alt+M* (the sound did not start: click the chip or
  press Alt+M to try again), *Sound: compatibility mode* (plain http, a little more delay), or
  *No sound in this browser*. Apart from *Tap for sound*, the chip shows for six seconds when
  it changes, and after that only along with the action bar.

### The terminal picture is garbled

Resize the terminal, or wait for the next full redraw. `--repaint-interval 60` redraws more
often. Refresh video (Alt+K) asks the device for a new keyframe.

### The browser display does not start

With `--web` or `--window`, scterm listens on its address and port before it connects to the
device. When it cannot, it says why, exits with status 1, and for these causes says what to do:

- `port 6969 on 127.0.0.1 is in use`: another program, often another scterm, listens there.
  Stop it, or choose another port with `--web-port PORT` (0 picks a free one).
- `port 80 on 127.0.0.1 needs privileges that scterm does not have`: ports below 1024 usually
  do. Choose another port with `--web-port`.
- `192.0.2.1 is not an address of this computer`: `--web-addr` takes one of its addresses,
  `0.0.0.0` for all of them, or the default, `127.0.0.1`.

### The window does not open

- `could not open a window: no chromium/chrome or firefox found on PATH`: install one of them,
  or set `SCTERM_CHROME`. The printed address still works in any browser.
- `no Wayland or X socket for this user`: there is no graphical session to open a window in.
  Use `--web` instead.
- `this private profile is held by a running window`: close the other scterm window.
- The browser exits at once: scterm prints its error lines and the log file with its output.

### Peer mode

- `scterm pair` finds nothing: the device must show an invitation (*Start serving*, then
  *Invite a device…*) on the same network. Discovery uses multicast DNS (UDP port 5353), and a
  firewall on this computer that drops incoming UDP can hide the device. Allow mDNS from the
  local network, or type the invitation instead.
- `could not reach NAME at HOST:PORT (…)`: the device must be serving. scterm connects to it
  over TCP, on port 27300 unless the invitation named another. When the saved address fails,
  scterm looks for the device on the local network once and, after checking that it is the
  paired device, uses and saves its new address. Networks that keep devices apart, such as
  some guest Wi-Fi networks, block this. `scterm peers --check` says what it finds.
- `NAME at HOST:PORT refused the connection: …`: the message says why and what to do, for
  example to pair again with `scterm pair`.
- `another device has control of NAME`: add `--takeover`.
- `NAME allows this computer to view only`: change what it allows on the device.

### Help and exit status

`scterm -h` or `scterm help` prints the flags, commands, environment variables and exit
statuses, and every command answers `-h` with its own help. The exit status is:

| Status | Meaning |
| --- | --- |
| 0 | A normal end: Quit, closing the window, or a command that worked |
| 1 | scterm could not start (a command-line mistake too), a command failed, or the session failed |
| 2 | The device was lost during the session (a crash exits 2 as well) |

## Reference

### Flags

The flags, grouped as `scterm -h` groups them. Long flags also work with one dash. A flag that
takes a value accepts `--flag value` or `--flag=value`.

#### Device

| Flag | Default | What it does |
| --- | --- | --- |
| `-s`, `--serial SERIAL` | the only ready device | Device to use, as `adb devices` lists it |
| `-n`, `--no-control`, `--control=false` | — | View only: send no input |

Without `-s`, scterm uses `ANDROID_SERIAL` when it is set.

#### Video

| Flag | Default | What it does |
| --- | --- | --- |
| `--no-video`, `--video=false` | — | Mirror without the picture |
| `-m`, `--max-size PIXELS` | 1280 | Cap the picture's longer side; 0 keeps the full size |
| `--max-fps FPS` | no cap | Cap the frames per second |
| `-b`, `--video-bit-rate RATE` | 8M | Video bits per second; K and M work, as in 1.5M |

#### Audio

| Flag | Default | What it does |
| --- | --- | --- |
| `--no-audio`, `--audio=false` | — | Mirror without sound |
| `--audio-bit-rate RATE` | 128K | Audio bits per second |
| `--audio-codec CODEC` | opus | `opus`, `aac`, `flac` or `raw` (uncompressed PCM) |
| `--audio-source SOURCE` | output (`--audio-dup`: playback) | What the device records (see below) |
| `--audio-dup` | — | Keep playing the sound on the device (see below) |

`--audio-source output` records everything the device plays, and mutes the device while scterm
runs. `playback` (Android 13 and later) records what apps play; apps can opt out, and with the
bundled server, game sound is not recorded. `mic` records the microphone. scrcpy's other
sources also work: `mic-unprocessed`, `mic-camcorder`, `mic-voice-recognition`,
`mic-voice-communication`, `voice-call`, `voice-call-uplink`, `voice-call-downlink` and
`voice-performance`.

`--audio-dup` keeps the sound playing on the device. Only `--audio-source playback` can do that
(Android 13 and later), so, as in scrcpy, `--audio-dup` implies it, and scterm refuses
`--audio-dup` with another source or with `--no-audio`, except in peer mode, where the device
gets neither flag. On Android 11 and 12 the sound then plays on the device only, and scterm says
why. Without `--audio-dup`, `playback` silences the recorded apps on the device. With `--web` or
`--window`, `--audio-dup` also plays the sound on this computer, as well as in the browser.

#### Display

| Flag | Default | What it does |
| --- | --- | --- |
| `--no-tui` | — | No picture, only a line of statistics (for tests) |
| `--web` | — | Show the device in a web browser (see [Browser](#browser)) |
| `--window` | — | Like `--web`, in a window of its own (see [Window](#window)) |
| `--web-port PORT` | 6969 | Port to serve on; 0 picks a free one |
| `--web-addr ADDRESS` | 127.0.0.1 | Address to listen on (see the warning in [Browser](#browser)) |
| `--web-quality N` | 5 | JPEG quality in the browser: 2 is best and largest, 31 smallest |
| `--window-size WxH` | the picture's size | Size of the `--window` window in pixels, as 720x1600 |

#### Peer

| Flag | Default | What it does |
| --- | --- | --- |
| `--peer NAME` | — | Connect to a paired device that runs the scterm app (see below) |
| `--takeover` | — | With `--peer`, take control from whoever has it |

`--peer=` connects to the only paired device, or asks which one. See [Peer mode](#peer-mode).

#### Debugging

| Flag | Default | What it does |
| --- | --- | --- |
| `--keys` | — | Print the Android keys scterm can send, and exit |
| `--dump-frames DIR` | — | With `--no-tui`, save the first 3 frames in DIR (PPM) |
| `--dump-audio FILE` | — | Write the device's audio packets to FILE, each after its size |
| `--repaint-interval FRAMES` | 300 | Redraw the whole terminal every FRAMES frames |

With `--audio-codec raw`, the packets `--dump-audio` writes are the PCM itself. Two environment
variables also help: `SCT_DEBUG_WEB=1` logs each browser's paint and decode statistics, and
`SCT_DEBUG_CONTROL=1` logs input as it is read and sent to the device.

### Commands

| Command | What it does |
| --- | --- |
| `scterm pair [INVITATION]` | Pair with a device that runs the scterm app |
| `scterm peers [--check]` | List the paired devices; `--check` tests each one |
| `scterm forget NAME` | Forget a paired device |
| `scterm doctor` | Check what scterm needs on this computer, and say what to do about problems |
| `scterm update [--check] [-y]` | Update to the latest release, or pull and rebuild |
| `scterm version` | Print the version |
| `scterm licenses` | Print the licenses of scterm and of the scrcpy server it bundles |
| `scterm help` | Print the help, as `scterm -h` does |
| `scterm COMMAND -h` | Print a command's own help |

### Environment variables

| Variable | What it does |
| --- | --- |
| `ANDROID_SERIAL` | The device to use when `-s` is not given |
| `SCTERM_HOME` | Where scterm keeps its identity and pairings |
| `SCTERM_CHROME` | The Chromium-family browser for `--window` (`CHROME_BIN`, `CHROMIUM_BIN` too) |
| `SCTERM_FIREFOX_KIOSK=1` | When `--window` uses Firefox, open it full screen without toolbars |
| `SCTERM_WEB_HOSTS` | More names to open the page by, comma-separated (see [Browser](#browser)) |
| `SCT_AUDIO_DEVICE` | The PulseAudio sink to play on, instead of following the desktop's choice |
| `PULSE_SERVER` | The sound server for the terminal display's sound, tried first (see below) |
| `PULSE_SINK` | The sink for the terminal display's sound when scterm names none (see below) |
| `SCT_DEBUG_WEB=1` | Log each browser's paint and decode statistics |
| `SCT_DEBUG_CONTROL=1` | Log input as it is read and sent to the device |

`PULSE_SERVER` and `PULSE_SINK` are libpulse's own variables. For the terminal display's sound,
scterm tries `PULSE_SERVER` first, then the socket in `XDG_RUNTIME_DIR`, which its own `pactl`
calls use; the clean-up of other scterms' sound at start takes the first that answers.
`PULSE_SINK` applies when scterm names no sink: when `SCT_AUDIO_DEVICE` is not set and `pactl`
is missing or lists no sound card and no sink in use. With `pactl`, scterm then follows the
desktop's output.

scterm also reads variables that other programs set: `ZELLIJ` (set by Zellij; scterm then
starts without Grab), `XDG_CONFIG_HOME` and `XDG_CACHE_HOME`, `XDG_RUNTIME_DIR` (where scterm
and `pactl` find PulseAudio, and where `--window` looks for a Wayland socket), and for
`--window` `WAYLAND_DISPLAY` and `DISPLAY`.

### Exit status

See [Help and exit status](#help-and-exit-status).

### Files

| Path | What it is |
| --- | --- |
| `~/.config/scterm/identity.pem` | This computer's identity for peer mode: key and certificate |
| `~/.config/scterm/peers.json` | The paired devices |
| `~/.cache/scterm/chromium/` | The private browser profile of `--window` |
| `scterm-YYYYMMDD-HHMMSS.png` | Terminal screenshots, in the current directory |
| `/data/local/tmp/scrcpy-server.jar` | On the device: the server, copied there at each start |

`SCTERM_HOME` replaces `~/.config/scterm`. `XDG_CONFIG_HOME` and `XDG_CACHE_HOME` replace
`~/.config` and `~/.cache`.

## How it works

```text
              ┌────────────────────────────────────────────┐
              │ scterm, on this computer                   │
              │ decodes video and sound (FFmpeg) and sends │
              │ input; draws in the terminal, or sends     │
              │ JPEG frames and sound to a browser         │
              └───────┬────────────────────────────┬───────┘
                      │ adb tunnel:                │ peer mode, no adb:
                      │ video, audio, control      │ mutual TLS, TCP port 27300
                      │                            │
     ┌────────────────▼────────────┐   ┌───────────▼──────────────────┐
     │ scrcpy server, bundled with │   │ scterm Android app, serving  │
     │ scterm and started by it:   │   │ the same scrcpy stream       │
     │ screen capture (H.264),     │   │ format, inside TLS           │
     │ sound, input                │   │                              │
     └─────────────────────────────┘   └──────────────────────────────┘
```

With adb, scterm copies the scrcpy server to the device, starts it, and receives the video,
audio and control connections through an adb tunnel. It decodes the H.264 video and the sound
with the FFmpeg libraries. The terminal display scales each frame to the terminal and draws it
with half-block characters. The browser display sends each frame to the page as a JPEG, and
the sound as raw PCM. Input goes back over the control connection. In peer mode, the same
streams come from the scterm Android app over TLS. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
has the details.

## Development

- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md): building, tests and developer tools
- [docs/RELEASING.md](docs/RELEASING.md): CI, releases and the Android signing key
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): how scterm works inside
- [docs/PEER_PROTOCOL.md](docs/PEER_PROTOCOL.md): the peer protocol
- [android/README.md](android/README.md): the Android app

## Credits

scterm uses the protocol of [scrcpy](https://github.com/Genymobile/scrcpy), by Genymobile and
its contributors, and bundles the scrcpy v4.1 server, which is licensed under the Apache
License 2.0 ([third_party/LICENSE.scrcpy](third_party/LICENSE.scrcpy)). The server's source is
in [third_party/scrcpy-server-src](third_party/scrcpy-server-src). scterm decodes video and
sound with the [FFmpeg](https://ffmpeg.org/) libraries and plays sound through PulseAudio.

## License

scterm, the Android app included, is released under the MIT License: see [LICENSE](LICENSE).
The scrcpy server, which scterm and the app both bundle, keeps its own license, the Apache
License 2.0 (see above); other third-party files, such as the Gradle wrapper, keep theirs too.

Both license texts come with every copy of scterm: `scterm licenses` prints them, each release
attaches them as `LICENSE` and `LICENSE.scrcpy` next to the binary and the APK, and the APK
holds them as `assets/LICENSE` and `assets/LICENSE.scrcpy`. scterm's changes to the server's
source are listed in [third_party/README.md](third_party/README.md).
