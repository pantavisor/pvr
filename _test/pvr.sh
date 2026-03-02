#!/bin/sh

set -e
. $(dirname $0)/helpers

# Default test image from GitLab registry (replaces alpine:latest)
TEST_IMAGE_DEFAULT="registry.gitlab.com/pantacor/pantavisor-web-status:master"
# Test images with specific tags (replaces alpine:3.19 and alpine:3.18)
TEST_IMAGE_TAG1="registry.gitlab.com/pantacor/pv-platforms/pvr-sdk:arm64v8"
TEST_IMAGE_TAG2="registry.gitlab.com/pantacor/pv-platforms/pvr-sdk:amd64"

## Update pvr-sdk with a new package that doesn't have been _dm
## but because is signed the state should remove first the _dm folder and the
## root.hash file.
test_package_update_with_less_files() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects
    $pvr import $dir/mocks/data/initial_state_01.tar.gz >/dev/null
    $pvr checkout >/dev/null 2>&1

    $pvr merge $dir/mocks/data/pvr-sdk_no_dm.tar.gz >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1
    $pvr commit

    result="$(cat $dir/results/.pvr/json | jq -c)"
    expected="$(cat $dir/expected/test_package_update_with_less_files.json | jq -c)"

    assertEquals "$expected" "$result"
}

test_pvr_support_big_numbers() {
    clean_results
    cd $dir/mocks/data/big_number/
    tar c json | gzip >/tmp/big_numbers.tar.gz

    cd $dir/results/

    $pvr init --objects $dir/objects
    $pvr import /tmp/big_numbers.tar.gz >/dev/null
    $pvr checkout >/dev/null 2>&1

    result="$(cat $dir/results/.pvr/json | jq -c)"
    expected="$(cat $dir/expected/test_big_numbers.json | jq -c)"

    assertEquals "$expected" "$result"

    $pvr export /tmp/exported_big_numbers.tgz
    cd /tmp/
    tar xzvf exported_big_numbers.tgz >/dev/null
    assertEquals "$expected" "$(cat json)"
}

## Remove the pv-avahi signature, this should remove the whole pv-avahi package
test_signature_removal() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects
    $pvr import $dir/mocks/data/initial_state_01.tar.gz >/dev/null
    $pvr checkout >/dev/null 2>&1

    rm -rf _sigs/pv-avahi.json
    $pvr add && $pvr commit >/dev/null 2>&1

    result="$(cat $dir/results/.pvr/json | jq -c)"
    expected="$(cat $dir/expected/test_signature_removal.json | jq -c)"

    assertEquals "$expected" "$result"
}

test_package_parts_selection() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects
    $pvr import $dir/mocks/data/initial_state_02.tar.gz >/dev/null
    $pvr checkout >/dev/null 2>&1

    $pvr merge $dir/mocks/data/initial_state_01.tar.gz#_sigs/pvr-sdk.json >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1

    cat $dir/results/.pvr/json | jq >/tmp/result.json
    cat $dir/expected/test_package_parts_selection.json | jq >/tmp/expected.json

    result=$(diff /tmp/result.json /tmp/expected.json)
    assertEquals "" "$result"
}

test_package_parts_selection_removal() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects
    $pvr import $dir/mocks/data/initial_state_02.tar.gz >/dev/null
    $pvr checkout >/dev/null 2>&1

    $pvr merge $dir/mocks/data/initial_state_01.tar.gz#-pv-avahi >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1

    result="$(cat $dir/results/.pvr/json | jq -c)"
    expected="$(cat $dir/expected/test_package_parts_selection_removal.json | jq -c)"

    assertEquals "$expected" "$result"
}

test_deploy_selecting_parts() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects
    $pvr merge $dir/mocks/data/initial_state_02.tar.gz#-_sigs/nginx-config.json,-_config/nginx >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1

    cd $dir

    $pvr deploy $dir/results \
        $dir/mocks/data/initial_state_01.tar.gz#bsp,_sigs/bsp.json,device.json \
        $dir/mocks/data/pvwebstatus.tar.gz >/dev/null 2>&1

    result="$(cat $dir/results/.pvr/json | jq -c)"
    expected="$(cat $dir/expected/test_deploy_selecting_parts.json | jq -c)"

    assertEquals "$expected" "$result"
}

test_deploy_unselecting_parts() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects
    $pvr merge $dir/mocks/data/initial_state_02.tar.gz#-_sigs/nginx-config.json,-_config/nginx >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1

    cd $dir

    $pvr deploy $dir/results \
        $dir/mocks/data/initial_state_01.tar.gz#-bsp,-_sigs/bsp.json,-device.json \
        $dir/mocks/data/pvwebstatus.tar.gz >/dev/null 2>&1

    result="$(cat $dir/results/.pvr/json | jq -c)"
    expected="$(cat $dir/expected/test_deploy_unselecting_parts.json | jq -c)"

    assertEquals "$expected" "$result"
}

