# App updates

Update source is fixed to `NorwayXZ/DJOneHub-mac-enhanced`. Pushing source code alone is not an App update. Publish a version tag with these assets:

- `DJOneHub-macOS-universal-update.zip` (required by the App)
- its `.sha256` (for manual verification)
- universal DMG and its `.sha256` (for fresh installs)

The workflow `.github/workflows/release.yml` builds and publishes these when a `v*` tag is pushed, or by workflow_dispatch with an existing tag. Before tagging, set the same version in `main.go` and `Info.plist`. GitHub Actions must be enabled on the fork. The workflow needs contents:write; no personal access token is embedded in the App. The App uses public GitHub API requests and may hit rate limits. It includes prereleases in its manual check.

Update ZIP structure:

```
DJOneHubUpdate/
  DJOneHubNotifier.app/
  runtime/
    bin/djonehub-macos
    lib/libusb-1.0.0.dylib
    ...
```

The App requires the GitHub asset API's `sha256:` digest. It verifies the archive, rejects traversal and symlinks, checks unpacked size, version, bundle ID, both architectures and code signatures. Ad-hoc signatures and a hash from the same account are integrity checks, NOT independent publisher authentication. For production, add Developer ID signing/notarization and a separately pinned signing key (or a maintained updater such as Sparkle with EdDSA). A compromised GitHub publisher account remains a trust risk.

Install only runs from the canonical user-directory installation. The backend prepare endpoint rejects active operations, stops proxy connections and grants a bounded update lease. An independent launchctl helper stops services, retains old runtime/App files, replaces programs (not user data), verifies the backend version, and starts the UI. Failure attempts rollback. Logs are in `~/Library/Logs/DJOneHub/update.log`. Backups remain under the DJOneHub application-support directory. The App must be installed once with this updater before it can update itself.

No release has been uploaded as part of this local implementation. Real GitHub download/install/rollback must be exercised against a published candidate before calling the updater production-ready.
