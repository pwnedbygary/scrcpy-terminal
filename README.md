# Screenshots for pull request #6

This branch holds only the images in the description of
[pull request #6](https://github.com/pwnedbygary/scrcpy-terminal/pull/6). It
shares no history with scterm's code. The description links to these files,
so deleting the branch breaks its images.

## How they were made

- Builds: "before" is `main` at 62d6cfa. "After" is the `overhaul` branch at
  c11cf92 with parts 37 to 41 on top, as they are landing.
- Device: a stand-in. A fake `adb` answers as one phone, DEMO0001, and a fake
  scrcpy 4.1 server streams a made-up home screen (576×1280, H.264, 30 frames
  a second) to scterm through its reverse tunnel. No real phone, and nothing
  from one, appears. For `scterm doctor`, a stand-in `chromium` that only
  prints a version takes a browser's place.
- Sandbox: every run in bubblewrap, with only a private loopback network, no
  sound server, no display and no D-Bus, and the host name `demo`. For the
  command line, the home directory is `/home/demo`.
- Browser display: headless Chrome 155, driven over the DevTools protocol.
  Desktop: 1280×800 with a mouse. Phone: 390×844 at twice the density, with
  touch.
- Terminal display: scterm in a pseudo-terminal at 100×56 and 80×28. A small
  terminal emulator keeps the screen, which is drawn as a terminal window,
  half-block cells as rectangles. The picture is what scterm sends: 3 bits
  for each colour channel. The branch also changed the colour levels:
  `main` paints the brightest level as 224 of 255, the branch as 255, so the
  "after" pictures are brighter.
- Command line: each command shown after the `bash` prompt is the command
  that runs, in a pseudo-terminal 150 columns wide (160 for `scterm
  doctor`, 110 for the help). The note after a command says how the stand-in
  was set up: for the first two, the fake `adb` lists the phone as not yet
  allowing USB debugging; for the third, another program holds port 6969.
- The terminal windows, prompts, notes and labels are drawn by the capture
  scripts; what is inside them is scterm's own output.

At 80×24, the terminal display paints a few columns of stray colour to the
right of the picture, on both builds: libswscale writes past the picture's
width into the black margin. The 80-column shots are at 80×28, where the
picture's width avoids it. That is an existing bug, to be fixed on its own.