test_adding_signature_to_existing_package() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects
    $pvr merge $dir/mocks/data/pvwebstatus.tar.gz#pvwebstatus >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1

    $pvr sig add --noconfig --part pvwebstatus
    $pvr add && $pvr commit
    cd $dir

    cat $dir/results/.pvr/json | jq >/tmp/result.json
    cat $dir/expected/test_adding_signature_to_existing_package.json | jq >/tmp/expected.json

    diffing=$(diff /tmp/result.json /tmp/expected.json)
    assertEquals "" "$diffing"
}

## Always generate a exported tar with the same sha256sum if the content is the same
## make sure updated rootfs has the same sha256sum to been identified as same object
test_reproducible_exports() {
    clean_results
    cd $dir/results/

    mkdir device
    cd device

    $pvr init --objects $dir/objects
    $pvr merge $dir/mocks/data/pvwebstatus.tar.gz >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1

    rm pvwebstatus/root.squashfs* && pvr app update pvwebstatus >/dev/null 2>&1

    $pvr sig add --noconfig --part pvwebstatus
    $pvr add && $pvr commit >/dev/null 2>&1 && $pvr export $dir/results/exported.1.tgz

    cd $dir/results/

    mkdir device2
    cd device2

    $pvr init --objects $dir/objects
    $pvr merge $dir/mocks/data/pvwebstatus.tar.gz >/dev/null 2>&1

    $pvr checkout >/dev/null 2>&1

    rm pvwebstatus/root.squashfs* && pvr app update pvwebstatus >/dev/null 2>&1

    $pvr sig add --noconfig --part pvwebstatus
    $pvr add && $pvr commit >/dev/null 2>&1 && $pvr export $dir/results/exported.2.tgz

    checksum1="$(sha256sum $dir/results/exported.1.tgz | awk '{print $1}')"
    checksum2="$(sha256sum $dir/results/exported.2.tgz | awk '{print $1}')"

    assertEquals "$checksum1" "$checksum2"
}

test_remove_app_with_signature() {
    clean_results

    cd $dir/results/

    mkdir device
    cd device

    $pvr init --objects $dir/objects
    $pvr get $dir/mocks/data/pvwebstatus.tar.gz >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1

    $pvr add && $pvr commit >/dev/null 2>&1
    $pvr sig add --noconfig --part pvwebstatus
    $pvr add && $pvr commit >/dev/null 2>&1
    $pvr sig ls >/dev/null 2>&1

    $pvr app rm pvwebstatus >/dev/null 2>&1
    $pvr add && $pvr commit >/dev/null 2>&1

    result="$($pvr json | jq)"
    expected="$(cat $dir/expected/test_remove_app_with_signature.json | jq)"

    assertEquals "$expected" "$result"
}

test_remove_part_already_signed() {
    clean_results

    cd $dir/results/

    mkdir device
    cd device

    $pvr init --objects $dir/objects
    $pvr get $dir/mocks/data/package_config_a.tgz >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1
    $pvr add && $pvr commit >/dev/null 2>&1

    $pvr get $dir/mocks/data/package_config_b.tgz >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1
    $pvr add && $pvr commit >/dev/null 2>&1

    # $pvr json | jq  > /tmp/result.json
    # cat $dir/expected/test_remove_part_already_signed_step1.json | jq > /tmp/expected.json

    # diff /tmp/result.json /tmp/expected.json

    $pvr app rm nginx >/dev/null 2>&1
    $pvr add >/dev/null 2>&1
    $pvr commit >/dev/null 2>&1

    result="$($pvr json | jq)"
    expected="$(cat $dir/expected/test_remove_part_already_signed_step2.json | jq)"

    assertEquals "$expected" "$result"
}

test_update_signature_remove_garbage() {
    clean_results

    cd $dir/mocks/data/removing_previous_sign_a
    rm -rf .pvr
    $pvr init --objects $dir/objects >/dev/null 2>&1
    $pvr add >/dev/null 2>&1
    $pvr commit >/dev/null 2>&1
    $pvr export /tmp/removing_previous_sign_a.tgz

    cd $dir/mocks/data/removing_previous_sign_b
    rm -rf .pvr
    $pvr init --objects $dir/objects >/dev/null 2>&1
    $pvr add >/dev/null 2>&1
    $pvr commit >/dev/null 2>&1
    $pvr export /tmp/removing_previous_sign_b.tgz

    cd $dir/results/
    mkdir device
    cd device

    $pvr init --objects $dir/objects
    $pvr get /tmp/removing_previous_sign_a.tgz >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1
    $pvr get /tmp/removing_previous_sign_b.tgz >/dev/null 2>&1
    $pvr checkout >/dev/null 2>&1
    $pvr json | jq >/tmp/result.json
    cat $dir/expected/test_pdate_signature_remove_gc.json | jq >/tmp/expected.json

    diffing=$(diff /tmp/result.json /tmp/expected.json)
    assertEquals "" "$diffing"
}

