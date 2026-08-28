# AndroidCyaml integration facade

This package is consumed only by `qwqgong-ui/AndroidCyaml`. It exposes a small
facade over mihomo's neutral process-resolution and XHTTP transport extension
points. Android `VpnService`, JNI, WebView, TUN lifecycle, and callback
implementations remain owned by the AndroidCyaml repository.

When synchronizing `MetaCubeX/mihomo:Alpha` into `dev`:

- preserve this directory;
- preserve the neutral extension points it calls;
- keep ordinary mihomo behavior unchanged when no callbacks are registered;
- verify both mihomo tests and the AndroidCyaml native build after API changes.

AndroidCyaml consumes a pinned `dev` commit and must not apply another source
patch layer on top of it.
