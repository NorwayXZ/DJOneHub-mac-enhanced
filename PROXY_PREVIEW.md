# Cellular Proxy Preview

## Current candidate: 1.3.0-proxy-preview.12

- Loopback SOCKS5 remains `127.0.0.1:10909`, with cellular-bound connections and no broadband fallback.
- DNS uses module DHCP DNS over TCP, with cellular-bound `223.5.5.5:53` fallback. DNS providers receive queried domains.
- Exit detection tries IPIP and ipify. Approximate geolocation, ISP and persistent IP history are displayed; provider locations can disagree.
- Native sidebar hit targets, menu-bar proxy status, notification-center integration and GitHub update UI are included.
- Optional multi-module supervision is experimental; multiple physical modules have not been validated.
- Network recovery identifies the real USB network interface; AT probing excludes DIAG interfaces. Voice can reuse an existing local ADB server, pinned to USB session/location and transport ID.
- Hardware validation: one QDC507 passed SOCKS5 remote-DNS HTTPS access while broadband remained the default route. Module voice drivers, calibration and UAC route start/stop passed; live two-way call audio remains unverified.
- Go race tests, Swift self-test, universal build and DMG integrity checks passed. This is an ad-hoc-signed, not notarized preview, not a stable release.

The sections below preserve earlier preview development notes. Their original test limitations describe those earlier versions, not the current candidate.

## Preview 1 historical notes

Version: 1.3.0-proxy-preview.1. This is an unsigned/not notarized hardware-test candidate, not a validated stable release.

## Included

- Persistent menu-bar entry; closing the window hides the Dock entry without stopping services.
- Sidebar navigation: cellular proxy, dialer, recent calls, messages, contacts, module/network, settings.
- Loopback-only SOCKS5 TCP CONNECT on 127.0.0.1:10909. No UDP, IPv6, or LAN sharing.
- USB vendor/product ancestry verification through IOKit. Multiple modules are rejected.
- Each connection uses IP_BOUND_IF; DNS uses a custom Go resolver over interface-bound TCP to 1.1.1.1:53. No system proxy or system DNS fallback.
- Public IPv4 destinations only; private, loopback and link-local targets are rejected.
- HTTPS public-IP detection through the same dial path, using api.ipify.org.
- Confirmed asynchronous radio reconnect with 60-second cooldown, operation exclusion and changed/unchanged/unverified/failed outcomes.
- Unexpected interface/default-route changes stop the listener and close clients. Restart manually after reconnecting hardware.

## Important limitations

This preview does NOT modify service order, scoped routes, or default gateways. Broadband must remain the system default, cellular data must be enabled, and the module must have a working scoped IPv4 route. If macOS selects the module as default, the proxy refuses to start/stops. A future explicit network-policy mode needs hardware verification and rollback before changing system configuration.

No automatic IP rotation, retry scheduler, proxy auto-start persistence, or LAN sharing is included. Reconnect can interrupt SMS and calls. Carrier CGNAT may assign the same shared public IPv4. The detector samples an external service's observed IP; carrier NAT may vary by destination.

Clients must send domain names to SOCKS (e.g. socks5h), rather than resolving them locally. Browser WebRTC, QUIC and other traffic bypassing SOCKS are outside this proxy's protection. This is not a whole-device VPN or a guarantee of browser anonymity.

## Hardware acceptance checklist (not yet performed)

1. Keep broadband connected, connect one module with a data-enabled SIM, enable cellular data.
2. Confirm system default route stays on broadband. Start proxy and run an explicit IP detection.
3. Compare a direct request with `curl --socks5-hostname 127.0.0.1:10909 https://api.ipify.org` (direct requests may be affected by existing VPNs).
4. Verify DNS and target sockets on the USB interface with packet capture. Test with VPN/TUN disabled, then enabled.
5. Unplug the module during transfers: proxy must fail, never use broadband. Replug, restart manually and verify new interface index detection.
6. Confirm reconnect, observe radio recovery and compare old/new IP. Repeat after cooldown; an unchanged IP is valid.
7. Check eSIM writes, module setup and active calls reject conflicting reconnects.
8. Close the main window; verify the permanent menu entry reopens it, and complete exit stops proxy and LaunchAgents.
9. Test sleep/wake, Intel hardware, broadband loss, multiple USB Ethernet adapters, IPv6-only destinations, and old force-off policy.

## Build / verify

```
go test -race ./...
cd macos/DJOneHubNotifier && swift build -c release
```

Go needs libusb headers and pkg-config. Packaging scripts download the pinned official libusb source with SHA-256 validation. No external module-side voice runtime is included.

Upstream PolyForm Noncommercial license and third-party notices remain applicable.

## Preview 3: click targets and exit metadata