## Test pvr app add from local and remote docker registry sources
test_app_add_from_docker_sources() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Configure the platform using _hostconfig/pvr/docker.json
    mkdir -p _hostconfig/pvr
    echo "{\"platforms\": [\"$HOST_PLATFORM\"]}" >_hostconfig/pvr/docker.json
    $pvr add .
    $pvr commit

    # Local source test: ensure image is available locally (from cache or pull)
    ensure_docker_image "registry.gitlab.com/pantacor/pantavisor-web-status:master"
    $pvr app add --from registry.gitlab.com/pantacor/pantavisor-web-status:master webstatus-local 2>&1 || echo "DEBUG: pvr app add exit code: $?"
    test -d webstatus-local
    assertEquals "0" "$?"

    platform_local=$(cat webstatus-local/src.json | jq -r .docker_platform)
    assertNotNull "docker_platform should be set" "$platform_local"
    assertEquals "$HOST_PLATFORM" "$platform_local"

    # Remote source test: add from remote
    $pvr app add --from registry.gitlab.com/pantacor/pantavisor-web-status:master --source remote webstatus-remote >/dev/null 2>&1
    test -d webstatus-remote
    assertEquals "0" "$?"

    platform_remote=$(cat webstatus-remote/src.json | jq -r .docker_platform)
    assertNotNull "docker_platform should be set" "$platform_remote"
    assertEquals "$HOST_PLATFORM" "$platform_remote"
}

## Test pvr app add with specific platform
test_app_add_with_platform() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add host platform version (use remote,local to allow pulling platform-specific variant)
    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-host 2>&1 || echo "DEBUG: pvr app add exit code: $?"
    test -d webstatus-host
    assertEquals "0" "$?"

    platform_host=$(cat webstatus-host/src.json | jq -r .docker_platform)
    assertEquals "$HOST_PLATFORM" "$platform_host"

    # Add other platform version (needs remote for cross-arch image)
    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $OTHER_PLATFORM webstatus-other >/dev/null 2>&1
    test -d webstatus-other
    assertEquals "0" "$?"

    platform_other=$(cat webstatus-other/src.json | jq -r .docker_platform)
    assertEquals "$OTHER_PLATFORM" "$platform_other"

    # Verify both apps have a digest set
    digest_host=$(cat webstatus-host/src.json | jq -r .docker_digest)
    digest_other=$(cat webstatus-other/src.json | jq -r .docker_digest)

    assertNotNull "host platform digest should be set" "$digest_host"
    assertNotNull "other platform digest should be set" "$digest_other"
}

## Test pvr app add without platform or hostconfig
test_app_add_without_platform_or_hostconfig() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Ensure no _hostconfig/pvr/docker.json exists
    rm -f _hostconfig/pvr/docker.json

    # Add app without specifying platform (use remote,local to allow pulling platform-specific variant)
    $pvr app add --from $TEST_IMAGE_DEFAULT webstatus-no-platform >/dev/null 2>&1
    test -d webstatus-no-platform
    assertEquals "0" "$?"

    # Without --platform or hostconfig, pvr defaults to the host architecture
    platform_default=$(cat webstatus-no-platform/src.json | jq -r .docker_platform)
    assertEquals "$HOST_PLATFORM" "$platform_default"
}

