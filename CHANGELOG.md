
<a name="053"></a>
## [053](https://gitlab.com/pantacor/pvr/compare/052...053) (2026-08-11)

### Chore

* **deps:** update go-containerregistry to v0.20.7

### Feat

* **device:** add --baud flag to device tty

### Fix

* **device:** unhang tty exit and stop mangling console output


<a name="052"></a>
## [052](https://gitlab.com/pantacor/pvr/compare/051...052) (2026-07-22)

### Chore

* update CHANGELOG.md for release 052
* **deps:** use pvwificonnect v1.7.0

### Ci

* fix republish helper to emit plain v2s2 via skopeo
* guard channel tags against non-plain (attestation) manifests
* make src/vendor/binary tarballs reproducible

### Docs

* add full command reference and simplify README
* add go install and pvr device tty documentation

### Feat

* add --unset-arg and --unset-config to app update and install
* support --arg and --config on app update and install
* add PV_DEV_LOG template arg for per-container devlog control
* add pvr device tty subcommand for USB serial console
* self-upgrade from GitLab packages; default tag latest; single entrypoint
* add pvr wifi subcommand for Improv Wi-Fi BLE provisioning
* **auth:** add --netrc/-n option to pvr login

### Fix

* surface API failures instead of reporting silent success
* stop stamping bogus -dirty and clean up build-time seds
* use Go 1.21-compatible for loops in device tty
* tty hangs on ctrl + c
* add Linux-only build tags for tty command
* correct vendor/go.mod inconsistency for device tty
* make Dockerfile.go1.21 work with pvwificonnect dependency
* **install:** resolve stable channel to a real release, add selftest

### Refactor

* use modules/client for all pvr wifi command logic
* import pvwificonnect/pkg/client instead of duplicating code

### Test

* cover MergeAppSrcArgs and arg/config flag parsing
* cover PV_DEV_LOG dev-log rendering in RUN_JSON
* verify whole-state signature and compact export json survive pvjson changes


<a name="051"></a>
## [051](https://gitlab.com/pantacor/pvr/compare/050...051) (2026-06-14)

### Feature

* extract state and repoutil into docker-free subpackages
* add pvr curl subcommand to wrap curl with pantahub auth

### Fix

* harden CI against dind startup race and flaky registry pulls
* preserve large integers through JSON encode/decode
* only apply docker- volume name prefix to path volumes
* pvrinstall format for linux

### Test

* add coverage for PV_VOLUME_MOUNTS and named-origin PV_VOLUME_IMPORTS


<a name="050"></a>
## [050](https://gitlab.com/pantacor/pvr/compare/049...050) (2026-06-14)

### Chore

* update CHANGELOG.md for release 050

### Feature

* add support to zip files when doing get, merge or import
* update dockerlib to use new library that supports cli client 1.44

### Fix

* error when reading the _host/config
* bring master changes into the branch with the go1.21 package too
* after adding error managment there were new path to handle
* solve error handle problem
* get docke image from remote needs to always pass the platform
* correct go dockerfile.go1.21
* ci test
* testing
* target on utils build, to build on any platform
* add test to validate binary format
* use remote on CI
* testing to use better cache with support to arch
* use docker cache on pvr test instead of pulling always
* make sure to read the api version and negociate from there
* docker lib should negociate without intervention
* it generate root.squashfs even with old src.json
* docker negotion needs to start from 1.46


<a name="049"></a>
## [049](https://gitlab.com/pantacor/pvr/compare/049-rc1...049) (2026-03-23)

### Feat

* add device status, steps, and statediff subcommands
* add --no-tail option to pvr device logs

### Feature

* add windows installer scripts

### Fix

* user correct posix expression
* donot add the .zip to tar.gz files
* build package in master
* don't build tools on master
* just update the upload of src and vendorr
* gitlab.ci upload packages
* make tools on master too
* docker remote pull was failing for multiarch containers
* new version schema was failing to build in gitlab-ci


