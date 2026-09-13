# Signing and notarizing

`make app` produces an **ad-hoc signed** bundle. That is fine on the machine that
built it, but on any other Mac macOS will quarantine it and refuse to open it
until someone runs:

```sh
xattr -dr com.apple.quarantine /Applications/DerbyAndAles.app
```

That is exactly the friction MDnATracker has today, and it undercuts the goal of
this project: someone other than the author should be able to install and run
the app without a Terminal.

Doing it properly needs an Apple Developer account ($99/year):

```sh
# 1. Sign with a Developer ID certificate, with a hardened runtime.
codesign --force --deep --timestamp --options runtime \
  --sign "Developer ID Application: YOUR NAME (TEAMID)" \
  dist/DerbyAndAles.app

# 2. Submit for notarization.
ditto -c -k --keepParent dist/DerbyAndAles.app dist/DerbyAndAles.zip
xcrun notarytool submit dist/DerbyAndAles.zip \
  --apple-id you@example.com --team-id TEAMID --password APP_SPECIFIC_PASSWORD \
  --wait

# 3. Staple the ticket so it works offline, which matters at a brewery.
xcrun stapler staple dist/DerbyAndAles.app
```

Then verify the result behaves like a downloaded app would:

```sh
spctl --assess --verbose=4 dist/DerbyAndAles.app
```

Not scheduled. Worth doing before anyone else installs this.

## One thing to watch

The app needs **local network access** to serve tablets and TVs. macOS prompts
for this on first launch, driven by `NSLocalNetworkUsageDescription` in
`Info.plist`. If the prompt is declined, the app still starts and `localhost`
works, but nothing else on the network can reach it — which looks like a
mysterious "the iPad can't connect" on race night. If that happens, re-enable it
under System Settings → Privacy & Security → Local Network.