## Test pvr app update preserves and correctly handles docker_platform, docker_name, docker_tag
## This test verifies pvr reads values from src.json without passing any override flags
test_app_update_preserves_docker_info() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app with specific platform (multi-arch image, use remote,local to allow pulling)
    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-test >/dev/null 2>&1
    test -d webstatus-test
    assertEquals "0" "$?"

    # Verify initial docker_platform, docker_name, docker_tag, docker_source are set
    initial_platform=$(cat webstatus-test/src.json | jq -r .docker_platform)
    initial_name=$(cat webstatus-test/src.json | jq -r .docker_name)
    initial_tag=$(cat webstatus-test/src.json | jq -r .docker_tag)
    initial_digest=$(cat webstatus-test/src.json | jq -r .docker_digest)
    initial_source=$(cat webstatus-test/src.json | jq -r .docker_source)

    assertEquals "$HOST_PLATFORM" "$initial_platform"
    assertNotNull "docker_name should be set" "$initial_name"
    assertNotNull "docker_tag should be set" "$initial_tag"
    assertNotNull "docker_digest should be set" "$initial_digest"

    echo "Initial: platform=$initial_platform name=$initial_name tag=$initial_tag source=$initial_source"

    # Run pvr app update WITHOUT any flags - pvr should read all values from src.json
    $pvr app update webstatus-test >/dev/null 2>&1
    assertEquals "0" "$?"

    # Verify docker_platform, docker_name, docker_tag are preserved after update
    updated_platform=$(cat webstatus-test/src.json | jq -r .docker_platform)
    updated_name=$(cat webstatus-test/src.json | jq -r .docker_name)
    updated_tag=$(cat webstatus-test/src.json | jq -r .docker_tag)
    updated_digest=$(cat webstatus-test/src.json | jq -r .docker_digest)

    echo "Updated: platform=$updated_platform name=$updated_name tag=$updated_tag"

    assertEquals "docker_platform should be preserved" "$initial_platform" "$updated_platform"
    assertEquals "docker_name should be preserved" "$initial_name" "$updated_name"
    assertEquals "docker_tag should be preserved" "$initial_tag" "$updated_tag"
    assertNotNull "docker_digest should still be set" "$updated_digest"
}

## Test pvr app update with single-arch image (non multi-arch)
## Uses registry.gitlab.com/pantacor/pv-platforms/pvr-sdk:arm64v8 which is a single-arch image
test_app_update_single_arch_image() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app from single-arch image (arm64v8 only, no multi-arch manifest)
    $pvr app add --from registry.gitlab.com/pantacor/pv-platforms/pvr-sdk:arm64v8 pvr-sdk-arm64 >/dev/null 2>&1
    test -d pvr-sdk-arm64
    assertEquals "0" "$?"

    # Verify initial docker_platform, docker_name, docker_tag are set
    initial_platform=$(cat pvr-sdk-arm64/src.json | jq -r .docker_platform)
    initial_name=$(cat pvr-sdk-arm64/src.json | jq -r .docker_name)
    initial_tag=$(cat pvr-sdk-arm64/src.json | jq -r .docker_tag)
    initial_digest=$(cat pvr-sdk-arm64/src.json | jq -r .docker_digest)
    initial_source=$(cat pvr-sdk-arm64/src.json | jq -r .docker_source)

    assertNotNull "docker_platform should be set for single-arch image" "$initial_platform"
    assertEquals "registry.gitlab.com/pantacor/pv-platforms/pvr-sdk" "$initial_name"
    assertEquals "arm64v8" "$initial_tag"
    assertNotNull "docker_digest should be set" "$initial_digest"

    echo "Initial single-arch: platform=$initial_platform name=$initial_name tag=$initial_tag source=$initial_source"

    # Run pvr app update WITHOUT any flags - pvr should read all values from src.json
    $pvr app update pvr-sdk-arm64 >/dev/null 2>&1
    assertEquals "0" "$?"

    # Verify docker_platform, docker_name, docker_tag are preserved after update
    updated_platform=$(cat pvr-sdk-arm64/src.json | jq -r .docker_platform)
    updated_name=$(cat pvr-sdk-arm64/src.json | jq -r .docker_name)
    updated_tag=$(cat pvr-sdk-arm64/src.json | jq -r .docker_tag)
    updated_digest=$(cat pvr-sdk-arm64/src.json | jq -r .docker_digest)

    echo "Updated single-arch: platform=$updated_platform name=$updated_name tag=$updated_tag"

    assertEquals "docker_platform should be preserved for single-arch" "$initial_platform" "$updated_platform"
    assertEquals "docker_name should be preserved" "$initial_name" "$updated_name"
    assertEquals "docker_tag should be preserved" "$initial_tag" "$updated_tag"
    assertNotNull "docker_digest should still be set" "$updated_digest"
}