<a name="049-rc1"></a>
## [049-rc1](https://gitlab.com/pantacor/pvr/compare/048...049-rc1) (2026-03-06)

### Feature

* update dockerlib to use new library that supports cli client 1.44

### Fix

* error when reading the _host/config
* bring master changes into the branch with the go1.21 package too
* after adding error managment there were new path to handle
* solve error handle problem
* get docke image from remote needs to always pass the platform
* correct go dockerfile.go1.21
* ci test
* testing
* target on utils build, to build on any platform
* add test to validate binary format
* use remote on CI
* testing to use better cache with support to arch
* use docker cache on pvr test instead of pulling always
* make sure to read the api version and negociate from there
* docker lib should negociate without intervention
* it generate root.squashfs even with old src.json
* docker negotion needs to start from 1.46


<a name="048"></a>
## [048](https://gitlab.com/pantacor/pvr/compare/047...048) (2026-03-04)

### Feature

* add windows installer scripts
* add revision to the pvr version command
* do polling pkce instead of just redirection callback
* add make release
* add release to makefile

### Fix

* user correct posix expression
* donot add the .zip to tar.gz files
* build package in master
* don't build tools on master
* just update the upload of src and vendorr
* gitlab.ci upload packages
* make tools on master too
* docker remote pull was failing for multiarch containers
* new version schema was failing to build in gitlab-ci
* correct install file name

### Refactor

* change the DoCallAuth to modify the request in order to see when auth is need it


<a name="047"></a>
## [047](https://gitlab.com/pantacor/pvr/compare/046...047) (2026-01-26)

### Docs

* add Ingress and Recovery examples and support granular variables
* add IPAM networking examples to PVR_TEMPLATES.md
* keep PVR_TEMPLATES.md as base reference (move examples to feature branches)
* add detailed examples for ingress, recovery and networking
* reference PVR_TEMPLATES.md in README.md
* add PVR_TEMPLATES.md documenting built-in template variables

### Feat

* **templates:** add PV_NETWORK_* args for IPAM pool networking

### Feature

* add authentication with pkce flow

### Fix

* docker image needs to use latest golang 1.21

### Templates

* update builtin-lxc-docker for ingress and recovery support


<a name="046"></a>
## [046](https://gitlab.com/pantacor/pvr/compare/045...046) (2025-11-04)

### Fix

* build breaks with any on older go versions


<a name="045"></a>
## [045](https://gitlab.com/pantacor/pvr/compare/044...045) (2025-11-04)

### Feature

* add support to get/merge/app update/fastcopy gitlabpackages
* **docs:** add information about how to use gitlab packages for pvr get and pvr merge

### Fix

* **logs:** page limit 500
* **post:** on post force make sure of reloading objects


<a name="044"></a>
## [044](https://gitlab.com/pantacor/pvr/compare/043...044) (2024-12-18)

### Repolib

* skip exporting dupes of objects to tar


<a name="043"></a>
## [043](https://gitlab.com/pantacor/pvr/compare/042...043) (2024-10-14)

### Feature

* delete device command


<a name="042"></a>
## [042](https://gitlab.com/pantacor/pvr/compare/040...042) (2024-10-09)

### Applib

* fix volumes split to only split the last :

### Feature

* GetRepo function will expand fragments with all files covert by signature
* **test:** add test for bignumbers

### Fix

* **statelib:** when patching states make sure of deleting previous signed elements
* **test:** correct the use of a folder object for testing


<a name="040"></a>
## [040](https://gitlab.com/pantacor/pvr/compare/039-rev003...040) (2024-07-18)


<a name="039-rev003"></a>
## [039-rev003](https://gitlab.com/pantacor/pvr/compare/039-rev002...039-rev003) (2024-07-17)


<a name="039-rev002"></a>
## [039-rev002](https://gitlab.com/pantacor/pvr/compare/039-rev001...039-rev002) (2024-07-16)


