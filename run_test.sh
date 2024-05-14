#!/bin/sh

# This script runs all the tests in the _test directory.
#
# Usage:
#   ./run_test.sh [test1 test2 ...]
#
# Requirements:
#   This script assumes that the current directory is the root directory of the
#   project.
#
# Arguments:
#   A space-separated list of test names to run. If no arguments are provided, all
#   the tests in the _test directory are run.
#
# Behaviour:
#   - Builds the pvr program
#   - Executes all the shell scripts in the _test directory that match the
#     provided test names.

go build -o _test/pvr .

for file in _test/*.sh; do
  sh $file -- $@
done