## Test pvr app update with --platform flag overwrites docker_platform in src.json
test_app_update_with_platform_override() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app with host platform initially (use remote,local to allow pulling platform-specific variant)
    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-platform-test >/dev/null 2>&1
    test -d webstatus-platform-test
    assertEquals "0" "$?"

    # Verify initial platform matches host
    initial_platform=$(cat webstatus-platform-test/src.json | jq -r .docker_platform)
    assertEquals "$HOST_PLATFORM" "$initial_platform"

    echo "Initial platform: $initial_platform"

    # Update with --platform to the other arch - should overwrite docker_platform in src.json
    $pvr app update webstatus-platform-test --platform $OTHER_PLATFORM >/dev/null 2>&1
    assertEquals "0" "$?"

    # Verify docker_platform was overwritten in src.json
    updated_platform=$(cat webstatus-platform-test/src.json | jq -r .docker_platform)
    assertEquals "docker_platform should be overwritten" "$OTHER_PLATFORM" "$updated_platform"

    echo "Updated platform: $updated_platform"

    # Verify docker_name and docker_tag are still preserved
    updated_name=$(cat webstatus-platform-test/src.json | jq -r .docker_name)
    updated_tag=$(cat webstatus-platform-test/src.json | jq -r .docker_tag)

    assertNotNull "docker_name should still be set" "$updated_name"
    assertNotNull "docker_tag should be preserved" "$updated_tag"
}

## Test pvr app update reads and preserves docker_source from src.json
test_app_update_preserves_docker_source() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app with local source (use remote,local to allow pulling platform-specific variant)
    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-source-test >/dev/null 2>&1
    test -d webstatus-source-test
    assertEquals "0" "$?"

    # Verify initial values are set including docker_source
    initial_platform=$(cat webstatus-source-test/src.json | jq -r .docker_platform)
    initial_name=$(cat webstatus-source-test/src.json | jq -r .docker_name)
    initial_tag=$(cat webstatus-source-test/src.json | jq -r .docker_tag)
    initial_source=$(cat webstatus-source-test/src.json | jq -r .docker_source)

    assertEquals "$HOST_PLATFORM" "$initial_platform"
    assertNotNull "docker_name should be set" "$initial_name"
    assertNotNull "docker_tag should be set" "$initial_tag"
    assertNotNull "docker_source should be set" "$initial_source"

    echo "Initial: platform=$initial_platform name=$initial_name tag=$initial_tag source=$initial_source"

    # Update WITHOUT any flags - pvr should read docker_source from src.json
    $pvr app update webstatus-source-test >/dev/null 2>&1
    assertEquals "0" "$?"

    # Verify values are preserved
    updated_platform=$(cat webstatus-source-test/src.json | jq -r .docker_platform)
    updated_name=$(cat webstatus-source-test/src.json | jq -r .docker_name)
    updated_tag=$(cat webstatus-source-test/src.json | jq -r .docker_tag)
    updated_source=$(cat webstatus-source-test/src.json | jq -r .docker_source)

    echo "Updated: platform=$updated_platform name=$updated_name tag=$updated_tag source=$updated_source"

    assertEquals "docker_platform should be preserved" "$initial_platform" "$updated_platform"
    assertEquals "docker_name should be preserved" "$initial_name" "$updated_name"
    assertEquals "docker_tag should be preserved" "$initial_tag" "$updated_tag"
    assertEquals "docker_source should be preserved" "$initial_source" "$updated_source"
}

## Test pvr app update with --from flag overwrites docker_name and docker_tag in src.json
test_app_update_with_from_override() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app with initial image (pvr-sdk:arm64v8)
    $pvr app add --from $TEST_IMAGE_TAG1 --platform $HOST_PLATFORM pvr-sdk-from-test >/dev/null 2>&1
    test -d pvr-sdk-from-test
    assertEquals "0" "$?"

    # Verify initial values
    initial_name=$(cat pvr-sdk-from-test/src.json | jq -r .docker_name)
    initial_tag=$(cat pvr-sdk-from-test/src.json | jq -r .docker_tag)
    assertNotNull "docker_name should be set" "$initial_name"
    assertEquals "arm64v8" "$initial_tag"

    echo "Initial: name=$initial_name tag=$initial_tag"

    # Update with --from to overwrite docker_name and docker_tag in src.json (switch to amd64)
    $pvr app update pvr-sdk-from-test --from $TEST_IMAGE_TAG2 >/dev/null 2>&1
    assertEquals "0" "$?"

    # Verify docker_name and docker_tag were overwritten in src.json
    updated_name=$(cat pvr-sdk-from-test/src.json | jq -r .docker_name)
    updated_tag=$(cat pvr-sdk-from-test/src.json | jq -r .docker_tag)

    echo "Updated: name=$updated_name tag=$updated_tag"

    assertEquals "docker_name should be preserved (same image)" "$initial_name" "$updated_name"
    assertEquals "docker_tag should be overwritten to amd64" "amd64" "$updated_tag"

    # Verify docker_platform is still set
    updated_platform=$(cat pvr-sdk-from-test/src.json | jq -r .docker_platform)
    assertNotNull "docker_platform should still be set" "$updated_platform"
}

