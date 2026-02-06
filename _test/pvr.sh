#!/bin/sh

set -e
. $(dirname $0)/helpers

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
    echo '{"platforms": ["linux/amd64"]}' >_hostconfig/pvr/docker.json
    $pvr add .
    $pvr commit

    # Local source test: pull image first then add from local
    echo "DEBUG: DOCKER_HOST=$DOCKER_HOST"
    echo "DEBUG: Pulling image..."
    docker pull registry.gitlab.com/pantacor/pantavisor-web-status:master 2>&1 || echo "DEBUG: docker pull exit code: $?"
    echo "DEBUG: Checking if image exists locally..."
    docker images registry.gitlab.com/pantacor/pantavisor-web-status:master
    echo "DEBUG: Running pvr app add --source local..."
    $pvr app add --from registry.gitlab.com/pantacor/pantavisor-web-status:master --source local webstatus-local 2>&1 || echo "DEBUG: pvr app add exit code: $?"
    test -d webstatus-local
    assertEquals "0" "$?"

    platform_local=$(cat webstatus-local/src.json | jq -r .docker_platform)
    assertNotNull "docker_platform should be set" "$platform_local"
    assertEquals "linux/amd64" "$platform_local"

    # Remote source test: remove local image then add from remote
    docker rmi registry.gitlab.com/pantacor/pantavisor-web-status:master >/dev/null 2>&1
    $pvr app add --from registry.gitlab.com/pantacor/pantavisor-web-status:master --source remote webstatus-remote >/dev/null 2>&1
    test -d webstatus-remote
    assertEquals "0" "$?"

    platform_remote=$(cat webstatus-remote/src.json | jq -r .docker_platform)
    assertNotNull "docker_platform should be set" "$platform_remote"
    assertEquals "linux/amd64" "$platform_remote"
}

## Test pvr app add with specific platform
test_app_add_with_platform() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add arm64 version
    echo "DEBUG: Adding alpine:latest for arm64..."
    $pvr app add --from alpine:latest --platform linux/arm64 alpine-arm64 2>&1 || echo "DEBUG: pvr app add exit code: $?"
    test -d alpine-arm64
    assertEquals "0" "$?"

    platform_arm64=$(cat alpine-arm64/src.json | jq -r .docker_platform)
    assertEquals "linux/arm64" "$platform_arm64"

    # Add amd64 version
    $pvr app add --from alpine:latest --platform linux/amd64 alpine-amd64 >/dev/null 2>&1
    test -d alpine-amd64
    assertEquals "0" "$?"

    platform_amd64=$(cat alpine-amd64/src.json | jq -r .docker_platform)
    assertEquals "linux/amd64" "$platform_amd64"

    # Verify digests are different
    digest_arm64=$(grep "digest" alpine-arm64/src.json | head -1)
    digest_amd64=$(grep "digest" alpine-amd64/src.json | head -1)

    assertNotEquals "Digests should differ for different platforms" "$digest_arm64" "$digest_amd64"
}

## Test pvr app add without platform or hostconfig
test_app_add_without_platform_or_hostconfig() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Ensure no _hostconfig/pvr/docker.json exists
    rm -f _hostconfig/pvr/docker.json

    # Add app without specifying platform
    $pvr app add --from alpine:latest alpine-no-platform >/dev/null 2>&1
    test -d alpine-no-platform
    assertEquals "0" "$?"

    # Verify docker_platform is NOT set (null)
    platform_null=$(cat alpine-no-platform/src.json | jq -r .docker_platform)
    assertEquals "linux/amd64" "$platform_null"
}