<a name="039-rev001"></a>
## [039-rev001](https://gitlab.com/pantacor/pvr/compare/039...039-rev001) (2024-07-12)

### Feature

* **container:** add vendor.tar.gz to artifacts


<a name="039"></a>
## [039](https://gitlab.com/pantacor/pvr/compare/038...039) (2024-06-28)

### Feature

* add objects relative to pvr.InitCustom for pvr init and pvr deploy
* make pvr binaries smaller with debugger include
* add fakeroot as subcommand it will use user namespaces on linux and fakeroot binary on darwin and windows
* add makefile to create binaries for debug and release, test and optimize with upx
* device token managment
* **app:** app remove will try to remove all the signed files of the app when delete the application
* **auth:** add support to user password via arguments and .netrc
* **sig:** add download command to download from URL using default URL
* **statelib:** delete app will check if files are already signed by another signature
* **testing:** add support to run specific test inside the _test/pvr.sh

### Fix

* makefile expose the goos goarch and goarm variables
* **auth:** login shortcut for interactive
* **auth:** getNewAccessToken methods need to be explicit
* **makefile:** remove go clean because deletes the _test folder

### Refactor

* -u and -p arguments mesages
* **app rm:** remove signature file aftger getting all the files to be deleted
* **auth:** Retry method numbers
* **statelib:** make clear why it should get signature from state


<a name="038"></a>
## [038](https://gitlab.com/pantacor/pvr/compare/041...038) (2024-04-29)


<a name="041"></a>
## [041](https://gitlab.com/pantacor/pvr/compare/041-rev001...041) (2024-04-17)


<a name="041-rev001"></a>
## [041-rev001](https://gitlab.com/pantacor/pvr/compare/037...041-rev001) (2024-04-17)

### Feature

* add objects relative to pvr.InitCustom for pvr init and pvr deploy
* make pvr binaries smaller with debugger include
* docker lib add support to oci manifest
* add fakeroot as subcommand it will use user namespaces on linux and fakeroot binary on darwin and windows
* app add --add-only this will add the container without installing it
* device token managment
* add makefile to create binaries for debug and release, test and optimize with upx
* **app:** app remove will try to remove all the signed files of the app when delete the application
* **app add:** add args-json to setup src.json args and config as parameters to set src.json config key
* **auth:** add support to user password via arguments and .netrc
* **cmd:** add version command with go build version
* **container:** add vendor.tar.gz to artifacts
* **device:** device creation return error if device exists and support pvr repo url
* **export:** make export builds reproducible by stardarize the timestamps and squashfs reproducibility
* **repolib:** move merge state logic to library function
* **self-upgrade:** self upgrade command always force update doesn't matter if installed_version have same digest
* **sig:** add download command to download from URL using default URL
* **statelib:** delete app will check if files are already signed by another signature
* **statelib:** on patch/commit remove signed parts before add new ones
* **steps:** move merge state logic to library function
* **testing:** add support to run specific test inside the _test/pvr.sh

### Fix

* pvr status show multiple changes for same device.
* use golang 1.21.5 to run test and build pvr
* pvr post --rev not working for non int strings
* makefile expose the goos goarch and goarm variables
* **app add:** app add need to add the app config to the src.json as the key config not docker_config
* **auth:** login shortcut for interactive
* **auth:** getNewAccessToken methods need to be explicit
* **ci:** change gitlab ci variables
* **ci:** push to tag branch
* **ci:** push to tag branch
* **cmd:** import dm as submodule
* **device:** device create with repo path, read correct usernick and devicenick
* **diff:** update json-patch to get right diffing and status
* **dockerlib:** use correct sha256 cache file when if cloning for local (removing the repository name)
* **jsonpatch:** update json-patch library to support big integer on json values
* **makefile:** remove go clean because deletes the _test folder
* **repolib:** solve problem with the upload of files with size 0
* **statelib:** convert ** into .* even when doesn't start with /
* **statelib:** remove glob and regex printing
* **statelib:** don't remove spec and signature content when adding new signature
* **statelib:** mark package as signed when signed parts are present
* **statelib:** normal merge should only remove and add again the frags in the patch json or the src removal frags
* **statelib:** use jsondiff and patch to merge two json instead of jsonpatch
* **test:** removing signature doesn't delete all the parts of package only the signature