## Test pvr app update with --source flag overwrites docker_source in src.json
test_app_update_with_source_override() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app with default source (from PVR_SOURCE env)
    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-source-override >/dev/null 2>&1
    test -d webstatus-source-override
    assertEquals "0" "$?"

    # Verify initial docker_source is set (value depends on PVR_SOURCE env)
    initial_source=$(cat webstatus-source-override/src.json | jq -r .docker_source)
    assertNotNull "docker_source should be set" "$initial_source"

    echo "Initial source: $initial_source"

    # Update with --source to change the source order (remote,local is the new value)
    $pvr app update webstatus-source-override --source remote,local >/dev/null 2>&1
    assertEquals "0" "$?"

    # Verify docker_source was overwritten in src.json
    updated_source=$(cat webstatus-source-override/src.json | jq -r .docker_source)
    assertEquals "docker_source should be overwritten" "remote,local" "$updated_source"

    echo "Updated source: $updated_source"

    # Verify docker_platform, docker_name, docker_tag are preserved
    updated_platform=$(cat webstatus-source-override/src.json | jq -r .docker_platform)
    updated_name=$(cat webstatus-source-override/src.json | jq -r .docker_name)
    updated_tag=$(cat webstatus-source-override/src.json | jq -r .docker_tag)

    assertEquals "docker_platform should be preserved" "$HOST_PLATFORM" "$updated_platform"
    assertNotNull "docker_name should still be set" "$updated_name"
    assertNotNull "docker_tag should be preserved" "$updated_tag"
}

## Test that pvr generates root.squashfs from docker layers (remote source) and
## produces the same sha256 when generated from the same image twice.
## Also verifies root.squashfs.docker-digest matches docker_digest in src.json.
test_squashfs_generation_from_layers_is_reproducible() {
    clean_results
    cd $dir/results/

    mkdir device1
    cd device1

    $pvr init --objects $dir/objects

    # Add an app from a docker image — this pulls layers and generates root.squashfs
    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-squash 2>&1
    assertEquals "pvr app add should succeed (device1)" "0" "$?"

    # Verify root.squashfs was actually generated
    test -f webstatus-squash/root.squashfs
    assertEquals "root.squashfs should exist (device1)" "0" "$?"

    # Verify docker-digest file was generated and matches src.json
    test -f webstatus-squash/root.squashfs.docker-digest
    assertEquals "root.squashfs.docker-digest should exist (device1)" "0" "$?"

    digest_file1="$(cat webstatus-squash/root.squashfs.docker-digest)"
    src_digest1="$(cat webstatus-squash/src.json | jq -r .docker_digest)"
    assertEquals "docker-digest file should match src.json docker_digest (device1)" "$src_digest1" "$digest_file1"

    squashfs_sha1="$(sha256sum webstatus-squash/root.squashfs | awk '{print $1}')"
    assertNotNull "root.squashfs sha should not be empty (device1)" "$squashfs_sha1"

    # Now do the same in a second directory
    cd $dir/results/

    mkdir device2
    cd device2

    $pvr init --objects $dir/objects

    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-squash 2>&1
    assertEquals "pvr app add should succeed (device2)" "0" "$?"

    test -f webstatus-squash/root.squashfs
    assertEquals "root.squashfs should exist (device2)" "0" "$?"

    # Verify docker-digest file was generated and matches src.json
    test -f webstatus-squash/root.squashfs.docker-digest
    assertEquals "root.squashfs.docker-digest should exist (device2)" "0" "$?"

    digest_file2="$(cat webstatus-squash/root.squashfs.docker-digest)"
    src_digest2="$(cat webstatus-squash/src.json | jq -r .docker_digest)"
    assertEquals "docker-digest file should match src.json docker_digest (device2)" "$src_digest2" "$digest_file2"

    squashfs_sha2="$(sha256sum webstatus-squash/root.squashfs | awk '{print $1}')"
    assertNotNull "root.squashfs sha should not be empty (device2)" "$squashfs_sha2"

    # The same image layers should produce the exact same squashfs
    assertEquals "root.squashfs sha should match between both generations" "$squashfs_sha1" "$squashfs_sha2"
}

