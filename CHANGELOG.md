# Changelog

## 1.0.0 (2026-10-07)


### Features

* **ble:** continuous scan — never quit, listen for late controllers ([85a8f33](https://github.com/thomaschampagne/zwiftboard/commit/85a8f3320d843ca0fdb4c4ef0f3e924579feb8f3))
* **config:** named key-mapping profiles with -p/--profile ([bc6d177](https://github.com/thomaschampagne/zwiftboard/commit/bc6d1771212cf36c59e72bceb9c252430e4894c1))
* **config:** profiles for 5 cycling apps, shortcuts verified against vendor docs ([93ed2a9](https://github.com/thomaschampagne/zwiftboard/commit/93ed2a960fb9b4f76542ff9bfbf303fccdb138db))
* **log:** leveled logging via log/slog, level from config.yaml ([fe1f986](https://github.com/thomaschampagne/zwiftboard/commit/fe1f9869624c3fd19393dadd3422d5f918310058))
* map zwift click v2 key events to keyboard mapped keys ([a84bf2b](https://github.com/thomaschampagne/zwiftboard/commit/a84bf2bf5472b7b6a38785afc45511359992e0b6))


### Bug Fixes

* **ble:** fall back to label when scan result has no name ([6e5230e](https://github.com/thomaschampagne/zwiftboard/commit/6e5230e8fb97ac259c139cc0a83c09111d8bec67))
* **ble:** split NewAddress by build target so macOS compiles ([1c1b06f](https://github.com/thomaschampagne/zwiftboard/commit/1c1b06f1850c89116fe0526f5470b59e14b3cfdf))