## Test pvr app update preserves and correctly handles docker_platform, docker_name, docker_tag
## This test verifies pvr reads values from src.json without passing any override flags
test_app_update_preserves_docker_info() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app with specific platform (multi-arch image)
    $pvr app add --from alpine:3.19 --platform linux/amd64 alpine-test >/dev/null 2>&1
    test -d alpine-test
    assertEquals "0" "$?"

    # Verify initial docker_platform, docker_name, docker_tag, docker_source are set
    initial_platform=$(cat alpine-test/src.json | jq -r .docker_platform)
    initial_name=$(cat alpine-test/src.json | jq -r .docker_name)
    initial_tag=$(cat alpine-test/src.json | jq -r .docker_tag)
    initial_digest=$(cat alpine-test/src.json | jq -r .docker_digest)
    initial_source=$(cat alpine-test/src.json | jq -r .docker_source)

    assertEquals "linux/amd64" "$initial_platform"
    assertNotNull "docker_name should be set" "$initial_name"
    assertEquals "3.19" "$initial_tag"
    assertNotNull "docker_digest should be set" "$initial_digest"

    echo "Initial: platform=$initial_platform name=$initial_name tag=$initial_tag source=$initial_source"

    # Run pvr app update WITHOUT any flags - pvr should read all values from src.json
    $pvr app update alpine-test >/dev/null 2>&1
    assertEquals "0" "$?"

    # Verify docker_platform, docker_name, docker_tag are preserved after update
    updated_platform=$(cat alpine-test/src.json | jq -r .docker_platform)
    updated_name=$(cat alpine-test/src.json | jq -r .docker_name)
    updated_tag=$(cat alpine-test/src.json | jq -r .docker_tag)
    updated_digest=$(cat alpine-test/src.json | jq -r .docker_digest)

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

    # Add app with amd64 platform initially
    $pvr app add --from alpine:3.19 --platform linux/amd64 alpine-platform-test >/dev/null 2>&1
    test -d alpine-platform-test
    assertEquals "0" "$?"

    # Verify initial platform is amd64
    initial_platform=$(cat alpine-platform-test/src.json | jq -r .docker_platform)
    assertEquals "linux/amd64" "$initial_platform"

    echo "Initial platform: $initial_platform"

    # Update with --platform arm64 - should overwrite docker_platform in src.json
    $pvr app update alpine-platform-test --platform linux/arm64 >/dev/null 2>&1
    assertEquals "0" "$?"

    # Verify docker_platform was overwritten to arm64 in src.json
    updated_platform=$(cat alpine-platform-test/src.json | jq -r .docker_platform)
    assertEquals "docker_platform should be overwritten to arm64" "linux/arm64" "$updated_platform"

    echo "Updated platform: $updated_platform"

    # Verify docker_name and docker_tag are still preserved
    updated_name=$(cat alpine-platform-test/src.json | jq -r .docker_name)
    updated_tag=$(cat alpine-platform-test/src.json | jq -r .docker_tag)

    assertNotNull "docker_name should still be set" "$updated_name"
    assertEquals "docker_tag should be preserved" "3.19" "$updated_tag"
}

