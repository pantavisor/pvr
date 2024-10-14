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
  $pvr import $dir/mocks/data/initial_state_01.tar.gz > /dev/null
  $pvr checkout > /dev/null 2>&1

  $pvr merge $dir/mocks/data/pvr-sdk_no_dm.tar.gz > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1
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
  $pvr import /tmp/big_numbers.tar.gz > /dev/null
  $pvr checkout > /dev/null 2>&1

  result="$(cat $dir/results/.pvr/json | jq -c)"
  expected="$(cat $dir/expected/test_big_numbers.json | jq -c)"

  assertEquals "$expected" "$result"

  $pvr export /tmp/exported_big_numbers.tgz
  cd /tmp/
  tar xzvf exported_big_numbers.tgz > /dev/null
  assertEquals "$expected" "$(cat json)"
}

## Remove the pv-avahi signature, this should remove the whole pv-avahi package
test_signature_removal() {
  clean_results
  cd $dir/results/

  $pvr init --objects $dir/objects
  $pvr import $dir/mocks/data/initial_state_01.tar.gz > /dev/null
  $pvr checkout > /dev/null 2>&1

  rm -rf _sigs/pv-avahi.json
  $pvr add && $pvr commit > /dev/null 2>&1

  result="$(cat $dir/results/.pvr/json | jq -c)"
  expected="$(cat $dir/expected/test_signature_removal.json | jq -c)"

  assertEquals "$expected" "$result"
}

test_package_parts_selection() {
  clean_results
  cd $dir/results/

  $pvr init --objects $dir/objects
  $pvr import $dir/mocks/data/initial_state_02.tar.gz > /dev/null
  $pvr checkout > /dev/null 2>&1

  $pvr merge $dir/mocks/data/initial_state_01.tar.gz#_sigs/pvr-sdk.json > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1

  cat $dir/results/.pvr/json | jq > /tmp/result.json
  cat $dir/expected/test_package_parts_selection.json | jq > /tmp/expected.json

  result=$(diff /tmp/result.json /tmp/expected.json)
  assertEquals "" "$result"
}

test_package_parts_selection_removal() {
  clean_results
  cd $dir/results/

  $pvr init --objects $dir/objects
  $pvr import $dir/mocks/data/initial_state_02.tar.gz > /dev/null
  $pvr checkout > /dev/null 2>&1

  $pvr merge $dir/mocks/data/initial_state_01.tar.gz#-pv-avahi > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1

  result="$(cat $dir/results/.pvr/json | jq -c)"
  expected="$(cat $dir/expected/test_package_parts_selection_removal.json | jq -c)"

  assertEquals "$expected" "$result"
}

test_deploy_selecting_parts() {
  clean_results
  cd $dir/results/

  $pvr init --objects $dir/objects
  $pvr merge $dir/mocks/data/initial_state_02.tar.gz#-_sigs/nginx-config.json,-_config/nginx > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1

  cd $dir

  $pvr deploy $dir/results \
    $dir/mocks/data/initial_state_01.tar.gz#bsp,_sigs/bsp.json,device.json \
    $dir/mocks/data/pvwebstatus.tar.gz > /dev/null 2>&1

  result="$(cat $dir/results/.pvr/json | jq -c)"
  expected="$(cat $dir/expected/test_deploy_selecting_parts.json | jq -c)"

  assertEquals "$expected" "$result"
}

test_deploy_unselecting_parts() {
  clean_results
  cd $dir/results/

  $pvr init --objects $dir/objects
  $pvr merge $dir/mocks/data/initial_state_02.tar.gz#-_sigs/nginx-config.json,-_config/nginx > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1

  cd $dir

  $pvr deploy $dir/results \
    $dir/mocks/data/initial_state_01.tar.gz#-bsp,-_sigs/bsp.json,-device.json \
    $dir/mocks/data/pvwebstatus.tar.gz > /dev/null 2>&1

  result="$(cat $dir/results/.pvr/json | jq -c)"
  expected="$(cat $dir/expected/test_deploy_unselecting_parts.json | jq -c)"

  assertEquals "$expected" "$result"
}

