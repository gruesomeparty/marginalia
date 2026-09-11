# Changelog

## 1.0.0 (2026-09-11)


### Features

* advertise request-feature skill on unknown flags and subcommands ([966e763](https://github.com/gruesomeparty/marginalia/commit/966e763a198de2fbc6c759381ad817375bb448ac))
* anchor markdown list items individually ([82ae748](https://github.com/gruesomeparty/marginalia/commit/82ae7486742ad648fb79484566f9525c1b7f3f0e))
* anchor mermaid diagrams statement by statement ([10f6694](https://github.com/gruesomeparty/marginalia/commit/10f66947273b40e6521d7f7f019ba535b9e023ad)), closes [#25](https://github.com/gruesomeparty/marginalia/issues/25)
* append-only JSONL feedback store with resolution view ([a731d9c](https://github.com/gruesomeparty/marginalia/commit/a731d9c391a2ddbbb0181b219aab5416547c8f89))
* block anchoring — section paths, quotes, content hashes ([37b4624](https://github.com/gruesomeparty/marginalia/commit/37b4624e3d652a20399b2fb9038ea8ce8f3c19cd))
* configurable reviews — framing, custom actions, read-only blocks ([#30](https://github.com/gruesomeparty/marginalia/issues/30)) ([104754e](https://github.com/gruesomeparty/marginalia/commit/104754e33b7c7a0d2c34541ceccfcb6fef5d7b95)), closes [#3](https://github.com/gruesomeparty/marginalia/issues/3)
* draw mermaid diagrams as anchored SVG, source one toggle away ([#37](https://github.com/gruesomeparty/marginalia/issues/37)) ([e5b6d17](https://github.com/gruesomeparty/marginalia/commit/e5b6d173639863e872dff0e81a596b892fad1597)), closes [#36](https://github.com/gruesomeparty/marginalia/issues/36)
* M4 automated implementation pipeline ([f7823b1](https://github.com/gruesomeparty/marginalia/commit/f7823b1b40887a6b3b26f6d9f92ebab1e80cb74b))
* M5 static share mode — export a review page, import the reply ([#33](https://github.com/gruesomeparty/marginalia/issues/33)) ([7123f3b](https://github.com/gruesomeparty/marginalia/commit/7123f3ba9ade18c7e99196a96d6e17b2e7b4ca2f)), closes [#5](https://github.com/gruesomeparty/marginalia/issues/5)
* multi-document review sessions with a navigable file tree ([4e6da18](https://github.com/gruesomeparty/marginalia/commit/4e6da180fac61de9935eb6adf5efcb51c5122761))
* package repo as an installable Claude plugin + marketplace ([e53dd4a](https://github.com/gruesomeparty/marginalia/commit/e53dd4a8be33479e22f34742faf391907e58edcd))
* report the suggest_edit replacements that are safe to apply ([1e1e867](https://github.com/gruesomeparty/marginalia/commit/1e1e867c8082c0d1cc12f56dcd71775d280a19d6))
* review .proto schemas anchored by schema path ([9b7875c](https://github.com/gruesomeparty/marginalia/commit/9b7875ca0502e687e64dbfef21d2af461bee2c67))
* review .proto schemas anchored by schema path ([60c7cdb](https://github.com/gruesomeparty/marginalia/commit/60c7cdb7ce73d69a53cc7d660fe8890b79f3bb59))
* review JSON, YAML and TOML as node-path anchored trees ([7e707e6](https://github.com/gruesomeparty/marginalia/commit/7e707e604b5fd14ce2aa688352bfa691c6be8793))
* review server — index, /api/doc, /api/feedback ([096409a](https://github.com/gruesomeparty/marginalia/commit/096409a5b5148e3dedbb3c44783c64998a951995))
* revision loop — materialized resolution, stale and orphaned notes ([787ba2d](https://github.com/gruesomeparty/marginalia/commit/787ba2d4c12ef54a09b907eda39b08541cd217d0))
* scaffold cobra CLI with version command ([a196e2b](https://github.com/gruesomeparty/marginalia/commit/a196e2bdf1e224529851d17f0d0547481578fa5b))
* self-contained CSP-safe review page template ([4d2531f](https://github.com/gruesomeparty/marginalia/commit/4d2531f3d436489e1902d335ae57e71770c151d3))
* serve --watch re-parses a document when its file changes ([#28](https://github.com/gruesomeparty/marginalia/issues/28)) ([c4e5540](https://github.com/gruesomeparty/marginalia/commit/c4e55408fa2f84f7fdc692b1f1cf9eae6f0324c7)), closes [#7](https://github.com/gruesomeparty/marginalia/issues/7)
* serve command with advertise-on-error routing ([e3138d7](https://github.com/gruesomeparty/marginalia/commit/e3138d76a97fce9f75dfd05406244a2a1662b99f))
* themes, ligatures, server-side highlighting, requester notes and skips ([#32](https://github.com/gruesomeparty/marginalia/issues/32)) ([4b1336f](https://github.com/gruesomeparty/marginalia/commit/4b1336fdbfd886970da1db37d20aea5a7457773f)), closes [#18](https://github.com/gruesomeparty/marginalia/issues/18)
* tree formats (JSON/YAML/TOML), multi-document sessions, list-item anchoring ([#22](https://github.com/gruesomeparty/marginalia/issues/22)) ([6f5a44d](https://github.com/gruesomeparty/marginalia/commit/6f5a44dd1539f4ef30d6443b97513bd495cdfe20))


### Bug Fixes

* capture nested code-block text so hashing and quotes see all block content ([bc8e613](https://github.com/gruesomeparty/marginalia/commit/bc8e6131cc0365e55faeaf955f07fc6559caade3))
* **lint:** drop a negated conjunction and an unused helper ([a630f25](https://github.com/gruesomeparty/marginalia/commit/a630f255d5e763503fedd662d7382dff997a8c04))
* **lint:** drop a negated conjunction and an unused helper ([977b1b7](https://github.com/gruesomeparty/marginalia/commit/977b1b7951fcefa32c0fc4649e3e80e670a9fa94))
* M1 tech-debt roundup — payload, shutdown drain, body cap, arg errors ([82c598f](https://github.com/gruesomeparty/marginalia/commit/82c598fc8672267e6fc3373049201dea4b284821))
* scope requester notes to the document they are about ([f5ae87c](https://github.com/gruesomeparty/marginalia/commit/f5ae87ccf5e7128a35866d8645a7b56b96dedd21))