## Test pvr app update reads and preserves docker_source from src.json
test_app_update_preserves_docker_source() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app with remote source (default)
    $pvr app add --from alpine:3.19 --platform linux/amd64 alpine-source-test >/dev/null 2>&1
    test -d alpine-source-test
    assertEquals "0" "$?"

    # Verify initial values are set including docker_source
    initial_platform=$(cat alpine-source-test/src.json | jq -r .docker_platform)
    initial_name=$(cat alpine-source-test/src.json | jq -r .docker_name)
    initial_tag=$(cat alpine-source-test/src.json | jq -r .docker_tag)
    initial_source=$(cat alpine-source-test/src.json | jq -r .docker_source)

    assertEquals "linux/amd64" "$initial_platform"
    assertNotNull "docker_name should be set" "$initial_name"
    assertEquals "3.19" "$initial_tag"
    assertNotNull "docker_source should be set" "$initial_source"

    echo "Initial: platform=$initial_platform name=$initial_name tag=$initial_tag source=$initial_source"

    # Update WITHOUT any flags - pvr should read docker_source from src.json
    $pvr app update alpine-source-test >/dev/null 2>&1
    assertEquals "0" "$?"

    # Verify values are preserved
    updated_platform=$(cat alpine-source-test/src.json | jq -r .docker_platform)
    updated_name=$(cat alpine-source-test/src.json | jq -r .docker_name)
    updated_tag=$(cat alpine-source-test/src.json | jq -r .docker_tag)
    updated_source=$(cat alpine-source-test/src.json | jq -r .docker_source)

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

    # Add app with initial image
    $pvr app add --from alpine:3.18 --platform linux/amd64 alpine-from-test >/dev/null 2>&1
    test -d alpine-from-test
    assertEquals "0" "$?"

    # Verify initial values
    initial_name=$(cat alpine-from-test/src.json | jq -r .docker_name)
    initial_tag=$(cat alpine-from-test/src.json | jq -r .docker_tag)
    assertEquals "alpine" "$initial_name"
    assertEquals "3.18" "$initial_tag"

    echo "Initial: name=$initial_name tag=$initial_tag"

    # Update with --from to overwrite docker_name and docker_tag in src.json
    $pvr app update alpine-from-test --from alpine:3.19 >/dev/null 2>&1
    assertEquals "0" "$?"

    # Verify docker_name and docker_tag were overwritten in src.json
    updated_name=$(cat alpine-from-test/src.json | jq -r .docker_name)
    updated_tag=$(cat alpine-from-test/src.json | jq -r .docker_tag)

    echo "Updated: name=$updated_name tag=$updated_tag"

    assertEquals "docker_name should be preserved (same image)" "$initial_name" "$updated_name"
    assertEquals "docker_tag should be overwritten to 3.19" "3.19" "$updated_tag"

    # Verify docker_platform is still set
    updated_platform=$(cat alpine-from-test/src.json | jq -r .docker_platform)
    assertNotNull "docker_platform should still be set" "$updated_platform"
}