test_adding_signature_to_existing_package() {
  clean_results
  cd $dir/results/

  $pvr init --objects $dir/objects
  $pvr merge $dir/mocks/data/pvwebstatus.tar.gz#pvwebstatus > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1

  $pvr sig add --noconfig --part pvwebstatus
  $pvr add && $pvr commit
  cd $dir

  cat $dir/results/.pvr/json | jq > /tmp/result.json
  cat $dir/expected/test_adding_signature_to_existing_package.json | jq > /tmp/expected.json

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
  $pvr merge $dir/mocks/data/pvwebstatus.tar.gz > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1

  rm pvwebstatus/root.squashfs* && pvr app update pvwebstatus > /dev/null 2>&1

  $pvr sig add --noconfig --part pvwebstatus
  $pvr add && $pvr commit > /dev/null 2>&1 && $pvr export $dir/results/exported.1.tgz

  cd $dir/results/

  mkdir device2
  cd device2

  $pvr init --objects $dir/objects
  $pvr merge $dir/mocks/data/pvwebstatus.tar.gz > /dev/null 2>&1

  $pvr checkout > /dev/null 2>&1

  rm pvwebstatus/root.squashfs* && pvr app update pvwebstatus > /dev/null 2>&1

  $pvr sig add --noconfig --part pvwebstatus
  $pvr add && $pvr commit > /dev/null 2>&1 && $pvr export $dir/results/exported.2.tgz

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
  $pvr get $dir/mocks/data/pvwebstatus.tar.gz > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1

  $pvr add && $pvr commit > /dev/null 2>&1
  $pvr sig add --noconfig --part pvwebstatus
  $pvr add && $pvr commit > /dev/null 2>&1
  $pvr sig ls > /dev/null 2>&1

  $pvr app rm pvwebstatus > /dev/null 2>&1
  $pvr add && $pvr commit > /dev/null 2>&1

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
  $pvr get $dir/mocks/data/package_config_a.tgz > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1
  $pvr add && $pvr commit > /dev/null 2>&1

  $pvr get $dir/mocks/data/package_config_b.tgz > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1
  $pvr add && $pvr commit > /dev/null 2>&1

  # $pvr json | jq  > /tmp/result.json
  # cat $dir/expected/test_remove_part_already_signed_step1.json | jq > /tmp/expected.json

  # diff /tmp/result.json /tmp/expected.json

  $pvr app rm nginx > /dev/null 2>&1
  $pvr add > /dev/null 2>&1
  $pvr commit > /dev/null 2>&1

  result="$($pvr json | jq)"
  expected="$(cat $dir/expected/test_remove_part_already_signed_step2.json | jq)"

  assertEquals "$expected" "$result"
}

test_update_signature_remove_garbage() {
  clean_results

  cd $dir/mocks/data/removing_previous_sign_a
  rm -rf .pvr
  $pvr init --objects $dir/objects > /dev/null 2>&1
  $pvr add > /dev/null 2>&1
  $pvr commit > /dev/null 2>&1
  $pvr export /tmp/removing_previous_sign_a.tgz

  cd $dir/mocks/data/removing_previous_sign_b
  rm -rf .pvr
  $pvr init --objects $dir/objects > /dev/null 2>&1
  $pvr add > /dev/null 2>&1
  $pvr commit > /dev/null 2>&1
  $pvr export /tmp/removing_previous_sign_b.tgz

  cd $dir/results/
  mkdir device
  cd device

  $pvr init --objects $dir/objects
  $pvr get /tmp/removing_previous_sign_a.tgz > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1
  $pvr get /tmp/removing_previous_sign_b.tgz > /dev/null 2>&1
  $pvr checkout > /dev/null 2>&1
  $pvr json | jq > /tmp/result.json
  cat $dir/expected/test_pdate_signature_remove_gc.json | jq > /tmp/expected.json

  diffing=$(diff /tmp/result.json /tmp/expected.json)
  assertEquals "" "$diffing"
}

# Load shUnit2.
. _test/lib/shunit2