## Test that pvr generates root.squashfs from local docker layers and
## produces the same sha256 when generated from the same image twice.
## Also verifies root.squashfs.docker-digest matches docker_digest in src.json.
test_squashfs_generation_from_local_layers_is_reproducible() {
    clean_results
    cd $dir/results/

    # Ensure image is available locally (from cache or pull)
    ensure_docker_image "$TEST_IMAGE_DEFAULT"

    mkdir device1
    cd device1

    $pvr init --objects $dir/objects

    # Add app from local docker daemon
    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-squash 2>&1
    assertEquals "pvr app add should succeed (device1)" "0" "$?"

    # Verify root.squashfs was actually generated
    test -f webstatus-squash/root.squashfs
    assertEquals "root.squashfs should exist (device1)" "0" "$?"

    # Verify docker-digest file was generated and matches src.json
    test -f webstatus-squash/root.squashfs.docker-digest
    assertEquals "root.squashfs.docker-digest should exist (device1)" "0" "$?"

    digest_file1="$(cat webstatus-squash/root.squashfs.docker-digest)"
    src_digest1="$(cat webstatus-squash/src.json | jq -r .docker_digest)"
    assertEquals "docker-digest file should match src.json docker_digest (device1)" "$src_digest1" "$digest_file1"

    squashfs_sha1="$(sha256sum webstatus-squash/root.squashfs | awk '{print $1}')"
    assertNotNull "root.squashfs sha should not be empty (device1)" "$squashfs_sha1"

    # Now do the same in a second directory
    cd $dir/results/

    mkdir device2
    cd device2

    $pvr init --objects $dir/objects

    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-squash 2>&1
    assertEquals "pvr app add should succeed (device2)" "0" "$?"

    test -f webstatus-squash/root.squashfs
    assertEquals "root.squashfs should exist (device2)" "0" "$?"

    # Verify docker-digest file was generated and matches src.json
    test -f webstatus-squash/root.squashfs.docker-digest
    assertEquals "root.squashfs.docker-digest should exist (device2)" "0" "$?"

    digest_file2="$(cat webstatus-squash/root.squashfs.docker-digest)"
    src_digest2="$(cat webstatus-squash/src.json | jq -r .docker_digest)"
    assertEquals "docker-digest file should match src.json docker_digest (device2)" "$src_digest2" "$digest_file2"

    squashfs_sha2="$(sha256sum webstatus-squash/root.squashfs | awk '{print $1}')"
    assertNotNull "root.squashfs sha should not be empty (device2)" "$squashfs_sha2"

    # The same local image layers should produce the exact same squashfs
    assertEquals "root.squashfs sha should match between both local generations" "$squashfs_sha1" "$squashfs_sha2"
}

## Test that pvr app update caches docker_config in src.json
## When src.json has no docker_config, update should fetch it and save it
test_app_update_caches_docker_config() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app (AddDockerApp caches docker_config)
    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-cache-test >/dev/null 2>&1
    test -d webstatus-cache-test
    assertEquals "0" "$?"

    # Verify docker_config is present after add
    docker_config_after_add=$(cat webstatus-cache-test/src.json | jq -r '.docker_config')
    assertNotEquals "docker_config should be set after add" "null" "$docker_config_after_add"

    # Remove docker_config from src.json to simulate a manually created or old src.json
    cat webstatus-cache-test/src.json | jq 'del(.docker_config)' >webstatus-cache-test/src.json.tmp
    mv webstatus-cache-test/src.json.tmp webstatus-cache-test/src.json

    # Verify docker_config is gone
    docker_config_removed=$(cat webstatus-cache-test/src.json | jq -r '.docker_config')
    assertEquals "docker_config should be removed" "null" "$docker_config_removed"

    # Run pvr app update - should fetch and cache docker_config
    $pvr app update webstatus-cache-test >/dev/null 2>&1
    assertEquals "pvr app update should succeed" "0" "$?"

    # Verify docker_config is now cached in src.json
    docker_config_after_update=$(cat webstatus-cache-test/src.json | jq -r '.docker_config')
    assertNotEquals "docker_config should be cached after update" "null" "$docker_config_after_update"
}

## Test that pvr app update does not emit the old "genrated by old pvr" warning
## when docker_config is missing from src.json
test_app_update_no_old_pvr_warning() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app
    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-warn-test >/dev/null 2>&1

    # Remove docker_config to trigger the fallback codepath in InstallDockerApp
    cat webstatus-warn-test/src.json | jq 'del(.docker_config)' >webstatus-warn-test/src.json.tmp
    mv webstatus-warn-test/src.json.tmp webstatus-warn-test/src.json

    # Remove squashfs so install actually runs the generation path
    rm -f webstatus-warn-test/root.squashfs webstatus-warn-test/root.squashfs.docker-digest

    # Run pvr app update and capture all output (stdout + stderr)
    output=$($pvr app update webstatus-warn-test 2>&1)

    # Verify the old misleading warning is NOT present
    echo "$output" | grep -q "genrated by old pvr" && fail "Should not emit 'genrated by old pvr' warning" || true
    echo "$output" | grep -q "WARNING: The src.json for" && fail "Should not emit old WARNING about src.json" || true
}

