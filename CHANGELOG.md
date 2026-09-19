# Changelog

## [0.2.1](https://github.com/Snipa22/go-tari-tools/compare/go-tari-tools-v0.2.0...go-tari-tools-v0.2.1) (2026-09-19)


### Bug Fixes

* chain docker-build into release-please run to fix untriggered releases ([dc78b16](https://github.com/Snipa22/go-tari-tools/commit/dc78b16385e611b1bb92833ae816929a2b7f29ef))
* pass release tag_name through to chained docker build for version tagging ([a4ffd27](https://github.com/Snipa22/go-tari-tools/commit/a4ffd27de987b8fc65cbf8fdc3d5dda16a28f003))

## [0.2.0](https://github.com/Snipa22/go-tari-tools/compare/go-tari-tools-v0.1.0...go-tari-tools-v0.2.0) (2026-09-19)


### Features

* add 4-tier payout batching policy with single_tx and address pre-validation ([b48718f](https://github.com/Snipa22/go-tari-tools/commit/b48718f1fdd18e71ae5a6ed2fa36407202b56a15))
* add Dockerfile and CI docker build for payoutDaemon ([1dd6162](https://github.com/Snipa22/go-tari-tools/commit/1dd6162dac2950fc1e355916388a7887653048b2))
* extract tool binaries from go-tari-grpc-lib and go-tari-faucet ([2e2db80](https://github.com/Snipa22/go-tari-tools/commit/2e2db80e1065d63678798925d3e5d0ac52864c10))
* scaffold go-tari-tools repo structure ([4b7fe3f](https://github.com/Snipa22/go-tari-tools/commit/4b7fe3f0c38ef8fceee8c10652fda89757d651b4))


### Bug Fixes

* correlate TransferResults to payments by index, not by echoed address ([02bc1c8](https://github.com/Snipa22/go-tari-tools/commit/02bc1c80190b00a2aca542ef77cc2f7bf43c794f))
* match docker-build.yml tag trigger to release-please's actual tag format ([e13bdec](https://github.com/Snipa22/go-tari-tools/commit/e13bdec04ae2465c693599e126c316f3588a72c2))
* remove local replace directive for go-tari-grpc-lib, resolve from published v3.2.0 tag ([164ce59](https://github.com/Snipa22/go-tari-tools/commit/164ce5918c1776c4a1c257154c2f3fd485626d7a))
* resolve go-tari-lib from published v1.0.0 tag, remove local replace directive ([ec20892](https://github.com/Snipa22/go-tari-tools/commit/ec20892f15af853d50830923e451f8d6c60e735d))
* target go-tari-lib/v2 module path ([02c6526](https://github.com/Snipa22/go-tari-tools/commit/02c652695e06979cb0499e2970e88db8c4451104))