- The sidebar hit shape is inside each full-width button label, covering its padding and background.
- Starting the proxy automatically verifies the cellular IPv4 exit. A successful probe records its timestamp and queries ipwho.is for approximate country/region/city and ISP, using the same interface-bound transport. Location failure does not invalidate a successful IP probe. No coordinates/GPS are collected.
- ipwho.is is a third-party service and sees the exit IP; availability, accuracy and service terms apply. Its response schema was checked using public resolver address 8.8.8.8, not the user's cellular address.
- Reconnect currently probes old IP, stops proxy sessions, sends AT+CFUN=0, waits 3 seconds, sends AT+CFUN=1 and probes recovery within a 100-second context. The recovery result distinguishes changed, unchanged, and recovered-without-old-baseline. These are measured results, not a promise of rotation.
- Quectel EG25-G Hardware Design v1.5 sections 3.4/3.5 distinguish CFUN=0 (minimum functionality, RF and SIM disabled), CFUN=4 (airplane mode, RF disabled) and CFUN=1 (full functionality). Therefore this implementation must not be described as exactly toggling airplane mode. DJI firmware and the actual module/carrier still require hardware verification. Reference: https://quectel.com/content/uploads/2024/04/Quectel_EG25-G_Hardware_Design_V1.5.pdf
- A longer disconnect may be evaluated experimentally; no reliable fixed delay guaranteeing a fresh carrier/CGNAT public IP has been established. LAN DHCP renewal and module reboot are not guarantees either. Do not automatically repeat disruptive cycles until an address changes.

## Preview 4: IP history and menu bar state

- Successful exit observations are saved locally to the user's Application Support/DJOneHub/proxy-ip-history.json. Consecutive observations of the same IP merge; returning to a previous IP creates another visit. Up to 500 visits are kept, including the verified IP immediately before reconnecting. Times represent observations, not continuous connectivity or traffic activity. Addresses seen before this version cannot be recovered automatically.
- History survives proxy stop and app/backend restart. Writes use a private temporary file and atomic rename; unreadable history is preserved with an error shown in the UI.
- The dashboard shows recent visits newest first, approximate location/ISP, observation times and repeat markers. Repeat detection is scoped to retained records.
- The app's menu bar antenna is green while the local proxy listener is running, orange during reconnect, and the normal template color while stopped. Backend unavailability clears the active indication. This indicates proxy state; exit reachability remains a separate IP check.
- Safe visual test: notifier --review-safe --review-proxy-active --show-window uses documentation-only example addresses without starting a listener or touching the module.

## Preview 5: native macOS Notification Center

- New SMS and missed calls create native local UserNotifications entries. Clicking them opens Messages or Recents. Live incoming calls retain the existing answer/decline panel and ringtone.
- Settings now show authorization state and include an enable switch, a message-content privacy switch, a shortcut to macOS notification settings, and a test notification with delivered-record verification.
- SMS text and verification codes are hidden by default in both notification-center entries and the app's SMS popup. Enabling previews reveals message text (limited to 240 characters in system notifications). Turning previews off removes previously delivered SMS notifications containing previews. Notification identifiers hash SMS identity rather than exposing content in identifiers; userInfo contains only a destination tab.
- Every newly observed SMS/missed call in a polling batch is submitted, rather than just the first. Initial polling establishes a baseline without flooding the user with historical events. A live incoming-call panel is not replaced by SMS or missed-call popups.
- Existing app reminders own sounds; native entries do not add a second simultaneous chime. System Focus/notification preferences still control visibility. The app must remain running to detect new events.
- Privacy and notification-identifier checks run in the notifier self-test. Real module-generated SMS/calls require a connected module for end-to-end verification.
- Installed preview.5 on this Mac with the previous runtime/app backed up. Enabled DJOneHub's existing disabled system-notification permission through macOS Settings. The installed app's test notification was verified against UserNotifications delivered records; the UI reported confirmation. No live modem was connected for a real SMS/call test.

## Preview 6: sidebar focus appearance

- On macOS 14 and later, sidebar buttons suppress the square system focus effect that could stay on the first button while a different page was selected. The green rounded selection background and full-width click targets remain.
- Verified in a native safe-preview window on this Mac: Settings shows no residual blue box around Cellular Proxy, and clicking the right-hand blank area still selects the requested page in one click. Older macOS versions retain the native focus treatment.

## Preview 7: opt-in multi-module foundation

See MULTI_MODULE_PREVIEW_ZH.md for the implemented supervisor/worker architecture, fixed bindings, confirmation rules, verification evidence and remaining physical-hardware acceptance tests. Single-module mode remains the default. The settings UI can switch the installed service into multi-module preview mode and back. Proxy and history data are selected per module; auxiliary tools remain limited to a single connected module. Traffic sampling now requires hardware-associated USB interfaces and clears unavailable samples.
