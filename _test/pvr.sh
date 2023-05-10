#!/bin/sh

set -e

cmd=$0
dir=$(sh -c "cd $(dirname $cmd); pwd")

[ -d $dir/results ] || mkdir -p $dir/results

clean_results() {
  rm -rf $dir/results/* 2>/dev/null
  rm -rf $dir/results/.pvr 2>/dev/null
}

go build . && mv pvr $dir/

pvr="$dir/pvr"

## Remove the pv-avahi signature, this should remove the whole pv-avahi package
test_signature_removal() {
  clean_results 
  cd $dir/results/

  $pvr init
  $pvr import $dir/mocks/data/initial_state_01.tar.gz > /dev/null
  $pvr checkout > /dev/null

  rm -rf _sigs/pv-avahi.json && $pvr add . > /dev/null 2>&1 && $pvr commit > /dev/null 2>&1

  result="$(cat $dir/results/.pvr/json | jq -c)"
  expected="$(cat $dir/expected/state_signature_removal.json | jq -c)"

  assertEquals "$expected" "$result"
}

## Update pvr-sdk with a new package that doesn't have been _dm
## but because is signed the state should remove first the _dm folder and the 
## root.hash file. 
test_package_update_with_less_files() {
  clean_results
  cd $dir/results/

  $pvr init
  $pvr import $dir/mocks/data/initial_state_01.tar.gz > /dev/null
  $pvr checkout > /dev/null

  $pvr merge $dir/mocks/data/pvr-sdk_no_dm.tar.gz > /dev/null 2>&1
  $pvr checkout > /dev/null

  result="$(cat $dir/results/.pvr/json | jq -c)"
  expected="$(cat $dir/expected/test_package_update_with_less_files.json | jq -c)"

  assertEquals "$expected" "$result"
}

test_package_parts_selection() {
  clean_results
  cd $dir/results/

  $pvr init
  $pvr import $dir/mocks/data/initial_state_02.tar.gz > /dev/null
  $pvr checkout > /dev/null

  $pvr merge $dir/mocks/data/initial_state_01.tar.gz#pvr-sdk,_sigs/pvr-sdk.json > /dev/null 2>&1
  $pvr checkout > /dev/null

  result="$(cat $dir/results/.pvr/json | jq -c)"
  expected="$(cat $dir/expected/test_package_parts_selection.json | jq -c)"

  assertEquals "$expected" "$result"
}

test_package_parts_selection_removal() {
  clean_results
  cd $dir/results/

  $pvr init
  $pvr import $dir/mocks/data/initial_state_02.tar.gz > /dev/null
  $pvr checkout > /dev/null

  $pvr merge $dir/mocks/data/initial_state_01.tar.gz#-pv-avahi > /dev/null 2>&1
  $pvr checkout > /dev/null

  result="$(cat $dir/results/.pvr/json | jq -c)"
  expected="$(cat $dir/expected/test_package_parts_selection_removal.json | jq -c)"

  assertEquals "$expected" "$result"
}

test_deploy_selecting_parts() {
  clean_results
  cd $dir/results/

  $pvr init
  $pvr merge $dir/mocks/data/initial_state_02.tar.gz#-_sigs/nginx-config.json,-_config/nginx > /dev/null 2>&1
  $pvr checkout > /dev/null

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

  $pvr init
  $pvr merge $dir/mocks/data/initial_state_02.tar.gz#-_sigs/nginx-config.json,-_config/nginx > /dev/null 2>&1
  $pvr checkout > /dev/null

  cd $dir

  $pvr deploy $dir/results \
    $dir/mocks/data/initial_state_01.tar.gz#-bsp,-_sigs/bsp.json,-device.json \
    $dir/mocks/data/pvwebstatus.tar.gz > /dev/null 2>&1

  result="$(cat $dir/results/.pvr/json | jq -c)"
  expected="$(cat $dir/expected/test_deploy_unselecting_parts.json | jq -c)"

  assertEquals "$expected" "$result"
}

# Load shUnit2.
. $dir/lib/shunit2