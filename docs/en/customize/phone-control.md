# Control a phone

The bundled **Phone Harness** plugin lets Wuu read a phone screen, open apps, tap, swipe, scroll, and type. Enable it in **Skills & Plugins** after installing the CLI and connecting a device. It is off by default.

Wuu runs the separately installed [Phone Harness](https://github.com/ShawnPana/phone-harness) CLI through the public plugin API. The plugin supports macOS and Linux. Personal iPhones require macOS iPhone Mirroring; Android devices use ADB; existing Phone Harness Cloud sessions can use either phone platform.

## Install and connect

Install the supported CLI version with [uv](https://docs.astral.sh/uv/):

```bash
uv tool install --python 3.12 phone-harness==0.3.2
phone-harness --version
```

Wuu looks on PATH and in `~/.local/bin`. For another installation, set `WUU_PHONE_HARNESS` to the absolute executable path before starting Wuu. The plugin requires CLI version 0.3.2 and rejects other versions before running task code. It does not install or update Python packages automatically.

| Device | Setup |
| --- | --- |
| Personal iPhone | Complete Apple's iPhone Mirroring pairing on the Mac. Keep the iPhone locked. Grant Accessibility and Screen Recording to the controlling process when macOS requests them. Run `phone-harness --doctor ios` to diagnose missing access. |
| Android | Install Android platform-tools, enable USB debugging, connect or pair the phone, and approve the computer on the device. Copy its exact serial from `adb devices -l`. Run `phone-harness --doctor android` for diagnostics. |
| Cloud phone | Sign in and start the intended session using Phone Harness's cloud CLI. Copy the session ID from `phone-harness cloud ls`. Rentals and billing are managed by Phone Harness, separately from Wuu. |

See the upstream [setup guide](https://github.com/ShawnPana/phone-harness/blob/main/install.md) for platform requirements and connection troubleshooting. Authentication, device unlocking, biometric prompts, and pairing require the user.

## Use it in a conversation

Ask for a specific phone and task, for example: “On my mirrored iPhone, open Settings and read the iOS version,” or “On Android device SERIAL, open the app and check its settings.” Wuu loads the bundled phone skill and uses `phone_exec`.

Each call explicitly selects `target`: `ios`, `android` with `serial`, or `cloud` with `session`. A cloud session is never rented by this tool. A cloud call rejects a `PHONE_HARNESS_PLATFORM` override loaded from the Phone Harness workspace, since it could select a local device instead. Remove that workspace override before using a cloud session.

The tool accepts Python with the upstream phone helpers pre-imported. `print(screen())` returns visible text; `screenshot()` attaches the latest newly written image from the call. Helpers include `open_app()`, `tap_text()`, `tap_image_point()`, `swipe()`, `scroll()`, and `type_text()`. Read the installed full helper guide with `phone-harness skill`.

Python variables reset between calls. Phone state and the upstream agent workspace persist. Calls from Wuu workspaces sharing the same Wuu home run one at a time; a separately launched controller or another Wuu home is outside that lock. iPhone Mirroring can use the Mac's foreground input and clipboard.

## Execution and limits

Calls allow 60 seconds by default, or 1–300 seconds with `timeout_s`. Wuu retains the last 64 KiB of each output stream and reports truncation. The latest fresh PNG, JPEG, or WebP printed on its own line is attached if it is at most 8 MiB. A vision-capable model can inspect that image; text-only models depend on OCR or accessibility results.

Stopping the task or disabling the plugin cancels its runner and active subprocesses. Already delivered input and remote requests can still have effects. Wuu does not automatically replay an interrupted script; inspect the screen before continuing. Disabling the plugin does not end cloud rentals. Use `phone-harness cloud stop` for a session you intend to stop.

Phone Harness and its Python scripts run with your local user authority, as other trusted plugins do. Wuu disables Phone Harness telemetry for these calls. Device credentials and cloud login remain managed by Phone Harness. Screenshots and screen text become conversation data. Screen access does not authorize sending messages, publishing, buying, or deleting beyond your requested task.

Device lock screens, Face ID, camera flows, DRM video, and controls with insufficient screen evidence can require manual interaction. This plugin does not make those interfaces universally automatable.
