# python-text: a component backend in Python

A component that draws with a program instead of layers (`spec/components.md`, "Backends"). `draw.py` reads one
JSON request line (`retake-raster/1`) on stdin and writes each frame to stdout: a JSON header line, `{"box": [x, y, w,
h]}`, then `w × h × 4` bytes of straight-alpha RGBA (or `{"same": true}`, `{"empty": true}`). It uses the Python
standard library only: the letters come from a built-in 5x7 pixel font, or from Pillow's default font when the use
sets `"pillow": true` and Pillow is installed.

It shows the pattern for reusing existing drawing code (a Pillow script, say): read the props from the request, draw
the picture once, and emit only the box it covers, moved and faded per frame. `{"same": true}` keeps settled frames
free.

Use it like any component: copy the folder to `components/python-text@1.0.0/` next to a timeline, then

```json
{ "id": "pop/faster", "use": "python-text@1.0.0", "from": "word:b/faster", "to": "word:b/faster+1.40",
  "props": { "text": "FASTER" } }
```

and pin it (`retake-edit component lock timeline.json`): the lockfile's hash covers `draw.py`, so editing the program
re-renders exactly its uses. Try the program alone:

```sh
req='{"protocol": "retake-raster/1", "props": {"text": "HI"}, "size": [320, 180], "scale": 1, "comp": [320, 180], "canvas": [1920, 1080], "fps": 10, "frames": [0, 3]}'
echo "$req" | python3 draw.py | head -c 200   # the request is one line
```

License: CC0-1.0.
