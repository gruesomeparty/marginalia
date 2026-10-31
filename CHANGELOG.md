# Changelog

## [0.6.0](https://github.com/gruesomeparty/marginalia/compare/v0.5.0...v0.6.0) (2026-10-31)


### Features

* agent-facing done signals follow the review's completion rule ([#211](https://github.com/gruesomeparty/marginalia/issues/211)) ([9d8a9c0](https://github.com/gruesomeparty/marginalia/commit/9d8a9c074b76bf57cda2ccfcbd3bc1b13819ef3b))
* **feedback:** per-reviewer verdicts and a completion rule ([#208](https://github.com/gruesomeparty/marginalia/issues/208)) ([c069c01](https://github.com/gruesomeparty/marginalia/commit/c069c016b7b768c3096dd34270aa36236ce9b0fe))
* **web:** show who wrote each note and follow other reviewers live ([#212](https://github.com/gruesomeparty/marginalia/issues/212)) ([00d5b96](https://github.com/gruesomeparty/marginalia/commit/00d5b9608f9885fd929a7390b36fddc7b9a9701f))

## [0.5.0](https://github.com/gruesomeparty/marginalia/compare/v0.4.0...v0.5.0) (2026-10-31)


### Features

* adopt mermaid-cli 12 and strip the webfonts it embeds ([#203](https://github.com/gruesomeparty/marginalia/issues/203)) ([1e41d99](https://github.com/gruesomeparty/marginalia/commit/1e41d9916f21d94f9a1381b5ac688c39d6caf6b9)), closes [#87](https://github.com/gruesomeparty/marginalia/issues/87)
* anchor mermaid sequence, state and ER diagrams per statement ([#196](https://github.com/gruesomeparty/marginalia/issues/196)) ([98d998d](https://github.com/gruesomeparty/marginalia/commit/98d998d36972ca2dbf3796ce6188ef5e259b7759))
* **ci:** flag PRs that conflict with main in the merge loop ([#198](https://github.com/gruesomeparty/marginalia/issues/198)) ([8a9e747](https://github.com/gruesomeparty/marginalia/commit/8a9e747b1f5aea944924bd818644ad4f5c208970))
* **hosted:** admit only listed reviewers, carry provider and subject ([#202](https://github.com/gruesomeparty/marginalia/issues/202)) ([6794c85](https://github.com/gruesomeparty/marginalia/commit/6794c8587f1c252c2ef11bd08811d12111670e90))
* **hosted:** let an agent long-poll a review over HTTP ([#186](https://github.com/gruesomeparty/marginalia/issues/186)) ([#200](https://github.com/gruesomeparty/marginalia/issues/200)) ([d5420da](https://github.com/gruesomeparty/marginalia/commit/d5420da92b06f176a4e5641897def9992f0c3c2c))
* make sequence, state and ER diagram pictures clickable ([#201](https://github.com/gruesomeparty/marginalia/issues/201)) ([aadf5d8](https://github.com/gruesomeparty/marginalia/commit/aadf5d8d3f97700fda4f66234e28464f38873fbd))
* **mcp:** wake await_review_done on questions, short default waits ([#193](https://github.com/gruesomeparty/marginalia/issues/193)) ([b175f4a](https://github.com/gruesomeparty/marginalia/commit/b175f4a11cc9333a51622d79047c116bb2351315)), closes [#182](https://github.com/gruesomeparty/marginalia/issues/182)
* **merge:** ask for the maintainer by change domain, not line count ([#195](https://github.com/gruesomeparty/marginalia/issues/195)) ([73ec8d6](https://github.com/gruesomeparty/marginalia/commit/73ec8d62a507fe27eaec29055d45da82a17f8954))
* opt-in token for serve, and provenance checks on import ([#204](https://github.com/gruesomeparty/marginalia/issues/204)) ([0ba148f](https://github.com/gruesomeparty/marginalia/commit/0ba148f038b6dd769349ef63d4b1afa5419da0d8))
* review OpenAPI specs by operation and component ([#197](https://github.com/gruesomeparty/marginalia/issues/197)) ([ced9063](https://github.com/gruesomeparty/marginalia/commit/ced9063f0ba7162a7e0e8763bd712dbc522138cb))
* review Terraform and HCL files anchored by block path ([#192](https://github.com/gruesomeparty/marginalia/issues/192)) ([1ee63e1](https://github.com/gruesomeparty/marginalia/commit/1ee63e1874027e60989ab95df9d9f7c6e60e76fc)), closes [#151](https://github.com/gruesomeparty/marginalia/issues/151)
* **triage:** re-triage an issue when Berkay answers its question ([#191](https://github.com/gruesomeparty/marginalia/issues/191)) ([b7aa8ad](https://github.com/gruesomeparty/marginalia/commit/b7aa8ad4bb4f843f66d22fdb7580e731d0a50d36))


### Bug Fixes

* **hosted:** count the agent wait on the review's logs ([#205](https://github.com/gruesomeparty/marginalia/issues/205)) ([d4f79e3](https://github.com/gruesomeparty/marginalia/commit/d4f79e33a198c34730253cb2daea3e58e57403c8))

## [0.4.0](https://github.com/gruesomeparty/marginalia/compare/v0.3.1...v0.4.0) (2026-10-30)


### Features

* answer requester notes in place ([#159](https://github.com/gruesomeparty/marginalia/issues/159)) ([9191c63](https://github.com/gruesomeparty/marginalia/commit/9191c63495eef7bfe490541b2a2578b183e1be94))
* filter and group the inbox ([#181](https://github.com/gruesomeparty/marginalia/issues/181)) ([3cd95f1](https://github.com/gruesomeparty/marginalia/commit/3cd95f178e76e961a63c7a76b497974273fe1199))
* finish a review from a summary sheet ([#165](https://github.com/gruesomeparty/marginalia/issues/165)) ([2da42bb](https://github.com/gruesomeparty/marginalia/commit/2da42bbe8d5d5176e87aa87d6cfdb10d2bfaca02))
* give verdicts their own place in the composer ([#169](https://github.com/gruesomeparty/marginalia/issues/169)) ([60e6aa4](https://github.com/gruesomeparty/marginalia/commit/60e6aa465d662f829542c7c04dcd37553230cc3b)), closes [#133](https://github.com/gruesomeparty/marginalia/issues/133)
* lead inbox rows with what needs the reviewer ([#170](https://github.com/gruesomeparty/marginalia/issues/170)) ([85713d9](https://github.com/gruesomeparty/marginalia/commit/85713d9f1575309d6189e2888940d0d48cb4aaf3)), closes [#134](https://github.com/gruesomeparty/marginalia/issues/134)
* link hosted review pages back to the inbox ([#180](https://github.com/gruesomeparty/marginalia/issues/180)) ([f41d5d2](https://github.com/gruesomeparty/marginalia/commit/f41d5d212e44bf1984b0ae5fe6801ee7afa669d8))
* list every note in one panel ([#188](https://github.com/gruesomeparty/marginalia/issues/188)) ([c53fa01](https://github.com/gruesomeparty/marginalia/commit/c53fa0137a0748d0e1612e4e3cb890f13050c4e9))
* make reviewing on a phone comfortable ([#187](https://github.com/gruesomeparty/marginalia/issues/187)) ([ace0bc9](https://github.com/gruesomeparty/marginalia/commit/ace0bc92fbdc5252bd4234f0cd0119489d5249d2))
* reach drawn diagram shapes from the keyboard ([#184](https://github.com/gruesomeparty/marginalia/issues/184)) ([708725f](https://github.com/gruesomeparty/marginalia/commit/708725fab3e2eba1be37646683554829e8333f84)), closes [#154](https://github.com/gruesomeparty/marginalia/issues/154)
* review SQL one statement at a time ([#189](https://github.com/gruesomeparty/marginalia/issues/189)) ([494bd0e](https://github.com/gruesomeparty/marginalia/commit/494bd0e53cde1ee6ca5fc4cd147d035fd8cb8e6e))
* serve --until-done, --timeout and a long-poll endpoint ([#164](https://github.com/gruesomeparty/marginalia/issues/164)) ([dafc28f](https://github.com/gruesomeparty/marginalia/commit/dafc28f4734a4a017e893b821094059d8debd388))
* show notes in a margin beside their block on wide screens ([#190](https://github.com/gruesomeparty/marginalia/issues/190)) ([9184abb](https://github.com/gruesomeparty/marginalia/commit/9184abbe9a41b25539b5824cfa7f0b053e10debf)), closes [#130](https://github.com/gruesomeparty/marginalia/issues/130)
* show review progress by section ([#168](https://github.com/gruesomeparty/marginalia/issues/168)) ([6355456](https://github.com/gruesomeparty/marginalia/commit/63554566bfb10056946a7e5dba40c36f5b8aa970)), closes [#132](https://github.com/gruesomeparty/marginalia/issues/132)


### Bug Fixes

* **ci:** let PRs that only document through the merge gate ([#177](https://github.com/gruesomeparty/marginalia/issues/177)) ([9082e3d](https://github.com/gruesomeparty/marginalia/commit/9082e3d5c0c08442b0bd44e25e8d013921fa6179))

## [0.3.1](https://github.com/gruesomeparty/marginalia/compare/v0.3.0...v0.3.1) (2026-10-29)


### Bug Fixes

* **ci:** keep the merge loop sweeping past a PR it cannot read ([#172](https://github.com/gruesomeparty/marginalia/issues/172)) ([6b9d92a](https://github.com/gruesomeparty/marginalia/commit/6b9d92ab12aa0f80a67e712c2d310c53fcac97f1))
* **deploy:** make the Cloudflare template deployable and stop strangers waking the container ([#157](https://github.com/gruesomeparty/marginalia/issues/157)) ([437f85c](https://github.com/gruesomeparty/marginalia/commit/437f85c5fda16259546455e29a05040a1ec5c292))

## [0.3.0](https://github.com/gruesomeparty/marginalia/compare/v0.2.0...v0.3.0) (2026-10-29)


### Features

* **deploy:** a credential-free Cloudflare deployment template ([a732a2e](https://github.com/gruesomeparty/marginalia/commit/a732a2ea1fb98b9be1b0dc2698cb025cdec651ca))
* **host:** read the public URL from MARGINALIA_PUBLIC_URL ([77ffbb2](https://github.com/gruesomeparty/marginalia/commit/77ffbb2905d4211b324214897489de3c4a9c6026))
* **pipeline:** a triage loop, an armed work loop, and agent notes in hidden comments ([d75b493](https://github.com/gruesomeparty/marginalia/commit/d75b493eaec6977846de34a42d896ea59d7357ba))


### Bug Fixes

* **deploy:** restart the container when a deploy changes its settings ([10191db](https://github.com/gruesomeparty/marginalia/commit/10191dba208e42e76b8e5e36435cda705c534e12))

## [0.2.0](https://github.com/gruesomeparty/marginalia/compare/v0.1.2...v0.2.0) (2026-10-14)


### Features

* **host:** /mcp as an OAuth resource server, so the instance works as a claude.ai connector ([#111](https://github.com/gruesomeparty/marginalia/issues/111)) ([2749b06](https://github.com/gruesomeparty/marginalia/commit/2749b06f6e456ff2e1ef208553a04b495ed0501c))
* **host:** hard caps on what agents can publish, and archiving of what outlived its retention ([#113](https://github.com/gruesomeparty/marginalia/issues/113)) ([acb7443](https://github.com/gruesomeparty/marginalia/commit/acb7443cb4d7730b4a030fa83c4ce7c47a2e0db1))
* **host:** notify the human when an agent publishes or replies ([#116](https://github.com/gruesomeparty/marginalia/issues/116)) ([5178895](https://github.com/gruesomeparty/marginalia/commit/51788952b5130d298172fbd2038263b892fb1bfd))
* **host:** one listener for many reviews, state in internal/storage ([#108](https://github.com/gruesomeparty/marginalia/issues/108)) ([43a11e4](https://github.com/gruesomeparty/marginalia/commit/43a11e4830e6c392ef725fbc253f921cad0cfe1a))
* **host:** publish_document — content in, a review URL out, revisions on republish ([#109](https://github.com/gruesomeparty/marginalia/issues/109)) ([a85cc2a](https://github.com/gruesomeparty/marginalia/commit/a85cc2a26f7ab628370509f8d6f17babd65451d0))
* **host:** resolve who is asking — verified human at the edge, agent on /mcp ([#110](https://github.com/gruesomeparty/marginalia/issues/110)) ([e00ea4d](https://github.com/gruesomeparty/marginalia/commit/e00ea4d384102804faa07c0643900f12ad9ddbbe))
* **host:** the inbox — every review, newest first, with what it is waiting on ([#112](https://github.com/gruesomeparty/marginalia/issues/112)) ([1ac10d3](https://github.com/gruesomeparty/marginalia/commit/1ac10d32266547e33456e93c6f22cf10e7ec05ec))
* **mcp:** serve the tools over Streamable HTTP with --http and a bearer token ([#106](https://github.com/gruesomeparty/marginalia/issues/106)) ([5e2aaba](https://github.com/gruesomeparty/marginalia/commit/5e2aaba1b3b92e709dd45e454f25a2d98bb06767))
* **storage:** put persistent state behind internal/storage, disk the default ([#107](https://github.com/gruesomeparty/marginalia/issues/107)) ([a32f738](https://github.com/gruesomeparty/marginalia/commit/a32f73865e49c6a2e2e922204718cb32474541cb))
* **storage:** R2 backend — the first remote implementation of internal/storage ([#114](https://github.com/gruesomeparty/marginalia/issues/114)) ([62e1008](https://github.com/gruesomeparty/marginalia/commit/62e1008d47b750c70a819a4066bbdb9134cff5ff))

## [0.1.2](https://github.com/gruesomeparty/marginalia/compare/v0.1.1...v0.1.2) (2026-09-26)


### Bug Fixes

* **pages:** pin mermaid-cli to the version testdata was rendered with ([#86](https://github.com/gruesomeparty/marginalia/issues/86)) ([024ecc7](https://github.com/gruesomeparty/marginalia/commit/024ecc743d1d7580361f224b7268bfd338f7651e))

## [0.1.1](https://github.com/gruesomeparty/marginalia/compare/v0.1.0...v0.1.1) (2026-09-26)


### Bug Fixes

* **release:** the cask token must be {{ .Env.NAME }} and nothing else ([#83](https://github.com/gruesomeparty/marginalia/issues/83)) ([5ab3e61](https://github.com/gruesomeparty/marginalia/commit/5ab3e617a4a742cb3183e478875b235681de3621))

## 0.1.0 (2026-09-20)


### Features

* a landing page on GitHub Pages, with the product itself as the hero ([03f18dc](https://github.com/gruesomeparty/marginalia/commit/03f18dcf3c3039b15ec6813c8ef48882637d36dd))
* advertise request-feature skill on unknown flags and subcommands ([966e763](https://github.com/gruesomeparty/marginalia/commit/966e763a198de2fbc6c759381ad817375bb448ac))
* anchor a note to a sentence, not just the block around it ([#49](https://github.com/gruesomeparty/marginalia/issues/49)) ([886d7c8](https://github.com/gruesomeparty/marginalia/commit/886d7c856f7c20e4bfb9fbea159b0e2e0b160937))
* anchor markdown list items individually ([82ae748](https://github.com/gruesomeparty/marginalia/commit/82ae7486742ad648fb79484566f9525c1b7f3f0e))
* anchor mermaid diagrams statement by statement ([10f6694](https://github.com/gruesomeparty/marginalia/commit/10f66947273b40e6521d7f7f019ba535b9e023ad)), closes [#25](https://github.com/gruesomeparty/marginalia/issues/25)
* append-only JSONL feedback store with resolution view ([a731d9c](https://github.com/gruesomeparty/marginalia/commit/a731d9c391a2ddbbb0181b219aab5416547c8f89))
* block anchoring — section paths, quotes, content hashes ([37b4624](https://github.com/gruesomeparty/marginalia/commit/37b4624e3d652a20399b2fb9038ea8ce8f3c19cd))
* close the revision loop — addressed, confirm, reopen ([#48](https://github.com/gruesomeparty/marginalia/issues/48)) ([e0bf85e](https://github.com/gruesomeparty/marginalia/commit/e0bf85e790ed5a4d146b0e53eb97eb6decec0dff))
* configurable reviews — framing, custom actions, read-only blocks ([#30](https://github.com/gruesomeparty/marginalia/issues/30)) ([104754e](https://github.com/gruesomeparty/marginalia/commit/104754e33b7c7a0d2c34541ceccfcb6fef5d7b95)), closes [#3](https://github.com/gruesomeparty/marginalia/issues/3)
* draw mermaid diagrams as anchored SVG, source one toggle away ([#37](https://github.com/gruesomeparty/marginalia/issues/37)) ([e5b6d17](https://github.com/gruesomeparty/marginalia/commit/e5b6d173639863e872dff0e81a596b892fad1597)), closes [#36](https://github.com/gruesomeparty/marginalia/issues/36)
* give a long review a shape — progress, movement, filters ([#46](https://github.com/gruesomeparty/marginalia/issues/46)) ([da0bee6](https://github.com/gruesomeparty/marginalia/commit/da0bee62fa6c54caf45c3e68d4d5e67a9bab5ccc))
* M4 automated implementation pipeline ([f7823b1](https://github.com/gruesomeparty/marginalia/commit/f7823b1b40887a6b3b26f6d9f92ebab1e80cb74b))
* M5 static share mode — export a review page, import the reply ([#33](https://github.com/gruesomeparty/marginalia/issues/33)) ([7123f3b](https://github.com/gruesomeparty/marginalia/commit/7123f3ba9ade18c7e99196a96d6e17b2e7b4ca2f)), closes [#5](https://github.com/gruesomeparty/marginalia/issues/5)
* multi-document review sessions with a navigable file tree ([4e6da18](https://github.com/gruesomeparty/marginalia/commit/4e6da180fac61de9935eb6adf5efcb51c5122761))
* package repo as an installable Claude plugin + marketplace ([e53dd4a](https://github.com/gruesomeparty/marginalia/commit/e53dd4a8be33479e22f34742faf391907e58edcd))
* publish a Homebrew cask to gruesomeparty/homebrew-tap on release ([2447641](https://github.com/gruesomeparty/marginalia/commit/24476412e52aa5386902314de74dcb5dc823bf8b))
* report the suggest_edit replacements that are safe to apply ([1e1e867](https://github.com/gruesomeparty/marginalia/commit/1e1e867c8082c0d1cc12f56dcd71775d280a19d6))
* review .proto schemas anchored by schema path ([9b7875c](https://github.com/gruesomeparty/marginalia/commit/9b7875ca0502e687e64dbfef21d2af461bee2c67))
* review .proto schemas anchored by schema path ([60c7cdb](https://github.com/gruesomeparty/marginalia/commit/60c7cdb7ce73d69a53cc7d660fe8890b79f3bb59))
* review a unified diff, anchored by file and hunk ([#52](https://github.com/gruesomeparty/marginalia/issues/52)) ([8564ffc](https://github.com/gruesomeparty/marginalia/commit/8564ffc8ed16fd326cb4d4ffaff17e0883cd0e01))
* review JSON, YAML and TOML as node-path anchored trees ([7e707e6](https://github.com/gruesomeparty/marginalia/commit/7e707e604b5fd14ce2aa688352bfa691c6be8793))
* review server — index, /api/doc, /api/feedback ([096409a](https://github.com/gruesomeparty/marginalia/commit/096409a5b5148e3dedbb3c44783c64998a951995))
* revision loop — materialized resolution, stale and orphaned notes ([787ba2d](https://github.com/gruesomeparty/marginalia/commit/787ba2d4c12ef54a09b907eda39b08541cd217d0))
* scaffold cobra CLI with version command ([a196e2b](https://github.com/gruesomeparty/marginalia/commit/a196e2bdf1e224529851d17f0d0547481578fa5b))
* self-contained CSP-safe review page template ([4d2531f](https://github.com/gruesomeparty/marginalia/commit/4d2531f3d436489e1902d335ae57e71770c151d3))
* serve --watch re-parses a document when its file changes ([#28](https://github.com/gruesomeparty/marginalia/issues/28)) ([c4e5540](https://github.com/gruesomeparty/marginalia/commit/c4e55408fa2f84f7fdc692b1f1cf9eae6f0324c7)), closes [#7](https://github.com/gruesomeparty/marginalia/issues/7)
* serve command with advertise-on-error routing ([e3138d7](https://github.com/gruesomeparty/marginalia/commit/e3138d76a97fce9f75dfd05406244a2a1662b99f))
* ship review framings so an agent picks a vocabulary ([b431897](https://github.com/gruesomeparty/marginalia/commit/b43189729cf5495157bc8617ed594c2f18dc3060)), closes [#45](https://github.com/gruesomeparty/marginalia/issues/45)
* **site:** build the landing page from obbin's design ([eecea5e](https://github.com/gruesomeparty/marginalia/commit/eecea5eecb738094d6ee9d95c69c9f2a51cb5faf))
* **site:** draw the hero's diagram, and lead with it ([53c6530](https://github.com/gruesomeparty/marginalia/commit/53c6530aeea18b798e699f771a0941b13f4416ef))
* speak MCP, so agents stop scraping banners and parsing JSONL ([0198e42](https://github.com/gruesomeparty/marginalia/commit/0198e4238ecd69674fadffabb1cfeba9b00b88ee)), closes [#43](https://github.com/gruesomeparty/marginalia/issues/43)
* themes, ligatures, server-side highlighting, requester notes and skips ([#32](https://github.com/gruesomeparty/marginalia/issues/32)) ([4b1336f](https://github.com/gruesomeparty/marginalia/commit/4b1336fdbfd886970da1db37d20aea5a7457773f)), closes [#18](https://github.com/gruesomeparty/marginalia/issues/18)
* threaded replies — answer a note where it was written ([#47](https://github.com/gruesomeparty/marginalia/issues/47)) ([744447e](https://github.com/gruesomeparty/marginalia/commit/744447e2c6c3733fe3c25f448862bffdb9720fe4))
* tree formats (JSON/YAML/TOML), multi-document sessions, list-item anchoring ([#22](https://github.com/gruesomeparty/marginalia/issues/22)) ([6f5a44d](https://github.com/gruesomeparty/marginalia/commit/6f5a44dd1539f4ef30d6443b97513bd495cdfe20))


### Bug Fixes

* capture nested code-block text so hashing and quotes see all block content ([bc8e613](https://github.com/gruesomeparty/marginalia/commit/bc8e6131cc0365e55faeaf955f07fc6559caade3))
* check an addressed claim against the sentence, not the paragraph ([#67](https://github.com/gruesomeparty/marginalia/issues/67)) ([a865479](https://github.com/gruesomeparty/marginalia/commit/a865479615ec1f41f296d7c947a585c04d824725))
* **lint:** drop a negated conjunction and an unused helper ([a630f25](https://github.com/gruesomeparty/marginalia/commit/a630f255d5e763503fedd662d7382dff997a8c04))
* **lint:** drop a negated conjunction and an unused helper ([977b1b7](https://github.com/gruesomeparty/marginalia/commit/977b1b7951fcefa32c0fc4649e3e80e670a9fa94))
* M1 tech-debt roundup — payload, shutdown drain, body cap, arg errors ([82c598f](https://github.com/gruesomeparty/marginalia/commit/82c598fc8672267e6fc3373049201dea4b284821))
* one path identity, whatever the caller spells ([031a428](https://github.com/gruesomeparty/marginalia/commit/031a428a6112307d632dfd1651c91ebc3c0000d2))
* scope requester notes to the document they are about ([f5ae87c](https://github.com/gruesomeparty/marginalia/commit/f5ae87ccf5e7128a35866d8645a7b56b96dedd21))
* the path-identity test had the path-identity bug ([a072fdc](https://github.com/gruesomeparty/marginalia/commit/a072fdcb8061f260063d72e52a5c3d105b26a553))
* the plugin never shipped the MCP server it tells agents to use ([4413270](https://github.com/gruesomeparty/marginalia/commit/4413270ee0fab793961d1247b1e5cf93d2750113))
