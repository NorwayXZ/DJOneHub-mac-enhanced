DJOneHub for Windows amd64
==========================

Install
-------
1. Extract the complete ZIP archive.
2. Double-click "Install DJOneHub.cmd".
3. Connect the DJI first-generation 4G module and install its Windows serial
   driver if Windows does not expose a COM port.

What works on Windows
---------------------
- Local control panel at http://127.0.0.1:7575 (opens in the browser).
- Status, AT control, SMS (with forwarding), GPS and call-state control over
  the module's COM port.
- Cellular proxy: on first connect the app switches the module to RNDIS
  (usbnet 3), which Windows drives without extra software, and reboots it
  once. The proxy then sends traffic out through the module only; broadband
  or Wi-Fi must stay the PC's default network.
- Traffic counters for the cellular adapter.

Not on Windows yet
------------------
- Several modules at once (one module only).
- Call audio, Mac notifications, Contacts and maps.
- Direct USB AT without a COM driver: Windows needs the module's USB serial
  driver (Quectel USB driver) to expose a COM port.

Moving the module back to a Mac: the Mac app switches it back to ECM
(usbnet 1) automatically.

This build is cross-compiled on macOS and has not yet been tried on a real
Windows PC with the module.

DJOneHub is an unofficial third-party project and is not affiliated with DJI,
Quectel, carriers or eSIM vendors.