### Refactor

* -u and -p arguments mesages
* **app rm:** remove signature file aftger getting all the files to be deleted
* **auth:** Retry method numbers
* **cmd:** refactor commands structure to have submodules for subcommands
* **statelib:** make clear why it should get signature from state


<a name="037"></a>
## [037](https://gitlab.com/pantacor/pvr/compare/036...037) (2023-02-20)

### Fix

* **repolib:** solve problem with the upload of files with size 0


<a name="036"></a>
## [036](https://gitlab.com/pantacor/pvr/compare/035...036) (2023-02-17)

### Feat

* **app update:** make posible to run RepoGet without merge it to the pristine state

### Feature

* **app:** add --base parameter to app update and app install
* **cli:** create a patch from another app
* **cli:** add cmd argument for patch updates
* **lib:** add MkTreeDiff two create diff between two rootfs
* **utils:** add json unmarshal helper to decode big int inside json

### Fix

* **app:** on pvr app update use the correct appManifest instead of the one inside the app object
* **app update:** don't stop update flow when updatge a pv app without docker_name
* **app update:** correct run.json template for --patch and --base features
* **app update:** pvr app updates uses parts[0] from url instead of part
* **dockerlib:** When adding an app search all the possible sources and ignore error until check all the sources
* **lib:** pvr merge and pvr get weren't updating pristine
* **manifest:** get manifest doesn't load default docker sources


<a name="035"></a>
## [035](https://gitlab.com/pantacor/pvr/compare/034...035) (2022-11-11)

### Fix

* GetWorkingJsonMap using json.Unmarshall without reference


<a name="034"></a>
## [034](https://gitlab.com/pantacor/pvr/compare/033...034) (2022-08-26)


<a name="033"></a>
## [033](https://gitlab.com/pantacor/pvr/compare/032...033) (2022-08-03)


<a name="032"></a>
## [032](https://gitlab.com/pantacor/pvr/compare/031...032) (2022-07-29)

### Feat

* **sign:** download certificates from gitlab if .pvr/pvs folder is empty

### Fix

* **debug:** use resty to upload objects in order to be able to trace those api calls
* **repolib:** upload progress bar uses normal http client unless the resty debug is set
* **signatures:** typo on download default signatures message

### Refactor

* **signature:** add function to load signatures from config folder


<a name="031"></a>
## [031](https://gitlab.com/pantacor/pvr/compare/030...031) (2022-03-15)


<a name="030"></a>
## [030](https://gitlab.com/pantacor/pvr/compare/029...030) (2022-03-03)


<a name="029"></a>
## [029](https://gitlab.com/pantacor/pvr/compare/028...029) (2022-01-20)

### Refactor

* **libpvr:** Solve all linter errors inside libpvr


<a name="028"></a>
## [028](https://gitlab.com/pantacor/pvr/compare/027...028) (2021-12-16)


<a name="027"></a>
## [027](https://gitlab.com/pantacor/pvr/compare/026...027) (2021-12-14)

### Feature

* **app:** Add support two new source type for applications in PVR (pvr, rootfs)


<a name="026"></a>
## [026](https://gitlab.com/pantacor/pvr/compare/025...026) (2021-11-02)


<a name="025"></a>
## [025](https://gitlab.com/pantacor/pvr/compare/024...025) (2021-08-24)


<a name="024"></a>
## [024](https://gitlab.com/pantacor/pvr/compare/023...024) (2021-08-23)


<a name="023"></a>
## [023](https://gitlab.com/pantacor/pvr/compare/022...023) (2021-07-29)


<a name="022"></a>
## [022](https://gitlab.com/pantacor/pvr/compare/021...022) (2021-07-29)


