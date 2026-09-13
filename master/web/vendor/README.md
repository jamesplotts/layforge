# vendor

Empty for now. This directory previously vendored three.js/cannon-es for
the WebGL dice tray; that tray was replaced by SVG/CSS dice rendered
directly in the chat log (see `../app.js`'s "client.roll* interactive
dice" section), which needs no third-party library. Kept as a directory
in case a future client feature needs a vendored ES module, matching
this client's "no build step" contract (see `../README.md`).
