# Changelog

All notable changes to this project will be documented in this file. See [standard-version](https://github.com/conventional-changelog/standard-version) for commit guidelines.

### [1.6.3](https://github.com/RogueTeam/textiplex/compare/v1.6.2...v1.6.3) (2026-07-04)


### Bug Fixes

* multiple performance improvements for query engine ([#5](https://github.com/RogueTeam/textiplex/issues/5)) ([b0df7d9](https://github.com/RogueTeam/textiplex/commit/b0df7d9259c28f07ee68ed4f90621973fdfe63ab))

### [1.6.2](https://github.com/RogueTeam/textiplex/compare/v1.6.1...v1.6.2) (2026-06-29)


### Bug Fixes

* iterate bitmap in batches of 32 uint32 ([6a19283](https://github.com/RogueTeam/textiplex/commit/6a19283393a01abc90ed7ae5f8c3ef50bdd3debd))
* iterate over the the shortest bitmap and use the largest one for confirming it exists ([8abf989](https://github.com/RogueTeam/textiplex/commit/8abf989bfab2e337e3321431f0658edb48c4fcdb))
* single source of truth for batch size ([77415a9](https://github.com/RogueTeam/textiplex/commit/77415a9945078d672a6d134a8a7c5e755ce815d1))
* smaller batches for better performance ([bfd1d92](https://github.com/RogueTeam/textiplex/commit/bfd1d926bc305e2289e10e78fb2e1ab924916367))

### [1.6.1](https://github.com/RogueTeam/textiplex/compare/v1.6.0...v1.6.1) (2026-06-28)

## [1.6.0](https://github.com/RogueTeam/textiplex/compare/v1.5.0...v1.6.0) (2026-06-28)


### Features

* support for reverse ordering of search ([6b53eec](https://github.com/RogueTeam/textiplex/commit/6b53eecf40facfd923c936bbcf8864956ed355fd))

## [1.5.0](https://github.com/RogueTeam/textiplex/compare/v1.3.0...v1.5.0) (2026-06-28)


### Features

* support for allfield compilation ([3271b56](https://github.com/RogueTeam/textiplex/commit/3271b562fa21ce38ca2d0abc1242376da486e8c3))

## [1.4.0](https://github.com/RogueTeam/textiplex/compare/v1.3.0...v1.4.0) (2026-06-28)


### Features

* include precomputed total document length ([ad579d3](https://github.com/RogueTeam/textiplex/commit/ad579d35bdf0073ea92cd4ddf540e69ac11565cd))
* initial support for parallel processing of collision tokens ([9b075bf](https://github.com/RogueTeam/textiplex/commit/9b075bf4aba04f42cbcd2be7e2e145d07193439e))
* make pass of token frequencies in a single sweep ([8e960f6](https://github.com/RogueTeam/textiplex/commit/8e960f60599f8b5e20ebb313bdde7f60ea205fad))
* support for loading the pending posting list' list in memory when there is available ([c6a56a0](https://github.com/RogueTeam/textiplex/commit/c6a56a0eaced619b2729d42b6ad7378cc9a0a49d))
* support for skipping n idxs ([88a7337](https://github.com/RogueTeam/textiplex/commit/88a7337319c654a559ed0e24dbb4ef2ca9835d94))
* support for token frequencies and posting list to be written in a single pass ([da2b30f](https://github.com/RogueTeam/textiplex/commit/da2b30ffe9ac9558f4348c17da1c775f03c0fd34))
* write token frequencies at the beginning of the function call ([80660c0](https://github.com/RogueTeam/textiplex/commit/80660c093a634b98870adc1cc6690bf5f7d2d115))


### Bug Fixes

* correct iteration code for collision fields ([97298f4](https://github.com/RogueTeam/textiplex/commit/97298f448b7d73ec1112da31cf22f351dd0fcc14))
* dorks properly permit numeric, float and date tokenizers ([4126022](https://github.com/RogueTeam/textiplex/commit/412602210f9db758a3eb744586c200f20c6e62a8))
* fix defer overflow on for loops ([d06ea56](https://github.com/RogueTeam/textiplex/commit/d06ea56b549153285f9a1cea71bad7df74604bbc))
* fixed typo on error handling for collision field ([fbc1607](https://github.com/RogueTeam/textiplex/commit/fbc160775e6122c6f85fd5aeaa9c49728d9c3915))
* force allocations to only support necessary size ([d9b525d](https://github.com/RogueTeam/textiplex/commit/d9b525da9977f462cfdc23a1e303a690c3a37e00))
* initial support for parallel processing ([9216f28](https://github.com/RogueTeam/textiplex/commit/9216f28500da2827bb6247a53d6b3dcdab773a6a))
* pool pending posting lists and work over pointers instead ([63355ba](https://github.com/RogueTeam/textiplex/commit/63355ba96cc3c0b462062e51e3bbf81cf1739fed))
* prepared cursor prior goroutine ([55cd581](https://github.com/RogueTeam/textiplex/commit/55cd5815da7bf36cf534fa7b83fe2e3f4a269324))
* restored to sequential since I/O gets destroyed by copying the same bytes over and over ([a1a2189](https://github.com/RogueTeam/textiplex/commit/a1a218912166dde2c1847e653fff4543ce56a412))
* split madavise call ([a9dc424](https://github.com/RogueTeam/textiplex/commit/a9dc424c3d61b7b65180e68c8e97eb8633a4ff46))
* support for fixed size precomputed for pre-iteration ([e9d2ab7](https://github.com/RogueTeam/textiplex/commit/e9d2ab78b6a1aa439bae797d2472ba64942c16e4))
* support for prealloc part of posting list by guessing collissions from A's side ([8e70d45](https://github.com/RogueTeam/textiplex/commit/8e70d45cad23f0d68449930cede4ddd60d496543))
* use local-file as swap ([835a3d7](https://github.com/RogueTeam/textiplex/commit/835a3d7ec976038e2283126dc6bd7a8b943f933a))
* working parallel processing of merge ([fdfe8cf](https://github.com/RogueTeam/textiplex/commit/fdfe8cfe5422df29d89c257c06cbbad6378c1936))
* working single pass construction of almost the entire dst file ([f812ab8](https://github.com/RogueTeam/textiplex/commit/f812ab8bed7c679ab70a7e137213a38abacd096d))
* write directly from pending files to dst ([89c6f68](https://github.com/RogueTeam/textiplex/commit/89c6f687698834400e9ad5b19c3962b0daf02b9b))
* write directly to the dst file ([b56393e](https://github.com/RogueTeam/textiplex/commit/b56393e6adc7782c77140e8b615004a1dd437638))
* write huge chunk directly using a syscall ([766bbab](https://github.com/RogueTeam/textiplex/commit/766bbabd4dd5af1cedd558e5ff7082b7dc9211c8))
* write the header with writeat instead of seek ([c1b8ea8](https://github.com/RogueTeam/textiplex/commit/c1b8ea8825a689cc59c47c0dcf4a9141621267ea))

## [1.3.0](https://github.com/RogueTeam/textiplex/compare/v1.2.1...v1.3.0) (2026-06-23)


### Features

* moved engine to roaring 32 ([5804e3b](https://github.com/RogueTeam/textiplex/commit/5804e3b9ba69024fbc83e8afdef46b254ec8cdce))
* parallel creation of document ids and the rest of the file ([e301b90](https://github.com/RogueTeam/textiplex/commit/e301b90b61867129ace90d7471cf5b5c02d962cb))
* removed experimental simd ([21700ad](https://github.com/RogueTeam/textiplex/commit/21700ad43ae4a2625a92fab198f743336c150617))
* support for simd operations ([a153aab](https://github.com/RogueTeam/textiplex/commit/a153aab961006d3b34cde05c2f8ab0d8ad4dfea7))
* updated .gitignore ([7935e90](https://github.com/RogueTeam/textiplex/commit/7935e903bdc49a5800a546fe52efcfd410f29ab1))
* write document ids directly into the target file without binary encoding ([32d39e3](https://github.com/RogueTeam/textiplex/commit/32d39e3da03c6bab62b964482ece315831d77ec4))


### Bug Fixes

* capture parentheses ([0379cc2](https://github.com/RogueTeam/textiplex/commit/0379cc2edcd15040e170ce53de97511acf598dae))
* correct setup of merge benchmark ([1973d32](https://github.com/RogueTeam/textiplex/commit/1973d32b6bf0a3e7de5715560faae556edf4c620))
* do not use append on buffer ([42452b6](https://github.com/RogueTeam/textiplex/commit/42452b64da4f59e210d7efe2a0182caba90d0eb8))
* improved benchmark initialization ([495f514](https://github.com/RogueTeam/textiplex/commit/495f5149b58d5c6ea976546dab3e31c2252665d6))
* improved hot path by writing entire structs directly ([6f6c593](https://github.com/RogueTeam/textiplex/commit/6f6c593265901571d54e188234586d03f0e1094e))
* improved write speed by removing almost all data binary conversions ([d57db5f](https://github.com/RogueTeam/textiplex/commit/d57db5ffffec909bed8ac79b16a92a6214a91093))
* keep using bufio even when writing directly ([dd7bcd2](https://github.com/RogueTeam/textiplex/commit/dd7bcd22466188b17633efcd2903578556265540))
* loop unrolling for bitmap operation ([3c84079](https://github.com/RogueTeam/textiplex/commit/3c840794cc8a4a409fe9384f1b8d23fcde529590))
* parallel heavy bench ([6a0ce35](https://github.com/RogueTeam/textiplex/commit/6a0ce358bbd3a96414c71e366703d1fc0665f2b5))
* reduced number of copies by writing directly to bufio ([051ea91](https://github.com/RogueTeam/textiplex/commit/051ea913fb69b3c7f27ef0768b6f565951bd24a3))
* remove copies from hot collision path ([8311f4f](https://github.com/RogueTeam/textiplex/commit/8311f4fc370e1571a4971b41d01af2e1b30cd42a))
* removed dead map ([68a6ff6](https://github.com/RogueTeam/textiplex/commit/68a6ff6a14e6b1b6b9dec93514d5d260714d3d94))
* restored writes without allocs ([a2b918f](https://github.com/RogueTeam/textiplex/commit/a2b918f5303a5521b7463f42e340f80be591c9cd))
* runtime.GC on every benchmark iteration ([69f1618](https://github.com/RogueTeam/textiplex/commit/69f1618b1105b57c5b9e1ff3c7dd9472803d9708))
* skip empty tokens ([e60a95b](https://github.com/RogueTeam/textiplex/commit/e60a95bb7da688cbafc8ae55c98c4be08983e0c1))
* support for correct type for document count ([982745b](https://github.com/RogueTeam/textiplex/commit/982745b4a7446577f8eb5f539226f16400843097))
* support vector as a receiver ([39b9b57](https://github.com/RogueTeam/textiplex/commit/39b9b5772bc627812faf4d7ce04c6c038fbf53c1))
* use counter instead ([e332ff4](https://github.com/RogueTeam/textiplex/commit/e332ff4c4ba615369866f6ec39c944810a501007))
* use directly vectors over data ([0babf33](https://github.com/RogueTeam/textiplex/commit/0babf33dde37546abb2167bb973eadcd31a31d87))
* use sequential advice for mmap load ([be351a5](https://github.com/RogueTeam/textiplex/commit/be351a5ca3060c03e1f4153d4e557889106dc33d))
* write directly to files on big-buffer optimization scenario ([971f509](https://github.com/RogueTeam/textiplex/commit/971f509e6b8bfe844bfa78e12efc60b5811f091a))
* write document lengths directly into destination without binary encoding ([f757cc9](https://github.com/RogueTeam/textiplex/commit/f757cc92c80fbc87f252dd9d19f1e06cbec225fe))

### [1.2.1](https://github.com/RogueTeam/textiplex/compare/v1.2.0...v1.2.1) (2026-06-21)


### Bug Fixes

* release using v prefix ([6438150](https://github.com/RogueTeam/textiplex/commit/6438150e22df56f5ea9bcc656f9c5910449e71cd))

## [1.2.0](https://github.com/RogueTeam/textiplex/compare/v1.1.6...v1.2.0) (2026-06-21)


### Features

* support caller setting number of max-workers ([514ed2f](https://github.com/RogueTeam/textiplex/commit/514ed2f8f33602ca7fa30d1fc548bfdcf0da533f))

### 1.1.6 (2026-06-21)


### Bug Fixes

* do not shadow variable ([29e91cc](https://github.com/RogueTeam/textiplex/commit/29e91cc7aed2dfbc085d74cedd0aa1d207a74105))
* increased size of raw-value to support up to 128 bytes of tokens ([b306308](https://github.com/RogueTeam/textiplex/commit/b306308551cb8686cf118c9e1084fda485d67fb0))

### [1.1.5](https://github.com/RogueTeam/textiplex/compare/v1.1.4...v1.1.5) (2026-06-19)

### [1.1.4](https://github.com/RogueTeam/textiplex/compare/v1.1.3...v1.1.4) (2026-06-19)

### [1.1.3](https://github.com/RogueTeam/textiplex/compare/v1.1.2...v1.1.3) (2026-06-19)

### [1.1.2](https://github.com/RogueTeam/textiplex/compare/v1.1.1...v1.1.2) (2026-06-19)


### Bug Fixes

* do not shadow variable ([29e91cc](https://github.com/RogueTeam/textiplex/commit/29e91cc7aed2dfbc085d74cedd0aa1d207a74105))

### 1.1.1 (2026-06-18)

## 1.1.0 (2026-06-18)


### Features

* added watermark protection ([aa602f5](https://github.com/RogueTeam/textiplex/commit/aa602f5d63f7ca262ead39faba3d2334ef141c7e))
* ci/cd infrastructure ([79366bd](https://github.com/RogueTeam/textiplex/commit/79366bda3eb19d4b3065e5ba2a6918e82ddc2999))
* data pooling ([fdf78a4](https://github.com/RogueTeam/textiplex/commit/fdf78a4a72d8c5c8b795070e5ee67dc52d6a5e17))
* document ids are also mmapped directly ([f46d081](https://github.com/RogueTeam/textiplex/commit/f46d081e101fd2e5e0a0a4d9fc683d69d0306329))
* full support for merge operation ([263c220](https://github.com/RogueTeam/textiplex/commit/263c220685134cb38fbed6b846f2883f7695ec36))
* full support for merging ([7528b33](https://github.com/RogueTeam/textiplex/commit/7528b338474b2dcc28a0b69e295699f2407fef95))
* hash as uint64 instead ([07d9f87](https://github.com/RogueTeam/textiplex/commit/07d9f87260050db3f303a9fd7e220f6db4aca802))
* improved benchmark to match bluge's apples to apples ([8d4c8e6](https://github.com/RogueTeam/textiplex/commit/8d4c8e649f23f167b7f08f4b2b8277bed423ae10))
* improved clause algorithm to reuse most of the iteration code ([6054305](https://github.com/RogueTeam/textiplex/commit/6054305bcf2bde23fb149039ca0abe15b9ac16dd))
* improved memory usage by pooling final build process ([fd199b1](https://github.com/RogueTeam/textiplex/commit/fd199b1f45faa85f6519a4dd1d27bea6cde6a493))
* improved testsuite thanks to claude help ([a562c2a](https://github.com/RogueTeam/textiplex/commit/a562c2a4f2168644075c7e384c076d711fd45d8a))
* included bench for bluge ([dc57af2](https://github.com/RogueTeam/textiplex/commit/dc57af2ded436cbbfd040cd1b43537572454b5d7))
* included bluge benchmarks for comparison ([dcd7908](https://github.com/RogueTeam/textiplex/commit/dcd7908340d2794c34a6f016aa795cbe58a01dcd))
* included bluge's fork benchmark ([718c5fe](https://github.com/RogueTeam/textiplex/commit/718c5fe3217902d79103d40c9b857817058897a8))
* initial ideas on how to insert documents ([2ec6885](https://github.com/RogueTeam/textiplex/commit/2ec68857410549db5f31a88b2f7ec2304ca602fb))
* initial implementation of merge function ([042bf48](https://github.com/RogueTeam/textiplex/commit/042bf48c06560673ef64a62ddb776fc9d4e1d6f3))
* initial support for batch living in fields ([f06cc52](https://github.com/RogueTeam/textiplex/commit/f06cc52e984e1382478c29e810555ce3a6a67aa9))
* initial support for batches ([2a07876](https://github.com/RogueTeam/textiplex/commit/2a0787643dd8f7660fb9f996b26e75609d22d719))
* initial support for buffer loaded ([e0e76ae](https://github.com/RogueTeam/textiplex/commit/e0e76aefef0c0eb93b5afb6bd66a43f483715d02))
* initial support for deltas ([1a8c363](https://github.com/RogueTeam/textiplex/commit/1a8c36360d2c9faee9a18dcf3894759d0eb5e1b9))
* initial support for dork parser ([aa769d4](https://github.com/RogueTeam/textiplex/commit/aa769d4a71b105b5caf4d9fe97b37e70b8abd4c4))
* initial support for dorks ([7b9d564](https://github.com/RogueTeam/textiplex/commit/7b9d5646c4fe73eab2aef741718223073f374d69))
* initial support for field construction  and tokenizer signature ([487ab25](https://github.com/RogueTeam/textiplex/commit/487ab25599de0ca4fb12c9f08b61c1e3471ce0e9))
* initial support for fixed token size ([66936df](https://github.com/RogueTeam/textiplex/commit/66936df0fe510cbe82f8b992b02057776de1051d))
* initial support for indexing wikipedia using textiplex ([abaa834](https://github.com/RogueTeam/textiplex/commit/abaa834ff8e9e00f3f8eaaf50f9a2424d8420638))
* initial support for levenshtein ([f367795](https://github.com/RogueTeam/textiplex/commit/f367795fb0f36d228003e5ff591f77bbae80a1f6))
* initial support for merge ([a7398d3](https://github.com/RogueTeam/textiplex/commit/a7398d351de2b7e20f00920a6f9e8fe0b567f3f6))
* initial support for merge with less allocations ([58be955](https://github.com/RogueTeam/textiplex/commit/58be9555cf95e2cde81dfa63f020da796b4595a9))
* initial support for query and levenshtein using correct token type ([c4fd89b](https://github.com/RogueTeam/textiplex/commit/c4fd89b5f5656179efbb9cd5ff1f4976301660a4))
* initial support for query engine ([35538a1](https://github.com/RogueTeam/textiplex/commit/35538a1e0e4b1d0acd13115de4ac8408118a9be4))
* initial support for save function ([c2a5a4c](https://github.com/RogueTeam/textiplex/commit/c2a5a4c730bd5fe12803b04cee943ace5017b8f6))
* initial support for scoring ([ec4c6ea](https://github.com/RogueTeam/textiplex/commit/ec4c6ea43644672afda980d2b3aec7a2b45322b3))
* initial support for simple query construction ([bf8825d](https://github.com/RogueTeam/textiplex/commit/bf8825d11465435fcf79d3c68fbadb3d17f50fce))
* initial support for simple reader ([7e07a80](https://github.com/RogueTeam/textiplex/commit/7e07a80041327c67757e87307233ae4fc2d498f2))
* initial support for tokenizers ([006b383](https://github.com/RogueTeam/textiplex/commit/006b3830333445a09148a9deb7e211e5698b8fc0))
* initial support for wikipedia data preparation ([f048378](https://github.com/RogueTeam/textiplex/commit/f048378dad9c86790bf5396a624b70da0db1319d))
* initial testing and insertion implementation ([d22a7d2](https://github.com/RogueTeam/textiplex/commit/d22a7d2155e944e3e8d6450088f44a0c81399055))
* initil support for automata ([216d3e8](https://github.com/RogueTeam/textiplex/commit/216d3e8a097d518b01057e5d25b46e18b81099d4))
* levenshtein like filtering ([ac1fc5a](https://github.com/RogueTeam/textiplex/commit/ac1fc5a4432d2bb1cf0d9cb72a1ec17195db1969))
* lock free btree ([4f2df79](https://github.com/RogueTeam/textiplex/commit/4f2df792699b15a0b4ff2af30dca93809b8d1035))
* make all tests pass and initial implementation of compiler ([a3a7ac8](https://github.com/RogueTeam/textiplex/commit/a3a7ac843185ee957434d828c57f0d5867556b3f))
* make caller responsability to produce bitmaps ([49b5674](https://github.com/RogueTeam/textiplex/commit/49b56742663b9c36250e2f077202c9c76d31f7c3))
* make merge work again with new token structure ([de804dc](https://github.com/RogueTeam/textiplex/commit/de804dce3a1c75ed5b077c6b44659767b05c2b7c))
* make most of tests pass ([e50900d](https://github.com/RogueTeam/textiplex/commit/e50900d7ffcaa91468b34311c79bc5977587d00a))
* memory friendlier search over mapped storage ([edc22a9](https://github.com/RogueTeam/textiplex/commit/edc22a9b407c4a6507441760240104deb7c91022))
* moved testsuite functions to its own file inside the package ([5130fd2](https://github.com/RogueTeam/textiplex/commit/5130fd2188394ba4fd465090a2f0059aa833673a))
* operate over files instead of io.Writer ([46df98e](https://github.com/RogueTeam/textiplex/commit/46df98e040bc7329f50bb4c936d3ee42bba6fed4))
* per storage bitmap pool ([1a1e340](https://github.com/RogueTeam/textiplex/commit/1a1e34011a10e68ba46aeee448b0e83b4b7ec4c1))
* placeholders for fuzzy searching ([cf0816b](https://github.com/RogueTeam/textiplex/commit/cf0816b8033f0314c40835cfabb2215575654ba8))
* prealloc score maps ([2fdaef1](https://github.com/RogueTeam/textiplex/commit/2fdaef18dd64e4e562707355e518cbece6a3e424))
* storage lazy loading of posting lists ([75578ef](https://github.com/RogueTeam/textiplex/commit/75578ef3d41762dbffeb4c40273b6fd621425ee4))
* support for batch living outside storage ([629a117](https://github.com/RogueTeam/textiplex/commit/629a11787ec369e7a4d4657995830f597fddcf2c))
* support for benchmarks ([b0d7cbb](https://github.com/RogueTeam/textiplex/commit/b0d7cbbff4609cbf2d1d07fbc32871e0d8712768))
* support for better library naming convention ([492f0c5](https://github.com/RogueTeam/textiplex/commit/492f0c5625bbf205c15b80f9168f766377709cbd))
* support for better searching by caching most of the query xxh3 actions ([5010e07](https://github.com/RogueTeam/textiplex/commit/5010e0733961451a1c3b36408eb832b77396d876))
* support for bluge fork wikipedia benchmark ([30576e0](https://github.com/RogueTeam/textiplex/commit/30576e05cf7d8364cd9b0698127b302e6d4b11ef))
* support for correct ranges ([987aee1](https://github.com/RogueTeam/textiplex/commit/987aee10c176433d965c93463c7f597de6a4d618))
* support for document lengths ([a78b736](https://github.com/RogueTeam/textiplex/commit/a78b73697dd7bbf68a84a4b88248e814ec29c603))
* support for fast parsing of wikipedia stored data ([56a7eb3](https://github.com/RogueTeam/textiplex/commit/56a7eb38515b81f752fbccb98af039a99ba4a3c8))
* support for full benchmarking of storage construction using batch helper ([38c2c4e](https://github.com/RogueTeam/textiplex/commit/38c2c4ef2e38ec268e58a9c968c8ae9553bc959a))
* support for levenshtein querying ([9f6d160](https://github.com/RogueTeam/textiplex/commit/9f6d16068f1e7494d1d0cf58c95ff75cbdf232a8))
* support for merge ([da30628](https://github.com/RogueTeam/textiplex/commit/da30628be70711117d55e72ef8ea43b23f36e1ea))
* support for size pre-computation ([30ac30c](https://github.com/RogueTeam/textiplex/commit/30ac30cebf846076c476837d014723e55e243ace))
* support for stopwords ([c9b7d72](https://github.com/RogueTeam/textiplex/commit/c9b7d720a94365661d3b8291e647ac380a63b3c3))
* support for worker pool on merge function ([b963005](https://github.com/RogueTeam/textiplex/commit/b9630054f4b73cbf2f56d1e114cb06edacdfb701))
* support for writer ([72dc20d](https://github.com/RogueTeam/textiplex/commit/72dc20d3ba2fc6ae1165917d880f8ea919bbba73))
* test script ([a6b3c98](https://github.com/RogueTeam/textiplex/commit/a6b3c98d2f95dccc1d7f257e6774a34ccddcf67d))
* use of buffered writers to improve performance ([7a9d7d9](https://github.com/RogueTeam/textiplex/commit/7a9d7d923e829e8cbb6e4d6bc0b537360e2a9665))
* write directly the documents lengths to a temporary file ([066197b](https://github.com/RogueTeam/textiplex/commit/066197baba67dbf04d4671b61ffe94be642f90b2))


### Bug Fixes

* added defer put of created batches ([8cc66bd](https://github.com/RogueTeam/textiplex/commit/8cc66bdcc8ccc81689fcad14bf3feb42eb213ac9))
* added guards for comparisons ([5c600c3](https://github.com/RogueTeam/textiplex/commit/5c600c31bb28d436cc327c152dd8771325a89bbb))
* added tuple for documents freqs ([2244d62](https://github.com/RogueTeam/textiplex/commit/2244d6203566cdc9e827c14e678de0217c010b17))
* benchs should not count resolving doc idxs ([8e781ae](https://github.com/RogueTeam/textiplex/commit/8e781ae97c7304c88e2e1b6566ed928f8b5c08f0))
* do not prealloc rather use pools ([e5a7ed7](https://github.com/RogueTeam/textiplex/commit/e5a7ed7361c0047865c1282d27d10996cb58f458))
* do not shadow err variable ([94898ea](https://github.com/RogueTeam/textiplex/commit/94898ea938d75c963bee5beb405b157ef8c7adbe))
* do not use mmap for reading source data ([ab1e1f5](https://github.com/RogueTeam/textiplex/commit/ab1e1f5f7dccdca833732d842e6ac5718a64ecef))
* enforce the use of unsorted insertions ([8c40e87](https://github.com/RogueTeam/textiplex/commit/8c40e8760aea99cca5b7f8fc772ecaee19a86a04))
* extended testsuite ([c4af426](https://github.com/RogueTeam/textiplex/commit/c4af42678c062348e2d23466558f2cbde61ef36a))
* extended testsuite ([a3616e5](https://github.com/RogueTeam/textiplex/commit/a3616e54e9c3b6a97c5244e80fbbe27f3521b0aa))
* filter those which its score is zero ([748da8b](https://github.com/RogueTeam/textiplex/commit/748da8bdb537a61312af6cc8c8ab80925761cbf4))
* fixed deadlock ([3209dbd](https://github.com/RogueTeam/textiplex/commit/3209dbdc874cfec120c13bfde45a01f7b665dc81))
* fixed fork benchmark for search ([b335541](https://github.com/RogueTeam/textiplex/commit/b335541b72edc2451304cd91915ec297c81ae422))
* fixed iteration issue not sorting properly tokens ([d2a652b](https://github.com/RogueTeam/textiplex/commit/d2a652b8b452e005dc8f2e26d14d0aecf1f880e0))
* fixed most of the tests ([288f66d](https://github.com/RogueTeam/textiplex/commit/288f66d2d4a0ce7417f454ce531496693ac3d981))
* fixed upstream bench to actual search results ([69002c7](https://github.com/RogueTeam/textiplex/commit/69002c7393a7a5a90ea0c53d833f87d5d9a35256))
* force a garbage collection after indexing ([06a29e5](https://github.com/RogueTeam/textiplex/commit/06a29e535172ee83743118b64aaa9213eab72f6f))
* isolate search logic in searcher ([8261fe3](https://github.com/RogueTeam/textiplex/commit/8261fe36fe8dc4263c6df972d66e8a4786115a79))
* make all query tests pass ([11e02ab](https://github.com/RogueTeam/textiplex/commit/11e02abc0c1fda0cce87295eed1cf12c394bade3))
* make all searching test pass ([ce5a231](https://github.com/RogueTeam/textiplex/commit/ce5a231d1731b57f0ec3dd4a94bf600ec57d433b))
* make all tests pass ([994f7fc](https://github.com/RogueTeam/textiplex/commit/994f7fc31c7ff9e908aff4979f42fa68afe43a20))
* make wikipedia test pass ([52fb16f](https://github.com/RogueTeam/textiplex/commit/52fb16f35253310260f8c43a9d9d96079a7f00fa))
* more aggresive merge ([4bb0ffa](https://github.com/RogueTeam/textiplex/commit/4bb0ffa908120d1ef48fb7b5bcb2fc8d215b940e))
* move pool logic to indexing function ([ad981da](https://github.com/RogueTeam/textiplex/commit/ad981da981f5649748bc20483deffeafe150d417))
* moved bm25 logic to its own file ([9256035](https://github.com/RogueTeam/textiplex/commit/9256035add6077dfde311b1be1f7f224c7ade674))
* moved levenshtein to its own package ([5513fc8](https://github.com/RogueTeam/textiplex/commit/5513fc81839dfe35c2c47ed4e73565cb05f164b5))
* moved tuples to its own package ([1a7926a](https://github.com/RogueTeam/textiplex/commit/1a7926a8cb9117f7adf8f608330a9af6f5ebc847))
* only permit four workers ([5701255](https://github.com/RogueTeam/textiplex/commit/5701255efb4c22737fa81c30265a1100c3f9d0bd))
* query tokenizer with zero copy allocations ([d74b8f1](https://github.com/RogueTeam/textiplex/commit/d74b8f110f1f97f4c55a18690c4702df1cbc11ac))
* removed levenshtein - automata is mandatory to traverse the tree faster ([eeb6410](https://github.com/RogueTeam/textiplex/commit/eeb64105ca1143300b21738bdfea7cec435946cc))
* removed maps that only consumes memory without huge performance benefits ([0a3dbb0](https://github.com/RogueTeam/textiplex/commit/0a3dbb044eaee20a11604c5a013238d88e82461f))
* removed non automata code ([4def010](https://github.com/RogueTeam/textiplex/commit/4def010aaa2b08b9c286622cafc35b47a326815e))
* removed println ([9da0e05](https://github.com/RogueTeam/textiplex/commit/9da0e05856bc5622c8a83be213fbf23b94b9d5d2))
* removed references from state struct ([2c1ee7e](https://github.com/RogueTeam/textiplex/commit/2c1ee7e467011d1a3949cb153836025803dbf1ad))
* resolve scores is its own operation ([b0e059c](https://github.com/RogueTeam/textiplex/commit/b0e059ce4656cf9875d81b2b311ffa728f1e7c34))
* reuse file on each iteration instead of creating one ([032f4b4](https://github.com/RogueTeam/textiplex/commit/032f4b4acf59af9fcbc02615e995a8b256aad3cf))
* skip failed to parse json ([7d5285e](https://github.com/RogueTeam/textiplex/commit/7d5285e12684abe713cd81bf4e7d66bde39180ab))
* sort by fields order too ([bc8f831](https://github.com/RogueTeam/textiplex/commit/bc8f8311d4b377bb7c631d0d0e9ee5b65b45ea2a))
* support for configurable bm25 length penalty and saturation ([973a614](https://github.com/RogueTeam/textiplex/commit/973a614e63f08c03bc107886e18ddf21ab9981f3))
* test should only print debug data every 1M records ([05fd9ff](https://github.com/RogueTeam/textiplex/commit/05fd9ff4afc0346d82585ad17cf0ad92e0850b73))
* updated benchmarks to use directory based indexes ([d822153](https://github.com/RogueTeam/textiplex/commit/d822153510071af9a60efaac7b63fafdd038eb05))
* updated gitignore ([247aa70](https://github.com/RogueTeam/textiplex/commit/247aa70ffc5fa8fed4694feff80b35a2234fac92))
* use correct insert function ([744927c](https://github.com/RogueTeam/textiplex/commit/744927c6bb5bb70ce2c42047d95cda287f055c1c))
* use slices for saving conditions in clause ([78b35ff](https://github.com/RogueTeam/textiplex/commit/78b35ffca0f82e37542c50b7dc54ea4d3800352b))
* use token pool of pre-alloc for huge merges ([850c6bb](https://github.com/RogueTeam/textiplex/commit/850c6bb37e775e3a43f1e9529e45a04554a26573))