<a name="021"></a>
## [021](https://gitlab.com/pantacor/pvr/compare/020...021) (2021-07-15)


<a name="020"></a>
## [020](https://gitlab.com/pantacor/pvr/compare/019...020) (2021-07-02)


<a name="019"></a>
## [019](https://gitlab.com/pantacor/pvr/compare/018...019) (2021-06-11)

### Feature

* **auth:** Try always to do auth by using anonimous user, if doesn't work...


<a name="018"></a>
## [018](https://gitlab.com/pantacor/pvr/compare/017...018) (2021-05-12)


<a name="017"></a>
## [017](https://gitlab.com/pantacor/pvr/compare/016...017) (2021-03-05)


<a name="016"></a>
## [016](https://gitlab.com/pantacor/pvr/compare/015...016) (2021-01-15)


<a name="015"></a>
## [015](https://gitlab.com/pantacor/pvr/compare/014...015) (2021-01-12)


<a name="014"></a>
## [014](https://gitlab.com/pantacor/pvr/compare/013...014) (2020-11-13)


<a name="013"></a>
## [013](https://gitlab.com/pantacor/pvr/compare/012...013) (2020-09-02)


<a name="012"></a>
## [012](https://gitlab.com/pantacor/pvr/compare/011...012) (2020-06-15)


<a name="011"></a>
## [011](https://gitlab.com/pantacor/pvr/compare/010...011) (2020-06-08)

### Fix

* pvr device get command is not able to get other users public device details

### Logs

* add optional --rev parameter which limits logs to a given revision


<a name="010"></a>
## [010](https://gitlab.com/pantacor/pvr/compare/009...010) (2020-01-13)


<a name="009"></a>
## [009](https://gitlab.com/pantacor/pvr/compare/008...009) (2019-10-11)


<a name="008"></a>
## [008](https://gitlab.com/pantacor/pvr/compare/007...008) (2019-06-28)


<a name="007"></a>
## [007](https://gitlab.com/pantacor/pvr/compare/007-rc2...007) (2019-06-21)


<a name="007-rc2"></a>
## [007-rc2](https://gitlab.com/pantacor/pvr/compare/006...007-rc2) (2019-06-20)

### Fix

* Go routines receiving reference where downloading same layer always


<a name="006"></a>
## [006](https://gitlab.com/pantacor/pvr/compare/006-rc3...006) (2018-12-12)


<a name="006-rc3"></a>
## [006-rc3](https://gitlab.com/pantacor/pvr/compare/006-rc2...006-rc3) (2018-12-12)


<a name="006-rc2"></a>
## [006-rc2](https://gitlab.com/pantacor/pvr/compare/006-rc1...006-rc2) (2018-12-11)


<a name="006-rc1"></a>
## [006-rc1](https://gitlab.com/pantacor/pvr/compare/005...006-rc1) (2018-12-02)


<a name="005"></a>
## [005](https://gitlab.com/pantacor/pvr/compare/004...005) (2018-09-10)


<a name="004"></a>
## [004](https://gitlab.com/pantacor/pvr/compare/004-rc5...004) (2018-05-28)


<a name="004-rc5"></a>
## [004-rc5](https://gitlab.com/pantacor/pvr/compare/004-rc4...004-rc5) (2018-05-28)


<a name="004-rc4"></a>
## [004-rc4](https://gitlab.com/pantacor/pvr/compare/004-rc3...004-rc4) (2018-05-27)


<a name="004-rc3"></a>
## [004-rc3](https://gitlab.com/pantacor/pvr/compare/003...004-rc3) (2018-05-27)


<a name="003"></a>
## [003](https://gitlab.com/pantacor/pvr/compare/002...003) (2018-05-09)


<a name="002"></a>
## [002](https://gitlab.com/pantacor/pvr/compare/001...002) (2017-09-30)


<a name="001"></a>
## 001 (2017-06-19)