## Test that no group warning is emitted when groups.json does not exist
## even when PV_GROUP is set in src.json args
test_no_group_warning_without_groups_json() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app
    $pvr app add --from $TEST_IMAGE_DEFAULT --platform $HOST_PLATFORM webstatus-group-test >/dev/null 2>&1

    # Add PV_GROUP to args in src.json (simulating user's src.json with "PV_GROUP": "root")
    cat webstatus-group-test/src.json | jq '.args.PV_GROUP = "root"' >webstatus-group-test/src.json.tmp
    mv webstatus-group-test/src.json.tmp webstatus-group-test/src.json

    # Ensure no groups.json exists in the repo
    rm -f groups.json

    # Run pvr app update and capture stderr
    output=$($pvr app update webstatus-group-test 2>&1)

    # Verify the group warning is NOT present (no groups.json means warning should not fire)
    echo "$output" | grep -q "group does not exist in groups.json" && fail "Should not warn about group when groups.json does not exist" || true
}

## Test that pvr app add from different platforms (remote and local)
## produces a squashfs with the correct binary architecture.
test_app_architecture_validation() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Configure the platform using _hostconfig/pvr/docker.json
    mkdir -p _hostconfig/pvr
    echo "{\"platforms\": [\"$HOST_PLATFORM\"]}" >_hostconfig/pvr/docker.json
    $pvr add .
    $pvr commit

    local img="registry.gitlab.com/pantacor/pvwificonnect:main"

    # 1. Test arm64 app (remote source)
    $pvr app add --from "$img" --platform linux/arm64 --source remote app-arm64 >/dev/null 2>&1
    test -f app-arm64/root.squashfs
    assertEquals "root.squashfs should exist for app-arm64" "0" "$?"

    unsquashfs -d app-arm64-extracted app-arm64/root.squashfs >/dev/null 2>&1
    # pvwificonnect is at app/pvwificonnect
    local test_bin="app/pvwificonnect"

    arch_info=$(file "app-arm64-extracted/$test_bin")
    echo "$arch_info" | grep -qiE "aarch64|arm64"
    assertEquals "Binary ($test_bin) should be aarch64: $arch_info" "0" "$?"

    # 2. Test amd64 app (remote source)
    $pvr app add --from "$img" --platform linux/amd64 --source remote app-amd64 >/dev/null 2>&1
    test -f app-amd64/root.squashfs
    assertEquals "root.squashfs should exist for app-amd64" "0" "$?"

    unsquashfs -d app-amd64-extracted app-amd64/root.squashfs >/dev/null 2>&1
    test_bin="app/pvwificonnect"

    arch_info=$(file "app-amd64-extracted/$test_bin")
    echo "$arch_info" | grep -qiE "x86-64"
    assertEquals "Binary ($test_bin) should be x86-64: $arch_info" "0" "$?"

    # 3. Test host platform (local source)
    # Ensure image is available locally for local source test
    ensure_docker_image "$img" "$HOST_PLATFORM"

    $pvr app add --from "$img" --platform "$HOST_PLATFORM" --source local app-host-local >/dev/null 2>&1
    test -f app-host-local/root.squashfs
    assertEquals "root.squashfs should exist for app-host-local" "0" "$?"

    unsquashfs -d app-host-local-extracted app-host-local/root.squashfs >/dev/null 2>&1
    test_bin="app/pvwificonnect"

    arch_info=$(file "app-host-local-extracted/$test_bin")
    if [ "$HOST_PLATFORM" = "linux/arm64" ]; then
        echo "$arch_info" | grep -qiE "aarch64|arm64"
    else
        echo "$arch_info" | grep -qiE "x86-64"
    fi
    assertEquals "Binary ($test_bin) should match host arch ($HOST_PLATFORM): $arch_info" "0" "$?"
}

# Pre-cache docker images locally to avoid Docker Hub rate limits.
# shunit2 calls oneTimeSetUp() once before all tests.
oneTimeSetUp() {
    # Cache multi-arch images with specific platform variants
    ensure_docker_image "$TEST_IMAGE_DEFAULT" "$HOST_PLATFORM"
    ensure_docker_image "$TEST_IMAGE_DEFAULT" "$OTHER_PLATFORM"
    ensure_docker_image "$TEST_IMAGE_TAG1"
    ensure_docker_image "$TEST_IMAGE_TAG2"
    ensure_docker_image "registry.gitlab.com/pantacor/pvwificonnect:main" "linux/arm64"
    ensure_docker_image "registry.gitlab.com/pantacor/pvwificonnect:main" "linux/amd64"
}

# Load shUnit2.
. _test/lib/shunit2