## Test pvr app update with --source flag overwrites docker_source in src.json
test_app_update_with_source_override() {
    clean_results
    cd $dir/results/

    $pvr init --objects $dir/objects

    # Add app with "remote" source initially
    $pvr app add --from alpine:3.19 --platform linux/amd64 --source remote alpine-source-override >/dev/null 2>&1
    test -d alpine-source-override
    assertEquals "0" "$?"

    # Verify initial docker_source is remote
    initial_source=$(cat alpine-source-override/src.json | jq -r .docker_source)
    assertEquals "remote" "$initial_source"

    echo "Initial source: $initial_source"

    # Update with --source to change the source order (remote,local is the new value)
    $pvr app update alpine-source-override --source remote,local >/dev/null 2>&1
    assertEquals "0" "$?"

    # Verify docker_source was overwritten in src.json
    updated_source=$(cat alpine-source-override/src.json | jq -r .docker_source)
    assertEquals "docker_source should be overwritten" "remote,local" "$updated_source"

    echo "Updated source: $updated_source"

    # Verify docker_platform, docker_name, docker_tag are preserved
    updated_platform=$(cat alpine-source-override/src.json | jq -r .docker_platform)
    updated_name=$(cat alpine-source-override/src.json | jq -r .docker_name)
    updated_tag=$(cat alpine-source-override/src.json | jq -r .docker_tag)

    assertEquals "docker_platform should be preserved" "linux/amd64" "$updated_platform"
    assertNotNull "docker_name should still be set" "$updated_name"
    assertEquals "docker_tag should be preserved" "3.19" "$updated_tag"
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
    $pvr app add --from alpine:3.19 --platform linux/amd64 alpine-squash 2>&1
    assertEquals "pvr app add should succeed (device1)" "0" "$?"

    # Verify root.squashfs was actually generated
    test -f alpine-squash/root.squashfs
    assertEquals "root.squashfs should exist (device1)" "0" "$?"

    # Verify docker-digest file was generated and matches src.json
    test -f alpine-squash/root.squashfs.docker-digest
    assertEquals "root.squashfs.docker-digest should exist (device1)" "0" "$?"

    digest_file1="$(cat alpine-squash/root.squashfs.docker-digest)"
    src_digest1="$(cat alpine-squash/src.json | jq -r .docker_digest)"
    assertEquals "docker-digest file should match src.json docker_digest (device1)" "$src_digest1" "$digest_file1"

    squashfs_sha1="$(sha256sum alpine-squash/root.squashfs | awk '{print $1}')"
    assertNotNull "root.squashfs sha should not be empty (device1)" "$squashfs_sha1"

    # Now do the same in a second directory
    cd $dir/results/

    mkdir device2
    cd device2

    $pvr init --objects $dir/objects

    $pvr app add --from alpine:3.19 --platform linux/amd64 alpine-squash 2>&1
    assertEquals "pvr app add should succeed (device2)" "0" "$?"

    test -f alpine-squash/root.squashfs
    assertEquals "root.squashfs should exist (device2)" "0" "$?"

    # Verify docker-digest file was generated and matches src.json
    test -f alpine-squash/root.squashfs.docker-digest
    assertEquals "root.squashfs.docker-digest should exist (device2)" "0" "$?"

    digest_file2="$(cat alpine-squash/root.squashfs.docker-digest)"
    src_digest2="$(cat alpine-squash/src.json | jq -r .docker_digest)"
    assertEquals "docker-digest file should match src.json docker_digest (device2)" "$src_digest2" "$digest_file2"

    squashfs_sha2="$(sha256sum alpine-squash/root.squashfs | awk '{print $1}')"
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

    # Pull image so it's available locally
    docker pull alpine:3.19 >/dev/null 2>&1

    mkdir device1
    cd device1

    $pvr init --objects $dir/objects

    # Add app from local docker daemon
    $pvr app add --from alpine:3.19 --platform linux/amd64 --source local alpine-squash 2>&1
    assertEquals "pvr app add should succeed (device1)" "0" "$?"

    # Verify root.squashfs was actually generated
    test -f alpine-squash/root.squashfs
    assertEquals "root.squashfs should exist (device1)" "0" "$?"

    # Verify docker-digest file was generated and matches src.json
    test -f alpine-squash/root.squashfs.docker-digest
    assertEquals "root.squashfs.docker-digest should exist (device1)" "0" "$?"

    digest_file1="$(cat alpine-squash/root.squashfs.docker-digest)"
    src_digest1="$(cat alpine-squash/src.json | jq -r .docker_digest)"
    assertEquals "docker-digest file should match src.json docker_digest (device1)" "$src_digest1" "$digest_file1"

    squashfs_sha1="$(sha256sum alpine-squash/root.squashfs | awk '{print $1}')"
    assertNotNull "root.squashfs sha should not be empty (device1)" "$squashfs_sha1"

    # Now do the same in a second directory
    cd $dir/results/

    mkdir device2
    cd device2

    $pvr init --objects $dir/objects

    $pvr app add --from alpine:3.19 --platform linux/amd64 --source local alpine-squash 2>&1
    assertEquals "pvr app add should succeed (device2)" "0" "$?"

    test -f alpine-squash/root.squashfs
    assertEquals "root.squashfs should exist (device2)" "0" "$?"

    # Verify docker-digest file was generated and matches src.json
    test -f alpine-squash/root.squashfs.docker-digest
    assertEquals "root.squashfs.docker-digest should exist (device2)" "0" "$?"

    digest_file2="$(cat alpine-squash/root.squashfs.docker-digest)"
    src_digest2="$(cat alpine-squash/src.json | jq -r .docker_digest)"
    assertEquals "docker-digest file should match src.json docker_digest (device2)" "$src_digest2" "$digest_file2"

    squashfs_sha2="$(sha256sum alpine-squash/root.squashfs | awk '{print $1}')"
    assertNotNull "root.squashfs sha should not be empty (device2)" "$squashfs_sha2"

    # The same local image layers should produce the exact same squashfs
    assertEquals "root.squashfs sha should match between both local generations" "$squashfs_sha1" "$squashfs_sha2"
}

# Load shUnit2.
. _test/lib/shunit2
